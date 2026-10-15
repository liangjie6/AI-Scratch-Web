package runmanager

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/runstore"
)

// TestProviderTimeoutFailsRun covers the AI_PROVIDER_TIMEOUT branch.
func TestProviderTimeoutFailsRun(t *testing.T) {
	fixture := newTestManager(t)
	block := make(chan struct{})
	fixture.fixture.setBlock(block)
	defer close(block)
	fixture.settings.AIBlocksProviderTimeoutSeconds = 0.2

	runID := fixture.startRun(t, testUnifiedRequest())
	record := fixture.waitForTerminal(t, runID, 10*time.Second)
	if record.Status != runstore.RunStatusFailed {
		t.Fatalf("status = %s", record.Status)
	}
	event := findEvent(t, fixture.events(t, runID), "failed", "generation_error")
	if event.Payload["code"] != "AI_PROVIDER_TIMEOUT" {
		t.Fatalf("code = %v", event.Payload["code"])
	}
	if event.Payload["message"] != "AI 生成超时，请稍后重试" {
		t.Fatalf("message = %v", event.Payload["message"])
	}
}

// TestProviderErrorFailsRun covers the AI_PROVIDER_ERROR branch.
func TestProviderErrorFailsRun(t *testing.T) {
	fixture := newTestManager(t)
	fixture.fixture.setFailStatus(http.StatusInternalServerError)

	runID := fixture.startRun(t, testUnifiedRequest())
	record := fixture.waitForTerminal(t, runID, 10*time.Second)
	if record.Status != runstore.RunStatusFailed {
		t.Fatalf("status = %s", record.Status)
	}
	event := findEvent(t, fixture.events(t, runID), "failed", "generation_error")
	if event.Payload["code"] != "AI_PROVIDER_ERROR" {
		t.Fatalf("code = %v", event.Payload["code"])
	}
	if event.Payload["message"] != "AI 生成暂时不可用，请稍后重试" {
		t.Fatalf("message = %v", event.Payload["message"])
	}
}

// TestMissingProgramContractsFailsRun covers the generic AI_GENERATION_ERROR
// branch of _execute_programs.
func TestMissingProgramContractsFailsRun(t *testing.T) {
	fixture := newTestManager(t)
	request := testUnifiedRequest()
	summary := map[string]any{
		"scope":                    "project",
		"output_contract":          "project_manifest",
		"project_id":               "project-1",
		"model_tier":               "normal",
		"contracts":                map[string]any{"requirements": "not a contract"},
		"asset_need_ids":           []any{"need_meow"},
		"asset_need_ops":           map[string]any{"need_meow": []any{"op_meow"}},
		"manifest_summary":         map[string]any{"program_target_count": 1, "resource_op_count": 1},
		"blueprint_asset_need_ids": []any{"need_meow"},
	}
	runID := seedAwaitingRun(t, fixture, summary, request)
	if _, err := fixture.manager.SubmitToolResult(context.Background(), fixture.toolResultParams(runID, "idempotency-key-9001", testToolResultPayload())); err != nil {
		t.Fatalf("tool result: %v", err)
	}
	if _, err := fixture.manager.SubmitProgramContexts(context.Background(), ProgramContextsParams{
		RunID:                runID,
		OwnerScopeHash:       testOwnerScopeHash,
		ExpectedProjectEpoch: testProjectEpoch,
		TargetIndex:          0,
		Target:               testProgramContext(),
		Provider:             fixture.provider,
		Semaphore:            fixture.semaphore(),
		Orchestrator:         fixture.orchestrator,
	}); err != nil {
		t.Fatalf("program contexts: %v", err)
	}
	record := fixture.waitForTerminal(t, runID, 10*time.Second)
	if record.Status != runstore.RunStatusFailed {
		t.Fatalf("status = %s", record.Status)
	}
	event := findEvent(t, fixture.events(t, runID), "failed", "generation_error")
	if event.Payload["code"] != "AI_GENERATION_ERROR" {
		t.Fatalf("code = %v", event.Payload["code"])
	}
	if event.Payload["message"] != "AI 输出未完成，请重新生成" {
		t.Fatalf("message = %v", event.Payload["message"])
	}
}

