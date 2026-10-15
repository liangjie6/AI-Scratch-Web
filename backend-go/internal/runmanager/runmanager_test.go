package runmanager

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ai-scratch-web/backend/internal/runstore"
	"github.com/ai-scratch-web/backend/internal/schema"
)

func testProgramContext() schema.TargetContext {
	return schema.TargetContext{
		Name: "Sprite1", IsStage: false, HasScripts: false, Mode: "append",
		Costumes: []string{"costume1"}, Backdrops: []string{}, Sounds: []string{},
		SpriteNames: []string{"Sprite1"}, CurrentProgram: "",
	}
}

func testToolResultPayload() map[string]any {
	return map[string]any{
		"tool_call_id":           "resolve_need_meow",
		"expected_project_epoch": testProjectEpoch,
		"need_id":                "need_meow",
		"op_id":                  "op_meow",
		"selected_source_kind":   "scratch_library",
		"normalized_name":        "Meow",
		"normalized_tags":        []any{},
	}
}

// manifestArtifactSummary runs one manifest pipeline through the real
// orchestrator and returns the artifact summary it persisted.
func manifestArtifactSummary(t *testing.T, fixture *testManager) map[string]any {
	t.Helper()
	runID := fixture.startRun(t, testUnifiedRequest())
	// A project_manifest request stops at the asset-resolution boundary, so the
	// run parks in AWAITING_TOOL_RESULT instead of reaching a terminal status.
	record := fixture.waitForStatus(t, runID, runstore.RunStatusAwaitingToolResult, 20*time.Second)
	if _, ok := record.ArtifactSummary["contracts"].(map[string]any); !ok {
		t.Fatalf("manifest run stored no contracts: %#v", record.ArtifactSummary)
	}
	return record.ArtifactSummary
}

