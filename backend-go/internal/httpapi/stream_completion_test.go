package httpapi

import (
	"context"
	"errors"
	"github.com/ai-scratch-web/backend/internal/privateaccess"
	"github.com/ai-scratch-web/backend/internal/provider"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type slowFlushRecorder struct{ *httptest.ResponseRecorder }

func (w slowFlushRecorder) Flush() { time.Sleep(time.Millisecond); w.ResponseRecorder.Flush() }
func TestBufferedTerminalErrorCannotBecomeSuccess(t *testing.T) {
	for i := 0; i < 100; i++ {
		deltas := make(chan provider.StreamDelta, 2)
		errs := make(chan error, 1)
		deltas <- provider.StreamDelta{Kind: provider.DeltaContent, Text: "partial"}
		errs <- errors.New("failed")
		close(errs)
		close(deltas)
		var content strings.Builder
		err := consumeTextStream(context.Background(), deltas, errs, func(delta provider.StreamDelta) { content.WriteString(delta.Text); time.Sleep(time.Microsecond) })
		if err == nil || content.String() != "partial" {
			t.Fatal("lost terminal error or buffered content")
		}
	}
}
func TestBothHTTPStreamsReportProviderErrorsAfterPartialContent(t *testing.T) {
	for _, tc := range []struct{ path, body, delta string }{
		{"/api/text/expand", `{"kind":"scene","text":"a cat"}`, "content_delta"},
		{"/api/ai-blocks/chat", `{"session_id":"s","request_id":"r","thinking":{"enabled":false,"strength":"low"},"messages":[{"role":"user","content":"hi"}],"target":{"name":"Stage","is_stage":true,"has_scripts":false,"mode":"append"}}`, "assistant_delta"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, failed := range []bool{true, false} {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
					if failed {
						io.WriteString(w, "data: invalid-json\n\n")
					} else {
						io.WriteString(w, "data: [DONE]\n\n")
					}
				}))
				server := testServer(t)
				server.Settings.AgnesAPIKey = "fake-provider-key"
				server.Settings.AgnesAPIBase = upstream.URL + "/"
				for i := 0; i < 20; i++ {
					rec := httptest.NewRecorder()
					req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
					req.Header.Set("Content-Type", "application/json")
					server.Handler().ServeHTTP(slowFlushRecorder{rec}, req)
					text := rec.Body.String()
					if rec.Code != 200 || !strings.Contains(text, "event: "+tc.delta) {
						t.Fatalf("missing partial data: %d %s", rec.Code, text)
					}
					if strings.Count(text, "event: message_complete") != boolInt(!failed) || strings.Count(text, "event: message_error") != boolInt(failed) {
						t.Fatalf("wrong terminal event, failed=%t: %s", failed, text)
					}
				}
				upstream.Close()
			}
		})
	}
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func TestPrivateAccessRejectsCallsBeforeProvider(t *testing.T) {
	server := testServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unauthorized provider call") }))
	defer upstream.Close()
	server.Settings.AgnesAPIKey = "fake-provider-key"
	server.Settings.AgnesAPIBase = upstream.URL
	wrap, _ := privateaccess.New("owner", "test-private-password-20261028")
	for _, path := range []string{"/api/text/expand", "/api/image/generate", "/api/ai-blocks/chat", "/api/ai-runs"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		req.Header.Set("X-AI-Run-Scope", "random-is-not-an-account")
		wrap(server.Handler()).ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("unprotected provider route %s: %d", path, rec.Code)
		}
	}
}
func TestCrossOriginSimpleJSONRequestsAreRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/image/generate", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", "text/plain")
	testServer(t).Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("cross-origin write was accepted: %d", rec.Code)
	}
}
