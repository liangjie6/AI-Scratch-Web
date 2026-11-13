package runmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/ai-scratch-web/backend/internal/authoring"
	"github.com/ai-scratch-web/backend/internal/jsonx"
	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/runstore"
	"github.com/ai-scratch-web/backend/internal/schema"
)

func decodeSourceContract(value any) (*authoring.FrozenContract, error) {
	raw, err := jsonx.Marshal(value)
	if err != nil {
		return nil, err
	}
	return authoring.DecodeFrozen(raw)
}

// Only private contracts contain the original request; public run summaries
// continue to redact that object along with the source snapshot and script code.
func (m *AiRunManager) persistSourceContract(runID, owner string, data map[string]any) error {
	c, err := decodeSourceContract(data["source_contract"])
	if err != nil {
		return err
	}
	m.guard.Lock()
	request := m.requests[runID]
	m.guard.Unlock()
	if request == nil {
		return runstore.NewRunTransitionError("source run has no original request snapshot")
	}
	if err := authoring.ValidateRequestBinding(c, request); err != nil {
		return err
	}
	blueprint, err := decodeContract[schema.ProjectBlueprint](data, "blueprint")
	if err != nil {
		return err
	}
	manifest, err := decodeContract[schema.ProjectManifest](data, "manifest")
	if err != nil {
		return err
	}
	if err := authoring.ValidateDerivedContracts(c, blueprint, manifest); err != nil {
		return err
	}
	expectedHash, err := authoring.EnvelopeHash(c)
	if err != nil {
		return err
	}
	if given, _ := data["source_contract_hash"].(string); given != expectedHash {
		return runstore.NewRunTransitionError("source contract envelope hash differs from the frozen artifact")
	}
	if err := m.mergeContract(runID, owner, "source_contract", c); err != nil {
		return err
	}
	if err := m.mergeContract(runID, owner, "source_request", request); err != nil {
		return err
	}
	_, err = m.store.MergeArtifactSummary(runstore.MergeArtifactSummaryParams{RunID: runID, OwnerScopeHash: owner, Values: map[string]any{"pipeline_version": c.PipelineVersion, "normalizer_version": c.NormalizerVersion, "catalog_hash": c.CatalogHash, "source_contract_hash": expectedHash}})
	return err
}

func (m *AiRunManager) runSourcePrograms(ctx context.Context, runID, owner string, request *schema.UnifiedGenerationRequest, prov *provider.TextProvider, semaphore chan struct{}, orch *orchestration.GenerationOrchestrator, artifacts map[string]any) error {
	c, err := decodeSourceContract(artifacts["source_contract"])
	if err != nil {
		return err
	}
	if err := authoring.ValidateRequestBinding(c, request); err != nil {
		return err
	}
	blueprint, err := decodeContract[schema.ProjectBlueprint](artifacts, "blueprint")
	if err != nil {
		return err
	}
	manifest, err := decodeContract[schema.ProjectManifest](artifacts, "manifest")
	if err != nil {
		return err
	}
	if err := authoring.ValidateDerivedContracts(c, blueprint, manifest); err != nil {
		return err
	}
	if err := m.restoreSourceBudget(runID, owner, request, c, orch); err != nil {
		return err
	}
	targets := authoring.ProgramTargets(c)
	if len(targets) != len(manifest.ProgramTargets) {
		return runstore.NewRunTransitionError("source target projection differs from Manifest")
	}
	// Runtime broadcasts are not generation dependencies. Every frozen target is
	// independent, retaining the existing provider batch size and peer cancellation.
	for offset := 0; offset < len(targets); offset += 2 {
		end := offset + 2
		if end > len(targets) {
			end = len(targets)
		}
		if err := m.runSourceChunk(ctx, runID, owner, request, c, manifest, offset, end, prov, semaphore, orch); err != nil {
			return err
		}
	}
	return m.transition(runID, owner, runstore.RunStatusReadyToCommit, "ready_to_commit", "generation_complete", map[string]any{"scope": "project", "program_count": len(targets), "pipeline_version": c.PipelineVersion, "source_contract_hash": c.Hash})
}
func (m *AiRunManager) runSourceChunk(ctx context.Context, runID, owner string, request *schema.UnifiedGenerationRequest, c *authoring.FrozenContract, manifest *schema.ProjectManifest, start, end int, prov *provider.TextProvider, semaphore chan struct{}, orch *orchestration.GenerationOrchestrator) error {
	chunkCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for index := start; index < end; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			err := func() (err error) {
				defer recoverError(&err)
				return m.generateSourceProgram(chunkCtx, runID, owner, request, c, manifest, index, prov, semaphore, orch)
			}()
			if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			mu.Lock()
			if first == nil {
				first = err
			}
			mu.Unlock()
			cancel()
		}(index)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return first
}
func (m *AiRunManager) generateSourceProgram(ctx context.Context, runID, owner string, request *schema.UnifiedGenerationRequest, c *authoring.FrozenContract, manifest *schema.ProjectManifest, index int, prov *provider.TextProvider, semaphore chan struct{}, orch *orchestration.GenerationOrchestrator) error {
	target, ok := m.programContext(runID, index)
	if !ok {
		return runstore.NewRunTransitionError("source run is missing transient program context")
	}
	programRequest := &orchestration.SourceProgramRequest{Request: *request, SourceContract: *c, TargetIndex: index, Target: target}
	if err := programRequest.Validate(); err != nil {
		return err
	}
	if err := acquireSemaphore(ctx, semaphore); err != nil {
		return err
	}
	defer releaseSemaphore(semaphore)
	genCtx, cancel := context.WithTimeout(ctx, providerTimeout(m.settings))
	defer cancel()
	events, errs := orch.GenerateSourceProgram(genCtx, programRequest, prov)
	jobIDs := map[int]string{index: manifest.ProgramTargets[index].JobID}
	err := m.consumeGeneration(ctx, genCtx, events, errs, func(event orchestration.GenerationEvent) error {
		_, err := m.ingestGenerationEvent(ctx, runID, owner, event, jobIDs, prov, semaphore, orch, false)
		return err
	})
	if err != nil && ctx.Err() == nil && errors.Is(genCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", errGenerationTimeout, err)
	}
	return err
}

