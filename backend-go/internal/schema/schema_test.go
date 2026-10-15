package schema

import (
	"errors"
	"strings"
	"testing"
)

func decodeTarget[T any](t *testing.T, raw string) (*T, error) {
	t.Helper()
	target := new(T)
	return target, DecodeStrict([]byte(raw), target)
}

func mustDecode[T any](t *testing.T, raw string) *T {
	t.Helper()
	target := new(T)
	if err := DecodeStrict([]byte(raw), target); err != nil {
		t.Fatalf("DecodeStrict failed: %v (raw=%s)", err, raw)
	}
	return target
}

const validManifestJSON = `{
	"schema_version": 3,
	"scope": "project",
	"summary": "Create a sprite",
	"resource_ops": [],
	"program_targets": [],
	"shared_state": {"variables": [], "lists": [], "broadcasts": []}
}`

const sampleScriptJSON = `{
	"id": "script_new_1", "purpose": "Move", "x": 20, "y": 20,
	"blocks": [
		{"opcode": "event_whenflagclicked", "fields": {}, "inputs": {}, "substacks": {}},
		{"opcode": "motion_movesteps", "fields": {}, "inputs": {"STEPS": {"kind": "number", "value": 10}}, "substacks": {}}
	]
}`

func TestCanonicalContractJSONMatchesBrowserIntegralNumberSpelling(t *testing.T) {
	canonical, err := CanonicalContractJSON(map[string]any{"confidence": 1.0, "nested": []any{2.0, 2.5}})
	if err != nil {
		t.Fatalf("CanonicalContractJSON failed: %v", err)
	}
	if canonical != `{"confidence":1,"nested":[2,2.5]}` {
		t.Fatalf("unexpected canonical JSON: %s", canonical)
	}
}

func TestCanonicalContractJSONUsesModelDumpSemantics(t *testing.T) {
	taskSpec := &ScratchTaskSpec{
		SchemaVersion: 2,
		Title:         "标题",
		Goal:          "目标",
		Entities: []TaskEntity{{
			EntityID:    "entity_cat",
			Role:        "sprite",
			Description: "小猫",
		}},
		Behaviors: []TaskBehavior{{
			BehaviorID:    "b_cat",
			OwnerEntityID: "entity_cat",
			Trigger:       TriggerSpec{Kind: "green_flag"},
			Actions: []ActionSpec{{
				Kind:  "move_steps",
				Steps: &OperandSpec{Kind: "literal", Value: float64(10)},
			}},
		}},
		SuccessCriteria: []SuccessCriterionSpec{{
			CriterionID:        "c1",
			Description:        "移动",
			RelatedBehaviorIDs: []string{"b_cat"},
			Priority:           "must",
			Verification:       "manual",
		}},
	}
	canonical, err := CanonicalContractJSON(taskSpec)
	if err != nil {
		t.Fatalf("CanonicalContractJSON failed: %v", err)
	}
	if !strings.Contains(canonical, `"assumptions":[]`) {
		t.Fatalf("expected empty list default in canonical JSON: %s", canonical)
	}
	if strings.Contains(canonical, `"existing_target_ref"`) || strings.Contains(canonical, "null") {
		t.Fatalf("exclude_none semantics violated: %s", canonical)
	}
	if !strings.Contains(canonical, `"title":"标题"`) {
		t.Fatalf("ensure_ascii=False semantics violated: %s", canonical)
	}
	if !strings.Contains(canonical, `"value":10`) {
		t.Fatalf("integral value normalization violated: %s", canonical)
	}
}

func TestContractSHA256MatchesCanonicalJSON(t *testing.T) {
	value := map[string]any{"b": 2, "a": 1}
	hash, err := ContractSHA256(value)
	if err != nil {
		t.Fatalf("ContractSHA256 failed: %v", err)
	}
	if len(hash) != 64 {
		t.Fatalf("unexpected hash length: %s", hash)
	}
	canonical, _ := CanonicalContractJSON(value)
	again, _ := ContractSHA256(value)
	if hash != again {
		t.Fatalf("hash is not stable")
	}
	if canonical != `{"a":1,"b":2}` {
		t.Fatalf("unexpected canonical: %s", canonical)
	}
}

