package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/ai-scratch-web/backend/internal/authoring"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/schema"
	"strings"
	"testing"
	"time"
)

func sourceTestRequest() *schema.UnifiedGenerationRequest {
	return &schema.UnifiedGenerationRequest{Thinking: schema.ThinkingSettings{Strength: "low"}, PipelineVersion: authoring.PipelineVersion, ModelTier: "normal", SessionID: "source-test", RequestID: "4ab4aaee-0c6f-4a60-9b9a-cefaea6c060d", Scope: "single_sprite", AssetPolicy: "existing_only", ProceedWithDefaults: true, PermittedExtensions: []string{}, Messages: []schema.ChatMessage{{Role: "user", Content: "green flag: go to x=0 y=0 then repeat exactly 3 times change x by 10"}}, Target: schema.TargetContext{Name: "Probe", Mode: "append", Costumes: []string{"costume1"}, Sounds: []string{}, SpriteNames: []string{"Probe"}, CurrentProgram: `{"scripts":[],"local_variables":[],"global_variables":[]}`}}
}
func sourceTestIntent(slots int) *authoring.IntentSpec {
	spec := &authoring.IntentSpec{SchemaVersion: 1, Title: "Move", Goal: "Move on green flag", Targets: []authoring.IntentTarget{{Key: "current", Name: "Probe", Kind: "sprite", Disposition: "current"}}, States: []authoring.IntentState{}, Assets: []authoring.IntentAsset{}, ScriptSlots: []authoring.IntentSlot{}, RequiredExtensions: []string{}, Assumptions: []string{}, SuccessCriteria: []string{"x changes"}, MissingQuestions: []string{}}
	for i := 0; i < slots; i++ {
		key := "move"
		trigger := authoring.Trigger{Kind: "green_flag"}
		if i == 1 {
			key = "key_move"
			trigger = authoring.Trigger{Kind: "key_pressed", Key: "space"}
		}
		spec.ScriptSlots = append(spec.ScriptSlots, authoring.IntentSlot{Key: key, Owner: "current", Trigger: trigger, Purpose: "Change x by ten", Reads: []string{}, Writes: []string{}, Emits: []string{}, Receives: []string{}, Assets: []string{}})
	}
	return spec
}
func sourceSlot(key string) string {
	return `{"slot_id":"` + key + `","status":"ready","body":[{"opcode":"motion_changexby","inputs":{"DX":10}}],"revision":null}`
}
func TestSourcePipelineRepairsOnlyFailedSlot(t *testing.T) {
	r := sourceTestRequest()
	intent, _ := json.Marshal(sourceTestIntent(2))
	p := newCannedProvider(map[string][]string{"default": {string(intent), sourceSlot("move"), `{"slot_id":"key_move","status":"ready","body":[{"opcode":"motion_changexby","inputs":{"DX":"10"}}],"revision":null}`, sourceSlot("key_move")}})
	o := NewGenerationOrchestrator(nil, 1, 2)
	events := []GenerationEvent{}
	if err := o.generateSourceUnified(context.Background(), r, p, func(e GenerationEvent) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if p.callCount() != 4 {
		t.Fatalf("expected plan + first slot + second slot and one repair, got %d", p.callCount())
	}
	repairs, scripts := 0, 0
	complete := false
	for _, e := range events {
		if e.Type == "error" {
			t.Fatalf("terminal error: %#v", e.Data)
		}
		if e.Type == "repair" {
			repairs++
			if e.Data["slot_id"] != "key_move" {
				t.Fatal(e.Data)
			}
		}
		if e.Type == "script_ready" {
			scripts++
		}
		if e.Type == "complete" {
			complete = true
		}
	}
	if repairs != 1 || scripts != 2 || !complete {
		t.Fatalf("repairs=%d scripts=%d complete=%v", repairs, scripts, complete)
	}
	slotPrompt := ""
	for _, m := range p.callMessages(1) {
		slotPrompt += m.Content
	}
	if !strings.Contains(slotPrompt, r.Messages[0].Content) {
		t.Fatal("compact context discarded exact user constants; prior regression changed repeat3 to forever")
	}
	flags, schemas := p.flags()
	for i := range flags {
		if flags[i] || schemas[i] != nil {
			t.Fatal("non-structured provider must not receive falsely strict schema")
		}
	}
}
func TestSourceRevisionStopsWithoutExpandingPermissions(t *testing.T) {
	r := sourceTestRequest()
	intent, _ := json.Marshal(sourceTestIntent(1))
	p := newCannedProvider(map[string][]string{"default": {string(intent), `{"slot_id":"move","status":"needs_revision","body":null,"revision":{"reason":"missing_interface","requested_opcodes":[],"explanation":"need another state"}}`}})
	o := NewGenerationOrchestrator(nil, 3, 2)
	last := ""
	if err := o.generateSourceUnified(context.Background(), r, p, func(e GenerationEvent) error {
		if e.Type == "error" {
			last = e.Data["code"].(string)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if last != "CONTRACT_REVISION_REQUIRED" || p.callCount() != 2 {
		t.Fatalf("code=%s calls=%d", last, p.callCount())
	}
}
func TestSourceProviderFailureNeverFallsBackToTagged(t *testing.T) {
	p := newCannedProvider(nil)
	p.failWith = errors.New("provider truncated")
	o := NewGenerationOrchestrator(nil, 3, 2)
	events := []GenerationEvent{}
	if err := o.generateSourceUnified(context.Background(), sourceTestRequest(), p, func(e GenerationEvent) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if p.callCount() != 1 || events[len(events)-1].Type != "error" {
		t.Fatalf("calls=%d events=%v", p.callCount(), events)
	}
}
func TestSourceBudgetPersistsAcrossTargetsAndRepairs(t *testing.T) {
	o := NewGenerationOrchestrator(nil, 1, 2)
	r := sourceTestRequest()
	a := o.sourceBudgetFor(sourceKey(r, "contract"))
	b := o.sourceBudgetFor(sourceKey(r, "contract"))
	if a != b {
		t.Fatal("budget reset")
	}
	for i := 0; i < 64; i++ {
		if err := a.reserve(1); err != nil {
			t.Fatal(err)
		}
	}
	if b.reserve(1) == nil {
		t.Fatal("calls escaped shared budget")
	}
	expired := &sourceBudget{expires: time.Now().Add(-time.Second)}
	if expired.reserve(1) == nil {
		t.Fatal("expired contract resumed")
	}
}
func TestSourceProgramRequestRejectsChangedSnapshot(t *testing.T) {
	r := sourceTestRequest()
	spec := sourceTestIntent(1)
	opts, err := authoring.OptionsFromRequest(r, spec)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := authoring.Freeze(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	req := &SourceProgramRequest{Request: *r, SourceContract: *frozen, TargetIndex: 0, Target: r.Target}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	req.Request.Target.Costumes = append(req.Request.Target.Costumes, "changed")
	if err := req.Validate(); err == nil {
		t.Fatal("changed base snapshot accepted")
	}
}

func TestSourceTransportUnwrapsOnlyCompleteSingleFence(t *testing.T) {
	for _, raw := range []string{"```json\n{\"x\":1}\n```", "```\n{\"x\":1}\n```"} {
		if string(unwrapSourceJSON([]byte(raw))) != `{"x":1}` {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"```json\n{\"x\":1}", "text\n```json\n{}\n```", "```json\n{}\n```\n```json\n{}\n```"} {
		if string(unwrapSourceJSON([]byte(raw))) != raw {
			t.Fatal("accepted incomplete or mixed envelope", raw)
		}
	}
}

func TestSourceTransportRetryIsBoundedAndCancellable(t *testing.T) {
	if delay, ok := sourceRetryDelay(&provider.TextProviderError{StatusCode: 429, RetryAfterSeconds: 40}, 0); !ok || delay != 40*time.Second {
		t.Fatalf("Retry-After not honored: %v %v", delay, ok)
	}
	for _, err := range []error{&provider.TextProviderError{StatusCode: 401}, &provider.TextProviderError{StatusCode: 429, RetryAfterSeconds: 90}} {
		if _, ok := sourceRetryDelay(err, 0); ok {
			t.Fatal("should not retry", err)
		}
	}
	if _, ok := sourceRetryDelay(&provider.TextProviderError{StatusCode: 429}, 2); ok {
		t.Fatal("retry budget exceeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := newCannedProvider(nil)
	p.failWith = &provider.TextProviderError{StatusCode: 429, Message: "rate limited"}
	o := NewGenerationOrchestrator(nil, 3, 2)
	r := sourceTestRequest()
	_, err := o.sourceJSON(ctx, r, p, o.sourceBudgetFor("rate-test"), "program", "move", nil, func(e GenerationEvent) error {
		if e.Data["provider_retry"] == true {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || p.callCount() != 1 {
		t.Fatalf("cancellation made extra paid requests: %v %d", err, p.callCount())
	}
}

func TestSourceEditPromptRetainsOnlyAuthorizedOriginalBody(t *testing.T) {
	raw := `{"scripts":[{"id":"script_1","code":{"opcode":"control_repeat","inputs":{"TIMES":3}},"protected":false},{"id":"script_2","code":{"opcode":"looks_say"},"protected":true},{"id":"script_3","code":{"opcode":"motion_movesteps"}}]}`
	got, err := sourceEditedScriptContext(raw, "script_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "control_repeat") || strings.Contains(got, "script_2") || strings.Contains(got, "script_3") {
		t.Fatalf("edit context lost original body or disclosed unrelated scripts: %s", got)
	}
	if _, err := sourceEditedScriptContext(raw, "script_2"); err == nil {
		t.Fatal("protected body disclosed")
	}
	if got, err := sourceEditedScriptContext(raw, ""); err != nil || got != "" {
		t.Fatal("append body contains original scripts")
	}
}

func TestSourceExpiryCannotReviveAfterBudgetEviction(t *testing.T) {
	r := sourceTestRequest()
	spec := sourceTestIntent(1)
	opts, err := authoring.OptionsFromRequest(r, spec)
	if err != nil {
		t.Fatal(err)
	}
	opts.ExpiresAtUnix = time.Now().Add(-time.Minute).Unix()
	frozen, err := authoring.Freeze(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	o := NewGenerationOrchestrator(nil, 1, 2)
	p := newCannedProvider(nil)
	req := &SourceProgramRequest{Request: *r, SourceContract: *frozen, Target: r.Target}
	if err := req.Validate(); err == nil {
		t.Fatal("expired request validated")
	}
	o.sourceBudgetFor(sourceKey(r, "unrelated-new-run"))
	code := ""
	if err := o.generateSourceTarget(context.Background(), req, p, func(e GenerationEvent) error {
		if e.Type == "error" {
			code, _ = e.Data["code"].(string)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if code != "SOURCE_CONTRACT_EXPIRED" || p.callCount() != 0 {
		t.Fatalf("expired artifact resumed after GC: %s calls=%d", code, p.callCount())
	}
}

func TestSourceMissingUsageJournalsCompletionBudget(t *testing.T) {
	p := newCannedProvider(map[string][]string{"default": {`{"ok":true}`}})
	o := NewGenerationOrchestrator(nil, 0, 1)
	r := sourceTestRequest()
	b := o.sourceBudgetFor("no-usage")
	finished := false
	if _, err := o.sourceJSON(context.Background(), r, p, b, "program", "move", nil, func(e GenerationEvent) error {
		if e.Data["source_call_finished"] == true {
			finished = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := b.tokens
	if !finished || before == 0 {
		t.Fatal("fallback output budget was not journaled")
	}
	b.charge(-99)
	if b.tokens != before {
		t.Fatal("negative provider counts reduced shared budget")
	}
}
