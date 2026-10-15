// Package httpapi implements the HTTP surface of the AI Scratch Web backend.
// It mirrors backend/app/main.py and backend/app/routes/*.py.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ai-scratch-web/backend/internal/config"
)

// Server wires deployment settings and services into an http.Handler.
type Server struct {
	Settings *config.Settings
	Logger   *slog.Logger
	// ExtraCORSOrigins extends the default development origins.
	ExtraCORSOrigins []string

	dependencies deps
	runs         runDeps
}

// New builds a server.
func New(settings *config.Settings, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{Settings: settings, Logger: logger}
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Detail apiError `json:"detail"`
}

// WriteError emits the FastAPI-compatible {"detail": {"code","message"}} shape.
func WriteError(writer http.ResponseWriter, status int, code, message string) {
	WriteJSON(writer, status, errorEnvelope{Detail: apiError{Code: code, Message: message}})
}

// WriteJSON emits a JSON response body.
func WriteJSON(writer http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte("{\"detail\":{\"code\":\"INTERNAL_ERROR\",\"message\":\"服务内部错误\"}}"))
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

// maxBodyBytes returns the configured request body limit for a path, or 0 when
// no limit applies.
func (s *Server) maxBodyBytes(path string) int {
	switch {
	case strings.HasPrefix(path, "/api/image/"):
		return s.Settings.ImageMaxRequestBytes
	case strings.HasPrefix(path, "/api/ai-blocks/"), strings.HasPrefix(path, "/api/ai-runs"):
		return s.Settings.AIBlocksMaxRequestBytes
	default:
		return 0
	}
}

func requestTooLarge() error { return errors.New("REQUEST_TOO_LARGE") }

// ReadBody reads a bounded request body honouring the deployment limits.
func (s *Server) ReadBody(writer http.ResponseWriter, request *http.Request) ([]byte, bool) {
	limit := s.maxBodyBytes(request.URL.Path)
	if limit <= 0 {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			WriteError(writer, http.StatusBadRequest, "INVALID_REQUEST", "请求内容无法读取")
			return nil, false
		}
		return body, true
	}
	if raw := request.Header.Get("Content-Length"); raw != "" {
		var declared int64
		if _, err := fmt.Sscanf(raw, "%d", &declared); err != nil || declared > int64(limit) {
			WriteError(writer, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "请求内容过大")
			return nil, false
		}
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, int64(limit)+1))
	if err != nil {
		WriteError(writer, http.StatusBadRequest, "INVALID_REQUEST", "请求内容无法读取")
		return nil, false
	}
	if len(body) > limit {
		WriteError(writer, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "请求内容过大")
		return nil, false
	}
	return body, true
}

// DecodeBody reads and strictly decodes a JSON request body.
func (s *Server) DecodeBody(writer http.ResponseWriter, request *http.Request, target any) bool {
	body, ok := s.ReadBody(writer, request)
	if !ok {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return false
	}
	return true
}

// Handler builds the complete HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/capabilities", s.handleCapabilities)
	mux.HandleFunc("POST /api/image/generate", s.handleImageGenerate)
	mux.HandleFunc("POST /api/text/expand", s.handleTextExpand)
	s.registerAIBlocks(mux)
	s.registerAIRuns(mux)
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		WriteJSON(writer, http.StatusNotFound, map[string]any{"detail": "Not Found"})
	})
	return s.withCORS(s.withLogging(mux))
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) allowedOrigins() []string {
	origins := []string{"http://127.0.0.1:5173", "http://localhost:5173"}
	origins = append(origins, s.ExtraCORSOrigins...)
	return origins
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	allowedMethods := "POST, GET"
	allowedHeaders := "Content-Type, X-AI-Run-Scope, Last-Event-ID"
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		originAllowed := false
		if origin != "" {
			for _, candidate := range s.allowedOrigins() {
				if candidate == origin {
					originAllowed = true
					break
				}
			}
			if !originAllowed && sameOrigin(origin, request.Host) {
				originAllowed = true
			}
		}
		if originAllowed {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Vary", "Origin")
		}
		if request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != "" {
			if originAllowed {
				writer.Header().Set("Access-Control-Allow-Methods", allowedMethods)
				writer.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
			}
			writer.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func sameOrigin(origin, host string) bool {
	trimmed := origin
	if index := strings.Index(trimmed, "://"); index >= 0 {
		trimmed = trimmed[index+3:]
	}
	if index := strings.IndexByte(trimmed, '/'); index >= 0 {
		trimmed = trimmed[:index]
	}
	return strings.EqualFold(trimmed, host)
}

// SSEWriter streams server-sent events.
type SSEWriter struct {
	writer  http.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter prepares an SSE response and returns a writer for frames.
func NewSSEWriter(writer http.ResponseWriter) (*SSEWriter, bool) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		return nil, false
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &SSEWriter{writer: writer, flusher: flusher}, true
}

// Event writes one SSE frame and flushes it.
func (s *SSEWriter) Event(name string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		body = []byte("{}")
	}
	fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", name, body)
	s.flusher.Flush()
}

// Comment writes an SSE comment frame and flushes it.
func (s *SSEWriter) Comment(text string) {
	fmt.Fprintf(s.writer, ": %s\n\n", text)
	s.flusher.Flush()
}

// Raw writes a pre-formatted SSE frame and flushes it.
func (s *SSEWriter) Raw(frame string) {
	_, _ = io.WriteString(s.writer, frame)
	s.flusher.Flush()
}
