package runmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ai-scratch-web/backend/internal/config"
	"github.com/ai-scratch-web/backend/internal/jsonx"
	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/runstore"
	"github.com/ai-scratch-web/backend/internal/schema"
)

// errGenerationTimeout marks an expired provider timeout so the outer handler
// can raise AI_PROVIDER_TIMEOUT like Python's except asyncio.TimeoutError.
var errGenerationTimeout = errors.New("ai provider generation timeout")

// providerTimeoutSeconds is the Python settings default used when a manager is
// built without settings.
const providerTimeoutSeconds = 480.0

func providerTimeout(settings *config.Settings) time.Duration {
	seconds := providerTimeoutSeconds
	if settings != nil && settings.AIBlocksProviderTimeoutSeconds > 0 {
		seconds = settings.AIBlocksProviderTimeoutSeconds
	}
	return time.Duration(seconds * float64(time.Second))
}

// recoverError converts a panic into an error so a goroutine behaves like a
// Python coroutine whose unexpected exception reaches the except Exception
// handler instead of crashing the process.
func recoverError(err *error) {
	recovered := recover()
	if recovered == nil {
		return
	}
	if value, ok := recovered.(error); ok {
		*err = fmt.Errorf("panic: %w", value)
		return
	}
	*err = fmt.Errorf("panic: %v", recovered)
}

// pythonTypeName renders the exception class name Python logs with
// type(exc).__name__.
func pythonTypeName(err error) string {
	name := fmt.Sprintf("%T", err)
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	return strings.TrimPrefix(name, "*")
}

func isGenerationTimeout(err error) bool {
	return errors.Is(err, errGenerationTimeout) || errors.Is(err, context.DeadlineExceeded)
}

