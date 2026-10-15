package runmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-scratch-web/backend/internal/config"
	"github.com/ai-scratch-web/backend/internal/jsonx"
	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/prompt"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/runstore"
	"github.com/ai-scratch-web/backend/internal/schema"
)

const (
	testOwnerScopeHash = "test-owner-scope-hash"
	testProjectEpoch   = 4
)

func strPtr(value string) *string { return &value }

func testSettings() *config.Settings {
	return &config.Settings{
		AgnesAPIKey:                    "test-key",
		AgnesChatModel:                 "test-model",
		AIBlocksProviderTimeoutSeconds: 20,
		AIBlocksMaxConcurrency:         2,
		AIBlocksMaxRepairRounds:        0,
	}
}

// testProjectContext mirrors the Python project_request_payload fixture.
func testProjectContext() *schema.ProjectContext {
	return &schema.ProjectContext{
		ProjectID: "project-1",
		Version:   testProjectEpoch,
		Targets: []schema.ProjectTargetContext{
			{
				Name: "Stage", IsStage: true, HasScripts: true, Mode: "append",
				Costumes: []string{"backdrop1"}, Backdrops: []string{"backdrop1"},
				Sounds: []string{}, SpriteNames: []string{"Sprite1"},
				CurrentProgram: "", TargetID: "stage-1", Revision: 2,
			},
			{
				Name: "Sprite1", IsStage: false, HasScripts: false, Mode: "append",
				Costumes: []string{"costume1"}, Backdrops: []string{},
				Sounds: []string{}, SpriteNames: []string{"Sprite1"},
				CurrentProgram: "", TargetID: "sprite-1", Revision: 3,
			},
		},
		SharedState: map[string]any{},
	}
}

func testUnifiedRequest() *schema.UnifiedGenerationRequest {
	return &schema.UnifiedGenerationRequest{
		ModelTier: schema.ModelTierNormal,
		SessionID: "session-1",
		RequestID: "request-1",
		Messages:  []schema.ChatMessage{{Role: "user", Content: "让角色走十步"}},
		Target: schema.TargetContext{
			Name: "Sprite1", IsStage: false, HasScripts: false, Mode: "append",
			Costumes: []string{"costume1"}, Backdrops: []string{}, Sounds: []string{},
			SpriteNames: []string{"Sprite1"}, CurrentProgram: "",
		},
		Thinking:            schema.ThinkingSettings{Enabled: false, Strength: schema.ThinkingStrengthLow},
		Scope:               "project",
		AssetPolicy:         "builtin_first",
		ProjectContext:      testProjectContext(),
		ProceedWithDefaults: true,
		PermittedExtensions: []string{},
	}
}

func testRequirements() *schema.RequirementsResponse {
	return &schema.RequirementsResponse{
		Ready:            true,
		Title:            "移动",
		Goal:             "点击绿旗后移动十步",
		BehaviorSteps:    []string{"移动十步"},
		Triggers:         []string{"绿旗"},
		Controls:         []string{},
		SuccessCriteria:  []string{"角色移动"},
		MissingQuestions: []string{},
	}
}

func testTaskSpec() *schema.ScratchTaskSpec {
	return &schema.ScratchTaskSpec{
		SchemaVersion: 2,
		Title:         "移动",
		Goal:          "点击绿旗后移动十步",
		Assumptions:   []schema.TaskAssumption{},
		Entities: []schema.TaskEntity{{
			EntityID: "e_sprite", Role: "sprite", Description: "角色",
			ExistingTargetRef: &schema.TaskTargetRef{Kind: "existing_sprite", Name: strPtr("Sprite1")},
		}},
		State: []schema.TaskState{},
		Behaviors: []schema.TaskBehavior{{
			BehaviorID: "b_move", OwnerEntityID: "e_sprite",
			Trigger:    schema.TriggerSpec{Kind: "green_flag"},
			Conditions: []schema.ConditionSpec{},
			Actions: []schema.ActionSpec{{
				Kind:  "move_steps",
				Steps: &schema.OperandSpec{Kind: "literal", Value: 10},
			}},
			Reads: []string{}, Writes: []string{},
		}},
		SuccessCriteria: []schema.SuccessCriterionSpec{{
			CriterionID: "c_move", Description: "角色移动十步",
			RelatedBehaviorIDs: []string{"b_move"}, Priority: "must", Verification: "manual",
		}},
		Unsupported: []schema.UnsupportedRequirement{},
	}
}

