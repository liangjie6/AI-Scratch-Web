package runmanager

import (
	"context"

	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/runstore"
	"github.com/ai-scratch-web/backend/internal/schema"
)

// SubmitToolResult records one asset resolution result and starts program
// generation once every declared need has a result.
//
// Python: AiRunManager.submit_tool_result.
func (m *AiRunManager) SubmitToolResult(ctx context.Context, params ToolResultParams) (map[string]any, error) {
	response, replayed, err := m.submitToolResultLocked(params)
	if err != nil {
		return nil, err
	}
	// Python returns from inside the guard on a replay, so a replay neither
	// restarts program generation nor signals waiters.
	if replayed {
		return response, nil
	}
	m.signal(params.RunID)
	return response, nil
}

func (m *AiRunManager) submitToolResultLocked(params ToolResultParams) (map[string]any, bool, error) {
	m.guard.Lock()
	defer m.guard.Unlock()

	record, err := m.store.GetRun(params.RunID, params.OwnerScopeHash)
	if err != nil {
		return nil, false, err
	}
	summary := record.ArtifactSummary
	needValue := params.Payload["need_id"]
	needID, needIsString := needValue.(string)
	expectedCalls := map[string]struct{}{}
	if items, ok := jsonArray(summary["asset_need_ids"]); ok {
		for _, item := range items {
			expectedCalls["resolve_"+pyStr(item)] = struct{}{}
		}
	}
	_, declared := expectedCalls[params.ToolCallID]
	if !needIsString || params.ToolCallID != "resolve_"+needID || !declared {
		return nil, false, runstore.NewRunTransitionError("tool_call_id must match a declared Blueprint asset need")
	}
	var allowedOps any
	if expectedOps, ok := jsonObjectValue(summary["asset_need_ops"]); ok {
		allowedOps = expectedOps[needID]
	}
	if !pythonContains(allowedOps, params.Payload["op_id"]) {
		return nil, false, runstore.NewRunTransitionError("tool result op_id is not declared for this asset need")
	}

	toolCallID := params.ToolCallID
	response, replayed, err := m.store.AcceptInteraction(runstore.AcceptInteractionParams{
		RunID:                params.RunID,
		OwnerScopeHash:       params.OwnerScopeHash,
		Operation:            "tool-results",
		IdempotencyKey:       params.IdempotencyKey,
		ExpectedProjectEpoch: m.toolResultEpoch(params),
		RequiredStatus:       runstore.RunStatusAwaitingToolResult,
		Phase:                "awaiting_tool_result",
		EventType:            "tool_result_received",
		Payload:              params.Payload,
		ToolCallID:           &toolCallID,
	})
	if err != nil {
		return nil, false, err
	}
	if replayed {
		return response, true, nil
	}
	if _, err := m.startProgramsIfReady(params.RunID, params.OwnerScopeHash, params.Provider, params.Semaphore, params.Orchestrator); err != nil {
		return nil, false, err
	}
	return response, false, nil
}

// toolResultEpoch resolves the expected project epoch of a tool result. The
// Python route passes the request field explicitly and also stores it in the
// payload; the Go HTTP layer currently only stores it in the payload, so the
// explicit field wins whenever it is set and the payload value is the
// fallback.
func (m *AiRunManager) toolResultEpoch(params ToolResultParams) int64 {
	if params.ExpectedProjectEpoch != 0 {
		return params.ExpectedProjectEpoch
	}
	if value, ok := jsonInt(params.Payload["expected_project_epoch"]); ok && value != 0 {
		return value
	}
	return params.ExpectedProjectEpoch
}

// SubmitProgramContexts stores one transient target context and starts program
// generation once the whole manifest target set is present.
//
// Python: AiRunManager.submit_program_contexts.
func (m *AiRunManager) SubmitProgramContexts(ctx context.Context, params ProgramContextsParams) (map[string]any, error) {
	m.guard.Lock()
	defer m.guard.Unlock()

	record, err := m.store.GetRun(params.RunID, params.OwnerScopeHash)
	if err != nil {
		return nil, err
	}
	if record.ProjectEpoch != params.ExpectedProjectEpoch {
		return nil, runstore.NewRunTransitionError("program contexts use a stale project epoch")
	}
	prior, hasPrior := m.programContexts[params.RunID][params.TargetIndex]
	if record.Status != runstore.RunStatusAwaitingToolResult {
		if hasPrior && equalTargetContext(prior, params.Target) {
			return record.PublicPayload(), nil
		}
		return nil, runstore.NewRunTransitionError("program contexts require run status awaiting_tool_result")
	}
	targetCount, isInt := nestedInt(record.ArtifactSummary, "manifest_summary", "program_target_count", -1)
	if !isInt || params.TargetIndex < 0 || params.TargetIndex >= targetCount {
		return nil, runstore.NewRunTransitionError("program context target_index is outside manifest targets")
	}
	contexts := m.programContexts[params.RunID]
	if contexts == nil {
		contexts = map[int]schema.TargetContext{}
		m.programContexts[params.RunID] = contexts
	}
	prior, hasPrior = contexts[params.TargetIndex]
	if hasPrior && !equalTargetContext(prior, params.Target) {
		return nil, runstore.NewRunTransitionError("program contexts were already submitted with different content")
	}
	contexts[params.TargetIndex] = params.Target
	started, err := m.startProgramsIfReady(params.RunID, params.OwnerScopeHash, params.Provider, params.Semaphore, params.Orchestrator)
	if err != nil {
		return nil, err
	}
	if started != nil {
		return started.PublicPayload(), nil
	}
	fresh, err := m.store.GetRun(params.RunID, params.OwnerScopeHash)
	if err != nil {
		return nil, err
	}
	return fresh.PublicPayload(), nil
}