// acquireSemaphore ports async with semaphore, including cancellation while
// waiting. The generation timeout starts after the semaphore is held, exactly
// like the nested async with blocks in Python.
func acquireSemaphore(ctx context.Context, semaphore chan struct{}) error {
	if semaphore == nil {
		return nil
	}
	select {
	case semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseSemaphore(semaphore chan struct{}) {
	if semaphore == nil {
		return
	}
	<-semaphore
}

// executeManifest runs the manifest pipeline of one run.
//
// Python: AiRunManager._execute_manifest.
func (m *AiRunManager) executeManifest(
	ctx context.Context,
	task *runTask,
	runID string,
	ownerScopeHash string,
	request *schema.UnifiedGenerationRequest,
	prov *provider.TextProvider,
	semaphore chan struct{},
	orch *orchestration.GenerationOrchestrator,
) {
	defer m.finishManifestTask(runID, ownerScopeHash, task)

	jobIDs := map[int]string{}
	err := m.runManifest(ctx, runID, ownerScopeHash, request, prov, semaphore, orch, jobIDs)
	m.handleExecutionError(ctx, runID, ownerScopeHash, "ai_run_failed", err)
}

// runManifest performs the work whose failures the Python except clauses map
// onto terminal run states.
func (m *AiRunManager) runManifest(
	ctx context.Context,
	runID string,
	ownerScopeHash string,
	request *schema.UnifiedGenerationRequest,
	prov *provider.TextProvider,
	semaphore chan struct{},
	orch *orchestration.GenerationOrchestrator,
	jobIDs map[int]string,
) (err error) {
	defer recoverError(&err)

	manifestRequest, err := buildManifestRequest(request)
	if err != nil {
		return err
	}
	if err := m.transition(runID, ownerScopeHash, runstore.RunStatusSpecifyingTask, "specifying_task", "phase_started", map[string]any{}); err != nil {
		return err
	}
	if err := acquireSemaphore(ctx, semaphore); err != nil {
		return err
	}
	defer releaseSemaphore(semaphore)

	genCtx, cancelGeneration := context.WithTimeout(ctx, providerTimeout(m.settings))
	defer cancelGeneration()
	// Python passes the ProjectManifestGenerationRequest subclass straight to
	// generate_unified. The Go schema keeps the two contracts as separate
	// structs, so the shared fields are converted exactly like the HTTP layer
	// converts them for the /ai-blocks/generate endpoint.
	events, errs := orch.GenerateUnified(genCtx, manifestRequestToUnified(manifestRequest), prov)
	return m.consumeGeneration(ctx, genCtx, events, errs, func(event orchestration.GenerationEvent) error {
		updated, ingestErr := m.ingestGenerationEvent(ctx, runID, ownerScopeHash, event, jobIDs, prov, semaphore, orch, true)
		jobIDs = updated
		return ingestErr
	})
}

// executePrograms runs the per-target program pipeline of one run.
//
// Python: AiRunManager._execute_programs.
func (m *AiRunManager) executePrograms(
	ctx context.Context,
	task *runTask,
	runID string,
	ownerScopeHash string,
	request *schema.UnifiedGenerationRequest,
	prov *provider.TextProvider,
	semaphore chan struct{},
	orch *orchestration.GenerationOrchestrator,
) {
	defer m.finishProgramsTask(runID, ownerScopeHash, task)

	err := m.runPrograms(ctx, runID, ownerScopeHash, request, prov, semaphore, orch)
	m.handleExecutionError(ctx, runID, ownerScopeHash, "ai_run_programs_failed", err)
}

// runPrograms loads the validated contracts, walks the dependency layers and
// finishes with the READY_TO_COMMIT transition.
func (m *AiRunManager) runPrograms(
	ctx context.Context,
	runID string,
	ownerScopeHash string,
	request *schema.UnifiedGenerationRequest,
	prov *provider.TextProvider,
	semaphore chan struct{},
	orch *orchestration.GenerationOrchestrator,
) (err error) {
	defer recoverError(&err)

	record, err := m.store.GetRun(runID, ownerScopeHash)
	if err != nil {
		return err
	}
	artifacts, ok := jsonObjectValue(record.ArtifactSummary["contracts"])
	if !ok {
		return runstore.NewRunTransitionError("run is missing the validated generation contracts")
	}
	requirements, err := decodeContract[schema.RequirementsResponse](artifacts, "requirements")
	if err != nil {
		return err
	}
	taskSpec, err := decodeContract[schema.ScratchTaskSpec](artifacts, "task_spec")
	if err != nil {
		return err
	}
	blueprint, err := decodeContract[schema.ProjectBlueprint](artifacts, "blueprint")
	if err != nil {
		return err
	}
	plan, err := decodeContract[schema.Plan](artifacts, "plan")
	if err != nil {
		return err
	}
	manifest, err := decodeContract[schema.ProjectManifest](artifacts, "manifest")
	if err != nil {
		return err
	}
	if err := schema.ValidateRequestExtensionPermissions(request, manifest); err != nil {
		return err
	}
	layers, err := programGenerationLayers(blueprint, manifest)
	if err != nil {
		return err
	}
	for _, layer := range layers {
		// Keep provider concurrency bounded while preserving dependency order.
		for offset := 0; offset < len(layer); offset += 2 {
			end := offset + 2
			if end > len(layer) {
				end = len(layer)
			}
			if err := m.runProgramChunk(ctx, runID, ownerScopeHash, request, requirements, taskSpec, blueprint, plan, manifest, layer[offset:end], prov, semaphore, orch); err != nil {
				return err
			}
		}
	}
	return m.transition(runID, ownerScopeHash, runstore.RunStatusReadyToCommit, "ready_to_commit", "generation_complete", map[string]any{
		"scope":         "project",
		"program_count": len(manifest.ProgramTargets),
	})
}

// runProgramChunk ports one asyncio.gather over at most two targets: the first
// failure cancels its peers, and run cancellation wins over the peer failure.
func (m *AiRunManager) runProgramChunk(
	ctx context.Context,
	runID string,
	ownerScopeHash string,
	request *schema.UnifiedGenerationRequest,
	requirements *schema.RequirementsResponse,
	taskSpec *schema.ScratchTaskSpec,
	blueprint *schema.ProjectBlueprint,
	plan *schema.Plan,
	manifest *schema.ProjectManifest,
	indexes []int,
	prov *provider.TextProvider,
	semaphore chan struct{},
	orch *orchestration.GenerationOrchestrator,
) error {
	chunkCtx, cancelChunk := context.WithCancel(ctx)
	defer cancelChunk()

	var waitGroup sync.WaitGroup
	var mutex sync.Mutex
	var firstErr error
	for _, index := range indexes {
		index := index
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			runErr := m.generateProgramSafely(chunkCtx, runID, ownerScopeHash, request, requirements, taskSpec, blueprint, plan, manifest, index, prov, semaphore, orch)
			if runErr == nil {
				return
			}
			// A cancelled run or an already cancelled peer is not the failure
			// that gather reports; Python re-raises the first real exception.
			if ctx.Err() != nil || errors.Is(runErr, context.Canceled) {
				return
			}
			mutex.Lock()
			if firstErr == nil {
				firstErr = runErr
			}
			mutex.Unlock()
			cancelChunk()
		}()
	}
	waitGroup.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return firstErr
}

func (m *AiRunManager) generateProgramSafely(
	ctx context.Context,
	runID string,
	ownerScopeHash string,
	request *schema.UnifiedGenerationRequest,
	requirements *schema.RequirementsResponse,
	taskSpec *schema.ScratchTaskSpec,
	blueprint *schema.ProjectBlueprint,
	plan *schema.Plan,
	manifest *schema.ProjectManifest,
	index int,
	prov *provider.TextProvider,
	semaphore chan struct{},
	orch *orchestration.GenerationOrchestrator,
) (err error) {
	defer recoverError(&err)
	return m.generateProgram(ctx, runID, ownerScopeHash, request, requirements, taskSpec, blueprint, plan, manifest, index, prov, semaphore, orch)
}

// generateProgram ports the inner generate_program closure of
// _execute_programs.
func (m *AiRunManager) generateProgram(
	ctx context.Context,
	runID string,
	ownerScopeHash string,
	request *schema.UnifiedGenerationRequest,
	requirements *schema.RequirementsResponse,
	taskSpec *schema.ScratchTaskSpec,
	blueprint *schema.ProjectBlueprint,
	plan *schema.Plan,
	manifest *schema.ProjectManifest,
	index int,
	prov *provider.TextProvider,
	semaphore chan struct{},
	orch *orchestration.GenerationOrchestrator,
) error {
	programTarget := manifest.ProgramTargets[index]
	targetContext, ok := m.programContext(runID, index)
	if !ok {
		return runstore.NewRunTransitionError("run is missing transient program context")
	}
	programRequest, err := buildProgramRequest(request, requirements, taskSpec, blueprint, plan, manifest, programTarget, index, targetContext)
	if err != nil {
		return err
	}
	if err := acquireSemaphore(ctx, semaphore); err != nil {
		return err
	}
	defer releaseSemaphore(semaphore)

	genCtx, cancelGeneration := context.WithTimeout(ctx, providerTimeout(m.settings))
	defer cancelGeneration()
	events, errs := orch.GenerateProjectProgram(genCtx, programRequest, prov)
	jobIDs := map[int]string{index: programTarget.JobID}
	err = m.consumeGeneration(ctx, genCtx, events, errs, func(event orchestration.GenerationEvent) error {
		_, ingestErr := m.ingestGenerationEvent(ctx, runID, ownerScopeHash, event, jobIDs, prov, semaphore, orch, false)
		return ingestErr
	})
	if err != nil && ctx.Err() == nil && errors.Is(genCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", errGenerationTimeout, err)
	}
	return err
}

// consumeGeneration drains a provider event stream. Events are always drained
// before the terminal error is read, mirroring an async-for loop that only
// sees the generator exception at the point where no event is left.
func (m *AiRunManager) consumeGeneration(
	ctx context.Context,
	genCtx context.Context,
	events <-chan orchestration.GenerationEvent,
	errs <-chan error,
	ingest func(orchestration.GenerationEvent) error,
) error {
	for events != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-genCtx.Done():
			return genCtx.Err()
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if err := ingest(event); err != nil {
				return err
			}
		}
	}
	if errs == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-genCtx.Done():
		return genCtx.Err()
	case err := <-errs:
		return err
	}
}