func testBlueprint(t *testing.T, taskSpec *schema.ScratchTaskSpec) *schema.ProjectBlueprint {
	t.Helper()
	hash, err := schema.ContractSHA256(taskSpec)
	if err != nil {
		t.Fatalf("contract hash: %v", err)
	}
	blueprint := &schema.ProjectBlueprint{
		SchemaVersion: 2,
		TaskSpecHash:  hash,
		AssetNeeds: []schema.BlueprintAssetNeed{{
			NeedID: "need_meow", Kind: "sound", Purpose: "叫声",
			SearchTerms: []string{"meow"}, Tags: []string{},
			FallbackOrder: []string{"scratch_library"},
			Constraints:   map[string]any{},
		}},
		EntityBindings: []schema.BlueprintEntityBinding{{
			EntityID: "e_sprite",
			Resolution: schema.BlueprintEntityResolution{
				Kind:      "existing_target",
				TargetRef: &schema.TaskTargetRef{Kind: "existing_sprite", Name: strPtr("Sprite1")},
			},
		}},
		StatePlan: []schema.BlueprintStatePlan{},
		BehaviorGraph: []schema.BlueprintBehaviorGraphNode{{
			BehaviorID: "b_move", OwnerEntityID: "e_sprite",
			DependsOn: []string{}, ProducesBroadcasts: []string{}, ConsumesBroadcasts: []string{},
		}},
		ProgramJobs: []schema.BlueprintProgramJob{{
			JobID: "job_move", OwnerEntityID: "e_sprite", Mode: "append",
			BehaviorIDs: []string{"b_move"},
		}},
		AcceptanceTests: []schema.BlueprintAcceptanceTest{},
	}
	if err := blueprint.Validate(); err != nil {
		t.Fatalf("blueprint fixture invalid: %v", err)
	}
	return blueprint
}

func testPlan() *schema.Plan {
	return &schema.Plan{Steps: []string{"监听绿旗", "移动十步"}}
}

func testManifest(t *testing.T, blueprint *schema.ProjectBlueprint) *schema.ProjectManifest {
	t.Helper()
	manifest := &schema.ProjectManifest{
		SchemaVersion:      3,
		Scope:              "project",
		Summary:            "让角色移动",
		Assumptions:        []string{},
		RequiredExtensions: []string{},
		ResourceOps: []schema.ResourceOp{{
			"op":             "add_sound",
			"op_id":          "op_meow",
			"source_need_id": "need_meow",
			"target":         map[string]any{"kind": "existing_sprite", "name": "Sprite1"},
			"name":           "Meow",
			"source": map[string]any{
				"kind": "scratch_library", "asset_type": "sound", "search_terms": []any{"meow"},
			},
			"tags": []any{},
		}},
		LocalState: []schema.ProjectLocalState{},
		ProgramTargets: []schema.ProjectProgramTarget{{
			JobID: "job_move", BehaviorIDs: []string{"b_move"}, OwnerEntityID: "e_sprite",
			Target: schema.TargetRef{Kind: "existing_sprite", Name: strPtr("Sprite1")},
			Mode:   "append", Intent: "让角色移动",
		}},
		SharedState: schema.ProjectSharedState{
			Variables:  []schema.ProjectVariableDefinition{},
			Lists:      []schema.ProjectListDefinition{},
			Broadcasts: []schema.ProjectBroadcastDefinition{},
		},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest fixture invalid: %v", err)
	}
	if err := schema.ValidateBlueprintManifestLineage(blueprint, manifest); err != nil {
		t.Fatalf("manifest lineage invalid: %v", err)
	}
	return manifest
}

func testProgramMetadata() map[string]any {
	return map[string]any{
		"schema_version":    2,
		"summary":           "绿旗后移动十步",
		"remove_script_ids": []any{},
		"procedures":        []any{},
	}
}

func testProgramState() map[string]any {
	return map[string]any{"variables": []any{}, "lists": []any{}, "broadcasts": []any{}}
}

func testScript() map[string]any {
	return map[string]any{
		"id": "script_move", "purpose": "移动十步", "x": 40, "y": 40,
		"blocks": []any{
			map[string]any{"opcode": "event_whenflagclicked", "fields": map[string]any{}, "inputs": map[string]any{}, "substacks": map[string]any{}},
			map[string]any{
				"opcode": "motion_movesteps", "fields": map[string]any{},
				"inputs":    map[string]any{"STEPS": map[string]any{"kind": "number", "value": 10}},
				"substacks": map[string]any{},
			},
		},
	}
}

