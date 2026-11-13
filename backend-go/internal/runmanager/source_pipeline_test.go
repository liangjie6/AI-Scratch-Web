package runmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-scratch-web/backend/internal/authoring"
	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/prompt"
	"github.com/ai-scratch-web/backend/internal/runstore"
)

func newSourceManager(t *testing.T) *testManager {
	t.Helper()
	fixture := newTestManager(t)
	fixture.settings.AIBlocksSourcePipelineEnabled = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Logf("source provider request parse: %v", err)
			http.Error(w, err.Error(), 400)
			return
		}
		if len(payload.Messages) < 2 {
			t.Logf("source provider missing messages: %#v", payload)
			http.Error(w, "missing messages", 400)
			return
		}
		var user map[string]json.RawMessage
		if err := json.NewDecoder(strings.NewReader(payload.Messages[len(payload.Messages)-1].Content)).Decode(&user); err != nil {
			t.Logf("source provider request parse: %v", err)
			http.Error(w, err.Error(), 400)
			return
		}
		output := ""
		if slotRaw, ok := user["slot"]; ok {
			var slot authoring.FrozenSlot
			_ = json.Unmarshal(slotRaw, &slot)
			output = `{"slot_id":"` + slot.Key + `","status":"ready","body":[{"opcode":"motion_changexby","inputs":{"DX":10}}],"revision":null}`
		} else {
			spec := authoring.IntentSpec{SchemaVersion: 1, Title: "移动", Goal: "点击绿旗走十步", Targets: []authoring.IntentTarget{{Key: "current", Name: "Sprite1", Kind: "sprite", Disposition: "current"}}, States: []authoring.IntentState{}, Assets: []authoring.IntentAsset{}, ScriptSlots: []authoring.IntentSlot{{Key: "move", Owner: "current", Trigger: authoring.Trigger{Kind: "green_flag"}, Purpose: "移动十步", Reads: []string{}, Writes: []string{}, Emits: []string{}, Receives: []string{}, Assets: []string{}}}, RequiredExtensions: []string{}, Assumptions: []string{}, SuccessCriteria: []string{"向右移动"}, MissingQuestions: []string{}}
			raw, _ := json.Marshal(spec)
			output = string(raw)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": output}}}}))
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 20, "total_tokens": 32}}))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	fixture.settings.AgnesAPIBase = server.URL
	fixture.provider.Client = server.Client()
	return fixture
}

func TestSourceDurableRunPinsContractAndRestoresBudgetAndRequest(t *testing.T) {
	fixture := newSourceManager(t)
	request := testUnifiedRequest()
	request.PipelineVersion = authoring.PipelineVersion
	runID := fixture.startRun(t, request)
	record := waitSourcePlan(t, fixture, runID)
	contracts, ok := jsonObjectValue(record.ArtifactSummary["contracts"])
	if !ok {
		t.Fatal("contracts missing")
	}
	for _, name := range []string{"source_contract", "source_request", "blueprint", "manifest", "plan"} {
		if contracts[name] == nil {
			t.Fatalf("missing pinned %s", name)
		}
	}
	if _, legacy := contracts["task_spec"]; legacy {
		t.Fatal("source run invented a legacy TaskSpec")
	}
	if record.ArtifactSummary["pipeline_version"] != authoring.PipelineVersion || record.ArtifactSummary["catalog_hash"] != authoring.CatalogFingerprint() {
		t.Fatal("versions were not pinned")
	}
	if record.ArtifactSummary["source_budget"] == nil {
		t.Fatal("source budget was not persisted")
	}
	if _, leaked := record.PublicPayload()["artifact_summary"].(map[string]any)["contracts"]; leaked {
		t.Fatal("private source snapshot leaked")
	}
	// Simulate fresh manager/orchestrator instances against the same durable store.
	fixture.manager = NewAiRunManager(fixture.store, fixture.settings)
	fixture.orchestrator = orchestration.NewGenerationOrchestrator(prompt.NewPromptBuilder(nil, nil, map[string]bool{}, false), 0, 2)
	_, err := fixture.manager.SubmitProgramContexts(context.Background(), ProgramContextsParams{RunID: runID, OwnerScopeHash: testOwnerScopeHash, ExpectedProjectEpoch: testProjectEpoch, TargetIndex: 0, Target: testProgramContext(), Provider: fixture.provider, Semaphore: fixture.semaphore(), Orchestrator: fixture.orchestrator})
	if err != nil {
		t.Fatal(err)
	}
	finished := fixture.waitForTerminal(t, runID, 10*time.Second)
	if finished.Status != runstore.RunStatusReadyToCommit {
		t.Fatalf("source run did not complete: %s %#v", finished.Status, fixture.events(t, runID))
	}
	c, err := decodeSourceContract(contracts["source_contract"])
	if err != nil {
		t.Fatal(err)
	}
	budget := fixture.orchestrator.SnapshotSourceBudget(request, c.Hash)
	if budget.Calls != 2 || budget.Tokens < 64 {
		t.Fatalf("planning budget was reset after recovery: %#v", budget)
	}
	// The READY event is stored just before the task's deferred cleanup runs.
	// Observe the cleanup under the manager lock rather than racing its maps.
	fixture.waitForTaskCleanup(t, runID)
	found := false
	for _, event := range fixture.events(t, runID) {
		if event.Type == "program_script_ready" {
			found = true
		}
	}
	if !found {
		t.Fatal("normalized scripts were not durably streamed")
	}
}

func TestSourceDurableRejectsTamperedProjection(t *testing.T) {
	fixture := newSourceManager(t)
	request := testUnifiedRequest()
	request.PipelineVersion = authoring.PipelineVersion
	runID := fixture.startRun(t, request)
	record := waitSourcePlan(t, fixture, runID)
	contracts, _ := jsonObjectValue(record.ArtifactSummary["contracts"])
	manifest, _ := jsonObjectValue(contracts["manifest"])
	manifest["summary"] = "client changed projection"
	_, err := fixture.store.MergeArtifactSummary(runstore.MergeArtifactSummaryParams{RunID: runID, OwnerScopeHash: testOwnerScopeHash, Values: map[string]any{"contracts": contracts}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.manager.SubmitProgramContexts(context.Background(), ProgramContextsParams{RunID: runID, OwnerScopeHash: testOwnerScopeHash, ExpectedProjectEpoch: testProjectEpoch, TargetIndex: 0, Target: testProgramContext(), Provider: fixture.provider, Semaphore: fixture.semaphore(), Orchestrator: fixture.orchestrator})
	if err != nil {
		t.Fatal(err)
	}
	record = fixture.waitForTerminal(t, runID, 10*time.Second)
	if record.Status != runstore.RunStatusFailed {
		t.Fatalf("tampered projection accepted: %s", record.Status)
	}
}

func waitSourcePlan(t *testing.T, fixture *testManager, runID string) runstore.RunRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := fixture.record(t, runID)
		if r.Status == runstore.RunStatusAwaitingToolResult {
			return r
		}
		if r.Status == runstore.RunStatusFailed || time.Now().After(deadline) {
			t.Fatalf("source failed: %s %#v", r.Status, fixture.events(t, runID))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