// handleExecutionError ports the shared except clauses of _execute_manifest and
// _execute_programs. The log event name differs per pipeline.
func (m *AiRunManager) handleExecutionError(ctx context.Context, runID string, ownerScopeHash string, logEvent string, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		// Python re-raises asyncio.CancelledError: cancel() (or shutdown) has
		// already written the terminal event, so no second state is written.
		return
	}
	var providerErr *provider.TextProviderError
	switch {
	case isGenerationTimeout(err):
		// A store failure here would surface as an unhandled task exception in
		// Python, which never reaches this module's logger.
		_ = m.failIfActive(runID, ownerScopeHash, "AI_PROVIDER_TIMEOUT", "AI 生成超时，请稍后重试")
	case errors.As(err, &providerErr):
		_ = m.failIfActive(runID, ownerScopeHash, "AI_PROVIDER_ERROR", "AI 生成暂时不可用，请稍后重试")
	default:
		logError(logEvent, runID, err)
		_ = m.failIfActive(runID, ownerScopeHash, "AI_GENERATION_ERROR", "AI 输出未完成，请重新生成")
	}
}

// finishManifestTask ports the finally block of _execute_manifest.
func (m *AiRunManager) finishManifestTask(runID string, ownerScopeHash string, task *runTask) {
	m.guard.Lock()
	if m.tasks[runID] == task {
		delete(m.tasks, runID)
	}
	if record, err := m.store.GetRun(runID, ownerScopeHash); err == nil && runstore.IsTerminal(record.Status) {
		delete(m.requests, runID)
	}
	m.guard.Unlock()
	m.signal(runID)
}