// seedAwaitingRun creates a run parked on the asset-resolution boundary with
// the contracts produced by the manifest pipeline.
func seedAwaitingRun(t *testing.T, fixture *testManager, summary map[string]any, request *schema.UnifiedGenerationRequest) string {
	t.Helper()
	record, err := fixture.store.CreateRun(runstore.CreateRunParams{
		OwnerScopeHash:  testOwnerScopeHash,
		ProjectEpoch:    testProjectEpoch,
		ArtifactSummary: summary,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	runID := record.RunID
	steps := []struct {
		status  runstore.RunStatus
		phase   string
		event   string
		payload map[string]any
	}{
		{runstore.RunStatusSpecifyingTask, "specifying_task", "phase_started", map[string]any{}},
		{runstore.RunStatusBlueprinting, "blueprinting", "phase_started", map[string]any{}},
		{runstore.RunStatusPlanningManifest, "planning_manifest", "phase_started", map[string]any{}},
		{runstore.RunStatusAwaitingToolResult, "awaiting_tool_result", "asset_resolution_required", map[string]any{
			"program_context_required": true,
			"tool_calls": []any{
				map[string]any{"tool_call_id": "resolve_need_meow", "need_id": "need_meow"},
			},
		}},
	}
	for _, step := range steps {
		if _, err := fixture.store.Transition(runstore.TransitionParams{
			RunID:          runID,
			OwnerScopeHash: testOwnerScopeHash,
			Target:         step.status,
			Phase:          step.phase,
			EventType:      step.event,
			Payload:        step.payload,
		}); err != nil {
			t.Fatalf("seed transition %s: %v", step.status, err)
		}
	}
	fixture.manager.guard.Lock()
	fixture.manager.requests[runID] = request
	fixture.manager.guard.Unlock()
	return runID
}

// assertSubsequence checks that want appears in order inside got.
func assertSubsequence(t *testing.T, got []string, want []string) {
	t.Helper()
	index := 0
	for _, item := range got {
		if index < len(want) && item == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("event sequence %v does not contain %v in order", got, want)
	}
}

func (m *testManager) toolResultParams(runID string, key string, payload map[string]any) ToolResultParams {
	return ToolResultParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		ToolCallID:           "resolve_need_meow",
		IdempotencyKey:       key,
		ExpectedProjectEpoch: testProjectEpoch,
		Payload:              payload,
		Provider:             m.provider,
		Semaphore:            m.semaphore(),
		Orchestrator:         m.orchestrator,
	}
}

// TestStartManifestRunThroughFakeProvider drives a durable run from Start to a
// terminal status with the real orchestrator and a fake HTTP text provider.
func TestStartManifestRunThroughFakeProvider(t *testing.T) {
	fixture := newTestManager(t)
	request := testUnifiedRequest()
	payload := func() map[string]any {
		response, err := fixture.manager.Start(context.Background(), fixture.startParams(request))
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		return response
	}()
	runID, _ := payload["run_id"].(string)
	if runID == "" {
		t.Fatalf("start response has no run_id: %#v", payload)
	}
	if payload["status"] != string(runstore.RunStatusSnapshotting) {
		t.Fatalf("start status = %v", payload["status"])
	}
	if payload["project_epoch"] != int64(testProjectEpoch) {
		t.Fatalf("start project_epoch = %v", payload["project_epoch"])
	}

	record := fixture.waitForStatus(t, runID, runstore.RunStatusAwaitingToolResult, 20*time.Second)
	pairs := eventPairs(fixture.events(t, runID))
	// output_contract=project_manifest makes the orchestrator stop after the
	// manifest, so the run parks on the durable asset-resolution boundary and
	// waits for tool results instead of generating programs inline.
	if last := pairs[len(pairs)-1]; last != "awaiting_tool_result/asset_resolution_required" {
		t.Fatalf("last event = %s in %v", last, pairs)
	}

	manifestSequence := []string{
		"specifying_task/phase_started",
		"specifying_task/requirements_ready",
		"specifying_task/task_spec_ready",
		"blueprinting/phase_started",
		"blueprinting/blueprint_ready",
		"planning_manifest/phase_started",
		"planning_manifest/manifest_ready",
	}
	assertSubsequence(t, pairs, manifestSequence)

	summary := record.ArtifactSummary
	contracts, ok := summary["contracts"].(map[string]any)
	if !ok {
		t.Fatalf("summary has no contracts: %#v", summary)
	}
	for _, name := range []string{"requirements", "task_spec", "blueprint", "plan", "manifest"} {
		if _, present := contracts[name]; !present {
			t.Fatalf("contracts missing %s: %#v", name, contracts)
		}
	}
	if got := summary["scope"]; got != "project" {
		t.Fatalf("summary scope = %v", got)
	}
	if got := summary["output_contract"]; got != "project_manifest" {
		t.Fatalf("summary output_contract = %v", got)
	}
	if got := summary["project_id"]; got != "project-1" {
		t.Fatalf("summary project_id = %v", got)
	}
	if got := summary["model_tier"]; got != "normal" {
		t.Fatalf("summary model_tier = %v", got)
	}
	if got := summary["blueprint_asset_need_ids"]; !reflect.DeepEqual(got, []any{"need_meow"}) {
		t.Fatalf("blueprint_asset_need_ids = %#v", got)
	}
	if got := summary["asset_need_ids"]; !reflect.DeepEqual(got, []any{"need_meow"}) {
		t.Fatalf("asset_need_ids = %#v", got)
	}
	needOps, ok := summary["asset_need_ops"].(map[string]any)
	if !ok {
		t.Fatalf("asset_need_ops = %#v", summary["asset_need_ops"])
	}
	if got := needOps["need_meow"]; !reflect.DeepEqual(got, []any{"op_meow"}) {
		t.Fatalf("asset_need_ops[need_meow] = %#v", got)
	}
	manifestSummary, ok := summary["manifest_summary"].(map[string]any)
	if !ok {
		t.Fatalf("manifest_summary = %#v", summary["manifest_summary"])
	}
	programTargetCount, countOK := pythonInt(manifestSummary["program_target_count"])
	resourceOpCount, opOK := pythonInt(manifestSummary["resource_op_count"])
	if !countOK || !opOK || programTargetCount != 1 || resourceOpCount != 1 {
		t.Fatalf("manifest_summary = %#v", manifestSummary)
	}

	called := fixture.fixture.calledSegments()
	// No program segment: the manifest-only contract must not run the program
	// pipeline before asset resolution completes.
	wantCalled := []string{"requirements", "task_spec", "blueprint", "plan", "manifest"}
	if !reflect.DeepEqual(called, wantCalled) {
		t.Fatalf("provider segments = %v", called)
	}
}

// TestToolResultAndProgramContextsEndToEnd drives the durable boundary: tool
// result, program contexts, program generation, idempotent replay.
func TestToolResultAndProgramContextsEndToEnd(t *testing.T) {
	fixture := newTestManager(t)
	request := testUnifiedRequest()
	summary := manifestArtifactSummary(t, fixture)
	runID := seedAwaitingRun(t, fixture, summary, request)

	// An undeclared tool call and an undeclared op are rejected with the
	// Python messages.
	if _, err := fixture.manager.SubmitToolResult(context.Background(), fixture.toolResultParams(runID, "idempotency-key-0001", map[string]any{
		"tool_call_id": "resolve_other", "need_id": "other", "op_id": "op_meow",
	})); err == nil {
		t.Fatal("undeclared tool_call_id was accepted")
	} else if err.Error() != "tool_call_id must match a declared Blueprint asset need" {
		t.Fatalf("tool_call_id error = %q", err.Error())
	}
	if _, err := fixture.manager.SubmitToolResult(context.Background(), fixture.toolResultParams(runID, "idempotency-key-0002", map[string]any{
		"tool_call_id": "resolve_need_meow", "need_id": "need_meow", "op_id": "op_other",
	})); err == nil {
		t.Fatal("undeclared op_id was accepted")
	} else if err.Error() != "tool result op_id is not declared for this asset need" {
		t.Fatalf("op_id error = %q", err.Error())
	}

	payload := testToolResultPayload()
	response, err := fixture.manager.SubmitToolResult(context.Background(), fixture.toolResultParams(runID, "idempotency-key-0003", payload))
	if err != nil {
		t.Fatalf("submit tool result: %v", err)
	}
	if response["accepted"] != true {
		t.Fatalf("tool result response = %#v", response)
	}
	if fixture.record(t, runID).Status != runstore.RunStatusAwaitingToolResult {
		t.Fatalf("programs started before every context was submitted: %s", fixture.record(t, runID).Status)
	}

	contexts := ProgramContextsParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		ExpectedProjectEpoch: testProjectEpoch,
		TargetIndex:          0,
		Target:               testProgramContext(),
		Provider:             fixture.provider,
		Semaphore:            fixture.semaphore(),
		Orchestrator:         fixture.orchestrator,
	}
	contextResponse, err := fixture.manager.SubmitProgramContexts(context.Background(), contexts)
	if err != nil {
		t.Fatalf("submit program contexts: %v", err)
	}
	if contextResponse["status"] != string(runstore.RunStatusGeneratingPrograms) {
		t.Fatalf("program context response status = %v", contextResponse["status"])
	}

	record := fixture.waitForTerminal(t, runID, 20*time.Second)
	if record.Status != runstore.RunStatusReadyToCommit {
		t.Fatalf("final status = %s, events = %v", record.Status, eventPairs(fixture.events(t, runID)))
	}
	wantEvents := []string{
		"snapshotting/phase_started",
		"specifying_task/phase_started",
		"blueprinting/phase_started",
		"planning_manifest/phase_started",
		"awaiting_tool_result/asset_resolution_required",
		"awaiting_tool_result/tool_result_received",
		// _start_programs_if_ready writes the transition event even though the
		// orchestrator then repeats phase_start for the program phase, exactly
		// like RunStore.transition in Python.
		"generating_programs/phase_started",
		"generating_programs/phase_started",
		"generating_programs/program_target_started",
		"program/cache_hit",
		"program/metadata",
		"program/shared_state",
		"generating_programs/program_script_ready",
		"generating_programs/program_target_ready",
		"ready_to_commit/generation_complete",
	}
	if got := eventPairs(fixture.events(t, runID)); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("event sequence = %v", got)
	}
	events := fixture.events(t, runID)
	toolResultEvent := findEvent(t, events, "awaiting_tool_result", "tool_result_received")
	if toolResultEvent.Attempt != 1 {
		t.Fatalf("tool result attempt = %d", toolResultEvent.Attempt)
	}
	if toolResultEvent.Payload["need_id"] != "need_meow" || toolResultEvent.Payload["op_id"] != "op_meow" {
		t.Fatalf("tool result payload = %#v", toolResultEvent.Payload)
	}
	complete := events[len(events)-1]
	if complete.Payload["scope"] != "project" {
		t.Fatalf("generation_complete payload = %#v", complete.Payload)
	}
	if count, ok := pythonInt(complete.Payload["program_count"]); !ok || count != 1 {
		t.Fatalf("generation_complete payload = %#v", complete.Payload)
	}
	programTargetStarted := findEvent(t, events, "generating_programs", "program_target_started")
	if programTargetStarted.Payload["job_id"] != "job_move" {
		t.Fatalf("program_target_started payload = %#v", programTargetStarted.Payload)
	}
	if index, ok := pythonInt(programTargetStarted.Payload["target_index"]); !ok || index != 0 {
		t.Fatalf("program_target_started payload = %#v", programTargetStarted.Payload)
	}

	// Idempotent replay of the tool result: same response, no new event, and no
	// second program launch.
	before := len(fixture.events(t, runID))
	replayed, err := fixture.manager.SubmitToolResult(context.Background(), fixture.toolResultParams(runID, "idempotency-key-0003", payload))
	if err != nil {
		t.Fatalf("replay tool result: %v", err)
	}
	if !sameJSON(t, replayed, response) {
		t.Fatalf("replayed response = %#v, first = %#v", replayed, response)
	}
	if after := len(fixture.events(t, runID)); after != before {
		t.Fatalf("replay appended events: %d -> %d", before, after)
	}

	// The same idempotency key with a different payload is a conflict.
	conflict := testToolResultPayload()
	conflict["normalized_name"] = "Other"
	if _, err := fixture.manager.SubmitToolResult(context.Background(), fixture.toolResultParams(runID, "idempotency-key-0003", conflict)); err == nil {
		t.Fatal("conflicting replay was accepted")
	} else {
		var conflictErr *runstore.IdempotencyConflictError
		if !errors.As(err, &conflictErr) {
			t.Fatalf("conflict error = %T %v", err, err)
		}
	}

	// The program task drops the transient contexts in its finally block, so a
	// context submitted after the run moved on no longer matches the retained
	// state and is rejected, exactly like the Python manager.
	if _, err := fixture.manager.SubmitProgramContexts(context.Background(), contexts); err == nil {
		t.Fatal("program context was accepted after the run moved on")
	} else if err.Error() != "program contexts require run status awaiting_tool_result" {
		t.Fatalf("program context error = %q", err.Error())
	}
}

