package schema

import (
	"strings"
	"testing"
)

func stringsReplaceAll(value, old, replacement string) string {
	return strings.Replace(value, old, replacement, 1)
}

func expectDecodeError[T any](t *testing.T, raw, want string) {
	t.Helper()
	if _, err := decodeTarget[T](t, raw); err == nil || err.Error() != want {
		t.Fatalf("DecodeStrict(%s)\n error = %v\n want  = %q", raw, err, want)
	}
}

func TestValidatorErrorMessages(t *testing.T) {
	t.Run("OperandSpec", func(t *testing.T) {
		expectDecodeError[OperandSpec](t, `{"kind": "literal"}`, "literal operand requires value")
		expectDecodeError[OperandSpec](t, `{"kind": "state"}`, "state operand requires state_id")
		expectDecodeError[OperandSpec](t, `{"kind": "attribute", "attribute": "x"}`, "attribute operand requires entity_id and attribute")
		expectDecodeError[OperandSpec](t, `{"kind": "random", "min": 5, "max": 1}`, "random operand requires min <= max")
		expectDecodeError[OperandSpec](t, `{"kind": "timer", "value": 3}`, "timer operand contains unrelated fields")
		expectDecodeError[OperandSpec](t, `{"kind": "attribute", "entity_id": "e", "attribute": "x", "value": 2}`, "attribute operand contains unrelated fields")
		expectDecodeError[OperandSpec](t, `{"kind": "literal", "value": 1, "min": 1}`, "literal operand contains unrelated fields")
	})
	t.Run("TriggerSpec", func(t *testing.T) {
		expectDecodeError[TriggerSpec](t, `{"kind": "key_pressed"}`, "key_pressed requires key")
		expectDecodeError[TriggerSpec](t, `{"kind": "green_flag", "key": "space"}`, "green_flag contains unrelated trigger fields")
		expectDecodeError[TriggerSpec](t, `{"kind": "timer_reached", "seconds": -1}`, "/seconds: Input should be greater than or equal to 0")
		expectDecodeError[TriggerSpec](t, `{"kind": "broadcast_received", "key": "a"}`, "broadcast_received requires state_id")
	})
	t.Run("ConditionSpec", func(t *testing.T) {
		expectDecodeError[ConditionSpec](t, `{"kind": "compare"}`, "compare condition requires left, comparison operator, and right")
		expectDecodeError[ConditionSpec](t, `{"kind": "touching"}`, "touching condition requires entity_id")
		expectDecodeError[ConditionSpec](t, `{"kind": "key_down"}`, "key_down condition requires key")
		expectDecodeError[ConditionSpec](t, `{"kind": "logical", "operands": [{"kind": "touching_edge"}, {"kind": "touching_edge"}]}`, "logical condition requires and/or and operands")
		expectDecodeError[ConditionSpec](t, `{"kind": "not"}`, "not condition requires operand")
	})
	t.Run("ActionSpec", func(t *testing.T) {
		expectDecodeError[ActionSpec](t, `{"kind": "set_axis", "axis": "x"}`, "set_axis requires value")
		expectDecodeError[ActionSpec](t, `{"kind": "loop", "mode": "repeat", "actions": [{"kind": "delete_this_clone"}]}`, "repeat loop requires count")
		expectDecodeError[ActionSpec](t, `{"kind": "loop", "mode": "repeat_until", "count": {"kind": "literal", "value": 1}, "actions": [{"kind": "delete_this_clone"}]}`, "repeat_until loop requires until")
		expectDecodeError[ActionSpec](t, `{"kind": "loop", "mode": "forever", "count": {"kind": "literal", "value": 1}, "actions": [{"kind": "delete_this_clone"}]}`, "forever loop cannot contain count or until")
	})
	t.Run("AcceptanceArrange", func(t *testing.T) {
		expectDecodeError[AcceptanceArrange](t, `{"kind": "set_timer", "seconds": 90000}`, "set_timer arrangement requires seconds from 0 to 86400")
		expectDecodeError[AcceptanceArrange](t, `{"kind": "set_list", "state_id": "s", "value": 1}`, "set_list arrangement requires a list value")
		expectDecodeError[AcceptanceArrange](t, `{"kind": "set_variable", "state_id": "s", "value": [1]}`, "set_variable arrangement requires a scalar value")
	})
	t.Run("AcceptanceExpectation", func(t *testing.T) {
		expectDecodeError[AcceptanceExpectation](t, `{"kind": "variable", "state_id": "s", "comparator": "contains", "value": 1}`, "variable expectation has an invalid comparator")
		expectDecodeError[AcceptanceExpectation](t, `{"kind": "clone_count", "entity_id": "e", "comparator": "eq", "value": "x"}`, "clone_count expectation requires a numeric value")
		expectDecodeError[AcceptanceExpectation](t, `{"kind": "position_range", "entity_id": "e", "axis": "x", "min": 5, "max": 1}`, "position_range expectation min cannot exceed max")
		expectDecodeError[AcceptanceExpectation](t, `{"kind": "broadcast_emitted", "state_id": "s"}`, "broadcast_emitted expectation requires min_count")
	})
	t.Run("TaskState", func(t *testing.T) {
		expectDecodeError[TaskState](t, `{"state_id": "s", "kind": "broadcast", "scope": "local", "owner_entity_id": "e"}`, "broadcast state must be global and has no owner or initial_value")
		expectDecodeError[TaskState](t, `{"state_id": "s", "kind": "variable", "scope": "local"}`, "local state requires owner_entity_id")
		expectDecodeError[TaskState](t, `{"state_id": "s", "kind": "variable", "scope": "global", "owner_entity_id": "e"}`, "global state cannot have owner_entity_id")
		expectDecodeError[TaskState](t, `{"state_id": "s", "kind": "list", "scope": "global", "initial_value": 1}`, "list initial_value must be an array")
		expectDecodeError[TaskState](t, `{"state_id": "s", "kind": "variable", "scope": "global", "initial_value": [1]}`, "variable initial_value cannot be an array")
	})
	t.Run("BlueprintStatePlan", func(t *testing.T) {
		expectDecodeError[BlueprintStatePlan](t, `{"ref_id": "r", "kind": "broadcast", "scope": "global", "name": "n", "initial_value": 0}`, "broadcast plan must be global and have no owner or initial value")
		expectDecodeError[BlueprintStatePlan](t, `{"ref_id": "r", "kind": "variable", "scope": "local", "name": "n"}`, "local state plan requires owner_entity_id")
		expectDecodeError[BlueprintStatePlan](t, `{"ref_id": "r", "kind": "variable", "scope": "global", "name": "n", "owner_entity_id": "e"}`, "global state plan cannot have owner_entity_id")
	})
	t.Run("BlueprintAssetNeed", func(t *testing.T) {
		expectDecodeError[BlueprintAssetNeed](t, `{"need_id": "n", "kind": "sprite", "purpose": "p", "search_terms": ["a"], "fallback_order": ["audio_generation"]}`, "audio_generation only applies to sound needs")
		expectDecodeError[BlueprintAssetNeed](t, `{"need_id": "n", "kind": "sound", "purpose": "p", "search_terms": ["a"], "fallback_order": ["image_generation"]}`, "image_generation cannot satisfy a sound need")
		expectDecodeError[BlueprintAssetNeed](t, `{"need_id": "n", "kind": "sprite", "purpose": "p", "search_terms": ["a"], "fallback_order": ["scratch_library", "scratch_library"]}`, "asset fallback_order entries must be unique")
		expectDecodeError[BlueprintAssetNeed](t, `{"need_id": "n", "kind": "sprite", "purpose": "p", "search_terms": ["a"], "fallback_order": ["scratch_library"], "constraints": {"bogus": 1}}`, "asset need constraints contain an unknown key")
	})
	t.Run("BlueprintEntityResolution", func(t *testing.T) {
		expectDecodeError[BlueprintEntityResolution](t, `{"kind": "existing_target"}`, "existing_target binding requires target_ref only")
		expectDecodeError[BlueprintEntityResolution](t, `{"kind": "new_sprite", "target_ref": {"kind": "stage"}}`, "new_sprite binding requires need_id only")
	})
	t.Run("AcceptanceTrigger", func(t *testing.T) {
		expectDecodeError[AcceptanceTrigger](t, `{"kind": "sprite_click"}`, "sprite_click acceptance trigger requires entity_id")
		expectDecodeError[AcceptanceTrigger](t, `{"kind": "broadcast"}`, "broadcast acceptance trigger requires state_id")
	})
	t.Run("ProjectLocalState", func(t *testing.T) {
		expectDecodeError[ProjectLocalState](t, `{"ref_id": "r", "kind": "variable", "target": {"kind": "stage"}, "name": "n"}`, "local state cannot target the stage")
		expectDecodeError[ProjectLocalState](t, `{"ref_id": "r", "kind": "variable", "target": {"kind": "current_target"}, "name": "n", "value": [1]}`, "local variable value cannot be an array")
		expectDecodeError[ProjectLocalState](t, `{"ref_id": "r", "kind": "list", "target": {"kind": "current_target"}, "name": "n", "value": 1}`, "local list value must be an array")
	})
	t.Run("TargetRef", func(t *testing.T) {
		expectDecodeError[TargetRef](t, `{"kind": "existing_sprite"}`, "existing_sprite requires name")
		expectDecodeError[TargetRef](t, `{"kind": "created_sprite"}`, "created_sprite requires op_id")
		expectDecodeError[TargetRef](t, `{"kind": "stage", "name": "Stage"}`, "stage cannot contain name or op_id")
	})
	t.Run("TaskTargetRef", func(t *testing.T) {
		expectDecodeError[TaskTargetRef](t, `{"kind": "existing_sprite"}`, "existing_sprite requires name")
		expectDecodeError[TaskTargetRef](t, `{"kind": "stage", "name": "Stage"}`, "stage cannot contain name")
	})
	t.Run("LiteralAndResourceRefInput", func(t *testing.T) {
		expectDecodeError[LiteralInput](t, `{"kind": "text", "value": 5}`, "text input requires a string value")
		expectDecodeError[LiteralInput](t, `{"kind": "number", "value": "5"}`, "number input requires a numeric value")
		expectDecodeError[ResourceRefInput](t, `{"kind": "resource_ref"}`, "resource_ref requires ref or legacy resource_type/op_id pair")
		expectDecodeError[ExistingAssetRefInput](t, `{"kind": "existing_asset_ref", "asset_type": "sprite"}`, "/name: Field required")
	})
	t.Run("ResourceOperations", func(t *testing.T) {
		expectDecodeError[AddCostumeOp](t, `{"op": "add_costume", "op_id": "o", "source_need_id": "n", "target": {"kind": "stage"}, "name": "n", "source": {"kind": "scratch_library", "asset_type": "costume", "search_terms": ["a"]}}`, "add_costume cannot target the stage")
		expectDecodeError[DeleteSpriteOp](t, `{"op": "delete_sprite", "op_id": "o", "name": "n", "target": {"kind": "current_target"}}`, "delete_sprite requires an existing_sprite target")
		expectDecodeError[ImageGenerationSource](t, `{"kind": "image_generation", "reason": "r", "prompt": "p", "style": "s", "width": 100, "height": 100, "sheet_layout": "1x1"}`, "width and height must be an allowed image size pair")
		expectDecodeError[AddBackdropOp](t, `{"op": "add_backdrop", "op_id": "o", "source_need_id": "n", "name": "n", "source": {"kind": "scratch_library", "asset_type": "costume", "search_terms": ["a"]}}`, "/asset_type: Input should be 'backdrop'")
		expectDecodeError[AddBackdropOp](t, `{"op": "add_backdrop", "op_id": "o", "source_need_id": "n", "name": "n", "source": {"kind": "bogus"}}`, "/source: Input tag 'bogus' found using 'kind' does not match any of the expected tags: 'scratch_library', 'image_generation'")
	})
	t.Run("ProgramMetadataAndProccodes", func(t *testing.T) {
		expectDecodeError[ProgramMetadata](t, `{"summary": "s", "remove_script_ids": ["bad"]}`, "remove_script_ids must use script_N style ids")
		expectDecodeError[ProgramMetadata](t, `{"summary": "s", "schema_version": 3}`, "/schema_version: Input should be 1 or 2")
		expectDecodeError[ProgramIR](t, `{
			"schema_version": 1, "summary": "s", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
			"procedures": [
				{"id": "procedure_a", "label": "jump", "body": [{"opcode": "looks_show"}]},
				{"id": "procedure_b", "label": "jump", "body": [{"opcode": "looks_show"}]}
			],
			"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [{"opcode": "event_whenflagclicked"}]}]
		}`, "procedure proccodes must be unique")
		expectDecodeError[Procedure](t, `{"id": "procedure_a", "label": "a%b", "body": [{"opcode": "looks_show"}]}`, "procedure label cannot contain Scratch placeholder characters")
		expectDecodeError[Procedure](t, `{"id": "procedure_a", "label": "a", "parameters": [{"id": "arg_x", "name": "x", "kind": "string_number"}, {"id": "arg_x", "name": "y", "kind": "string_number"}], "body": [{"opcode": "looks_show"}]}`, "procedure parameter ids must be unique")
		expectDecodeError[Script](t, `{"id": "bad", "purpose": "p", "x": 0, "y": 0, "blocks": [{"opcode": "looks_show"}]}`, "/id: String should match pattern '^script_[A-Za-z0-9_-]+$'")
	})
	t.Run("ProjectSharedState", func(t *testing.T) {
		expectDecodeError[ProjectSharedState](t, `{"variables": [{"ref_id": "a", "name": "X", "scope": "global"}, {"ref_id": "b", "name": "x", "scope": "global"}], "lists": [], "broadcasts": []}`, "variables names must be unique")
		expectDecodeError[ProjectSharedState](t, `{"variables": [], "lists": [], "broadcasts": [{"ref_id": "a", "name": "X"}, {"ref_id": "b", "name": "x"}]}`, "broadcast names must be unique")
		expectDecodeError[ProjectSharedState](t, `{"variables": [{"ref_id": "a", "name": "X", "scope": "global"}], "lists": [{"ref_id": "a", "name": "Y", "scope": "global"}], "broadcasts": []}`, "shared state ref_id values must be unique")
	})
	t.Run("ProjectManifest", func(t *testing.T) {
		duplicateOpID := stringsReplaceAll(validManifestJSON, `"resource_ops": []`,
			`"resource_ops": [`+
				`{"op":"add_backdrop","op_id":"op_1","source_need_id":"n","name":"a","source":{"kind":"scratch_library","asset_type":"backdrop","search_terms":["a"]}},`+
				`{"op":"add_backdrop","op_id":"op_1","source_need_id":"n","name":"b","source":{"kind":"scratch_library","asset_type":"backdrop","search_terms":["b"]}}`+
				`]`)
		expectDecodeError[ProjectManifest](t, duplicateOpID, "resource op_id values must be unique")
		badCreated := stringsReplaceAll(validManifestJSON, `"program_targets": []`,
			`"program_targets": [{"job_id":"j","behavior_ids":["b"],"owner_entity_id":"e","target":{"kind":"created_sprite","op_id":"missing"},"mode":"append","intent":"i"}]`)
		expectDecodeError[ProjectManifest](t, badCreated, "created_sprite must reference a prior add_sprite or add_builtin_sprite operation")
		duplicateExtensions := stringsReplaceAll(validManifestJSON, `"summary": "Create a sprite",`, `"summary": "Create a sprite", "required_extensions": ["pen", "pen"],`)
		expectDecodeError[ProjectManifest](t, duplicateExtensions, "required_extensions must be unique")
		duplicateJobs := stringsReplaceAll(validManifestJSON, `"program_targets": []`,
			`"program_targets": [`+
				`{"job_id":"j","behavior_ids":["b"],"owner_entity_id":"e","target":{"kind":"stage"},"mode":"append","intent":"i"},`+
				`{"job_id":"j","behavior_ids":["b"],"owner_entity_id":"e","target":{"kind":"current_target"},"mode":"append","intent":"i"}`+
				`]`)
		expectDecodeError[ProjectManifest](t, duplicateJobs, "program job_id values must be unique")
		duplicateTargets := stringsReplaceAll(validManifestJSON, `"program_targets": []`,
			`"program_targets": [`+
				`{"job_id":"j1","behavior_ids":["b"],"owner_entity_id":"e","target":{"kind":"stage"},"mode":"append","intent":"i"},`+
				`{"job_id":"j2","behavior_ids":["b"],"owner_entity_id":"e","target":{"kind":"stage"},"mode":"append","intent":"i"}`+
				`]`)
		expectDecodeError[ProjectManifest](t, duplicateTargets, "program targets must be unique")
	})
}

func TestAddBuiltinSpriteDefaults(t *testing.T) {
	raw := `{"op": "add_builtin_sprite", "op_id": "op_1", "source_need_id": "need_1", "owner_entity_id": "entity_1",
		"name": "Milk", "x": 0, "y": 0, "size": 100,
		"source": {"kind": "scratch_library", "asset_type": "sprite", "search_terms": ["Milk"]},
		"include_costumes": "all"}`
	dumped, err := decodeResourceOperationDump(mustParseJSON(t, raw).(map[string]any))
	if err != nil {
		t.Fatalf("decodeResourceOperationDump failed: %v", err)
	}
	if dumped["include_sounds"] != "all" {
		t.Fatalf("expected include_sounds default all, got %v", dumped["include_sounds"])
	}
}