func TestRequirementsMissingQuestionsDefaultsToEmpty(t *testing.T) {
	value := mustDecode[RequirementsResponse](t, `{
		"ready": true, "title": "标题", "goal": "目标", "behavior_steps": ["步骤一"],
		"triggers": ["点击绿旗"], "controls": [], "success_criteria": ["完成"]
	}`)
	if value.MissingQuestions == nil || len(value.MissingQuestions) != 0 {
		t.Fatalf("missing_questions must default to [], got %#v", value.MissingQuestions)
	}
}

func TestValidationRequestRejectsProgramFieldsForManifest(t *testing.T) {
	_, err := decodeTarget[ValidationRequest](t, `{
		"kind": "manifest", "payload": {"schema_version": 3}, "mode": "append"
	}`)
	if err == nil || err.Error() != "manifest validation does not accept program-only fields" {
		t.Fatalf("expected manifest program-field rejection, got %v", err)
	}
	target := mustDecode[ValidationRequest](t, `{
		"kind": "manifest", "payload": {"schema_version": 3}
	}`)
	if target.TargetKind != "sprite" || target.Mode != "append" {
		t.Fatalf("unexpected defaults: %#v", target)
	}
}

func TestProjectManifestRecovery(t *testing.T) {
	manifest := mustDecode[ProjectManifest](t, validManifestJSON)
	if manifest.Scope != "project" || manifest.ResourceOps == nil || len(manifest.ResourceOps) != 0 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if manifest.SchemaVersion != 3 {
		t.Fatalf("unexpected schema version: %d", manifest.SchemaVersion)
	}
	cases := []struct {
		name     string
		raw      string
		wantPath string
	}{
		{"requires shared_state", strings.Replace(validManifestJSON, `"shared_state": {"variables": [], "lists": [], "broadcasts": []}`, `"assumptions": []`, 1), "/shared_state"},
		{"requires resource_ops", strings.Replace(validManifestJSON, `"resource_ops": [],`, "", 1), "/resource_ops"},
		{"requires program_targets", strings.Replace(validManifestJSON, `"program_targets": [],`, "", 1), "/program_targets"},
		{"requires shared_state collections", strings.Replace(validManifestJSON, `"variables": []`, `"variables": [], "extra": []`, 1), ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := decodeTarget[ProjectManifest](t, testCase.raw); err == nil {
				t.Fatalf("expected validation error")
			} else if testCase.wantPath != "" {
				var schemaError *SchemaValidationError
				if !errors.As(err, &schemaError) || schemaError.Path != testCase.wantPath {
					t.Fatalf("expected path %s, got %v", testCase.wantPath, err)
				}
			}
		})
	}
}

func TestProjectManifestRequiresSharedStateCollections(t *testing.T) {
	raw := strings.Replace(validManifestJSON, `{"variables": [], "lists": [], "broadcasts": []}`, `{"lists": [], "broadcasts": []}`, 1)
	_, err := decodeTarget[ProjectManifest](t, raw)
	if err == nil {
		t.Fatalf("expected missing shared_state.variables error")
	}
	var schemaError *SchemaValidationError
	if !errors.As(err, &schemaError) || schemaError.Path != "/shared_state/variables" {
		t.Fatalf("expected path /shared_state/variables, got %v", err)
	}
}

func TestProjectManifestRejectsLegacyTargetAliases(t *testing.T) {
	raw := strings.Replace(validManifestJSON, `"resource_ops": []`, `"resource_ops": [{"op": "select_backdrop", "op_id": "select-1", "name": "Forest", "target": {"kind": "stage"}}]`, 1)
	_, err := decodeTarget[ProjectManifest](t, raw)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected extra-field rejection, got %v", err)
	}
}

func TestProjectManifestRejectsUnsupportedSchemaVersion(t *testing.T) {
	raw := strings.Replace(validManifestJSON, `"schema_version": 3`, `"schema_version": 1`, 1)
	_, err := decodeTarget[ProjectManifest](t, raw)
	if err == nil || !strings.Contains(err.Error(), "/schema_version") {
		t.Fatalf("expected schema_version error, got %v", err)
	}
}