// TestProgramContextsConflictAndStaleEpoch covers the remaining context guards.
func TestProgramContextsConflictAndStaleEpoch(t *testing.T) {
	fixture := newTestManager(t)
	request := testUnifiedRequest()
	summary := manifestArtifactSummary(t, fixture)
	// Two manifest targets keep the run parked on the tool-result boundary, so
	// a submitted context stays transient and can be compared.
	manifestSummary, ok := summary["manifest_summary"].(map[string]any)
	if !ok {
		t.Fatalf("manifest_summary = %#v", summary["manifest_summary"])
	}
	manifestSummary["program_target_count"] = 2
	runID := seedAwaitingRun(t, fixture, summary, request)

	params := ProgramContextsParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		ExpectedProjectEpoch: testProjectEpoch,
		TargetIndex:          0,
		Target:               testProgramContext(),
		Provider:             fixture.provider,
		Semaphore:            fixture.semaphore(),
		Orchestrator:         fixture.orchestrator,
	}
	response, err := fixture.manager.SubmitProgramContexts(context.Background(), params)
	if err != nil {
		t.Fatalf("submit program contexts: %v", err)
	}
	if response["status"] != string(runstore.RunStatusAwaitingToolResult) {
		t.Fatalf("status = %v", response["status"])
	}

	same, err := fixture.manager.SubmitProgramContexts(context.Background(), params)
	if err != nil {
		t.Fatalf("resubmit identical context: %v", err)
	}
	if same["status"] != string(runstore.RunStatusAwaitingToolResult) {
		t.Fatalf("status = %v", same["status"])
	}

	different := params
	different.Target = schema.TargetContext{Name: "Stage", IsStage: true, Mode: "append"}
	if _, err := fixture.manager.SubmitProgramContexts(context.Background(), different); err == nil {
		t.Fatal("different context content was accepted")
	} else if err.Error() != "program contexts were already submitted with different content" {
		t.Fatalf("context conflict error = %q", err.Error())
	}

	outOfRange := params
	outOfRange.TargetIndex = 5
	if _, err := fixture.manager.SubmitProgramContexts(context.Background(), outOfRange); err == nil {
		t.Fatal("out of range target_index was accepted")
	} else if err.Error() != "program context target_index is outside manifest targets" {
		t.Fatalf("range error = %q", err.Error())
	}

	stale := params
	stale.ExpectedProjectEpoch = testProjectEpoch + 1
	if _, err := fixture.manager.SubmitProgramContexts(context.Background(), stale); err == nil {
		t.Fatal("stale project epoch was accepted")
	} else if err.Error() != "program contexts use a stale project epoch" {
		t.Fatalf("epoch error = %q", err.Error())
	}
}

