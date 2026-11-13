package privateaccess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateBoundary(t *testing.T) {
	wrap, err := New("owner", "test-private-password-20261028")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
	for _, path := range []string{"/", "/api/chat/sessions", "/api/image/generate", "/api/v1/backups", "/uploads/file.html", "/assets/app.js"} {
		for _, password := range []string{"", "wrong", "test-private-password-20261028"} {
			req := httptest.NewRequest("GET", path, nil)
			if password != "" {
				req.SetBasicAuth("owner", password)
			}
			rec := httptest.NewRecorder()
			before := calls
			handler.ServeHTTP(rec, req)
			expected := 401
			if password == "test-private-password-20261028" {
				expected = 204
			}
			if rec.Code != expected {
				t.Fatalf("%s: status=%d", path, rec.Code)
			}
			if expected == 401 && (calls != before || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic ")) {
				t.Fatal("unauthorized request reached application")
			}
		}
	}
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	before := calls
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 || before != calls {
		t.Fatal("readiness exposed an application route")
	}
}
func TestMissingSecretAndNonLoopbackFailClosed(t *testing.T) {
	t.Setenv("TEST_ACCESS_PASSWORD", "")
	if _, err := FromEnv("TEST", "127.0.0.1"); err == nil {
		t.Fatal("missing secret accepted")
	}
	t.Setenv("TEST_ACCESS_PASSWORD", "test-private-password-20261028")
	for _, host := range []string{"0.0.0.0", "::", "192.0.2.1", "localhost"} {
		if _, err := FromEnv("TEST", host); err == nil {
			t.Fatalf("unsafe bind %s", host)
		}
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		if _, err := FromEnv("TEST", host); err != nil {
			t.Fatal(err)
		}
	}
}
