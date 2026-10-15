package schema

import (
	"errors"
	"strings"
	"testing"

	"github.com/ai-scratch-web/backend/internal/jsonx"
)

func backdropResource(withSelectAfter bool) map[string]any {
	value := map[string]any{
		"op":             "add_backdrop",
		"op_id":          "backdrop_1",
		"source_need_id": "need_1",
		"name":           "森林",
		"source":         map[string]any{"kind": "scratch_library", "asset_type": "backdrop", "search_terms": []any{"森林"}},
	}
	if withSelectAfter {
		value["select_after"] = false
	}
	return value
}

func TestAddBackdropNormalizesCompatibilityFieldsAndDefaults(t *testing.T) {
	raw := backdropResource(false)
	raw["target"] = map[string]any{"kind": "stage"}
	raw["x"] = nil
	raw["y"] = nil
	raw["size"] = nil
	normalizedValue, diagnostics, err := NormalizeResourceOp(raw)
	if err != nil {
		t.Fatalf("NormalizeResourceOp failed: %v", err)
	}
	normalized := normalizedValue.(map[string]any)
	if normalized["select_after"] != false {
		t.Fatalf("expected select_after default false")
	}
	if _, present := normalized["target"]; present {
		t.Fatalf("expected target to be removed")
	}
	for _, field := range []string{"x", "y", "size"} {
		if _, present := normalized[field]; present {
			t.Fatalf("expected %s to be removed", field)
		}
	}
	codes := make([]string, 0, len(diagnostics))
	for _, issue := range diagnostics {
		codes = append(codes, issue.Code)
	}
	want := []string{"DEFAULT_APPLIED", "REDUNDANT_STAGE_TARGET_REMOVED", "NULL_FIELD_REMOVED", "NULL_FIELD_REMOVED", "NULL_FIELD_REMOVED"}
	if strings.Join(codes, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected diagnostics: %v", codes)
	}
	dumped, err := decodeResourceOperationDump(normalized)
	if err != nil {
		t.Fatalf("resource operation validation failed: %v", err)
	}
	if dumped["select_after"] != false {
		t.Fatalf("expected dumped select_after false")
	}
	if _, present := dumped["target"]; present {
		t.Fatalf("expected dumped operation to omit target")
	}
}

func TestAddCostumeDefaultsSelectAfterToFalse(t *testing.T) {
	operation := map[string]any{
		"op":             "add_costume",
		"op_id":          "costume_1",
		"source_need_id": "need_1",
		"target":         map[string]any{"kind": "current_target"},
		"name":           "走路",
		"source":         map[string]any{"kind": "scratch_library", "asset_type": "costume", "search_terms": []any{"走路"}},
	}
	normalizedValue, _, err := NormalizeResourceOp(operation)
	if err != nil {
		t.Fatalf("NormalizeResourceOp failed: %v", err)
	}
	dumped, err := decodeResourceOperationDump(normalizedValue.(map[string]any))
	if err != nil {
		t.Fatalf("resource operation validation failed: %v", err)
	}
	if dumped["select_after"] != false {
		t.Fatalf("expected select_after default false")
	}
}

func TestAddBackdropReportsIndependentNormalizationErrors(t *testing.T) {
	cases := []struct {
		field string
		value any
		code  string
	}{
		{"target", map[string]any{"kind": "existing_sprite", "name": "猫"}, "INVALID_BACKDROP_TARGET"},
		{"x", 10, "INVALID_BACKDROP_PLACEMENT"},
		{"source", map[string]any{"kind": "scratch_library", "asset_type": "costume", "search_terms": []any{"森林"}}, "INVALID_BACKDROP_SOURCE"},
	}
	for _, testCase := range cases {
		raw := backdropResource(true)
		raw[testCase.field] = testCase.value
		_, _, err := NormalizeResourceOp(raw)
		var resourceError *ResourceContractError
		if !errors.As(err, &resourceError) {
			t.Fatalf("expected ResourceContractError for %s, got %v", testCase.field, err)
		}
		issue := resourceError.Issue.AsDict()
		if issue["code"] != testCase.code {
			t.Fatalf("unexpected code: %v", issue["code"])
		}
		if !strings.HasPrefix(issue["path"].(string), "/resource_ops/0/") {
			t.Fatalf("unexpected path: %v", issue["path"])
		}
		if issue["operation"] != "add_backdrop" || issue["repair_scope"] != "resource_op" || issue["repair_id"] != "backdrop_1" {
			t.Fatalf("unexpected issue: %#v", issue)
		}
	}
}

