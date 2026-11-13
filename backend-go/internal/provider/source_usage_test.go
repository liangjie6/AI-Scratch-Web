package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ai-scratch-web/backend/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUsageIncludesCacheAndReasoningWithoutDoubleCounting(t *testing.T) {
	u := NormalizeUsage(map[string]any{"prompt_tokens": 20, "completion_tokens": 10, "prompt_cache_hit_tokens": 12, "completion_tokens_details": map[string]any{"reasoning_tokens": 6}})
	if u["cached_input_tokens"] != 12 || u["reasoning_tokens"] != 6 || u["total_tokens"] != 30 {
		t.Fatalf("unexpected usage: %v", u)
	}
}

func TestIncompleteOutputIsNeverAcceptedAsACompletedObject(t *testing.T) {
	for _, reason := range []string{"length", "content_filter"} {
		if _, _, err := parseOpenAILines(`data: {"choices":[{"delta":{},"finish_reason":"` + reason + `"}]}`); err == nil {
			t.Fatalf("finish_reason=%s was accepted", reason)
		}
	}
	if _, _, err := parseAnthropicLines(`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`); err == nil {
		t.Fatal("Anthropic max_tokens was accepted")
	}
}

func TestSourceAgnesOptionsAreIsolatedFromLegacyRequests(t *testing.T) {
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, payload)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"{}"}}]}`)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer server.Close()
	p := NewTextProvider(&config.Settings{AgnesAPIBase: server.URL, AgnesAPIKey: "test-only", AgnesChatModel: "agnes-2.5-flash", AIBlocksProviderTimeoutSeconds: 30}, "agnes")
	p.Client = server.Client()
	for _, selected := range []*TextProvider{p.ForSourceAuthoring(), p} {
		stream, errs := selected.StreamWithReasoning(context.Background(), []ChatMessage{{Role: "user", Content: "Return JSON"}}, false, "low", false, nil)
		for range stream {
		}
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if payloads[0]["response_format"].(map[string]any)["type"] != "json_object" || payloads[0]["temperature"] != 0.2 {
		t.Fatal(payloads[0])
	}
	if _, present := payloads[1]["response_format"]; present {
		t.Fatal("legacy payload changed")
	}
	if _, present := payloads[1]["temperature"]; present {
		t.Fatal("shared provider mutated")
	}
}

func TestProviderRetryAfterSupportsSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	if retryAfterSeconds("21", now) != 21 {
		t.Fatal("seconds")
	}
	if got := retryAfterSeconds(now.Add(20*time.Second).Format(http.TimeFormat), now); got < 20 || got > 21 {
		t.Fatal(got)
	}
	if retryAfterSeconds("invalid", now) != 0 {
		t.Fatal("invalid Retry-After accepted")
	}
}

func TestTruncationKeepsFinalReportedUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13},"choices":[{"delta":{},"finish_reason":"length"}]}`)
	}))
	defer server.Close()
	p := NewTextProvider(&config.Settings{AgnesAPIBase: server.URL, AgnesAPIKey: "test-only", AgnesChatModel: "agnes", AIBlocksProviderTimeoutSeconds: 30}, "agnes")
	p.Client = server.Client()
	stream, errs := p.StreamWithReasoning(context.Background(), nil, false, "low", false, nil)
	total := 0
	for d := range stream {
		total += d.Usage["total_tokens"]
	}
	if total != 13 {
		t.Fatalf("truncated usage lost: %d", total)
	}
	if err := <-errs; err == nil {
		t.Fatal("truncated response was accepted")
	}
}