// TestSubmitDecisionValidation covers the decision need_id guard and receipt.
func TestSubmitDecisionValidation(t *testing.T) {
	fixture := newTestManager(t)
	request := testUnifiedRequest()
	summary := manifestArtifactSummary(t, fixture)
	runID := seedAwaitingRun(t, fixture, summary, request)

	if _, err := fixture.manager.SubmitDecision(context.Background(), DecisionParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		IdempotencyKey:       "decision-key-000001",
		ExpectedProjectEpoch: testProjectEpoch,
		Payload:              map[string]any{"decision_kind": "generation_consent", "need_id": "need_unknown", "approved": true},
	}); err == nil {
		t.Fatal("undeclared decision need_id was accepted")
	} else if err.Error() != "decision need_id is not declared by the Blueprint" {
		t.Fatalf("decision error = %q", err.Error())
	}

	payload := map[string]any{"decision_kind": "generation_consent", "need_id": "need_meow", "approved": true}
	response, err := fixture.manager.SubmitDecision(context.Background(), DecisionParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		IdempotencyKey:       "decision-key-000002",
		ExpectedProjectEpoch: testProjectEpoch,
		Payload:              payload,
	})
	if err != nil {
		t.Fatalf("submit decision: %v", err)
	}
	if response["accepted"] != true {
		t.Fatalf("decision response = %#v", response)
	}
	events := fixture.events(t, runID)
	last := events[len(events)-1]
	if last.Phase != "awaiting_tool_result" || last.Type != "decision_received" {
		t.Fatalf("decision event = %s/%s", last.Phase, last.Type)
	}
	if last.Payload["need_id"] != "need_meow" {
		t.Fatalf("decision payload = %#v", last.Payload)
	}
}