// Source runs can restore their original, private planning snapshot after a
// process restart. Legacy runs retain their existing transient-request policy.
func (m *AiRunManager) restoreSourceRequest(record *runstore.RunRecord) (*schema.UnifiedGenerationRequest, error) {
	contracts, ok := jsonObjectValue(record.ArtifactSummary["contracts"])
	if !ok {
		return nil, runstore.NewRunTransitionError("source run has no persisted contracts")
	}
	if _, ok := contracts["source_contract"]; !ok {
		return nil, runstore.NewRunTransitionError("the server no longer has the active request for this run")
	}
	request, err := decodeContract[schema.UnifiedGenerationRequest](contracts, "source_request")
	if err != nil {
		return nil, err
	}
	c, err := decodeSourceContract(contracts["source_contract"])
	if err != nil {
		return nil, err
	}
	if err := authoring.ValidateRequestBinding(c, request); err != nil {
		return nil, err
	}
	return request, nil
}

// The start-of-call event is journaled before the HTTP request. Counts remain
// monotonic across target concurrency and are restored before source recovery.
func (m *AiRunManager) persistSourceBudget(runID, owner string, orch *orchestration.GenerationOrchestrator) error {
	if orch == nil {
		return nil
	}
	m.guard.Lock()
	defer m.guard.Unlock()
	request := m.requests[runID]
	if request == nil {
		return runstore.NewRunTransitionError("source budget has no bound request")
	}
	record, err := m.store.GetRun(runID, owner)
	if err != nil {
		return err
	}
	hash := request.RequestID
	if contracts, ok := jsonObjectValue(record.ArtifactSummary["contracts"]); ok {
		if value, exists := contracts["source_contract"]; exists {
			c, err := decodeSourceContract(value)
			if err != nil {
				return err
			}
			hash = c.Hash
		}
	}
	snapshot := orch.SnapshotSourceBudget(request, hash)
	_, err = m.store.MergeArtifactSummary(runstore.MergeArtifactSummaryParams{RunID: runID, OwnerScopeHash: owner, Values: map[string]any{"source_budget": snapshot}})
	return err
}
func (m *AiRunManager) restoreSourceBudget(runID, owner string, request *schema.UnifiedGenerationRequest, c *authoring.FrozenContract, orch *orchestration.GenerationOrchestrator) error {
	record, err := m.store.GetRun(runID, owner)
	if err != nil {
		return err
	}
	value, ok := record.ArtifactSummary["source_budget"]
	if !ok {
		return runstore.NewRunTransitionError("source run is missing its durable budget")
	}
	raw, err := jsonx.Marshal(value)
	if err != nil {
		return err
	}
	var snapshot orchestration.SourceBudgetSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	return orch.RestoreSourceBudget(request, c.Hash, snapshot)
}
