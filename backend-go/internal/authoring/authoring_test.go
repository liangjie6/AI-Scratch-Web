package authoring

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ai-scratch-web/backend/internal/schema"
)

func singleFixture(t *testing.T, mode string) (*FrozenContract, *schema.UnifiedGenerationRequest) {
	t.Helper()
	request := &schema.UnifiedGenerationRequest{PipelineVersion: PipelineVersion, Scope: "single_sprite", AssetPolicy: "builtin_first", Target: schema.TargetContext{Name: "猫", Mode: mode, Costumes: []string{"cat"}, Backdrops: []string{"白背景"}, Sounds: []string{"pop"}, SpriteNames: []string{"猫", "苹果"}, CurrentProgram: `{"scripts":[{"id":"script_1","protected":false},{"id":"script_2","protected":true}],"local_variables":[],"global_variables":[]}`}}
	spec := &IntentSpec{SchemaVersion: 1, Title: "移动", Goal: "移动并计分", Targets: []IntentTarget{{Key: "cat", Name: "猫", Kind: "sprite", Disposition: "current"}}, States: []IntentState{{Key: "score", Name: "得分", Kind: "variable", Scope: "global", Disposition: "new", Initial: 0}}, ScriptSlots: []IntentSlot{{Key: "init", Owner: "cat", Trigger: Trigger{Kind: "green_flag"}, Purpose: "初始化并移动", Reads: []string{"score"}, Writes: []string{"score"}}}}
	if mode == "modify" {
		spec.ScriptSlots[0].EditRef = "script_1"
	}
	options, err := OptionsFromRequest(request, spec)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Freeze(spec, options)
	if err != nil {
		t.Fatal(err)
	}
	return c, request
}
func parseResponse(t *testing.T, body string) *SlotResponse {
	t.Helper()
	r, err := ParseCompact([]byte(body), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestIntentRouteDefaultsOnlyMissingVersion(t *testing.T) {
	for _, raw := range []string{`{"title":"移动"}`, `{"schema_version":1,"title":"移动"}`} {
		spec, err := ParseIntent([]byte(raw), Limits{})
		if err != nil || spec.SchemaVersion != IntentVersion {
			t.Fatalf("route-pinned version default failed: %s %v", raw, err)
		}
	}
	for _, raw := range []string{`{"schema_version":0}`, `{"schema_version":2}`, `{"schema_version":null}`} {
		_, err := ParseIntent([]byte(raw), Limits{})
		requireCode(t, err, "VERSION")
	}
	_, err := ParseIntent([]byte(`null`), Limits{})
	requireCode(t, err, "SHAPE")
	_, err = ParseIntent([]byte(`{"title":"a","title":"b"}`), Limits{})
	requireCode(t, err, "DUPLICATE_KEY")
}

func TestSingleNormalizeAndAssembleModes(t *testing.T) {
	for _, mode := range []string{"append", "modify", "rewrite"} {
		t.Run(mode, func(t *testing.T) {
			c, request := singleFixture(t, mode)
			response := parseResponse(t, `{"slot_id":"init","status":"ready","revision":null,"body":[{"opcode":"data_setvariableto","fields":{"VARIABLE":{"state":"score"}},"inputs":{"VALUE":"0"}},{"opcode":"control_repeat","inputs":{"TIMES":3},"substacks":{"SUBSTACK":[{"opcode":"motion_movesteps","inputs":{"STEPS":10}}]}},{"opcode":"control_if","inputs":{"CONDITION":{"opcode":"operator_gt","inputs":{"OPERAND1":{"state":"score"},"OPERAND2":"0"}}},"substacks":{"SUBSTACK":[{"opcode":"looks_say","inputs":{"MESSAGE":"完成"}}]}}]}`)
			script, err := NormalizeSlot(c, "cat", response, NormalizeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if script.Blocks[0].Opcode != "event_whenflagclicked" || script.Blocks[1].Fields["VARIABLE"] != "得分" {
				t.Fatalf("hat/state normalization: %#v", script)
			}
			p, err := AssembleProgram(c, "cat", map[string]schema.Script{"init": *script})
			if err != nil {
				t.Fatal(err)
			}
			if p.SchemaVersion != 1 || len(p.Variables) != 1 || p.Variables[0].Name != "得分" {
				t.Fatalf("single declarations: %#v", p)
			}
			if mode == "modify" && len(p.RemoveScriptIDs) != 1 || mode != "modify" && len(p.RemoveScriptIDs) != 0 {
				t.Fatalf("edit mode %s: %v", mode, p.RemoveScriptIDs)
			}
			if err := ValidateRequestBinding(c, request); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(c)
			copy, err := DecodeFrozen(raw)
			if err != nil || copy.Hash != c.Hash {
				t.Fatalf("frozen round trip: %v", err)
			}
		})
	}
}

func projectFixture(t *testing.T) (*FrozenContract, *DerivedContracts, *schema.UnifiedGenerationRequest) {
	t.Helper()
	request := &schema.UnifiedGenerationRequest{PipelineVersion: PipelineVersion, SessionID: "source-project-session", RequestID: "8e2a8474-3a2a-4bc1-970f-824c4c62e908", Scope: "project", AssetPolicy: "builtin_first", Target: schema.TargetContext{Name: "猫", Mode: "append", Costumes: []string{"cat"}, Backdrops: []string{"白背景"}, Sounds: []string{"pop"}, SpriteNames: []string{"猫"}, CurrentProgram: "{}"}, ProjectContext: &schema.ProjectContext{ProjectID: "project", Version: 3, Targets: []schema.ProjectTargetContext{{Name: "舞台", IsStage: true, Mode: "append", TargetID: "stage", Revision: 3, Costumes: []string{"白背景"}, Backdrops: []string{"白背景"}, CurrentProgram: "{}"}, {Name: "猫", Mode: "append", TargetID: "current_target", Revision: 3, Costumes: []string{"cat"}, Sounds: []string{"pop"}, CurrentProgram: "{}"}}}}
	spec := &IntentSpec{SchemaVersion: 1, Title: "接苹果", Goal: "苹果下落，接到后计分", Targets: []IntentTarget{{Key: "cat", Name: "猫", Kind: "sprite", Disposition: "current"}, {Key: "stage", Name: "舞台", Kind: "stage", Disposition: "existing"}, {Key: "apple", Name: "苹果", Kind: "sprite", Disposition: "new"}}, States: []IntentState{{Key: "score", Name: "得分", Kind: "variable", Scope: "global", Disposition: "new", Initial: 0}, {Key: "hit", Name: "接到", Kind: "broadcast", Scope: "global", Disposition: "new"}, {Key: "speed", Name: "速度", Kind: "variable", Scope: "local", Owner: "apple", Disposition: "new", Initial: 5}}, Assets: []IntentAsset{{Key: "apple_sprite", Owner: "apple", Kind: "sprite", Name: "苹果", Purpose: "下落物", Disposition: "new", SearchTerms: []string{"Apple"}}, {Key: "forest", Owner: "stage", Kind: "backdrop", Name: "森林", Purpose: "游戏背景", Disposition: "new", SearchTerms: []string{"Forest"}}, {Key: "pop", Owner: "cat", Kind: "sound", Name: "pop", Purpose: "得分音效", Disposition: "reuse"}}, ScriptSlots: []IntentSlot{{Key: "cat_init", Owner: "cat", Trigger: Trigger{Kind: "green_flag"}, Purpose: "初始化得分", Writes: []string{"score"}}, {Key: "cat_hit", Owner: "cat", Trigger: Trigger{Kind: "broadcast_received", State: "hit"}, Purpose: "接苹果得分", Reads: []string{"score"}, Writes: []string{"score"}, Receives: []string{"hit"}, Assets: []string{"pop"}}, {Key: "apple_fall", Owner: "apple", Trigger: Trigger{Kind: "green_flag"}, Purpose: "苹果下落并广播接到", Reads: []string{"speed"}, Emits: []string{"hit"}, Assets: []string{"cat"}}}, SuccessCriteria: []string{"接到苹果增加得分"}}
	opts, err := OptionsFromRequest(request, spec)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Freeze(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := DeriveLegacy(c)
	if err != nil {
		t.Fatal(err)
	}
	return c, derived, request
}

func TestProjectDeriveNormalize(t *testing.T) {
	c, d, request := projectFixture(t)
	if len(d.Manifest.ProgramTargets) != 2 || len(ProgramTargets(c)) != 2 || len(d.Manifest.ResourceOps) != 2 {
		t.Fatalf("resource-only stage projection: %#v", d.Manifest)
	}
	if err := schema.ValidateBlueprintManifestLineage(d.Blueprint, d.Manifest); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequestBinding(c, request); err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{"cat_init": `[{"opcode":"data_setvariableto","fields":{"VARIABLE":{"state":"score"}},"inputs":{"VALUE":"0"}}]`, "cat_hit": `[{"opcode":"data_changevariableby","fields":{"VARIABLE":{"state":"score"}},"inputs":{"VALUE":1}},{"opcode":"sound_play","inputs":{"SOUND_MENU":{"asset":"pop"}}}]`, "apple_fall": `[{"opcode":"motion_changeyby","inputs":{"DY":{"opcode":"operator_subtract","inputs":{"NUM1":0,"NUM2":{"state":"speed"}}}}},{"opcode":"event_broadcast","inputs":{"BROADCAST_INPUT":{"state":"hit"}}}]`}
	programs := []*schema.ProgramIR{}
	for _, target := range ProgramTargets(c) {
		scripts := map[string]schema.Script{}
		for _, slot := range c.ScriptSlots {
			if slot.Owner != target.Handle {
				continue
			}
			r := parseResponse(t, `{"slot_id":"`+slot.Key+`","status":"ready","revision":null,"body":`+bodies[slot.Key]+`}`)
			s, err := NormalizeSlot(c, target.Handle, r, NormalizeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			scripts[slot.Key] = *s
		}
		p, err := AssembleProgram(c, target.Handle, scripts)
		if err != nil {
			t.Fatal(err)
		}
		if p.SchemaVersion != 2 || len(p.Variables) != 0 || len(p.Broadcasts) != 0 || len(p.Lists) != 0 {
			t.Fatalf("project ProgramIR must use manifest bindings: %#v", p)
		}
		programs = append(programs, p)
	}
	if path := os.Getenv("AUTHORING_FIXTURE_OUTPUT"); path != "" {
		hash, _ := fingerprint(c)
		bpHash, _ := schema.ContractSHA256(d.Blueprint)
		raw, _ := json.MarshalIndent(map[string]any{"source_contract": c, "source_contract_hash": hash, "blueprint": d.Blueprint, "blueprint_hash": bpHash, "manifest": d.Manifest, "steps": d.Plan.Steps, "programs": programs, "request": request}, "", "  ")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompactRejectsMalformedAndOversizedJSON(t *testing.T) {
	tests := []struct {
		raw, code string
		limits    Limits
	}{{`{"slot_id":"a","slot_id":"b"}`, "DUPLICATE_KEY", Limits{}}, {`{"slot_id":"a"} {}`, "JSON_TRAILING", Limits{}}, {`{"body":[[[[]]]]}`, "JSON_DEPTH", Limits{MaxJSONDepth: 2}}, {`{"slot_id":"init","status":"ready","body":[],"revision":null,"unexpected":1}`, "SHAPE", Limits{}}, {`{"slot_id":"init","status":"ready","body":null,"revision":null}`, "SHAPE", Limits{}}, {`{"slot_id":"init","status":"ready","body":[],"revision":null}`, "RESPONSE_SIZE", Limits{MaxBytes: 8}}}
	for _, test := range tests {
		_, err := ParseCompact([]byte(test.raw), test.limits)
		requireCode(t, err, test.code)
	}
}

func TestNormalizerRejectsPolicyAndShapeViolations(t *testing.T) {
	c, _ := singleFixture(t, "append")
	tests := []struct{ body, code string }{{`[{"opcode":"motion_movesteps","inputs":{"STEPS":"10"}}]`, "LITERAL_TYPE"}, {`[{"opcode":"looks_say","inputs":{"MESSAGE":7}}]`, "LITERAL_TYPE"}, {`[{"opcode":"control_if","inputs":{"CONDITION":true},"substacks":{"SUBSTACK":[]}}]`, "BOOLEAN_EXPECTED"}, {`[{"opcode":"event_whenflagclicked"}]`, "STACK_EXPECTED"}, {`[{"opcode":"motion_movesteps","inputs":{"STEP":10}}]`, "UNKNOWN_PORT"}, {`[{"opcode":"control_repeat","inputs":{"TIMES":3}}]`, "MISSING_PORT"}, {`[{"opcode":"control_delete_this_clone"},{"opcode":"motion_movesteps","inputs":{"STEPS":1}}]`, "TERMINAL_FOLLOWED"}, {`[{"opcode":"data_setvariableto","fields":{"VARIABLE":"得分"},"inputs":{"VALUE":"0"}}]`, "LOGICAL_REFERENCE"}, {`[{"opcode":"motion_goto","inputs":{"TO":{"builtin":"edge"}}}]`, "BUILTIN_NOT_ALLOWED"}, {`[{"opcode":"motion_goto","inputs":{"TO":"猫"}}]`, "LOGICAL_REFERENCE"}, {`[{"opcode":"procedures_call"}]`, "PROCEDURE_LEGACY_REQUIRED"}, {`[{"opcode":"operator_add","inputs":{"NUM1":1,"NUM2":2}}]`, "STACK_EXPECTED"}, {`[{"opcode":"looks_seteffectto","fields":{"EFFECT":"made-up"},"inputs":{"VALUE":1}}]`, "MENU_VALUE"}}
	for _, test := range tests {
		t.Run(test.code+test.body[:min(len(test.body), 40)], func(t *testing.T) {
			r := parseResponse(t, `{"slot_id":"init","status":"ready","body":`+test.body+`,"revision":null}`)
			_, err := NormalizeSlot(c, "cat", r, NormalizeOptions{})
			requireCode(t, err, test.code)
		})
	}
	r := parseResponse(t, `{"slot_id":"init","status":"ready","body":[{"opcode":"motion_movesteps","inputs":{"STEPS":10}}],"revision":null}`)
	_, err := NormalizeSlot(c, "cat", r, NormalizeOptions{OpcodeProfile: map[string]bool{}})
	requireCode(t, err, "CAPABILITY_NOT_IN_PROFILE")
	_, err = NormalizeSlot(c, "cat", r, NormalizeOptions{BaseSnapshotHash: strings.Repeat("f", 64)})
	requireCode(t, err, "STALE_SNAPSHOT")
}

func TestSnapshotReuseAndEditProtection(t *testing.T) {
	c, request := singleFixture(t, "modify")
	request.Target.CurrentProgram = `{"scripts":[{"id":"script_1","protected":true}],"local_variables":[],"global_variables":[]}`
	err := ValidateRequestBinding(c, request)
	requireCode(t, err, "EDIT_NOT_ALLOWED")
	raw, _ := json.Marshal(c)
	var changed FrozenContract
	_ = json.Unmarshal(raw, &changed)
	changed.Title = "tampered"
	requireCode(t, ValidateFrozen(&changed), "ARTIFACT_HASH")
	request.Target.CurrentProgram = `{"scripts":[],"local_variables":[{"name":"队列","type":"list","length":10,"preview":[1,2,3,4,5]}],"global_variables":[{"name":"得分","type":"","value":3}]}`
	spec := &IntentSpec{SchemaVersion: 1, Title: "复用", Goal: "保留列表现值", Targets: []IntentTarget{{Key: "cat", Name: "猫", Kind: "sprite", Disposition: "current"}}, States: []IntentState{{Key: "queue", Name: "队列", Kind: "list", Scope: "local", Owner: "cat", Disposition: "reuse"}, {Key: "score", Name: "得分", Kind: "variable", Scope: "global", Disposition: "reuse"}}, ScriptSlots: []IntentSlot{{Key: "read", Owner: "cat", Trigger: Trigger{Kind: "green_flag"}, Purpose: "读取", Reads: []string{"queue", "score"}}}}
	request.Target.Mode = "append"
	opts, err := OptionsFromRequest(request, spec)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := Freeze(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.States[0].Value != nil {
		t.Fatal("list preview must not replace the full current list")
	}
	if frozen.States[1].Value != float64(3) {
		t.Fatalf("snapshot scalar missing: %#v", frozen.States[1])
	}
}

func TestSnapshotBroadcastDoesNotLeakGlobalScopeToNextLocal(t *testing.T) {
	request := &schema.UnifiedGenerationRequest{Scope: "single_sprite", Target: schema.TargetContext{Name: "猫", Mode: "append", CurrentProgram: `{"local_variables":[{"name":"通知","type":"broadcast_msg","value":"通知"},{"name":"速度","type":"","value":5}],"global_variables":[]}`}}
	symbols, err := SnapshotBindings(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols.States) != 2 || symbols.States[0].Kind != "broadcast" || symbols.States[0].Scope != "global" || symbols.States[1].Scope != "local" || symbols.States[1].Owner != "current" {
		t.Fatalf("local state scope was contaminated: %#v", symbols.States)
	}
}

func TestRecomputedFrozenHashDoesNotBypassSemanticChecks(t *testing.T) {
	c, _ := singleFixture(t, "append")
	c.ScriptSlots[0].Emits = []string{"missing"}
	c.Hash, _ = contractFingerprint(c)
	raw, _ := json.Marshal(c)
	_, err := DecodeFrozen(raw)
	requireCode(t, err, "STATE_UNKNOWN")
	if _, err := DeriveLegacy(c); err == nil {
		t.Fatal("invalid reference reached deterministic derivation")
	}
}

func TestMergeRechecksSlotPermissions(t *testing.T) {
	c, _ := singleFixture(t, "append")
	c.ScriptSlots = append(c.ScriptSlots, FrozenSlot{IntentSlot: IntentSlot{Key: "other", Owner: "cat", Trigger: Trigger{Kind: "green_flag"}, Purpose: "不访问得分", Reads: []string{}, Writes: []string{}, Emits: []string{}, Receives: []string{}, Assets: []string{}}, ScriptID: stableID("script_", "other")})
	c.Hash, _ = contractFingerprint(c)
	good, err := NormalizeSlot(c, "cat", parseResponse(t, `{"slot_id":"init","status":"ready","revision":null,"body":[{"opcode":"data_setvariableto","fields":{"VARIABLE":{"state":"score"}},"inputs":{"VALUE":"0"}}]}`), NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	other, err := NormalizeSlot(c, "cat", parseResponse(t, `{"slot_id":"other","status":"ready","revision":null,"body":[]}`), NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	other.Blocks = append(other.Blocks, good.Blocks[1])
	_, err = AssembleProgram(c, "cat", map[string]schema.Script{"init": *good, "other": *other})
	requireCode(t, err, "STATE_ACCESS")
}

func TestMergeRechecksStaticMenuValues(t *testing.T) {
	c, _ := singleFixture(t, "append")
	script, err := NormalizeSlot(c, "cat", parseResponse(t, `{"slot_id":"init","status":"ready","revision":null,"body":[{"opcode":"looks_seteffectto","fields":{"EFFECT":"COLOR"},"inputs":{"VALUE":10}}]}`), NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	script.Blocks[1].Fields["EFFECT"] = "not-a-menu-option"
	_, err = AssembleProgram(c, "cat", map[string]schema.Script{"init": *script})
	requireCode(t, err, "MENU_VALUE")
}

func TestProfileAndDepthAndWholeTargetBudgets(t *testing.T) {
	c, _ := singleFixture(t, "append")
	nested := `{"opcode":"motion_movesteps","inputs":{"STEPS":1}}`
	for i := 0; i < 4; i++ {
		nested = `{"opcode":"control_repeat","inputs":{"TIMES":1},"substacks":{"SUBSTACK":[` + nested + `]}}`
	}
	r := parseResponse(t, `{"slot_id":"init","status":"ready","revision":null,"body":[`+nested+`]}`)
	_, err := NormalizeSlot(c, "cat", r, NormalizeOptions{Limits: Limits{MaxBlockDepth: 2}})
	requireCode(t, err, "BLOCK_DEPTH")
	many := make([]CompactBlock, 120)
	for i := range many {
		many[i] = CompactBlock{Opcode: "motion_movesteps", Inputs: map[string]any{"STEPS": 1}}
	}
	_, err = NormalizeSlot(c, "cat", &SlotResponse{SlotID: "init", Status: "ready", Body: many}, NormalizeOptions{})
	requireCode(t, err, "BLOCK_BUDGET")
}

func TestSourceRequestBindingRejectsExpandedScopeAndResourcePolicy(t *testing.T) {
	c, _, request := projectFixture(t)
	request.AssetPolicy = "existing_only"
	requireCode(t, ValidateRequestBinding(c, request), "ASSET_POLICY")
	request.AssetPolicy = "builtin_first"
	request.Scope = "single_sprite"
	if err := ValidateRequestBinding(c, request); err == nil {
		t.Fatal("expanded scope was not bound to original request")
	}
}

func TestTargetContextTrimmingKeepsAuthorizedEditOnly(t *testing.T) {
	c, _ := singleFixture(t, "modify")
	pc, err := ContextForTarget(c, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pc.Target.Context.CurrentProgram, "script_2") || !strings.Contains(pc.Target.Context.CurrentProgram, "script_1") {
		t.Fatalf("unexpected modify slice: %s", pc.Target.Context.CurrentProgram)
	}
	c, _ = singleFixture(t, "append")
	pc, err = ContextForTarget(c, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if pc.Target.Context.CurrentProgram != "{}" {
		t.Fatal("append sent existing implementation")
	}
}

func TestFrozenIndependentOfMutableInput(t *testing.T) {
	request := &schema.UnifiedGenerationRequest{Scope: "single_sprite", Target: schema.TargetContext{Name: "猫", Mode: "append", Costumes: []string{"cat"}, CurrentProgram: "{}"}}
	spec := &IntentSpec{SchemaVersion: 1, Title: "只读快照", Goal: "移动", Targets: []IntentTarget{{Key: "current", Name: "猫", Kind: "sprite", Disposition: "current"}}, ScriptSlots: []IntentSlot{{Key: "move", Owner: "current", Trigger: Trigger{Kind: "green_flag"}, Purpose: "移动"}}}
	opts, err := OptionsFromRequest(request, spec)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Freeze(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	request.Target.Costumes[0] = "mutated"
	spec.ScriptSlots[0].Purpose = "mutated"
	if err := ValidateFrozen(c); err != nil {
		t.Fatal("frozen artifact retained mutable input aliases", err)
	}
}