// TestIngestProtocolErrorsAndRepair drives _ingest_generation_event directly for
// the branches a real orchestrator only reaches on a broken stream.
func TestIngestProtocolErrorsAndRepair(t *testing.T) {
	t.Run("program_target_ready_without_job", func(t *testing.T) {
		fixture := newTestManager(t)
		runID := seedAwaitingRun(t, fixture, seededSummary(t, fixture), testUnifiedRequest())
		if err := fixture.manager.transition(runID, testOwnerScopeHash, runstore.RunStatusGeneratingPrograms, "generating_programs", "phase_started", map[string]any{}); err != nil {
			t.Fatalf("transition: %v", err)
		}
		_, err := fixture.manager.ingestGenerationEvent(context.Background(), runID, testOwnerScopeHash, orchestration.GenerationEvent{
			Type: "phase_complete", Phase: "program", Data: map[string]any{"target_index": 7, "script_count": 1},
		}, map[int]string{0: "job_move"}, fixture.provider, nil, fixture.orchestrator, false)
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		event := findEvent(t, fixture.events(t, runID), "failed", "generation_error")
		if event.Payload["code"] != "RUN_PROTOCOL_ERROR" || event.Payload["message"] != "程序事件缺少已声明的 job_id" {
			t.Fatalf("payload = %#v", event.Payload)
		}
	})

	t.Run("script_ready_without_job", func(t *testing.T) {
		fixture := newTestManager(t)
		runID := seedAwaitingRun(t, fixture, seededSummary(t, fixture), testUnifiedRequest())
		if err := fixture.manager.transition(runID, testOwnerScopeHash, runstore.RunStatusGeneratingPrograms, "generating_programs", "phase_started", map[string]any{}); err != nil {
			t.Fatalf("transition: %v", err)
		}
		_, err := fixture.manager.ingestGenerationEvent(context.Background(), runID, testOwnerScopeHash, orchestration.GenerationEvent{
			Type: "script_ready", Phase: "program", Data: map[string]any{"target_index": 7},
		}, map[int]string{0: "job_move"}, fixture.provider, nil, fixture.orchestrator, false)
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		event := findEvent(t, fixture.events(t, runID), "failed", "generation_error")
		if event.Payload["code"] != "RUN_PROTOCOL_ERROR" || event.Payload["message"] != "脚本事件缺少已声明的 job_id" {
			t.Fatalf("payload = %#v", event.Payload)
		}
	})

	t.Run("program_error_event_raises_into_gather", func(t *testing.T) {
		fixture := newTestManager(t)
		runID := seedAwaitingRun(t, fixture, seededSummary(t, fixture), testUnifiedRequest())
		if err := fixture.manager.transition(runID, testOwnerScopeHash, runstore.RunStatusGeneratingPrograms, "generating_programs", "phase_started", map[string]any{}); err != nil {
			t.Fatalf("transition: %v", err)
		}
		_, err := fixture.manager.ingestGenerationEvent(context.Background(), runID, testOwnerScopeHash, orchestration.GenerationEvent{
			Type: "error", Phase: "program", Data: map[string]any{"code": "PROGRAM_INVALID", "message": "自定义错误"},
		}, map[int]string{0: "job_move"}, fixture.provider, nil, fixture.orchestrator, false)
		if err == nil || err.Error() != "项目目标程序生成失败" {
			t.Fatalf("ingest error = %v", err)
		}
		event := findEvent(t, fixture.events(t, runID), "failed", "generation_error")
		if event.Payload["code"] != "PROGRAM_INVALID" || event.Payload["message"] != "自定义错误" {
			t.Fatalf("payload = %#v", event.Payload)
		}
	})

	t.Run("repair_attempt_and_phase_completed_fallback", func(t *testing.T) {
		fixture := newTestManager(t)
		runID := seedAwaitingRun(t, fixture, seededSummary(t, fixture), testUnifiedRequest())
		if err := fixture.manager.transition(runID, testOwnerScopeHash, runstore.RunStatusGeneratingPrograms, "generating_programs", "phase_started", map[string]any{}); err != nil {
			t.Fatalf("transition: %v", err)
		}
		if _, err := fixture.manager.ingestGenerationEvent(context.Background(), runID, testOwnerScopeHash, orchestration.GenerationEvent{
			Type: "repair", Phase: "", Data: map[string]any{"attempt": 2},
		}, map[int]string{0: "job_move"}, fixture.provider, nil, fixture.orchestrator, false); err != nil {
			t.Fatalf("repair ingest: %v", err)
		}
		if _, err := fixture.manager.ingestGenerationEvent(context.Background(), runID, testOwnerScopeHash, orchestration.GenerationEvent{
			Type: "phase_complete", Phase: "custom_phase", Data: map[string]any{"value": 1},
		}, map[int]string{0: "job_move"}, fixture.provider, nil, fixture.orchestrator, false); err != nil {
			t.Fatalf("phase_complete ingest: %v", err)
		}
		if _, err := fixture.manager.ingestGenerationEvent(context.Background(), runID, testOwnerScopeHash, orchestration.GenerationEvent{
			Type: "content_delta", Data: map[string]any{"text": "secret provider text"},
		}, map[int]string{0: "job_move"}, fixture.provider, nil, fixture.orchestrator, false); err != nil {
			t.Fatalf("content_delta ingest: %v", err)
		}
		events := fixture.events(t, runID)
		repair := findEvent(t, events, "generating_programs", "repair_started")
		if repair.Attempt != 2 {
			t.Fatalf("repair attempt = %d", repair.Attempt)
		}
		completed := findEvent(t, events, "custom_phase", "phase_completed")
		if count, ok := pythonInt(completed.Payload["value"]); !ok || count != 1 {
			t.Fatalf("phase_completed payload = %#v", completed.Payload)
		}
		for _, event := range events {
			if event.Type == "content_delta" {
				t.Fatalf("provider text was persisted: %#v", event)
			}
		}
	})
}

