// Package privateaccess protects the single-owner deployment boundary.
package privateaccess

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"os"
)

// FromEnv requires a deployment secret before opening any application database.
// The HTTP backend is loopback-only; public access goes through the TLS proxy.
func FromEnv(prefix, host string) (func(http.Handler) http.Handler, error) {
	user := os.Getenv(prefix + "_ACCESS_USER")
	if user == "" {
		user = "owner"
	}
	password := os.Getenv(prefix + "_ACCESS_PASSWORD")
	return NewOnHost(host, user, password)
}

func NewOnHost(host, user, password string) (func(http.Handler) http.Handler, error) {
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("private HTTP backend must bind a loopback IP")
	}
	if user == "" {
		user = "owner"
	}
	return New(user, password)
}

func New(user, password string) (func(http.Handler) http.Handler, error) {
	if user == "" || len(password) < 16 {
		return nil, fmt.Errorf("private access requires a user and an ACCESS_PASSWORD of at least 16 bytes")
	}
	expectedUser, expectedPassword := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(password))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Readiness is deliberately separate from all application/data endpoints.
			if r.URL.Path == "/healthz" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.Write([]byte(`{"status":"ok"}`))
				return
			}
			user, password, ok := r.BasicAuth()
			actualUser, actualPassword := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(password))
			valid := subtle.ConstantTimeCompare(actualUser[:], expectedUser[:]) & subtle.ConstantTimeCompare(actualPassword[:], expectedPassword[:])
			if !ok || valid != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Private tool", charset="UTF-8"`)
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, "Authentication required", http.StatusUnauthorized)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "Cross-site request rejected", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}