func TestProjectManifestMissingSchemaVersionReportsSchemaError(t *testing.T) {
	raw := strings.Replace(validManifestJSON, `"schema_version": 3,`, "", 1)
	_, err := decodeTarget[ProjectManifest](t, raw)
	if err == nil {
		t.Fatalf("expected missing schema_version error")
	}
	var schemaError *SchemaValidationError
	if !errors.As(err, &schemaError) || schemaError.Path != "/schema_version" {
		t.Fatalf("expected path /schema_version, got %v", err)
	}
}

func TestProjectManifestRequiresAppendForNonCurrentTargets(t *testing.T) {
	raw := strings.Replace(validManifestJSON, `"program_targets": []`,
		`"program_targets": [{"job_id": "job_1", "behavior_ids": ["b1"], "owner_entity_id": "e1", "target": {"kind": "stage"}, "mode": "modify", "intent": "x"}]`, 1)
	_, err := decodeTarget[ProjectManifest](t, raw)
	if err == nil || err.Error() != "stage, existing_sprite and created_sprite programs must use append" {
		t.Fatalf("expected append rule error, got %v", err)
	}
}

func TestProjectProgramSharedStateNormalizesOmittedListValues(t *testing.T) {
	sharedState := mustDecode[ProjectSharedState](t, `{"variables": [], "lists": [{"ref_id": "state_items", "name": "items", "value": [], "scope": "global"}], "broadcasts": []}`)
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 1, "summary": "list parity", "remove_script_ids": [],
		"variables": [], "lists": [{"name": "items", "scope": "global"}], "broadcasts": [],
		"scripts": [`+sampleScriptJSON+`]
	}`)
	result, err := ValidateProjectProgramSharedState(program, sharedState)
	if err != nil {
		t.Fatalf("expected shared state parity to pass, got %v", err)
	}
	if result != program {
		t.Fatalf("expected the program to be returned unchanged")
	}
}

func TestProjectProgramGlobalStateMustMatchManifest(t *testing.T) {
	sharedState := mustDecode[ProjectSharedState](t, `{"variables": [{"ref_id": "state_score", "name": "score", "value": 0, "scope": "global"}], "lists": [], "broadcasts": []}`)
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 1, "summary": "mismatch", "remove_script_ids": [],
		"variables": [], "lists": [], "broadcasts": [],
		"scripts": [`+sampleScriptJSON+`]
	}`)
	_, err := ValidateProjectProgramSharedState(program, sharedState)
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	if contractError.Issues[0].Path != "$.variables" || ToJSONPointer(contractError.Issues[0].Path) != "/variables" {
		t.Fatalf("unexpected issue: %#v", contractError.Issues[0])
	}
	if contractError.Issues[0].Message != "project program global variables must exactly repeat manifest.shared_state.variables" {
		t.Fatalf("unexpected message: %s", contractError.Issues[0].Message)
	}
}

const badProgramJSON = `{
	"schema_version": 1, "summary": "bad", "remove_script_ids": [],
	"variables": [], "lists": [], "broadcasts": [],
	"scripts": [{"id": "script_new_1", "purpose": "Move", "x": 20, "y": 20, "blocks": [
		{"opcode": "motion_movesteps", "fields": {}, "inputs": {"STEPS": {"kind": "number", "value": 10}}, "substacks": {}}
	]}]
}`

func TestValidateProgramContractsReportsStructuredPaths(t *testing.T) {
	program := mustDecode[ProgramIR](t, badProgramJSON)
	_, err := ValidateProgramContracts(program)
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	if contractError.Issues[0].Path != "$.scripts[0].blocks[0]" {
		t.Fatalf("unexpected issue path: %s", contractError.Issues[0].Path)
	}
	if contractError.Issues[0].Message != "script must start with an event hat block" {
		t.Fatalf("unexpected issue message: %s", contractError.Issues[0].Message)
	}
	if ToJSONPointer(contractError.Issues[0].Path) != "/scripts/0/blocks/0" {
		t.Fatalf("unexpected JSON pointer: %s", ToJSONPointer(contractError.Issues[0].Path))
	}
}

