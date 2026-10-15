package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeStrictRejectsNestedUnknownFields(t *testing.T) {
	raw := strings.Replace(validManifestJSON, `"summary": "Create a sprite",`,
		`"summary": "Create a sprite", "thinking": {"enabled": true, "bogus": 1},`, 1)
	if _, err := decodeTarget[ProjectManifest](t, raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected nested unknown field rejection, got %v", err)
	}
}

func TestBlockUnionValidation(t *testing.T) {
	if _, err := decodeTarget[ProgramIR](t, `{
		"schema_version": 1, "summary": "u", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked", "inputs": {"X": {"kind": "bogus"}}}
		]}]
	}`); err == nil || !strings.Contains(err.Error(), "does not match any of the expected tags") {
		t.Fatalf("expected union tag error, got %v", err)
	}
	if _, err := decodeTarget[ProgramIR](t, `{
		"schema_version": 1, "summary": "u", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked", "fields": {"V": null}}
		]}]
	}`); err == nil {
		t.Fatalf("expected null field value rejection")
	}
	if _, err := decodeTarget[ProgramIR](t, `{
		"schema_version": 1, "summary": "u", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "motion_movesteps", "inputs": {"STEPS": {"kind": "number", "value": "10"}}}
		]}]
	}`); err == nil || err.Error() != "number input requires a numeric value" {
		t.Fatalf("expected numeric literal error, got %v", err)
	}
}

func TestProjectContextSoftBudget(t *testing.T) {
	big := strings.Repeat("x", ProjectContextSoftBudgetBytes+1024)
	raw := `{
		"session_id": "s", "request_id": "r", "messages": [{"role": "user", "content": "hi"}],
		"target": {"name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"},
		"scope": "project",
		"project_context": {"project_id": "p", "version": 1, "targets": [{"target_id": "t1", "revision": 1, "name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"}], "shared_state": {"blob": "` + big + `"}}
	}`
	if _, err := decodeTarget[UnifiedGenerationRequest](t, raw); err == nil ||
		err.Error() != "project_context exceeds the 64 KiB soft budget" {
		t.Fatalf("expected soft budget error, got %v", err)
	}
}

func TestDecodeProjectGenerationRequestUnion(t *testing.T) {
	manifestRequest := `{
		"session_id": "s", "request_id": "r", "messages": [{"role": "user", "content": "hi"}],
		"target": {"name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"},
		"scope": "project",
		"project_context": {"project_id": "p", "version": 1, "targets": [{"target_id": "t1", "revision": 1, "name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"}]},
		"output_contract": "project_manifest"
	}`
	decoded, err := DecodeProjectGenerationRequest([]byte(manifestRequest))
	if err != nil {
		t.Fatalf("DecodeProjectGenerationRequest failed: %v", err)
	}
	if _, ok := decoded.(*ProjectManifestGenerationRequest); !ok {
		t.Fatalf("expected ProjectManifestGenerationRequest, got %T", decoded)
	}
}

func TestOptionalActionListAcceptsNull(t *testing.T) {
	spec := mustDecode[ActionSpec](t, `{"kind": "delete_this_clone", "actions": null}`)
	if spec.Actions != nil {
		t.Fatalf("expected nil actions")
	}
	if _, err := decodeTarget[ActionSpec](t, `{"kind": "loop", "mode": "repeat", "actions": []}`); err == nil {
		t.Fatalf("expected empty actions rejection")
	}
	if _, err := decodeTarget[ActionSpec](t, `{"kind": "loop", "mode": "repeat"}`); err == nil ||
		err.Error() != "loop requires actions" {
		t.Fatalf("expected missing actions error, got %v", err)
	}
}