// TestCancelStopsGenerationAndReplays covers cancellation and its idempotency.
func TestCancelStopsGenerationAndReplays(t *testing.T) {
	fixture := newTestManager(t)
	block := make(chan struct{})
	fixture.fixture.setBlock(block)
	defer close(block)

	runID := fixture.startRun(t, testUnifiedRequest())
	fixture.waitForStatus(t, runID, runstore.RunStatusSpecifyingTask, 10*time.Second)

	response, err := fixture.manager.Cancel(context.Background(), CancelParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		ExpectedProjectEpoch: testProjectEpoch,
		IdempotencyKey:       "cancel-key-00000001",
	})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if response["status"] != string(runstore.RunStatusCancelled) {
		t.Fatalf("cancel response = %#v", response)
	}
	if response["terminal"] != true {
		t.Fatalf("cancelled run is not terminal: %#v", response)
	}
	events := fixture.events(t, runID)
	last := events[len(events)-1]
	if last.Phase != "cancelled" || last.Type != "generation_error" {
		t.Fatalf("cancellation event = %s/%s", last.Phase, last.Type)
	}
	if last.Payload["code"] != "RUN_CANCELLED" || last.Payload["message"] != "用户已取消本次生成" {
		t.Fatalf("cancellation payload = %#v", last.Payload)
	}

	// The background task must not write a second terminal state.
	time.Sleep(150 * time.Millisecond)
	record := fixture.record(t, runID)
	if record.Status != runstore.RunStatusCancelled {
		t.Fatalf("run status after cancel = %s", record.Status)
	}
	for _, event := range fixture.events(t, runID) {
		if event.Phase == "failed" {
			t.Fatalf("cancel wrote a failure event: %#v", event)
		}
	}

	countBefore := len(fixture.events(t, runID))
	replayed, err := fixture.manager.Cancel(context.Background(), CancelParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		ExpectedProjectEpoch: testProjectEpoch,
		IdempotencyKey:       "cancel-key-00000001",
	})
	if err != nil {
		t.Fatalf("replay cancel: %v", err)
	}
	if !sameJSON(t, replayed, response) {
		t.Fatalf("replayed cancel = %#v, first = %#v", replayed, response)
	}
	if countAfter := len(fixture.events(t, runID)); countAfter != countBefore {
		t.Fatalf("cancel replay appended events: %d -> %d", countBefore, countAfter)
	}

	// Cancelling a run that is already terminal is idempotent and writes no
	// further event even with a fresh key.
	second, err := fixture.manager.Cancel(context.Background(), CancelParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		ExpectedProjectEpoch: testProjectEpoch,
		IdempotencyKey:       "cancel-key-00000002",
	})
	if err != nil {
		t.Fatalf("second cancel: %v", err)
	}
	if second["status"] != string(runstore.RunStatusCancelled) {
		t.Fatalf("second cancel status = %v", second["status"])
	}
	if countAfter := len(fixture.events(t, runID)); countAfter != countBefore {
		t.Fatalf("second cancel appended events: %d -> %d", countBefore, countAfter)
	}
}

