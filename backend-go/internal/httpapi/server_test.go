package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ai-scratch-web/backend/internal/config"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	directory := t.TempDir()
	settings := &config.Settings{
		ImageRequestTimeoutSeconds:     90,
		ImageMaxDownloadBytes:          12_582_912,
		ImageMaxRequestBytes:           16_384,
		ImageMaxConcurrency:            2,
		AIBlocksMaxRequestBytes:        262_144,
		AIBlocksMaxConcurrency:         2,
		AIBlocksProviderTimeoutSeconds: 480,
		AIBlocksMaxRepairRounds:        3,
		AIAssetAutoMatchMinScore:       8,
		AIAssetAutoMatchMinMargin:      5,
		AIRunsEventRetentionHours:      24,
		AIRunsRetentionDays:            7,
		AIDurableRunsRolloutPercent:    100,
		AIBlocksCommitEnabled:          true,
		AIProjectAssetsEnabled:         true,
		AISoundAssetsEnabled:           true,
		AILogicalResourceRefsEnabled:   true,
		AIShortCommitEnabled:           true,
		AICrashCheckpointEnabled:       true,
		AIRunsDatabasePath:             directory + "/ai-runs.sqlite3",
		ProviderBaseURL:                "https://example.invalid/v1",
		ProviderModel:                  "gpt-image-2.5-sunburst",
		AgnesAPIBase:                   "https://example.invalid/v1/",
		AgnesChatModel:                 "agnes",
		DeepSeekAPIBase:                "https://example.invalid",
		DeepSeekChatModel:              "deepseek",
		GPTAPIBase:                     "https://example.invalid/v1",
		GPTChatModel:                   "gpt",
	}
	return New(settings, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func do(t *testing.T, server *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func TestHealthAndCapabilities(t *testing.T) {
	server := testServer(t)

	response := do(t, server, http.MethodGet, "/health", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("/health = %d", response.Code)
	}
	var health map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health["status"] != "ok" {
		t.Fatalf("health = %v", health)
	}
	if health["provider_configured"] != false {
		t.Fatalf("provider_configured must be false without a key: %v", health)
	}

	response = do(t, server, http.MethodGet, "/api/capabilities", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("/api/capabilities = %d", response.Code)
	}
	var capabilities map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &capabilities); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	features, _ := capabilities["features"].(map[string]any)
	if features["durable_runs"] != false {
		t.Fatalf("durable_runs must be false by default: %v", features)
	}
	if features["ai_commit"] != true {
		t.Fatalf("ai_commit must default to true: %v", features)
	}
}

func TestUnknownRouteReturnsFastAPIShape(t *testing.T) {
	server := testServer(t)
	response := do(t, server, http.MethodGet, "/nope", "", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown route = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "Not Found") {
		t.Fatalf("unknown route body = %s", response.Body.String())
	}
}

func TestCORSPreflight(t *testing.T) {
	server := testServer(t)
	response := do(t, server, http.MethodOptions, "/api/ai-blocks/generate-unified", "", map[string]string{
		"Origin":                        "http://127.0.0.1:5173",
		"Access-Control-Request-Method": "POST",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("preflight = %d", response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:5173" {
		t.Fatalf("missing allow-origin header: %v", response.Header())
	}
	if !strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "X-AI-Run-Scope") {
		t.Fatalf("missing allow-headers: %v", response.Header())
	}
}

func TestImageRequestValidation(t *testing.T) {
	server := testServer(t)
	base := `{"prompt":"a cat","style":"cartoon","width":480,"height":360,"target":"sprite","sheet_layout":"1x1"}`

	response := do(t, server, http.MethodPost, "/api/image/generate", base, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured provider must yield 503, got %d (%s)", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "CONFIGURATION_ERROR") {
		t.Fatalf("body = %s", response.Body.String())
	}

	response = do(t, server, http.MethodPost, "/api/image/generate", `{"prompt":"a cat","style":"cartoon","width":480,"height":360,"target":"sprite","sheet_layout":"1x1","model":"agnes-image-2.5-flash"}`, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured agnes must yield 503, got %d", response.Code)
	}

	response = do(t, server, http.MethodPost, "/api/image/generate", `{"prompt":"a cat","style":"cartoon","width":480,"height":360,"target":"sprite","sheet_layout":"1x1","model":"gpt-image-2.5-sunburst"}`, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured sui-xiang must yield 503, got %d", response.Code)
	}

	invalid := []string{
		`{"prompt":"a cat","style":"cartoon","width":481,"height":360,"target":"sprite","sheet_layout":"1x1"}`,
		`{"prompt":"a cat","style":"nope","width":480,"height":360,"target":"sprite","sheet_layout":"1x1"}`,
		`{"prompt":"a cat","style":"custom","width":480,"height":360,"target":"sprite","sheet_layout":"1x1"}`,
		`{"prompt":"a cat","style":"cartoon","width":480,"height":360,"target":"sprite","sheet_layout":"2x2"}`,
		`{"prompt":"a cat","style":"cartoon","width":480,"height":360,"target":"sprite","sheet_layout":"1x1","extra":1}`,
		`{"prompt":"a cat","style":"cartoon","width":480,"height":360,"target":"sprite","sheet_layout":"1x1","model":"unknown-model"}`,
	}
	for _, payload := range invalid {
		response := do(t, server, http.MethodPost, "/api/image/generate", payload, nil)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("payload %s must be rejected with 422, got %d", payload, response.Code)
		}
	}
}

func TestImageRequestBodyLimit(t *testing.T) {
	server := testServer(t)
	payload := `{"prompt":"` + strings.Repeat("x", 20_000) + `","style":"cartoon","width":480,"height":360,"target":"sprite","sheet_layout":"1x1"}`
	response := do(t, server, http.MethodPost, "/api/image/generate", payload, nil)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d (%s)", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "REQUEST_TOO_LARGE") {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestTextExpansionRequiresAgnesKey(t *testing.T) {
	server := testServer(t)
	response := do(t, server, http.MethodPost, "/api/text/expand", `{"kind":"scene","text":"小猫"}`, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured Agnes must yield 503, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "AGNES_NOT_CONFIGURED") {
		t.Fatalf("body = %s", response.Body.String())
	}
	response = do(t, server, http.MethodPost, "/api/text/expand", `{"kind":"nope","text":"小猫"}`, nil)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid kind = %d", response.Code)
	}
}