func TestProgramContractsExtensionAndReporterRules(t *testing.T) {
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 1, "summary": "pen", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked"},
			{"opcode": "pen_clear"},
			{"opcode": "motion_xposition"}
		]}]
	}`)
	_, err := ValidateProgramContracts(program)
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	messages := make([]string, 0, len(contractError.Issues))
	for _, issue := range contractError.Issues {
		messages = append(messages, issue.Path+" "+issue.Message)
	}
	joined := strings.Join(messages, " | ")
	if !strings.Contains(joined, "extension pen is not declared for this program") {
		t.Fatalf("expected extension issue, got %s", joined)
	}
	if !strings.Contains(joined, "reporter blocks must be nested in an input") {
		t.Fatalf("expected reporter issue, got %s", joined)
	}
	allowed := DefaultProgramContractOptions()
	allowed.AllowedExtensions = map[string]bool{"pen": true}
	if _, err := ValidateProgramContracts(mustDecode[ProgramIR](t, `{
		"schema_version": 1, "summary": "pen", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked"},
			{"opcode": "pen_clear"}
		]}]
	}`), allowed); err != nil {
		t.Fatalf("expected allowed extension to pass, got %v", err)
	}
}

func TestTaskSpecBlueprintLineageRoundTrip(t *testing.T) {
	taskSpecJSON := `{
		"schema_version": 2, "title": "接红包", "goal": "小猫接住红包得分",
		"entities": [
			{"entity_id": "entity_cat", "role": "sprite", "description": "小猫"},
			{"entity_id": "entity_packet", "role": "sprite", "description": "红包"}
		],
		"state": [
			{"state_id": "state_score", "kind": "variable", "scope": "global", "initial_value": 0},
			{"state_id": "state_hit", "kind": "broadcast", "scope": "global"}
		],
		"behaviors": [
			{"behavior_id": "b_init", "owner_entity_id": "entity_cat", "trigger": {"kind": "green_flag"},
			 "actions": [{"kind": "set_state", "state_id": "state_score", "value": {"kind": "literal", "value": 0}}]},
			{"behavior_id": "b_catch", "owner_entity_id": "entity_cat", "trigger": {"kind": "broadcast_received", "state_id": "state_hit"},
			 "actions": [{"kind": "change_state", "state_id": "state_score", "delta": {"kind": "literal", "value": 1}},
						 {"kind": "broadcast", "state_id": "state_hit"}]}
		],
		"success_criteria": [
			{"criterion_id": "c_score", "description": "得分", "related_behavior_ids": ["b_init", "b_catch"], "priority": "must", "verification": "executable"},
			{"criterion_id": "c_manual", "description": "手动", "related_behavior_ids": ["b_init"], "priority": "should", "verification": "manual"}
		]
	}`
	taskSpec := mustDecode[ScratchTaskSpec](t, taskSpecJSON)
	hash, err := ContractSHA256(taskSpec)
	if err != nil {
		t.Fatalf("ContractSHA256 failed: %v", err)
	}
	blueprintJSON := `{
		"schema_version": 2,
		"task_spec_hash": "` + hash + `",
		"asset_needs": [],
		"entity_bindings": [
			{"entity_id": "entity_cat", "resolution": {"kind": "existing_target", "target_ref": {"kind": "current_target"}}},
			{"entity_id": "entity_packet", "resolution": {"kind": "existing_target", "target_ref": {"kind": "stage"}}}
		],
		"state_plan": [
			{"ref_id": "state_score", "kind": "variable", "scope": "global", "name": "score", "initial_value": 0},
			{"ref_id": "state_hit", "kind": "broadcast", "scope": "global", "name": "hit"}
		],
		"behavior_graph": [
			{"behavior_id": "b_init", "owner_entity_id": "entity_cat", "depends_on": [], "produces_broadcasts": [], "consumes_broadcasts": []},
			{"behavior_id": "b_catch", "owner_entity_id": "entity_cat", "depends_on": [], "produces_broadcasts": ["state_hit"], "consumes_broadcasts": ["state_hit"]}
		],
		"program_jobs": [
			{"job_id": "job_cat", "owner_entity_id": "entity_cat", "mode": "append", "behavior_ids": ["b_init", "b_catch"]}
		],
		"acceptance_tests": [
			{"test_id": "t_score", "source_criterion_id": "c_score", "related_behavior_ids": ["b_init", "b_catch"], "trigger": {"kind": "green_flag"},
			 "arrange": [], "expect": [{"kind": "variable", "state_id": "state_score", "comparator": "gte", "value": 0}], "timeout_ms": 1000}
		]
	}`
	blueprint := mustDecode[ProjectBlueprint](t, blueprintJSON)
	if err := ValidateTaskSpecBlueprintLineage(taskSpec, blueprint); err != nil {
		t.Fatalf("expected lineage to pass, got %v", err)
	}
	badHash := strings.Replace(blueprintJSON, hash, strings.Repeat("b", 64), 1)
	if _, err := decodeTarget[ProjectBlueprint](t, badHash); err != nil {
		t.Fatalf("blueprint decode failed: %v", err)
	}
	if err := ValidateTaskSpecBlueprintLineage(taskSpec, mustDecode[ProjectBlueprint](t, badHash)); err == nil ||
		err.Error() != "blueprint task_spec_hash does not match the canonical TaskSpec" {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
	badBroadcast := strings.Replace(blueprintJSON, `"consumes_broadcasts": ["state_hit"]`, `"consumes_broadcasts": []`, 1)
	if err := ValidateTaskSpecBlueprintLineage(taskSpec, mustDecode[ProjectBlueprint](t, badBroadcast)); err == nil {
		t.Fatalf("expected broadcast edge mismatch")
	}
	unknownBehavior := strings.Replace(blueprintJSON, `"behavior_ids": ["b_init", "b_catch"]`, `"behavior_ids": ["b_init", "b_missing"]`, 1)
	if _, err := decodeTarget[ProjectBlueprint](t, unknownBehavior); err == nil ||
		err.Error() != "program jobs must cover every behavior exactly once" {
		t.Fatalf("expected behavior coverage error, got %v", err)
	}
}

func TestProjectBlueprintNewSpriteBindingRule(t *testing.T) {
	raw := strings.Replace(makeBlueprintPayload(false),
		`{"need_id": "need_milk_sprite", "kind": "sprite", "purpose": "Milk", "search_terms": ["Milk"], "fallback_order": ["scratch_library"], "owner_entity_id": "entity_milk"}`,
		`{"need_id": "need_milk_sprite", "kind": "sprite", "purpose": "Milk", "search_terms": ["Milk"], "fallback_order": ["scratch_library"], "owner_entity_id": "entity_cat"}`, 1)
	if _, err := decodeTarget[ProjectBlueprint](t, raw); err == nil ||
		err.Error() != "new_sprite binding must reference its entity sprite need" {
		t.Fatalf("expected binding error, got %v", err)
	}
}

func TestManifestProgramTargetAsStringIsRejected(t *testing.T) {
	raw := strings.Replace(validManifestJSON, `"program_targets": []`,
		`"program_targets": [{"job_id": "job_1", "behavior_ids": ["b1"], "owner_entity_id": "e1", "target": "stage", "mode": "append", "intent": "x"}]`, 1)
	if _, err := decodeTarget[ProjectManifest](t, raw); err == nil {
		t.Fatalf("expected legacy target string to be rejected")
	}
}
