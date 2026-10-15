package schema

import (
	"errors"
	"strings"
	"testing"
)

func existingAssetProgram(t *testing.T) *ProgramIR {
	t.Helper()
	return mustDecode[ProgramIR](t, `{
		"schema_version": 2, "summary": "assets", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked"},
			{"opcode": "looks_switchcostumeto", "inputs": {"COSTUME": {"kind": "existing_asset_ref", "asset_type": "costume", "name": "cat"}}}
		]}]
	}`)
}

func TestValidateProjectProgramResourceRefsExistingAssets(t *testing.T) {
	manifest := mustDecode[ProjectManifest](t, validManifestJSON)
	program := existingAssetProgram(t)
	if _, err := ValidateProjectProgramResourceRefs(program, manifest, map[string][]string{"costume": {"cat"}}); err != nil {
		t.Fatalf("expected existing asset to resolve, got %v", err)
	}
	_, err := ValidateProjectProgramResourceRefs(program, manifest, map[string][]string{"costume": {"dog"}})
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	if contractError.Issues[0].Path != "$.scripts[0].blocks[1].inputs.COSTUME.name" ||
		contractError.Issues[0].Message != "existing_asset_ref must name an existing asset in the target snapshot" {
		t.Fatalf("unexpected issue: %#v", contractError.Issues[0])
	}
	_, err = ValidateProjectProgramResourceRefs(program, manifest, map[string][]string{"costume": {"cat", "cat"}})
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	if contractError.Issues[0].Message != "existing_asset_ref must resolve to exactly one asset in the target snapshot" {
		t.Fatalf("unexpected message: %s", contractError.Issues[0].Message)
	}
}

func TestValidateBlueprintManifestLineageStateProjection(t *testing.T) {
	blueprint := mustDecode[ProjectBlueprint](t, deadlockBlueprintPayload())
	manifest := mustDecode[ProjectManifest](t, strings.Replace(deadlockManifestJSON(),
		`"ref_id": "state_score"`, `"ref_id": "state_other"`, 1))
	err := ValidateBlueprintManifestLineage(blueprint, manifest)
	if err == nil || err.Error() != "manifest state must project every Blueprint state exactly once" {
		t.Fatalf("expected state projection error, got %v", err)
	}
}

func TestValidateBlueprintManifestLineageResourceNeed(t *testing.T) {
	blueprint := mustDecode[ProjectBlueprint](t, deadlockBlueprintPayload())
	manifest := mustDecode[ProjectManifest](t, strings.Replace(deadlockManifestJSON(),
		`"owner_entity_id": "entity_packet"`, `"owner_entity_id": "entity_cat"`, 1))
	err := ValidateBlueprintManifestLineage(blueprint, manifest)
	if err == nil || err.Error() != "manifest created sprite must match its Blueprint entity binding" {
		t.Fatalf("expected binding mismatch, got %v", err)
	}
}

func TestNormalizeResourceOpForValidation(t *testing.T) {
	raw := backdropResource(false)
	value, err := NormalizeResourceOpForValidation(raw)
	if err != nil {
		t.Fatalf("NormalizeResourceOpForValidation failed: %v", err)
	}
	if value.(map[string]any)["select_after"] != false {
		t.Fatalf("expected select_after default")
	}
	if _, err := NormalizeResourceOpForValidation(`not an object`); err != nil {
		t.Fatalf("expected non-object passthrough, got %v", err)
	}
}