// testProgramIR validates the same program the tagged fixture produces.
func testProgramIR(t *testing.T) *schema.ProgramIR {
	t.Helper()
	raw, err := jsonx.Marshal(map[string]any{
		"schema_version":    2,
		"summary":           "绿旗后移动十步",
		"remove_script_ids": []any{},
		"variables":         []any{},
		"lists":             []any{},
		"broadcasts":        []any{},
		"procedures":        []any{},
		"scripts":           []any{testScript()},
	})
	if err != nil {
		t.Fatalf("marshal program: %v", err)
	}
	program := &schema.ProgramIR{}
	if err := schema.DecodeStrict(raw, program); err != nil {
		t.Fatalf("program fixture invalid: %v", err)
	}
	return program
}

func tag(name string, value any) string {
	return "<" + name + ">" + mustJSON(value) + "</" + name + ">"
}

// testSegmentOutputs builds the tagged provider output per orchestrator segment.
func testSegmentOutputs(t *testing.T) map[string]string {
	t.Helper()
	taskSpec := testTaskSpec()
	if err := taskSpec.Validate(); err != nil {
		t.Fatalf("task spec fixture invalid: %v", err)
	}
	blueprint := testBlueprint(t, taskSpec)
	if err := schema.ValidateTaskSpecBlueprintLineage(taskSpec, blueprint); err != nil {
		t.Fatalf("blueprint lineage invalid: %v", err)
	}
	manifest := testManifest(t, blueprint)
	if err := testProgramIR(t).Validate(); err != nil {
		t.Fatalf("program fixture invalid: %v", err)
	}
	program := strings.Join([]string{
		"<program target_index=\"0\">",
		tag("metadata", testProgramMetadata()),
		tag("shared_state", testProgramState()),
		"<script index=\"0\" total=\"1\">" + mustJSON(testScript()) + "</script>",
		"</program>",
	}, "")
	return map[string]string{
		"requirements": tag("requirements", testRequirements()),
		"task_spec":    tag("task_spec", taskSpec),
		"blueprint":    tag("blueprint", blueprint),
		"plan":         tag("plan", testPlan()),
		"manifest":     tag("manifest", manifest),
		"program:0":    program,
	}
}