func TestResourceContractIssueAsDictKeys(t *testing.T) {
	issue := NewResourceContractIssue("CODE", "/path", "op", []any{1, 2, 3}, "expected", stringPtr("op_1"))
	dict := issue.AsDict()
	if len(dict) != 7 {
		t.Fatalf("expected exactly 7 keys, got %#v", dict)
	}
	for _, key := range []string{"code", "path", "operation", "actual", "expected", "repair_scope", "repair_id"} {
		if _, present := dict[key]; !present {
			t.Fatalf("missing key %s in %#v", key, dict)
		}
	}
	big := strings.Repeat("x", 4096)
	bounded := NewResourceContractIssue("CODE", "/path", "op", big, "expected", nil).AsDict()
	actual := bounded["actual"].(string)
	if len(actual) != MaxBoundedActual || !strings.HasSuffix(actual, "...") {
		t.Fatalf("unexpected bounded actual length: %d", len(actual))
	}
}

func TestManifestResourceOpRuleDetail(t *testing.T) {
	raw := strings.Replace(validManifestJSON, `"resource_ops": []`,
		`"resource_ops": [{"op":"add_backdrop","op_id":"backdrop_bad","name":"森林","source":{"kind":"scratch_library","asset_type":"costume","search_terms":["森林"]}}]`, 1)
	_, err := decodeTarget[ProjectManifest](t, raw)
	var resourceError *ResourceContractError
	if !errors.As(err, &resourceError) {
		t.Fatalf("expected ResourceContractError, got %v", err)
	}
	if resourceError.Issue.Code != "INVALID_BACKDROP_SOURCE" {
		t.Fatalf("unexpected code: %s", resourceError.Issue.Code)
	}
	if resourceError.Issue.Path != "/resource_ops/0/source/asset_type" {
		t.Fatalf("unexpected path: %s", resourceError.Issue.Path)
	}
	if resourceError.Issue.Operation != "add_backdrop" || resourceError.Issue.RepairScope != "resource_op" {
		t.Fatalf("unexpected issue: %#v", resourceError.Issue)
	}
	if !strings.HasSuffix(resourceError.Issue.Expected, "asset_type=backdrop") {
		t.Fatalf("unexpected expected text: %s", resourceError.Issue.Expected)
	}
	if resourceError.Error() != "INVALID_BACKDROP_SOURCE at /resource_ops/0/source/asset_type: "+resourceError.Issue.Expected {
		t.Fatalf("unexpected error text: %s", resourceError.Error())
	}
}

func TestNormalizeProjectManifestPayloadNormalizesTargetsAndOps(t *testing.T) {
	raw := map[string]any{
		"resource_ops": []any{map[string]any{
			"op":     "add_backdrop",
			"op_id":  "backdrop_1",
			"name":   "森林",
			"target": map[string]any{"kind": "stage"},
			"source": map[string]any{"kind": "scratch_library", "asset_type": "backdrop", "search_terms": []any{"森林"}},
		}},
		"program_targets": []any{map[string]any{
			"target": map[string]any{"kind": "stage", "name": "Stage"},
		}},
	}
	normalizedValue, diagnostics, err := NormalizeProjectManifestPayload(raw)
	if err != nil {
		t.Fatalf("NormalizeProjectManifestPayload failed: %v", err)
	}
	normalized := normalizedValue.(map[string]any)
	operations := normalized["resource_ops"].([]any)
	operation := operations[0].(map[string]any)
	if operation["select_after"] != false {
		t.Fatalf("expected select_after default")
	}
	if _, present := operation["target"]; present {
		t.Fatalf("expected redundant stage target removed")
	}
	targets := normalized["program_targets"].([]any)
	programTarget := targets[0].(map[string]any)
	target := programTarget["target"].(map[string]any)
	if _, present := target["name"]; present {
		t.Fatalf("expected redundant target name removed")
	}
	codes := make([]string, 0, len(diagnostics))
	for _, issue := range diagnostics {
		codes = append(codes, issue.Code)
	}
	want := []string{"DEFAULT_APPLIED", "REDUNDANT_STAGE_TARGET_REMOVED", "REDUNDANT_TARGET_FIELD_REMOVED"}
	if strings.Join(codes, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected diagnostics: %v", codes)
	}
	if diagnostics[2].RepairScope != "manifest" {
		t.Fatalf("expected manifest repair scope: %#v", diagnostics[2])
	}
}