// TestSSEEventsReplayAndKeepalive covers the sse_events generator.
func TestSSEEventsReplayAndKeepalive(t *testing.T) {
	fixture := newTestManager(t)
	request := testUnifiedRequest()
	summary := manifestArtifactSummary(t, fixture)
	runID := seedAwaitingRun(t, fixture, summary, request)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := SSEEvents(ctx, fixture.manager, runID, testOwnerScopeHash, 0)

	// A pending wakeup yields a keepalive immediately after the replay.
	fixture.manager.signal(runID)

	collected := []*runstore.RunEvent{}
	keepalives := 0
	deadline := time.After(10 * time.Second)
	for keepalives == 0 {
		select {
		case event := <-events:
			if event == nil {
				keepalives++
				continue
			}
			collected = append(collected, event)
		case err := <-errs:
			t.Fatalf("sse error: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for the keepalive")
		}
	}
	if len(collected) != 5 {
		t.Fatalf("replayed %d events, want 5", len(collected))
	}
	if collected[0].Phase != "snapshotting" || collected[4].Phase != "awaiting_tool_result" {
		t.Fatalf("replay order = %v", eventPairs([]runstore.RunEvent{*collected[0], *collected[4]}))
	}
	cancel()

	// A terminal run closes the stream without a keepalive.
	if _, err := fixture.manager.Cancel(context.Background(), CancelParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		ExpectedProjectEpoch: testProjectEpoch,
		IdempotencyKey:       "cancel-key-00000003",
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	terminalEvents, terminalErrs := SSEEvents(context.Background(), fixture.manager, runID, testOwnerScopeHash, 0)
	count := 0
	for event := range terminalEvents {
		if event == nil {
			t.Fatal("terminal stream emitted a keepalive")
		}
		count++
	}
	if err := <-terminalErrs; err != nil {
		t.Fatalf("terminal stream error: %v", err)
	}
	if count != 6 {
		t.Fatalf("terminal replay returned %d events, want 6", count)
	}

	// Replay from a cursor skips earlier events on a terminal run.
	cursorEvents, cursorErrs := SSEEvents(context.Background(), fixture.manager, runID, testOwnerScopeHash, collected[1].Sequence)
	replayed := []runstore.RunEvent{}
	for event := range cursorEvents {
		if event == nil {
			t.Fatal("terminal cursor stream emitted a keepalive")
		}
		replayed = append(replayed, *event)
	}
	if err := <-cursorErrs; err != nil {
		t.Fatalf("cursor replay error: %v", err)
	}
	if len(replayed) != 4 {
		t.Fatalf("cursor replay returned %d events, want 4", len(replayed))
	}
	if replayed[0].Sequence <= collected[1].Sequence {
		t.Fatalf("cursor replay returned sequence %d", replayed[0].Sequence)
	}

	// An unknown run reports the store error on the error channel.
	missingEvents, missingErrs := SSEEvents(context.Background(), fixture.manager, "run_missing", testOwnerScopeHash, 0)
	for range missingEvents {
	}
	var notFound *runstore.RunNotFoundError
	if err := <-missingErrs; !errors.As(err, &notFound) {
		t.Fatalf("missing run error = %v", err)
	}
}

// TestProgramGenerationLayers ports the Python layering unit test.
func TestProgramGenerationLayers(t *testing.T) {
	blueprint := &schema.ProjectBlueprint{
		BehaviorGraph: []schema.BlueprintBehaviorGraphNode{
			{BehaviorID: "b_cat", OwnerEntityID: "cat", DependsOn: []string{"b_milk"}},
			{BehaviorID: "b_milk", OwnerEntityID: "milk", DependsOn: []string{"b_cat"}},
		},
	}
	manifest := &schema.ProjectManifest{
		ProgramTargets: []schema.ProjectProgramTarget{
			{JobID: "job_cat", BehaviorIDs: []string{"b_cat"}},
			{JobID: "job_milk", BehaviorIDs: []string{"b_milk"}},
		},
	}
	layers, err := programGenerationLayers(blueprint, manifest)
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	flattened := []int{}
	for _, layer := range layers {
		flattened = append(flattened, layer...)
	}
	if !reflect.DeepEqual(flattened, []int{0, 1}) {
		t.Fatalf("self-healed layers = %v", layers)
	}

	// A behavior assigned to two targets is rejected.
	duplicate := &schema.ProjectManifest{
		ProgramTargets: []schema.ProjectProgramTarget{
			{JobID: "job_cat", BehaviorIDs: []string{"b_cat"}},
			{JobID: "job_milk", BehaviorIDs: []string{"b_cat"}},
		},
	}
	if _, err := programGenerationLayers(blueprint, duplicate); err == nil {
		t.Fatal("duplicate behavior assignment was accepted")
	} else if err.Error() != "behavior is assigned to multiple program targets: b_cat" {
		t.Fatalf("duplicate error = %q", err.Error())
	}

	// A dependency without a program target is rejected.
	missing := &schema.ProjectManifest{
		ProgramTargets: []schema.ProjectProgramTarget{
			{JobID: "job_cat", BehaviorIDs: []string{"b_cat"}},
		},
	}
	if _, err := programGenerationLayers(blueprint, missing); err == nil {
		t.Fatal("unassigned dependency was accepted")
	} else if err.Error() != "Blueprint dependency has no program target: b_milk" {
		t.Fatalf("missing dependency error = %q", err.Error())
	}

	// A behavior without a program target is rejected.
	unassigned := &schema.ProjectBlueprint{
		BehaviorGraph: []schema.BlueprintBehaviorGraphNode{
			{BehaviorID: "b_extra", OwnerEntityID: "cat"},
		},
	}
	assigned := &schema.ProjectManifest{
		ProgramTargets: []schema.ProjectProgramTarget{
			{JobID: "job_cat", BehaviorIDs: []string{"b_cat"}},
		},
	}
	if _, err := programGenerationLayers(unassigned, assigned); err == nil {
		t.Fatal("unassigned behavior was accepted")
	} else if err.Error() != "Blueprint behavior has no program target: b_extra" {
		t.Fatalf("missing behavior error = %q", err.Error())
	}

	// Dependencies order the layers.
	acyclic := &schema.ProjectBlueprint{
		BehaviorGraph: []schema.BlueprintBehaviorGraphNode{
			{BehaviorID: "b_cat", OwnerEntityID: "cat"},
			{BehaviorID: "b_milk", OwnerEntityID: "milk", DependsOn: []string{"b_cat"}},
		},
	}
	ordered, err := programGenerationLayers(acyclic, manifest)
	if err != nil {
		t.Fatalf("ordered layers: %v", err)
	}
	if !reflect.DeepEqual(ordered, [][]int{{0}, {1}}) {
		t.Fatalf("dependency layers = %v", ordered)
	}
}

// TestWaitForEvents covers the asyncio.Event analogue.
func TestWaitForEvents(t *testing.T) {
	manager := NewAiRunManager(nil, nil)
	done := make(chan bool, 1)
	go func() {
		manager.WaitForEvents("run-1", 5)
		done <- true
	}()
	time.Sleep(20 * time.Millisecond)
	manager.signal("run-1")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not wake on signal")
	}

	// A signal raised before the wait returns immediately.
	manager.signal("run-2")
	start := time.Now()
	manager.WaitForEvents("run-2", 5)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("pending signal waited %s", elapsed)
	}

	// A missed signal times out.
	start = time.Now()
	manager.WaitForEvents("run-3", 0.05)
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("timeout wait returned after %s", elapsed)
	}
}