func TestValidateProgramContractsAcceptsValidProgram(t *testing.T) {
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 1, "summary": "ok", "remove_script_ids": [],
		"variables": [], "lists": [], "broadcasts": [],
		"scripts": [`+sampleScriptJSON+`]
	}`)
	result, err := ValidateProgramContracts(program, ProgramContractOptions{TargetKind: "sprite", AllowResourceRefs: true})
	if err != nil {
		t.Fatalf("expected program contract to pass, got %v", err)
	}
	if result != program {
		t.Fatalf("expected the program to be returned unchanged")
	}
}

func TestValidateProgramForMode(t *testing.T) {
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 1, "summary": "ok", "remove_script_ids": ["script_old"],
		"variables": [], "lists": [], "broadcasts": [],
		"scripts": [`+sampleScriptJSON+`]
	}`)
	if _, err := ValidateProgramForMode(program, "append"); err == nil || err.Error() != "append mode cannot remove scripts by id" {
		t.Fatalf("expected append removal rejection, got %v", err)
	}
	currentProgram := `{"scripts": [{"id": "script_old", "protected": true}]}`
	if _, err := ValidateProgramForMode(program, "modify", currentProgram); err == nil || err.Error() != "modify mode cannot remove protected scripts" {
		t.Fatalf("expected protected script rejection, got %v", err)
	}
	unprotected := `{"scripts": [{"id": "script_other", "protected": true}]}`
	if _, err := ValidateProgramForMode(program, "modify", unprotected); err != nil {
		t.Fatalf("expected modify to pass, got %v", err)
	}
}

func TestExtractSingleJSON(t *testing.T) {
	value, err := ExtractSingleJSON(`{"schema_version": 2, "summary": "test"}`)
	if err != nil {
		t.Fatalf("ExtractSingleJSON failed: %v", err)
	}
	object := value.(map[string]any)
	if object["summary"] != "test" || object["schema_version"] != int64(2) {
		t.Fatalf("unexpected parse result: %#v", object)
	}
	fenced, err := ExtractSingleJSON("  " + "```" + "json\n{\"a\": 1}\n" + "```" + "  ")
	if err != nil {
		t.Fatalf("fenced ExtractSingleJSON failed: %v", err)
	}
	if fenced.(map[string]any)["a"] != int64(1) {
		t.Fatalf("unexpected fenced result: %#v", fenced)
	}
	if _, err := ExtractSingleJSON(`{"a": 1} trailing`); err == nil || err.Error() != "trailing content after JSON" {
		t.Fatalf("expected trailing content error, got %v", err)
	}
	if _, err := ExtractSingleJSON("text " + "\x60\x60\x60" + " {a: 1}"); err == nil || err.Error() != "invalid JSON wrapper" {
		t.Fatalf("expected invalid wrapper error, got %v", err)
	}
	if _, err := ExtractSingleJSON(`{`); err == nil || !strings.HasPrefix(err.Error(), "invalid JSON response at line ") {
		t.Fatalf("expected invalid JSON response error, got %v", err)
	}
}

func TestNormalizeContractTextAndLogicalKey(t *testing.T) {
	normalized, err := NormalizeContractText("  Ｓｃｏｒｅ  ")
	if err != nil {
		t.Fatalf("NormalizeContractText failed: %v", err)
	}
	if normalized != "Score" {
		t.Fatalf("expected NFKC normalization, got %q", normalized)
	}
	key, err := LogicalKey("  Ｓｃｏｒｅ  ")
	if err != nil {
		t.Fatalf("LogicalKey failed: %v", err)
	}
	if key != "score" {
		t.Fatalf("expected folded key, got %q", key)
	}
	if _, err := NormalizeContractText("bad\x01text"); err == nil || err.Error() != "strings cannot contain control characters" {
		t.Fatalf("expected control character error, got %v", err)
	}
	if _, err := NormalizeContractText("   "); err == nil || err.Error() != "string must not be empty after normalization" {
		t.Fatalf("expected empty error, got %v", err)
	}
}

func TestToJSONPointer(t *testing.T) {
	cases := map[string]string{
		"$.scripts[0].blocks[0]": "/scripts/0/blocks/0",
		"$":                      "",
		"":                       "",
		"/already/pointer":       "/already/pointer",
		"$['a~b']['c/d']":        "/'a~0b'/'c/d'",
	}
	for input, want := range cases {
		if got := ToJSONPointer(input); got != want {
			t.Fatalf("ToJSONPointer(%q) = %q, want %q", input, got, want)
		}
	}
	if got := ToJSONPointer([]any{"scripts", 1}); got != "/scripts/1" {
		t.Fatalf("unexpected tuple pointer: %q", got)
	}
}