func TestValidateProjectProgramResourceRefs(t *testing.T) {
	manifest := mustDecode[ProjectManifest](t, strings.Replace(validManifestJSON, `"resource_ops": []`,
		`"resource_ops": [{"op":"add_builtin_sprite","op_id":"op_milk","source_need_id":"need_milk","owner_entity_id":"entity_packet","name":"Milk","x":0,"y":175,"size":100,"source":{"kind":"scratch_library","asset_type":"sprite","search_terms":["Milk"]},"include_costumes":"all"}]`, 1))
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 2, "summary": "refs", "remove_script_ids": [],
		"variables": [], "lists": [], "broadcasts": [], "procedures": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked"},
			{"opcode": "looks_switchcostumeto", "inputs": {"COSTUME": {"kind": "resource_ref", "resource_type": "costume", "op_id": "op_missing"}}}
		]}]
	}`)
	_, err := ValidateProjectProgramResourceRefs(program, manifest, nil)
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	if contractError.Issues[0].Path != "$.scripts[0].blocks[1].inputs.COSTUME.ref.op_id" {
		t.Fatalf("unexpected issue: %#v", contractError.Issues[0])
	}
	if contractError.Issues[0].Message != "resource_ref must reference a manifest resource operation" {
		t.Fatalf("unexpected message: %s", contractError.Issues[0].Message)
	}
}

func TestValidateProjectProgramStateRefs(t *testing.T) {
	manifest := mustDecode[ProjectManifest](t, strings.Replace(validManifestJSON,
		`"shared_state": {"variables": [], "lists": [], "broadcasts": []}`,
		`"shared_state": {"variables": [{"ref_id": "state_score", "name": "score", "value": 0, "scope": "global"}], "lists": [], "broadcasts": []}`, 1))
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 2, "summary": "refs", "remove_script_ids": [],
		"variables": [], "lists": [], "broadcasts": [], "procedures": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked"},
			{"opcode": "data_setvariableto", "fields": {"VARIABLE": {"kind": "state_ref", "state_kind": "list", "ref_id": "state_score"}}, "inputs": {"VALUE": {"kind": "text", "value": "1"}}}
		]}]
	}`)
	_, err := ValidateProjectProgramStateRefs(program, manifest)
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	if contractError.Issues[0].Path != "$.scripts[0].blocks[1].fields.VARIABLE.state_kind" {
		t.Fatalf("unexpected issue: %#v", contractError.Issues[0])
	}
	if contractError.Issues[0].Message != "state_ref type does not match manifest state" {
		t.Fatalf("unexpected message: %s", contractError.Issues[0].Message)
	}
}