func mustJSON(value any) string {
	raw, err := jsonx.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

var programMarker = regexp.MustCompile("target_index=([0-9]+) 的完整 <program> 标签")

// providerFixture is a fake Agnes HTTP provider that routes each provider call
// to the tagged output of the segment named by the prompt instruction.
type providerFixture struct {
	outputs map[string]string

	mu         sync.Mutex
	segments   []string
	block      chan struct{}
	failStatus int
	server     *httptest.Server
}

func newProviderFixture(t *testing.T, outputs map[string]string) *providerFixture {
	t.Helper()
	fixture := &providerFixture{outputs: outputs}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		instruction := ""
		if messages, ok := body["messages"].([]any); ok && len(messages) > 0 {
			if last, ok := messages[len(messages)-1].(map[string]any); ok {
				instruction, _ = last["content"].(string)
			}
		}
		segment, output, ok := fixture.route(instruction)
		if !ok {
			http.Error(writer, "unknown segment", http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		fixture.segments = append(fixture.segments, segment)
		block := fixture.block
		failStatus := fixture.failStatus
		fixture.mu.Unlock()
		if failStatus != 0 {
			http.Error(writer, "provider failure", failStatus)
			return
		}
		if block != nil {
			select {
			case <-block:
			case <-request.Context().Done():
				return
			}
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		flusher, _ := writer.(http.Flusher)
		for _, chunk := range splitRunes(output, 37) {
			payload := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": chunk}}}}
			fmt.Fprintf(writer, "data: %s\n\n", mustJSON(payload))
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(writer, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *providerFixture) route(instruction string) (string, string, bool) {
	switch {
	case strings.Contains(instruction, "<requirements>JSON</requirements>"):
		return "requirements", f.outputs["requirements"], true
	case strings.Contains(instruction, "<task_spec>JSON</task_spec>"):
		return "task_spec", f.outputs["task_spec"], true
	case strings.Contains(instruction, "<blueprint>JSON</blueprint>"):
		return "blueprint", f.outputs["blueprint"], true
	case strings.Contains(instruction, "<plan>JSON</plan>"):
		return "plan", f.outputs["plan"], true
	case strings.Contains(instruction, "<manifest>JSON</manifest>"):
		return "manifest", f.outputs["manifest"], true
	}
	if match := programMarker.FindStringSubmatch(instruction); match != nil {
		index, _ := strconv.Atoi(match[1])
		key := "program:" + strconv.Itoa(index)
		return key, f.outputs[key], true
	}
	return "", "", false
}

func (f *providerFixture) setBlock(block chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block = block
}

func (f *providerFixture) setFailStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failStatus = status
}

func (f *providerFixture) calledSegments() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.segments...)
}

func splitRunes(value string, size int) []string {
	runes := []rune(value)
	chunks := make([]string, 0, len(runes)/size+1)
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

// testManager bundles the manager, store and orchestrator shared by a test.
type testManager struct {
	manager      *AiRunManager
	store        *runstore.RunStore
	orchestrator *orchestration.GenerationOrchestrator
	settings     *config.Settings
	fixture      *providerFixture
	provider     *provider.TextProvider
}

func newTestManager(t *testing.T) *testManager {
	t.Helper()
	settings := testSettings()
	store, err := runstore.NewStore(runstore.Config{
		DatabasePath:        t.TempDir() + "/ai_runs.sqlite3",
		EventRetentionHours: 24,
		RunRetentionDays:    7,
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Initialize(); err != nil {
		t.Fatalf("initialize store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	orchestrator := orchestration.NewGenerationOrchestrator(
		prompt.NewPromptBuilder(nil, nil, map[string]bool{}, false),
		0,
		2,
	)
	fixture := newProviderFixture(t, testSegmentOutputs(t))
	providerInstance := &provider.TextProvider{Settings: settings, Provider: "agnes", Client: fixture.server.Client()}
	settings.AgnesAPIBase = fixture.server.URL
	return &testManager{
		manager:      NewAiRunManager(store, settings),
		store:        store,
		orchestrator: orchestrator,
		settings:     settings,
		fixture:      fixture,
		provider:     providerInstance,
	}
}

func (m *testManager) semaphore() chan struct{} { return make(chan struct{}, 2) }

func (m *testManager) startParams(request *schema.UnifiedGenerationRequest) StartParams {
	return StartParams{
		Request:        request,
		OwnerScopeHash: testOwnerScopeHash,
		Provider:       m.provider,
		Semaphore:      m.semaphore(),
		Orchestrator:   m.orchestrator,
	}
}

// startRun starts a run and returns its id.
func (m *testManager) startRun(t *testing.T, request *schema.UnifiedGenerationRequest) string {
	t.Helper()
	payload, err := m.manager.Start(context.Background(), m.startParams(request))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	runID, _ := payload["run_id"].(string)
	if runID == "" {
		t.Fatalf("start payload has no run_id: %#v", payload)
	}
	return runID
}

func (m *testManager) record(t *testing.T, runID string) runstore.RunRecord {
	t.Helper()
	record, err := m.store.GetRun(runID, testOwnerScopeHash)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	return record
}

func (m *testManager) waitForTerminal(t *testing.T, runID string, timeout time.Duration) runstore.RunRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		record := m.record(t, runID)
		if runstore.IsTerminal(record.Status) {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not reach a terminal status (last %s)", runID, record.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (m *testManager) waitForStatus(t *testing.T, runID string, status runstore.RunStatus, timeout time.Duration) runstore.RunRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		record := m.record(t, runID)
		if record.Status == status {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not reach %s (last %s)", runID, status, record.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (m *testManager) events(t *testing.T, runID string) []runstore.RunEvent {
	t.Helper()
	events, err := m.store.EventsAfter(runstore.EventsAfterParams{
		RunID: runID, OwnerScopeHash: testOwnerScopeHash, Sequence: 0,
	})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return events
}

// sameJSON compares two API payloads after a JSON round trip, because a value
// built in Go (int64) and the same value decoded from a stored response
// (float64) are not DeepEqual even though Python sees the same int.
func sameJSON(t *testing.T, left any, right any) bool {
	t.Helper()
	leftJSON, err := jsonx.Canonical(left)
	if err != nil {
		t.Fatalf("canonical left: %v", err)
	}
	rightJSON, err := jsonx.Canonical(right)
	if err != nil {
		t.Fatalf("canonical right: %v", err)
	}
	return leftJSON == rightJSON
}

// findEvent returns the single event of a phase/type pair.
func findEvent(t *testing.T, events []runstore.RunEvent, phase string, eventType string) runstore.RunEvent {
	t.Helper()
	var found []runstore.RunEvent
	for _, event := range events {
		if event.Phase == phase && event.Type == eventType {
			found = append(found, event)
		}
	}
	if len(found) != 1 {
		t.Fatalf("found %d %s/%s events", len(found), phase, eventType)
	}
	return found[0]
}

func eventPairs(events []runstore.RunEvent) []string {
	pairs := make([]string, 0, len(events))
	for _, event := range events {
		pairs = append(pairs, event.Phase+"/"+event.Type)
	}
	return pairs
}
