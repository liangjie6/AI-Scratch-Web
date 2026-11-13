package authoring

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ai-scratch-web/backend/internal/schema"
)

func trustedReuseFixture(t *testing.T) (*IntentSpec, FreezeOptions, *schema.UnifiedGenerationRequest, string) {
	t.Helper()
	request := &schema.UnifiedGenerationRequest{PipelineVersion: PipelineVersion, Scope: "single_sprite", AssetPolicy: "builtin_first", Target: schema.TargetContext{Name: "猫", Mode: "append", CurrentProgram: `{"local_variables":[{"name":"得分","type":"","value":7}],"global_variables":[]}`}}
	snapshot, err := SnapshotBindings(request)
	if err != nil {
		t.Fatal(err)
	}
	handle := snapshot.States[0].Handle
	spec := &IntentSpec{SchemaVersion: 1, Title: "复用", Goal: "复用已有得分变量", Targets: []IntentTarget{{Key: "cat", Name: "猫", Kind: "sprite", Disposition: "current"}}, States: []IntentState{}, Assets: []IntentAsset{}, ScriptSlots: []IntentSlot{{Key: "update", Owner: "cat", Trigger: Trigger{Kind: "green_flag"}, Purpose: "更新已有变量", Reads: []string{handle}, Writes: []string{handle}}}}
	opts, err := OptionsFromRequest(request, spec)
	if err != nil {
		t.Fatal(err)
	}
	return spec, opts, request, handle
}

func TestBindTrustedStateHandleReusesValueWithoutMutatingIntent(t *testing.T) {
	spec, opts, request, handle := trustedReuseFixture(t)
	backing := []IntentState{{Key: "sentinel"}}
	spec.States = backing[:0]
	before, _ := json.Marshal(spec)
	bound, err := BindTrustedSymbols(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.States) != 1 || bound.States[0].Key != handle || bound.States[0].Owner != "cat" || bound.States[0].Disposition != "reuse" || bound.States[0].Initial != nil {
		t.Fatalf("trusted declaration changed: %#v", bound.States)
	}
	c, err := Freeze(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.States) != 1 || c.States[0].Value != float64(7) || c.States[0].Scope != "local" || c.States[0].Owner != "cat" {
		t.Fatalf("trusted value or ownership changed: %#v", c.States)
	}
	if err := ValidateRequestBinding(c, request); err != nil {
		t.Fatalf("auto-bound contract could not be reconstructed: %v", err)
	}
	after, _ := json.Marshal(spec)
	if string(after) != string(before) || backing[0].Key != "sentinel" {
		t.Fatal("binding mutated the caller or its slice backing array")
	}
}

func TestBindTrustedStateRejectsUnknownCrossOwnerAndInitialOverride(t *testing.T) {
	t.Run("unknown", func(t *testing.T) {
		spec, opts, _, _ := trustedReuseFixture(t)
		spec.ScriptSlots[0].Reads = []string{"得分"}
		spec.ScriptSlots[0].Writes = nil
		_, err := Freeze(spec, opts)
		requireCode(t, err, "STATE_UNKNOWN")
	})
	t.Run("cross owner", func(t *testing.T) {
		spec, _, request, _ := trustedReuseFixture(t)
		request.Scope = "project"
		request.ProjectContext = &schema.ProjectContext{ProjectID: "p", Version: 1, Targets: []schema.ProjectTargetContext{{Name: "猫", TargetID: "current_target", Mode: "append", CurrentProgram: request.Target.CurrentProgram}, {Name: "狗", TargetID: "dog_target", Mode: "append", CurrentProgram: "{}"}}}
		spec.Targets = append(spec.Targets, IntentTarget{Key: "dog", Name: "狗", Kind: "sprite", Disposition: "existing"})
		spec.ScriptSlots[0].Owner = "dog"
		opts, err := OptionsFromRequest(request, spec)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Freeze(spec, opts)
		requireCode(t, err, "STATE_SCOPE")
	})
	t.Run("explicit initial", func(t *testing.T) {
		spec, opts, _, handle := trustedReuseFixture(t)
		spec.States = []IntentState{{Key: handle, Name: "得分", Kind: "variable", Scope: "local", Owner: "cat", Disposition: "reuse", Initial: 999}}
		_, err := Freeze(spec, opts)
		requireCode(t, err, "STATE_REUSE")
	})
	t.Run("ambiguous trusted handle", func(t *testing.T) {
		spec, opts, _, _ := trustedReuseFixture(t)
		opts.ExistingStates = append(opts.ExistingStates, opts.ExistingStates[0])
		_, err := Freeze(spec, opts)
		requireCode(t, err, "STATE_AMBIGUOUS")
	})
	t.Run("post binding budget", func(t *testing.T) {
		spec, opts, _, _ := trustedReuseFixture(t)
		for i := 0; i < 150; i++ {
			spec.States = append(spec.States, IntentState{Key: fmt.Sprintf("new%d", i), Name: fmt.Sprintf("v%d", i), Kind: "variable", Scope: "global", Disposition: "new", Initial: 0})
		}
		_, err := Freeze(spec, opts)
		requireCode(t, err, "BUDGET")
	})
}

func TestStateStoragePortsPreserveNumberAndNumericText(t *testing.T) {
	_, request := singleFixture(t, "append")
	spec := &IntentSpec{SchemaVersion: 1, Title: "类型", Goal: "保留变量和列表值的原始标量类型", Targets: []IntentTarget{{Key: "cat", Name: "猫", Kind: "sprite", Disposition: "current"}}, States: []IntentState{{Key: "score", Name: "得分", Kind: "variable", Scope: "global", Disposition: "new", Initial: 0}, {Key: "queue", Name: "队列", Kind: "list", Scope: "local", Owner: "cat", Disposition: "new", Initial: []any{}}}, ScriptSlots: []IntentSlot{{Key: "store", Owner: "cat", Trigger: Trigger{Kind: "green_flag"}, Purpose: "保存原始标量", Writes: []string{"score", "queue"}}}}
	opts, err := OptionsFromRequest(request, spec)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Freeze(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []struct{ opcode, field, handle, input, extra string }{{"data_setvariableto", "VARIABLE", "score", "VALUE", ""}, {"data_addtolist", "LIST", "queue", "ITEM", ""}, {"data_insertatlist", "LIST", "queue", "ITEM", `,"INDEX":"1"`}, {"data_replaceitemoflist", "LIST", "queue", "ITEM", `,"INDEX":"1"`}} {
		for _, literal := range []struct{ raw, kind string }{{"0", "number"}, {`"007"`, "text"}} {
			t.Run(port.opcode+"/"+literal.kind, func(t *testing.T) {
				raw := fmt.Sprintf(`{"slot_id":"store","status":"ready","revision":null,"body":[{"opcode":%q,"fields":{%q:{"state":%q}},"inputs":{%q:%s%s}}]}`, port.opcode, port.field, port.handle, port.input, literal.raw, port.extra)
				script, err := NormalizeSlot(c, "cat", parseResponse(t, raw), NormalizeOptions{})
				if err != nil {
					t.Fatal(err)
				}
				input := script.Blocks[1].Inputs[port.input].(map[string]any)
				value, _ := json.Marshal(input["value"])
				if input["kind"] != literal.kind || string(value) != literal.raw {
					t.Fatalf("storage literal was cast: %#v", input)
				}
				if _, err := AssembleProgram(c, "cat", map[string]schema.Script{"store": *script}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