// finishProgramsTask ports the finally block of _execute_programs.
func (m *AiRunManager) finishProgramsTask(runID string, ownerScopeHash string, task *runTask) {
	m.guard.Lock()
	if m.tasks[runID] == task {
		delete(m.tasks, runID)
	}
	delete(m.requests, runID)
	delete(m.programContexts, runID)
	m.guard.Unlock()
	m.signal(runID)
}

// programContext returns the transient target context of one manifest target.
func (m *AiRunManager) programContext(runID string, index int) (schema.TargetContext, bool) {
	m.guard.Lock()
	defer m.guard.Unlock()
	contexts := m.programContexts[runID]
	if contexts == nil {
		return schema.TargetContext{}, false
	}
	target, ok := contexts[index]
	return target, ok
}

// transition ports AiRunManager._transition: a rejected transition is ignored
// when the run already reached a terminal status.
func (m *AiRunManager) transition(runID string, ownerScopeHash string, target runstore.RunStatus, phase string, eventType string, payload map[string]any) error {
	_, err := m.store.Transition(runstore.TransitionParams{
		RunID:          runID,
		OwnerScopeHash: ownerScopeHash,
		Target:         target,
		Phase:          phase,
		EventType:      eventType,
		Payload:        payload,
	})
	if err == nil {
		return nil
	}
	var transitionErr *runstore.RunTransitionError
	if !errors.As(err, &transitionErr) {
		return err
	}
	record, getErr := m.store.GetRun(runID, ownerScopeHash)
	if getErr != nil {
		return getErr
	}
	if !runstore.IsTerminal(record.Status) {
		return err
	}
	return nil
}

// appendEvent ports AiRunManager._append.
func (m *AiRunManager) appendEvent(runID string, ownerScopeHash string, phase string, eventType string, payload map[string]any, attempt int64) (runstore.RunEvent, error) {
	return m.store.AppendEvent(runstore.AppendEventParams{
		RunID:          runID,
		OwnerScopeHash: ownerScopeHash,
		Phase:          phase,
		EventType:      eventType,
		Payload:        payload,
		Attempt:        attempt,
	})
}

// failIfActive ports AiRunManager._fail_if_active.
func (m *AiRunManager) failIfActive(runID string, ownerScopeHash string, code string, message string) error {
	err := m.transition(runID, ownerScopeHash, runstore.RunStatusFailed, "failed", "generation_error", map[string]any{
		"code":    code,
		"message": message,
	})
	var notFound *runstore.RunNotFoundError
	if errors.As(err, &notFound) {
		slog.Warn("ai_run_missing_while_failing", "run_id", runID)
		return nil
	}
	return err
}

// mergeContract ports AiRunManager._merge_contract.
func (m *AiRunManager) mergeContract(runID string, ownerScopeHash string, name string, value any) error {
	if value == nil {
		return runstore.NewRunTransitionError("%s contract is missing from a completed generation phase", name)
	}
	record, err := m.store.GetRun(runID, ownerScopeHash)
	if err != nil {
		return err
	}
	contracts := map[string]any{}
	if existing, ok := jsonObjectValue(record.ArtifactSummary["contracts"]); ok {
		for key, item := range existing {
			contracts[key] = item
		}
	}
	contracts[name] = value
	_, err = m.store.MergeArtifactSummary(runstore.MergeArtifactSummaryParams{
		RunID:          runID,
		OwnerScopeHash: ownerScopeHash,
		Values:         map[string]any{"contracts": contracts},
	})
	return err
}

