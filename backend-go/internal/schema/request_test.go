package schema

import (
	"strings"
	"testing"
)

func lineageTaskSpecJSON() string {
	return `{
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
			{"criterion_id": "c_score", "description": "得分", "related_behavior_ids": ["b_init", "b_catch"], "priority": "must", "verification": "executable"}
		]
	}`
}

func lineageBlueprintJSON(hash string) string {
	return `{
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
}

func lineageManifestJSON() string {
	return `{
		"schema_version": 3,
		"scope": "project",
		"summary": "接红包小游戏",
		"resource_ops": [],
		"program_targets": [
			{"job_id": "job_cat", "behavior_ids": ["b_init", "b_catch"], "owner_entity_id": "entity_cat",
			 "target": {"kind": "current_target"}, "mode": "append", "intent": "小猫初始化与接红包"}
		],
		"shared_state": {
			"variables": [{"ref_id": "state_score", "name": "得分", "scope": "global", "value": 0}],
			"lists": [],
			"broadcasts": [{"ref_id": "state_hit", "name": "hit"}]
		}
	}`
}

func lineageProgramRequestJSON(t *testing.T, targetIndex int) string {
	t.Helper()
	taskSpec := mustDecode[ScratchTaskSpec](t, lineageTaskSpecJSON())
	hash, err := ContractSHA256(taskSpec)
	if err != nil {
		t.Fatalf("ContractSHA256 failed: %v", err)
	}
	programTarget := `{"job_id": "job_cat", "behavior_ids": ["b_init", "b_catch"], "owner_entity_id": "entity_cat", "target": {"kind": "current_target"}, "mode": "append", "intent": "小猫初始化与接红包"}`
	return `{
		"session_id": "s", "request_id": "r", "messages": [{"role": "user", "content": "hi"}],
		"target": {"name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"},
		"scope": "project",
		"project_context": {"project_id": "p", "version": 1, "targets": [{"target_id": "t1", "revision": 1, "name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"}]},
		"output_contract": "project_program",
		"requirements": {"ready": true, "title": "接红包", "goal": "小猫接住红包得分", "behavior_steps": ["初始化", "接红包"], "triggers": ["点击绿旗"], "controls": [], "success_criteria": ["得分"]},
		"task_spec": ` + lineageTaskSpecJSON() + `,
		"blueprint": ` + lineageBlueprintJSON(hash) + `,
		"plan": {"steps": ["初始化", "接红包"]},
		"manifest": ` + lineageManifestJSON() + `,
		"program_target": ` + programTarget + `,
		"target_index": ` + itoa(targetIndex) + `
	}`
}

func TestProjectProgramGenerationRequestRoundTrip(t *testing.T) {
	raw := lineageProgramRequestJSON(t, 0)
	request, err := decodeTarget[ProjectProgramGenerationRequest](t, raw)
	if err != nil {
		t.Fatalf("expected project program request to validate, got %v", err)
	}
	if request.OutputContract != "project_program" || request.TargetIndex != 0 {
		t.Fatalf("unexpected request: %#v", request)
	}
	if len(request.Manifest.ResourceOps) != 0 {
		t.Fatalf("unexpected resource ops")
	}
}

func TestProjectProgramGenerationRequestTargetIndexOutOfRange(t *testing.T) {
	raw := lineageProgramRequestJSON(t, 5)
	if _, err := decodeTarget[ProjectProgramGenerationRequest](t, raw); err == nil ||
		err.Error() != "target_index is outside manifest.program_targets" {
		t.Fatalf("expected target_index error, got %v", err)
	}
	mismatched := strings.Replace(lineageProgramRequestJSON(t, 0),
		`"intent": "小猫初始化与接红包"}`, `"intent": "另一段意图"}`, 1)
	if _, err := decodeTarget[ProjectProgramGenerationRequest](t, mismatched); err == nil ||
		err.Error() != "program_target must match manifest.program_targets[target_index]" {
		t.Fatalf("expected program_target mismatch, got %v", err)
	}
}
