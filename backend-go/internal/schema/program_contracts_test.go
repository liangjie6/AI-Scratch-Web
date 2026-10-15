package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestProgramContractsProcedureRules(t *testing.T) {
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 1, "summary": "proc", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"procedures": [{"id": "procedure_jump", "label": "jump",
			"parameters": [{"id": "arg_height", "name": "height", "kind": "string_number"}], "warp": false,
			"body": [
				{"opcode": "motion_changeyby", "inputs": {"DY": {"kind": "number", "value": 10}}},
				{"opcode": "argument_reporter_string_number", "argument_id": "arg_height"},
				{"opcode": "procedures_call", "procedure_id": "procedure_jump"}
			]}],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [{"opcode": "event_whenflagclicked"}]}]
	}`)
	_, err := ValidateProgramContracts(program)
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	rendered := make([]string, 0, len(contractError.Issues))
	for _, issue := range contractError.Issues {
		rendered = append(rendered, issue.Path+" -> "+issue.Message)
	}
	joined := strings.Join(rendered, " | ")
	for _, want := range []string{
		"$.procedures[0].body[1] -> procedure argument reporter must be nested in an input",
		"$.procedures[0].body[2].opcode -> procedure body cannot contain event hats or procedure calls",
		"$.procedures[0].body[2].inputs.arg_height -> required procedure argument is missing",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing issue %q in %s", want, joined)
		}
	}
	if len(contractError.Issues) != 3 {
		t.Fatalf("expected 3 issues, got %d: %s", len(contractError.Issues), joined)
	}
}

func TestProgramContractsBooleanInputRule(t *testing.T) {
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 1, "summary": "cond", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked"},
			{"opcode": "control_if", "inputs": {"CONDITION": {"kind": "text", "value": "x"}}}
		]}]
	}`)
	_, err := ValidateProgramContracts(program)
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	if contractError.Issues[0].Path != "$.scripts[0].blocks[1].inputs.CONDITION" ||
		contractError.Issues[0].Message != "boolean input requires a nested boolean block" {
		t.Fatalf("unexpected issue: %#v", contractError.Issues[0])
	}
}

func TestProgramContractsTopLevelProcedureCallIsUnknownOpcode(t *testing.T) {
	// Faithful to the Python reference: BLOCK_CONTRACTS does not contain
	// procedures_call, so a script-level call is reported as an unknown opcode.
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 1, "summary": "call", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"procedures": [{"id": "procedure_jump", "label": "jump", "parameters": [], "warp": false,
			"body": [{"opcode": "motion_changeyby", "inputs": {"DY": {"kind": "number", "value": 10}}}]}],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked"},
			{"opcode": "procedures_call", "procedure_id": "procedure_jump"}
		]}]
	}`)
	_, err := ValidateProgramContracts(program)
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	if contractError.Issues[0].Path != "$.scripts[0].blocks[1].opcode" || contractError.Issues[0].Message != "unknown opcode" {
		t.Fatalf("unexpected issue: %#v", contractError.Issues[0])
	}
}

func TestProgramContractsResourceRefsRequireCapability(t *testing.T) {
	program := mustDecode[ProgramIR](t, `{
		"schema_version": 2, "summary": "refs", "remove_script_ids": [], "variables": [], "lists": [], "broadcasts": [],
		"scripts": [{"id": "script_1", "purpose": "p", "x": 0, "y": 0, "blocks": [
			{"opcode": "event_whenflagclicked"},
			{"opcode": "looks_switchcostumeto", "inputs": {"COSTUME": {"kind": "resource_ref", "resource_type": "costume", "op_id": "op_1"}}}
		]}]
	}`)
	previous := GetDeploymentSettings()
	t.Cleanup(func() { SetDeploymentSettings(previous) })
	settings := previous
	settings.AILogicalResourceRefsEnabled = false
	SetDeploymentSettings(settings)
	_, err := ValidateProgramContracts(program)
	var contractError *ProgramContractError
	if !errors.As(err, &contractError) {
		t.Fatalf("expected ProgramContractError, got %v", err)
	}
	if contractError.Issues[0].Path != "$.scripts[0].blocks[1].inputs.COSTUME.kind" ||
		contractError.Issues[0].Message != "resource_ref is only valid for project programs with a Manifest binding table" {
		t.Fatalf("unexpected issue: %#v", contractError.Issues[0])
	}
	SetDeploymentSettings(previous)
	if _, err := ValidateProgramContracts(program); err != nil {
		t.Fatalf("expected resource refs to pass once enabled, got %v", err)
	}
}