func makeBlueprintPayload(cyclic bool) string {
	cyclicCatch := `["b_milk_spawn"]`
	cyclicGameOver := `["b_init_cat"]`
	if cyclic {
		cyclicCatch = `["b_milk_spawn", "b_init_cat"]`
		cyclicGameOver = `["b_milk_catch"]`
	}
	return `{
		"schema_version": 2,
		"task_spec_hash": "` + strings.Repeat("a", 64) + `",
		"entity_bindings": [
			{"entity_id": "entity_stage", "resolution": {"kind": "existing_target", "target_ref": {"kind": "stage"}}},
			{"entity_id": "entity_cat", "resolution": {"kind": "existing_target", "target_ref": {"kind": "current_target"}}},
			{"entity_id": "entity_milk", "resolution": {"kind": "new_sprite", "need_id": "need_milk_sprite"}}
		],
		"asset_needs": [
			{"need_id": "need_milk_sprite", "kind": "sprite", "purpose": "Milk", "search_terms": ["Milk"], "fallback_order": ["scratch_library"], "owner_entity_id": "entity_milk"}
		],
		"state_plan": [
			{"ref_id": "score", "kind": "variable", "scope": "global", "name": "score", "initial_value": 0}
		],
		"behavior_graph": [
			{"behavior_id": "b_init_cat", "owner_entity_id": "entity_cat"},
			{"behavior_id": "b_cat_left", "owner_entity_id": "entity_cat"},
			{"behavior_id": "b_cat_right", "owner_entity_id": "entity_cat"},
			{"behavior_id": "b_milk_spawn", "owner_entity_id": "entity_milk"},
			{"behavior_id": "b_milk_fall", "owner_entity_id": "entity_milk", "depends_on": ["b_milk_spawn"]},
			{"behavior_id": "b_milk_catch", "owner_entity_id": "entity_milk", "depends_on": ` + cyclicCatch + `},
			{"behavior_id": "b_game_over", "owner_entity_id": "entity_cat", "depends_on": ` + cyclicGameOver + `}
		],
		"program_jobs": [
			{"job_id": "job_cat", "owner_entity_id": "entity_cat", "mode": "rewrite", "behavior_ids": ["b_init_cat", "b_cat_left", "b_cat_right", "b_game_over"]},
			{"job_id": "job_milk", "owner_entity_id": "entity_milk", "mode": "append", "behavior_ids": ["b_milk_spawn", "b_milk_fall", "b_milk_catch"]}
		],
		"acceptance_tests": []
	}`
}