// SubmitDecision records a client decision for a run awaiting tool results.
//
// Python: AiRunManager.submit_decision.
func (m *AiRunManager) SubmitDecision(ctx context.Context, params DecisionParams) (map[string]any, error) {
	var response map[string]any
	err := func() error {
		m.guard.Lock()
		defer m.guard.Unlock()
		summary, err := m.store.GetRun(params.RunID, params.OwnerScopeHash)
		if err != nil {
			return err
		}
		needValue, exists := params.Payload["need_id"]
		if exists && needValue != nil {
			declaredNeeds := summary.ArtifactSummary["blueprint_asset_need_ids"]
			if !pythonContains(declaredNeeds, needValue) {
				return runstore.NewRunTransitionError("decision need_id is not declared by the Blueprint")
			}
		}
		response, _, err = m.store.AcceptInteraction(runstore.AcceptInteractionParams{
			RunID:                params.RunID,
			OwnerScopeHash:       params.OwnerScopeHash,
			Operation:            "decisions",
			IdempotencyKey:       params.IdempotencyKey,
			ExpectedProjectEpoch: params.ExpectedProjectEpoch,
			RequiredStatus:       runstore.RunStatusAwaitingToolResult,
			Phase:                "awaiting_tool_result",
			EventType:            "decision_received",
			Payload:              params.Payload,
		})
		return err
	}()
	if err != nil {
		return nil, err
	}
	m.signal(params.RunID)
	return response, nil
}

// startProgramsIfReady transitions to GENERATING_PROGRAMS and launches the
// program pipeline once every asset need has a tool result and every manifest
// target has a transient context.
//
// Python: AiRunManager._start_programs_if_ready. The caller must hold the
// manager guard, exactly like the Python call sites inside the lock. It
// returns the transition result when the program task was started, so
// submit_program_contexts can return the same record Python observes.
func (m *AiRunManager) startProgramsIfReady(
	runID string,
	ownerScopeHash string,
	prov *provider.TextProvider,
	semaphore chan struct{},
	orch *orchestration.GenerationOrchestrator,
) (*runstore.RunRecord, error) {
	record, err := m.store.GetRun(runID, ownerScopeHash)
	if err != nil {
		return nil, err
	}
	if record.Status != runstore.RunStatusAwaitingToolResult {
		return nil, nil
	}
	summary := record.ArtifactSummary
	expectedCalls := map[string]struct{}{}
	if items, ok := jsonArray(summary["asset_need_ids"]); ok {
		for _, item := range items {
			if text, ok := item.(string); ok {
				expectedCalls["resolve_"+text] = struct{}{}
			}
		}
	}
	accepted, err := m.store.AcceptedToolCallIds(runstore.AcceptedToolCallIdsParams{
		RunID:          runID,
		OwnerScopeHash: ownerScopeHash,
	})
	if err != nil {
		return nil, err
	}
	targetCount, targetCountIsInt := nestedInt(summary, "manifest_summary", "program_target_count", -1)
	contexts := m.programContexts[runID]
	if !targetCountIsInt || !subsetOf(expectedCalls, accepted) || !sameIndexSet(contexts, targetCount) {
		return nil, nil
	}
	request := m.requests[runID]
	if request == nil {
		request, err = m.restoreSourceRequest(&record)
		if err != nil {
			return nil, err
		}
		m.requests[runID] = request
	}
	transitioned, err := m.store.Transition(runstore.TransitionParams{
		RunID:          runID,
		OwnerScopeHash: ownerScopeHash,
		Target:         runstore.RunStatusGeneratingPrograms,
		Phase:          "generating_programs",
		EventType:      "phase_started",
		Payload:        map[string]any{},
	})
	if err != nil {
		return nil, err
	}
	taskCtx, cancel := context.WithCancel(context.Background())
	task := &runTask{cancel: cancel, done: make(chan struct{})}
	m.tasks[runID] = task
	go func() {
		defer close(task.done)
		m.executePrograms(taskCtx, task, runID, ownerScopeHash, request, prov, semaphore, orch)
	}()
	return &transitioned, nil
}

// subsetOf reports whether every key of subset is present in set, mirroring
// Python's expected_calls.issubset(accepted).
func subsetOf(subset map[string]struct{}, set map[string]struct{}) bool {
	for key := range subset {
		if _, ok := set[key]; !ok {
			return false
		}
	}
	return true
}

// sameIndexSet mirrors set(contexts) == set(range(target_count)).
func sameIndexSet(contexts map[int]schema.TargetContext, targetCount int) bool {
	if len(contexts) != targetCount {
		return false
	}
	for index := range contexts {
		if index < 0 || index >= targetCount {
			return false
		}
	}
	return true
}
