package schema

import "testing"

// The Python request hierarchy exposes output_contract only on the project
// subtypes. Go flattens that hierarchy, so the shared UnifiedGenerationRequest
// carries a non-JSON OutputContract slot that the subtypes fill in. These tests
// lock both halves of that contract.
func TestProjectManifestRequestCarriesOutputContract(t *testing.T) {
	raw := []byte(`{"session_id":"s","request_id":"r","messages":[{"role":"user","content":"hi"}],` +
		`"target":{"name":"Stage","is_stage":true,"has_scripts":false,"mode":"append"},` +
		`"scope":"project","project_context":{"project_id":"p","version":1,"targets":[{"target_id":"stage","revision":1,"name":"Stage","is_stage":true,"has_scripts":false,"mode":"append"}]}}`)
	decoded, err := DecodeProjectGenerationRequest(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	manifest, ok := decoded.(*ProjectManifestGenerationRequest)
	if !ok {
		t.Fatalf("expected a manifest request, got %T", decoded)
	}
	if manifest.OutputContract != "project_manifest" {
		t.Fatalf("wire default = %q", manifest.OutputContract)
	}
	if got := manifest.AsUnifiedGenerationRequest().OutputContract; got != "project_manifest" {
		t.Fatalf("shared view must carry the discriminator, got %q", got)
	}
}

func TestPlainUnifiedRequestRejectsOutputContract(t *testing.T) {
	// Python's UnifiedGenerationRequest is a StrictModel: output_contract is an
	// unknown field there and must stay rejected even though the Go struct now
	// has a non-JSON slot for it.
	raw := []byte(`{"session_id":"s","request_id":"r","messages":[{"role":"user","content":"hi"}],` +
		`"target":{"name":"Stage","is_stage":true,"has_scripts":false,"mode":"append"},` +
		`"output_contract":"project_manifest"}`)
	var request UnifiedGenerationRequest
	if err := DecodeStrict(raw, &request); err == nil {
		t.Fatal("a plain unified request must not accept output_contract")
	}
}

func TestUnifiedRequestOutputContractDefaultsToEmpty(t *testing.T) {
	raw := []byte(`{"session_id":"s","request_id":"r","messages":[{"role":"user","content":"hi"}],` +
		`"target":{"name":"Stage","is_stage":true,"has_scripts":false,"mode":"append"}}`)
	var request UnifiedGenerationRequest
	if err := DecodeStrict(raw, &request); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if request.OutputContract != "" {
		t.Fatalf("a plain unified request must have an empty output contract, got %q", request.OutputContract)
	}
}