func TestAIBlocksValidateProgramAndManifest(t *testing.T) {
	server := testServer(t)

	program := `{"kind":"program","payload":{"schema_version":1,"summary":"smoke","scripts":[{"id":"script_smoke","purpose":"smoke","x":0,"y":0,"blocks":[{"opcode":"event_whenflagclicked"}]}]},"target_kind":"sprite","mode":"append"}`
	response := do(t, server, http.MethodPost, "/api/ai-blocks/validate", program, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("validate program = %d (%s)", response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode validate: %v", err)
	}
	if result["valid"] != true {
		t.Fatalf("minimal ProgramIR must validate: %s", response.Body.String())
	}

	invalid := `{"kind":"program","payload":{"schema_version":9,"summary":"smoke","scripts":[{"id":"script_smoke","purpose":"smoke","x":0,"y":0,"blocks":[{"opcode":"event_whenflagclicked"}]}]},"target_kind":"sprite","mode":"append"}`
	response = do(t, server, http.MethodPost, "/api/ai-blocks/validate", invalid, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("validate invalid program = %d", response.Code)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode validate: %v", err)
	}
	if result["valid"] != false {
		t.Fatalf("schema_version 9 must be rejected: %s", response.Body.String())
	}
	issues, _ := result["issues"].([]any)
	if len(issues) == 0 {
		t.Fatalf("expected at least one issue: %s", response.Body.String())
	}

	manifest := `{"kind":"manifest","payload":{"schema_version":2,"scope":"project"}}`
	response = do(t, server, http.MethodPost, "/api/ai-blocks/validate", manifest, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("validate manifest = %d", response.Code)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode validate: %v", err)
	}
	if result["valid"] != false {
		t.Fatalf("a v2 manifest must be rejected: %s", response.Body.String())
	}
}

func TestAIBlocksRejectsUnknownFields(t *testing.T) {
	server := testServer(t)
	payload := `{"kind":"program","payload":{"schema_version":1,"summary":"s","scripts":[]},"unexpected":true}`
	response := do(t, server, http.MethodPost, "/api/ai-blocks/validate", payload, nil)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown field must yield 422, got %d (%s)", response.Code, response.Body.String())
	}
}

func TestDurableRunsDisabledByDefault(t *testing.T) {
	server := testServer(t)
	response := do(t, server, http.MethodPost, "/api/ai-runs", `{}`, map[string]string{"X-AI-Run-Scope": "scope"})
	if response.Code != http.StatusNotFound {
		t.Fatalf("durable runs must be gated off, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "DURABLE_RUNS_DISABLED") {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestStreamEndpointsRequireConfiguredProvider(t *testing.T) {
	server := testServer(t)
	payload := `{"session_id":"s","request_id":"r","messages":[{"role":"user","content":"hi"}],"target":{"name":"Stage","is_stage":true,"has_scripts":false,"mode":"append"}}`
	for _, path := range []string{"/api/ai-blocks/chat", "/api/ai-blocks/generate-unified"} {
		response := do(t, server, http.MethodPost, path, payload, nil)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s without a key must yield 503, got %d (%s)", path, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), "AI_PROVIDER_NOT_CONFIGURED") {
			t.Fatalf("%s body = %s", path, response.Body.String())
		}
	}
}
