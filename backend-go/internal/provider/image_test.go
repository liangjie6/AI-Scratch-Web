package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-scratch-web/backend/internal/config"
)

func TestAgnesRatioForSize(t *testing.T) {
	tests := []struct {
		w, h     int
		expected string
	}{
		{480, 360, "4:3"},
		{640, 360, "16:9"},
		{360, 360, "1:1"},
		{360, 480, "3:4"},
		{800, 600, "4:3"},
		{500, 500, "1:1"},
	}
	for _, tc := range tests {
		if got := AgnesRatioForSize(tc.w, tc.h); got != tc.expected {
			t.Errorf("AgnesRatioForSize(%d, %d) = %q, expected %q", tc.w, tc.h, got, tc.expected)
		}
	}
}

func TestAgnesImageProvider_Generate(t *testing.T) {
	dummyB64 := base64.StdEncoding.EncodeToString([]byte("fake-png-data"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-agnes-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content type: %s", r.Header.Get("Content-Type"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["model"] != "agnes-image-2.5-flash" {
			t.Errorf("expected model agnes-image-2.5-flash, got %v", payload["model"])
		}
		if payload["ratio"] != "4:3" {
			t.Errorf("expected ratio 4:3, got %v", payload["ratio"])
		}
		if payload["return_base64"] != true {
			t.Errorf("expected return_base64 true, got %v", payload["return_base64"])
		}

		resp := map[string]any{
			"task_id": "test-task-123",
			"data": []map[string]any{
				{"b64_json": dummyB64},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	settings := &config.Settings{
		AgnesAPIKey:                "test-agnes-key",
		AgnesAPIBase:               server.URL + "/",
		ImageRequestTimeoutSeconds: 10,
		ImageMaxDownloadBytes:      1024 * 1024,
	}

	p := NewAgnesImageProvider(settings)
	img, err := p.Generate(context.Background(), ImageParams{
		Model:  "agnes-image-2.5-flash",
		Prompt: "a cute kitten",
		Width:  480,
		Height: 360,
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if img.RequestID != "test-task-123" {
		t.Errorf("expected request id test-task-123, got %s", img.RequestID)
	}
	if string(img.Data) != "fake-png-data" {
		t.Errorf("expected fake-png-data, got %s", string(img.Data))
	}
}

func TestSuiXiangProvider_Generate(t *testing.T) {
	dummyB64 := base64.StdEncoding.EncodeToString([]byte("fake-sui-xiang-data"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-sui-xiang-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["model"] != "gpt-image-2.5-sunburst" {
			t.Errorf("expected model gpt-image-2.5-sunburst, got %v", payload["model"])
		}
		resp := map[string]any{
			"id": "sui-xiang-id",
			"data": []map[string]any{
				{"b64_json": dummyB64},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	settings := &config.Settings{
		SuiXiangAPIKey:             "test-sui-xiang-key",
		ProviderBaseURL:            server.URL,
		ProviderModel:              "gpt-image-2.5-sunburst",
		ImageRequestTimeoutSeconds: 10,
		ImageMaxDownloadBytes:      1024 * 1024,
	}

	p := NewSuiXiangProvider(settings)
	img, err := p.Generate(context.Background(), ImageParams{
		Model:  "gpt-image-2.5-sunburst",
		Prompt: "a puppy",
		Width:  480,
		Height: 360,
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if img.RequestID != "sui-xiang-id" {
		t.Errorf("expected request id sui-xiang-id, got %s", img.RequestID)
	}
	if string(img.Data) != "fake-sui-xiang-data" {
		t.Errorf("expected fake-sui-xiang-data, got %s", string(img.Data))
	}
}

func TestSuiXiangProviderBackgroundOptions(t *testing.T) {
	for _, background := range []string{"", "transparent"} {
		t.Run("background="+background, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if background == "transparent" {
					if payload["background"] != "transparent" || payload["output_format"] != "png" {
						t.Errorf("transparent sprites must request alpha-capable PNG output: %v", payload)
					}
				} else if payload["background"] != nil || payload["output_format"] != nil {
					t.Errorf("ordinary image requests should retain provider defaults: %v", payload)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": "cG5n"}}})
			}))
			defer server.Close()
			p := NewSuiXiangProvider(&config.Settings{
				SuiXiangAPIKey: "test-key", ProviderBaseURL: server.URL,
				ImageRequestTimeoutSeconds: 10, ImageMaxDownloadBytes: 1024,
			})
			if _, err := p.Generate(context.Background(), ImageParams{
				Model: "gpt-image-2.5-sunburst", Prompt: "a sprite", Background: background,
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