// decodeContract ports Model.model_validate(artifacts[name]).
func decodeContract[T any](artifacts map[string]any, name string) (*T, error) {
	value, ok := artifacts[name]
	if !ok {
		return nil, fmt.Errorf("contract %s is missing from the validated generation contracts", name)
	}
	raw, err := jsonx.Marshal(value)
	if err != nil {
		return nil, err
	}
	target := new(T)
	if err := schema.DecodeStrict(raw, target); err != nil {
		return nil, err
	}
	return target, nil
}

// modelDump ports model_dump(mode='json').
func modelDump(value any) (map[string]any, error) {
	raw, err := jsonx.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jsonx.ParseObject(raw)
}

// buildManifestRequest ports
// ProjectManifestGenerationRequest.model_validate({**request.model_dump(mode='json'), 'output_contract': 'project_manifest'}).
func buildManifestRequest(request *schema.UnifiedGenerationRequest) (*schema.ProjectManifestGenerationRequest, error) {
	object, err := modelDump(request)
	if err != nil {
		return nil, err
	}
	object["output_contract"] = "project_manifest"
	raw, err := jsonx.Marshal(object)
	if err != nil {
		return nil, err
	}
	target := &schema.ProjectManifestGenerationRequest{}
	if err := schema.DecodeStrict(raw, target); err != nil {
		return nil, err
	}
	return target, nil
}

// manifestRequestToUnified converts the manifest contract to the unified
// contract the Go orchestrator accepts. It mirrors manifestToUnified in the
// HTTP layer; only output_contract is dropped, and the orchestrator decides the
// manifest pipeline from the project scope.
func manifestRequestToUnified(manifest *schema.ProjectManifestGenerationRequest) *schema.UnifiedGenerationRequest {
	return &schema.UnifiedGenerationRequest{
		OutputContract:      manifest.OutputContract,
		ModelTier:           manifest.ModelTier,
		SessionID:           manifest.SessionID,
		RequestID:           manifest.RequestID,
		Messages:            manifest.Messages,
		Target:              manifest.Target,
		Thinking:            manifest.Thinking,
		Scope:               manifest.Scope,
		AssetPolicy:         manifest.AssetPolicy,
		ProjectContext:      manifest.ProjectContext,
		ProceedWithDefaults: manifest.ProceedWithDefaults,
		PermittedExtensions: manifest.PermittedExtensions,
	}
}

// buildProgramRequest ports the ProjectProgramGenerationRequest.model_validate
// call inside generate_program.
func buildProgramRequest(
	request *schema.UnifiedGenerationRequest,
	requirements *schema.RequirementsResponse,
	taskSpec *schema.ScratchTaskSpec,
	blueprint *schema.ProjectBlueprint,
	plan *schema.Plan,
	manifest *schema.ProjectManifest,
	programTarget schema.ProjectProgramTarget,
	index int,
	targetContext schema.TargetContext,
) (*schema.ProjectProgramGenerationRequest, error) {
	object, err := modelDump(request)
	if err != nil {
		return nil, err
	}
	object["output_contract"] = "project_program"
	steps := []struct {
		key   string
		value any
	}{
		{"requirements", requirements},
		{"task_spec", taskSpec},
		{"blueprint", blueprint},
		{"plan", plan},
		{"manifest", manifest},
		{"program_target", programTarget},
	}
	for _, step := range steps {
		dumped, err := modelDump(step.value)
		if err != nil {
			return nil, err
		}
		object[step.key] = dumped
	}
	object["target_index"] = index
	targetDump, err := modelDump(targetContext)
	if err != nil {
		return nil, err
	}
	object["target"] = targetDump
	raw, err := jsonx.Marshal(object)
	if err != nil {
		return nil, err
	}
	target := &schema.ProjectProgramGenerationRequest{}
	if err := schema.DecodeStrict(raw, target); err != nil {
		return nil, err
	}
	return target, nil
}