func TestProjectBlueprintDetectsJobLevelCyclicDependency(t *testing.T) {
	acyclic := mustDecode[ProjectBlueprint](t, makeBlueprintPayload(false))
	if acyclic.SchemaVersion != 2 {
		t.Fatalf("unexpected schema version")
	}
	if _, err := decodeTarget[ProjectBlueprint](t, makeBlueprintPayload(true)); err == nil ||
		err.Error() != "blueprint program jobs dependencies cannot contain a cycle" {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestProjectBlueprintRejectsAcceptanceTimeoutOverflow(t *testing.T) {
	raw := strings.Replace(makeBlueprintPayload(false), `"acceptance_tests": []`,
		`"acceptance_tests": [`+
			`{"test_id": "t1", "source_criterion_id": "c1", "related_behavior_ids": ["b_init_cat"], "trigger": {"kind": "green_flag"}, "expect": [{"kind": "scripts_stopped"}], "timeout_ms": 3000},`+
			`{"test_id": "t2", "source_criterion_id": "c1", "related_behavior_ids": ["b_init_cat"], "trigger": {"kind": "green_flag"}, "expect": [{"kind": "scripts_stopped"}], "timeout_ms": 2000}`+
			`]`, 1)
	if _, err := decodeTarget[ProjectBlueprint](t, raw); err == nil ||
		err.Error() != "acceptance test total timeout must not exceed 4000ms" {
		t.Fatalf("expected timeout overflow error, got %v", err)
	}
}

func TestNormalizeProjectBlueprintPayloadConvertsModesToAppend(t *testing.T) {
	raw := mustParseJSON(t, deadlockBlueprintPayload())
	normalized := NormalizeProjectBlueprintPayload(raw).(map[string]any)
	jobs := normalized["program_jobs"].([]any)
	for index, job := range jobs {
		if job.(map[string]any)["mode"] != "append" {
			t.Fatalf("job %d mode = %v, want append", index, job.(map[string]any)["mode"])
		}
	}
	blueprint := mustDecode[ProjectBlueprint](t, mustMarshalString(t, normalized))
	for _, job := range blueprint.ProgramJobs {
		if job.Mode != "append" {
			t.Fatalf("decoded job mode = %s, want append", job.Mode)
		}
	}
}

func deadlockBlueprintPayload() string {
	return `{
		"schema_version": 2,
		"task_spec_hash": "` + strings.Repeat("a", 64) + `",
		"asset_needs": [
			{"need_id": "need_milk", "kind": "sprite", "purpose": "红包角色", "owner_entity_id": "entity_packet", "search_terms": ["Milk", "红包"], "fallback_order": ["scratch_library"]}
		],
		"entity_bindings": [
			{"entity_id": "entity_cat", "resolution": {"kind": "existing_target", "target_ref": {"kind": "existing_sprite", "name": "角色1"}}},
			{"entity_id": "entity_packet", "resolution": {"kind": "new_sprite", "need_id": "need_milk"}}
		],
		"state_plan": [
			{"ref_id": "state_score", "name": "state_score", "kind": "variable", "scope": "global", "initial_value": 0}
		],
		"behavior_graph": [
			{"behavior_id": "b_cat_init", "owner_entity_id": "entity_cat", "depends_on": []},
			{"behavior_id": "b_packet_spawn", "owner_entity_id": "entity_packet", "depends_on": []}
		],
		"program_jobs": [
			{"job_id": "job_cat", "owner_entity_id": "entity_cat", "mode": "rewrite", "behavior_ids": ["b_cat_init"]},
			{"job_id": "job_packet", "owner_entity_id": "entity_packet", "mode": "modify", "behavior_ids": ["b_packet_spawn"]}
		],
		"acceptance_tests": []
	}`
}

func deadlockManifestJSON() string {
	return `{
		"schema_version": 3,
		"scope": "project",
		"summary": "接红包小游戏",
		"assumptions": [],
		"required_extensions": [],
		"resource_ops": [
			{"op": "add_builtin_sprite", "op_id": "op_milk", "source_need_id": "need_milk", "owner_entity_id": "entity_packet",
			 "name": "Milk", "x": 0, "y": 175, "size": 100,
			 "source": {"kind": "scratch_library", "asset_type": "sprite", "search_terms": ["Milk"]},
			 "include_costumes": "all"}
		],
		"shared_state": {
			"variables": [{"ref_id": "state_score", "name": "得分", "scope": "global", "value": 0}],
			"lists": [], "broadcasts": []
		},
		"local_state": [],
		"program_targets": [
			{"job_id": "job_cat", "behavior_ids": ["b_cat_init"], "owner_entity_id": "entity_cat",
			 "target": {"kind": "existing_sprite", "name": "角色1"}, "mode": "append", "intent": "小猫初始化与移动"},
			{"job_id": "job_packet", "behavior_ids": ["b_packet_spawn"], "owner_entity_id": "entity_packet",
			 "target": {"kind": "created_sprite", "op_id": "op_milk"}, "mode": "append", "intent": "红包生成与飘落"}
		]
	}`
}

func TestLineageValidationAcceptsAppendWhenBlueprintHadRewrite(t *testing.T) {
	blueprint := mustDecode[ProjectBlueprint](t, deadlockBlueprintPayload())
	if blueprint.ProgramJobs[0].Mode != "rewrite" || blueprint.ProgramJobs[1].Mode != "modify" {
		t.Fatalf("expected raw modes to be preserved: %#v", blueprint.ProgramJobs)
	}
	manifest := mustDecode[ProjectManifest](t, deadlockManifestJSON())
	if err := ValidateBlueprintManifestLineage(blueprint, manifest); err != nil {
		t.Fatalf("expected lineage to accept append targets, got %v", err)
	}
}

func TestValidateBlueprintManifestLineageRejectsJobFieldMismatch(t *testing.T) {
	blueprint := mustDecode[ProjectBlueprint](t, deadlockBlueprintPayload())
	manifest := mustDecode[ProjectManifest](t, strings.Replace(deadlockManifestJSON(),
		`"job_id": "job_cat", "behavior_ids": ["b_cat_init"], "owner_entity_id": "entity_cat"`,
		`"job_id": "job_cat", "behavior_ids": ["b_packet_spawn"], "owner_entity_id": "entity_cat"`, 1))
	if err := ValidateBlueprintManifestLineage(blueprint, manifest); err == nil ||
		err.Error() != "manifest program target must preserve Blueprint job fields" {
		t.Fatalf("expected job field mismatch, got %v", err)
	}
}

func TestScratchTaskSpecLineageBranches(t *testing.T) {
	base := `{
		"schema_version": 2, "title": "t", "goal": "g",
		"entities": [{"entity_id": "entity_cat", "role": "sprite", "description": "cat"}],
		"state": [{"state_id": "state_score", "kind": "variable", "scope": "global"}],
		"behaviors": [{"behavior_id": "b_cat", "owner_entity_id": "entity_cat", "trigger": {"kind": "green_flag"},
			"actions": [{"kind": "move_steps", "steps": {"kind": "literal", "value": 10}}], READS}],
		"success_criteria": [{"criterion_id": "c1", "description": "d", "related_behavior_ids": ["b_cat"], "priority": "must", "verification": "executable"}]
	}`
	valid := strings.Replace(base, "READS", `"reads": ["state_score"]`, 1)
	if _, err := decodeTarget[ScratchTaskSpec](t, valid); err != nil {
		t.Fatalf("expected valid task spec, got %v", err)
	}
	unknownState := strings.Replace(base, "READS", `"reads": ["state_missing"]`, 1)
	if _, err := decodeTarget[ScratchTaskSpec](t, unknownState); err == nil ||
		err.Error() != "behavior reads and writes must reference declared state" {
		t.Fatalf("expected unknown state error, got %v", err)
	}
	unknownCriterion := strings.Replace(valid, `"related_behavior_ids": ["b_cat"]`, `"related_behavior_ids": ["b_missing"]`, 1)
	if _, err := decodeTarget[ScratchTaskSpec](t, unknownCriterion); err == nil ||
		err.Error() != "success criterion must reference declared behaviors" {
		t.Fatalf("expected unknown behavior error, got %v", err)
	}
	badTrigger := strings.Replace(valid, `{"kind": "green_flag"}`, `{"kind": "broadcast_received", "state_id": "state_score"}`, 1)
	if _, err := decodeTarget[ScratchTaskSpec](t, badTrigger); err == nil ||
		err.Error() != "broadcast trigger must reference a broadcast state" {
		t.Fatalf("expected broadcast trigger error, got %v", err)
	}
}

func TestProjectManifestDeploymentCapabilities(t *testing.T) {
	previous := GetDeploymentSettings()
	t.Cleanup(func() { SetDeploymentSettings(previous) })

	destructiveManifest := strings.Replace(validManifestJSON, `"resource_ops": []`,
		`"resource_ops": [{"op":"delete_sprite","op_id":"op_1","name":"猫","target":{"kind":"existing_sprite","name":"猫"}}]`, 1)
	if _, err := decodeTarget[ProjectManifest](t, destructiveManifest); err == nil ||
		err.Error() != "destructive resource operations are disabled by this deployment" {
		t.Fatalf("expected destructive rejection, got %v", err)
	}

	settings := previous
	settings.AIDestructiveResourceOpsEnabled = true
	settings.AIProjectAssetsEnabled = false
	SetDeploymentSettings(settings)
	if _, err := decodeTarget[ProjectManifest](t, destructiveManifest); err == nil ||
		err.Error() != "project asset operations are disabled by this deployment" {
		t.Fatalf("expected project asset rejection, got %v", err)
	}

	settings = previous
	settings.AISoundAssetsEnabled = false
	SetDeploymentSettings(settings)
	soundManifest := strings.Replace(validManifestJSON, `"resource_ops": []`,
		`"resource_ops": [{"op":"add_sound","op_id":"op_1","source_need_id":"need_1","target":{"kind":"current_target"},"name":"喵","source":{"kind":"scratch_library","asset_type":"sound","search_terms":["喵"]}}]`, 1)
	if _, err := decodeTarget[ProjectManifest](t, soundManifest); err == nil ||
		err.Error() != "sound asset operations are disabled by this deployment" {
		t.Fatalf("expected sound asset rejection, got %v", err)
	}
}

func mustParseJSON(t *testing.T, raw string) any {
	t.Helper()
	value, err := ExtractSingleJSON(raw)
	if err != nil {
		t.Fatalf("ExtractSingleJSON failed: %v", err)
	}
	return value
}

func mustMarshalString(t *testing.T, value any) string {
	t.Helper()
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return string(encoded)
}
