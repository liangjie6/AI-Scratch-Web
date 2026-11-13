package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSourceCapabilitiesFollowFeatureFlag(t *testing.T) {
	s := testServer(t)
	for _, enabled := range []bool{false, true} {
		s.Settings.AIBlocksSourcePipelineEnabled = enabled
		r := do(t, s, http.MethodGet, "/api/capabilities", "", nil)
		var body map[string]any
		if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		features := body["features"].(map[string]any)
		if features["source_pipeline"] != enabled || body["source_pipeline_version"] != "scratch-source/1" {
			t.Fatalf("capabilities: %v", body)
		}
	}
}
func TestSourceEndpointDisabledBeforeProviderCall(t *testing.T) {
	s := testServer(t)
	r := do(t, s, http.MethodPost, "/api/ai-blocks/generate-source-program", `{}`, nil)
	if r.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
}
func TestSourceEndpointRejectsMalformedAndDuplicateContracts(t *testing.T) {
	s := testServer(t)
	s.Settings.AIBlocksSourcePipelineEnabled = true
	for _, body := range []string{`{}`, `{"target_index":0,"target_index":1}`, `{"request":{"pipeline_version":"scratch-source/2"},"source_contract":{},"target_index":0,"target":{}}`} {
		r := do(t, s, http.MethodPost, "/api/ai-blocks/generate-source-program", body, nil)
		if r.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
		}
	}
}