// seededSummary builds an artifact summary whose program phase cannot decode,
// used only by the ingestion unit tests.
func seededSummary(t *testing.T, fixture *testManager) map[string]any {
	t.Helper()
	return map[string]any{
		"scope":                    "project",
		"output_contract":          "project_manifest",
		"project_id":               "project-1",
		"model_tier":               "normal",
		"asset_need_ids":           []any{"need_meow"},
		"asset_need_ops":           map[string]any{"need_meow": []any{"op_meow"}},
		"manifest_summary":         map[string]any{"program_target_count": 1, "resource_op_count": 1},
		"blueprint_asset_need_ids": []any{"need_meow"},
	}
}

// TestShutdownCancelsLiveTasks covers shutdown.
func TestShutdownCancelsLiveTasks(t *testing.T) {
	fixture := newTestManager(t)
	block := make(chan struct{})
	fixture.fixture.setBlock(block)
	defer close(block)

	runID := fixture.startRun(t, testUnifiedRequest())
	fixture.waitForStatus(t, runID, runstore.RunStatusSpecifyingTask, 10*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		fixture.manager.Shutdown(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown did not return")
	}

	// A shutdown cancels tasks without writing a terminal state, exactly like
	// Python's task.cancel() path.
	time.Sleep(100 * time.Millisecond)
	record := fixture.record(t, runID)
	if runstore.IsTerminal(record.Status) {
		t.Fatalf("shutdown terminalized the run: %s", record.Status)
	}
	for _, event := range fixture.events(t, runID) {
		if event.Phase == "failed" {
			t.Fatalf("shutdown wrote a failure event: %#v", event)
		}
	}
	if tasks := fixture.managerTaskCount(); tasks != 0 {
		t.Fatalf("manager still tracks %d tasks", tasks)
	}
}

func (m *testManager) managerTaskCount() int {
	m.manager.guard.Lock()
	defer m.manager.guard.Unlock()
	return len(m.manager.tasks)
}

// TestStartRejectsNonProjectScope covers the Python ValueError guard.
func TestStartRejectsNonProjectScope(t *testing.T) {
	fixture := newTestManager(t)
	request := testUnifiedRequest()
	request.Scope = "single_sprite"
	request.ProjectContext = nil
	if _, err := fixture.manager.Start(context.Background(), fixture.startParams(request)); err == nil {
		t.Fatal("single_sprite request was accepted")
	} else if err.Error() != "durable runs currently require project scope and project_context" {
		t.Fatalf("start error = %q", err.Error())
	}

	request = testUnifiedRequest()
	request.ProjectContext = nil
	if _, err := fixture.manager.Start(context.Background(), fixture.startParams(request)); err == nil {
		t.Fatal("request without project_context was accepted")
	}
}

// TestStartPayloadDoesNotLeakContracts keeps the public payload sanitized.
func TestStartPayloadDoesNotLeakContracts(t *testing.T) {
	fixture := newTestManager(t)
	response, err := fixture.manager.Start(context.Background(), fixture.startParams(testUnifiedRequest()))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, leaked := response["artifact_summary"].(map[string]any)["contracts"]; leaked {
		t.Fatalf("start payload leaked contracts: %#v", response)
	}
	if got := response["protocol_version"]; got != runstore.RunProtocolVersion {
		t.Fatalf("protocol_version = %v", got)
	}
}