func TestProviderAndThinkingTables(t *testing.T) {
	if ProviderForTier("advanced") != "deepseek" || ProviderForTier("unknown") != "agnes" {
		t.Fatalf("unexpected provider mapping")
	}
	if ThinkingBudget("low") != 1024 || ThinkingBudget("unknown") != 2048 {
		t.Fatalf("unexpected thinking budget")
	}
	if DeepSeekReasoningEffort[ThinkingStrengthMax] != "high" || DeepSeekReasoningEffort[ThinkingStrengthLow] != "low" {
		t.Fatalf("unexpected deepseek effort table")
	}
}

func TestValidateRequestExtensionPermissions(t *testing.T) {
	request := mustDecode[UnifiedGenerationRequest](t, `{
		"session_id": "s", "request_id": "r", "messages": [{"role": "user", "content": "hi"}],
		"target": {"name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"},
		"scope": "project",
		"project_context": {"project_id": "p", "version": 1, "targets": [{"target_id": "t1", "revision": 1, "name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"}]}
	}`)
	previous := GetDeploymentSettings()
	t.Cleanup(func() { SetDeploymentSettings(previous) })
	settings := previous
	settings.AITextToSpeechExtensionEnabled = true
	SetDeploymentSettings(settings)
	manifest := mustDecode[ProjectManifest](t, strings.Replace(validManifestJSON, `"summary": "Create a sprite",`, `"summary": "Create a sprite", "required_extensions": ["text2speech"],`, 1))
	if err := ValidateRequestExtensionPermissions(request, manifest); err == nil || err.Error() != "text2speech requires explicit request permission" {
		t.Fatalf("expected permission error, got %v", err)
	}
	request.PermittedExtensions = []string{"text2speech"}
	if err := ValidateRequestExtensionPermissions(request, manifest); err != nil {
		t.Fatalf("expected permission grant to pass, got %v", err)
	}
}

func TestUnifiedGenerationRequestScopeRules(t *testing.T) {
	base := `{
		"session_id": "s", "request_id": "r", "messages": [{"role": "user", "content": "hi"}],
		"target": {"name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"}
	}`
	if _, err := decodeTarget[UnifiedGenerationRequest](t, strings.Replace(base, `"session_id"`, `"scope": "project", "session_id"`, 1)); err == nil {
		t.Fatalf("expected project scope without project_context to fail")
	}
	single := `{
		"session_id": "s", "request_id": "r", "messages": [{"role": "user", "content": "hi"}],
		"target": {"name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"},
		"project_context": {"project_id": "p", "version": 1, "targets": [{"target_id": "t1", "revision": 1, "name": "Sprite1", "is_stage": false, "has_scripts": false, "mode": "append"}]}
	}`
	if _, err := decodeTarget[UnifiedGenerationRequest](t, single); err == nil || err.Error() != "project_context is only valid for project generation" {
		t.Fatalf("expected single_sprite context rejection, got %v", err)
	}
}

func TestProgramIRLimits(t *testing.T) {
	deepBlock := `{"opcode": "looks_show"}`
	for index := 0; index < MaxBlockDepth+2; index++ {
		deepBlock = `{"opcode": "control_if", "inputs": {"CONDITION": {"kind": "block", "block": ` + deepBlock + `}}}`
	}
	raw := `{"schema_version": 1, "summary": "deep", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [], "scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [` + deepBlock + `]}]}`
	if _, err := decodeTarget[ProgramIR](t, raw); err == nil || err.Error() != "block nesting is too deep" {
		t.Fatalf("expected nesting error, got %v", err)
	}
	blockList := func(count int) string {
		items := make([]string, 0, count+1)
		items = append(items, `{"opcode": "event_whenflagclicked"}`)
		for index := 0; index < count; index++ {
			items = append(items, `{"opcode": "looks_show"}`)
		}
		return strings.Join(items, ",")
	}
	tooMany := `{"schema_version": 1, "summary": "many", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [], "scripts": [` +
		`{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [` + blockList(60) + `]},` +
		`{"id": "script_2", "purpose": "p", "x": 0, "y": 0, "blocks": [` + blockList(60) + `]}]}`
	if _, err := decodeTarget[ProgramIR](t, tooMany); err == nil || err.Error() != "program has too many blocks" {
		t.Fatalf("expected block count error, got %v", err)
	}
}
