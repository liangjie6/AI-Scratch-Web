package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ai-scratch-web/backend/internal/authoring"
	"github.com/ai-scratch-web/backend/internal/orchestration"
	"github.com/ai-scratch-web/backend/internal/prompt"
	"github.com/ai-scratch-web/backend/internal/provider"
	"github.com/ai-scratch-web/backend/internal/resourcecontract"
	"github.com/ai-scratch-web/backend/internal/schema"
)

func (s *Server) registerAIBlocks(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ai-blocks/chat", s.handleAIBlocksChat)
	mux.HandleFunc("POST /api/ai-blocks/generate-unified", s.handleAIBlocksGenerateUnified)
	mux.HandleFunc("POST /api/ai-blocks/generate-source-program", s.handleAIBlocksGenerateSourceProgram)
	mux.HandleFunc("POST /api/ai-blocks/generate", s.handleAIBlocksGenerate)
	mux.HandleFunc("POST /api/ai-blocks/validate", s.handleAIBlocksValidate)
}

func requestLogRef(requestID string) string {
	sum := sha256.Sum256([]byte(requestID))
	return hex.EncodeToString(sum[:])[:16]
}

// selectProvider mirrors _select_provider.
func (s *Server) selectProvider(tier string) *provider.TextProvider {
	base := s.deps().textProvider
	if tier == "normal" {
		return base
	}
	return base.ForTier(tier)
}

// requireProvider mirrors _require_provider and reports 503 on failure.
func (s *Server) requireProvider(writer http.ResponseWriter, tier string) (*provider.TextProvider, bool) {
	selected := s.selectProvider(tier)
	if err := selected.EnsureConfigured(); err != nil {
		s.log().Error("ai_provider_not_configured", "tier", tier)
		WriteError(writer, http.StatusServiceUnavailable, "AI_PROVIDER_NOT_CONFIGURED", "AI 服务暂未配置")
		return nil, false
	}
	return selected, true
}

func (s *Server) acquireSlot(ctx context.Context, semaphore chan struct{}) bool {
	select {
	case semaphore <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) releaseSlot(semaphore chan struct{}) { <-semaphore }

func (s *Server) providerTimeout() time.Duration {
	return time.Duration(s.Settings.AIBlocksProviderTimeoutSeconds * float64(time.Second))
}

func decodeErrorEnvelope(err error) (int, string, string) {
	switch err.(type) {
	case *schema.SchemaValidationError:
		return http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确"
	default:
		return http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确"
	}
}

func (s *Server) writeDecodeError(writer http.ResponseWriter, err error) {
	status, code, message := decodeErrorEnvelope(err)
	WriteError(writer, status, code, message)
}

type chatStreamRequest struct {
	ModelTier string               `json:"model_tier"`
	Target    schema.TargetContext `json:"target"`
	Messages  []schema.ChatMessage `json:"messages"`
}

func (s *Server) handleAIBlocksChat(writer http.ResponseWriter, request *http.Request) {
	body, ok := s.ReadBody(writer, request)
	if !ok {
		return
	}
	var decoded schema.ChatRequest
	if err := schema.DecodeStrict(body, &decoded); err != nil {
		s.writeDecodeError(writer, err)
		return
	}
	selected, ok := s.requireProvider(writer, string(decoded.ModelTier))
	if !ok {
		return
	}

	targetJSON, err := json.Marshal(decoded.Target)
	if err != nil {
		targetJSON = []byte("{}")
	}
	messages := make([]prompt.Message, 0, len(decoded.Messages))
	for _, message := range decoded.Messages {
		messages = append(messages, prompt.Message{Role: message.Role, Content: message.Content})
	}
	chatMessages := s.deps().promptBuilder.BuildChatMessages(targetJSON, messages)
	providerMessages := make([]provider.ChatMessage, 0, len(chatMessages))
	for _, message := range chatMessages {
		providerMessages = append(providerMessages, provider.ChatMessage{Role: message.Role, Content: message.Content})
	}

	sse, ok := NewSSEWriter(writer)
	if !ok {
		WriteError(writer, http.StatusInternalServerError, "STREAM_UNAVAILABLE", "服务端不支持流式响应")
		return
	}
	semaphore := s.deps().blocksSemaphore
	if !s.acquireSlot(request.Context(), semaphore) {
		return
	}
	defer s.releaseSlot(semaphore)

	ctx, cancel := context.WithTimeout(request.Context(), s.providerTimeout())
	defer cancel()

	contentChunks := 0
	deltas, errs := selected.StreamWithReasoning(ctx, providerMessages, decoded.Thinking.Enabled, string(decoded.Thinking.Strength), false, nil)
	err = consumeTextStream(ctx, deltas, errs, func(delta provider.StreamDelta) {
		switch delta.Kind {
		case provider.DeltaReasoning:
			if delta.Text != "" {
				sse.Event("reasoning_delta", map[string]any{"request_id": decoded.RequestID, "text": delta.Text})
			}
		case provider.DeltaContent:
			if delta.Text != "" {
				contentChunks++
				sse.Event("assistant_delta", map[string]any{"request_id": decoded.RequestID, "text": delta.Text})
			}
		}
	})
	if errors.Is(err, context.Canceled) {
		return
	}
	if err != nil || contentChunks == 0 {
		code, message := "AI_CHAT_ERROR", "AI 对话暂时不可用，请稍后重试"
		if errors.Is(err, context.DeadlineExceeded) {
			code, message = "AI_PROVIDER_TIMEOUT", "AI 对话超时，请稍后重试"
		}
		s.log().Error("ai_chat_failed", "request_ref", requestLogRef(decoded.RequestID), "content_chunks", contentChunks)
		sse.Event("message_error", map[string]any{"request_id": decoded.RequestID, "code": code, "message": message})
		return
	}
	s.log().Info("ai_chat_completed", "request_ref", requestLogRef(decoded.RequestID), "content_chunks", contentChunks)
	sse.Event("message_complete", map[string]any{"request_id": decoded.RequestID})
}

func (s *Server) handleAIBlocksGenerateUnified(writer http.ResponseWriter, request *http.Request) {
	body, ok := s.ReadBody(writer, request)
	if !ok {
		return
	}
	var decoded schema.UnifiedGenerationRequest
	if err := schema.DecodeStrict(body, &decoded); err != nil {
		s.writeDecodeError(writer, err)
		return
	}
	selected, ok := s.requireProvider(writer, string(decoded.ModelTier))
	if !ok {
		return
	}
	s.streamGeneration(writer, request, decoded.RequestID, func(ctx context.Context) (<-chan orchestration.GenerationEvent, <-chan error) {
		return s.deps().orchestrator.GenerateUnified(ctx, &decoded, selected)
	})
}

func (s *Server) handleAIBlocksGenerate(writer http.ResponseWriter, request *http.Request) {
	body, ok := s.ReadBody(writer, request)
	if !ok {
		return
	}
	decoded, err := schema.DecodeProjectGenerationRequest(body)
	if err != nil {
		s.writeDecodeError(writer, err)
		return
	}
	contract, ok := decoded.(*schema.ProjectProgramGenerationRequest)
	if !ok {
		manifest, isManifest := decoded.(*schema.ProjectManifestGenerationRequest)
		if !isManifest {
			WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
			return
		}
		selected, ok := s.requireProvider(writer, string(manifest.ModelTier))
		if !ok {
			return
		}
		unified := manifestToUnified(manifest)
		s.streamGeneration(writer, request, manifest.RequestID, func(ctx context.Context) (<-chan orchestration.GenerationEvent, <-chan error) {
			return s.deps().orchestrator.GenerateUnified(ctx, unified, selected)
		})
		return
	}
	selected, ok := s.requireProvider(writer, string(contract.ModelTier))
	if !ok {
		return
	}
	s.streamGeneration(writer, request, contract.RequestID, func(ctx context.Context) (<-chan orchestration.GenerationEvent, <-chan error) {
		return s.deps().orchestrator.GenerateProjectProgram(ctx, contract, selected)
	})
}

func (s *Server) streamGeneration(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	start func(ctx context.Context) (<-chan orchestration.GenerationEvent, <-chan error),
) {
	sse, ok := NewSSEWriter(writer)
	if !ok {
		WriteError(writer, http.StatusInternalServerError, "STREAM_UNAVAILABLE", "服务端不支持流式响应")
		return
	}
	semaphore := s.deps().blocksSemaphore
	if !s.acquireSlot(request.Context(), semaphore) {
		return
	}
	defer s.releaseSlot(semaphore)

	ctx, cancel := context.WithTimeout(request.Context(), s.providerTimeout())
	defer cancel()

	terminal := false
	events, errs := start(ctx)
	emitError := func(code, message string) {
		if terminal {
			return
		}
		terminal = true
		sse.Event("error", map[string]any{"request_id": requestID, "code": code, "message": message})
	}
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				emitError("AI_PROVIDER_TIMEOUT", "AI 生成超时，请稍后重试")
			}
			return
		case event, open := <-events:
			if !open {
				return
			}
			sse.Event(event.Type, event.Payload())
			if event.Type == "complete" || event.Type == "error" {
				terminal = true
				return
			}
		case err, open := <-errs:
			if !open {
				continue
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				emitError("AI_PROVIDER_TIMEOUT", "AI 生成超时，请稍后重试")
				return
			}
			var providerErr *provider.TextProviderError
			if errors.As(err, &providerErr) {
				s.log().Error("ai_unified_provider_failed", "request_ref", requestLogRef(requestID))
				emitError("AI_PROVIDER_ERROR", "AI 生成暂时不可用，请稍后重试")
				return
			}
			s.log().Error("ai_unified_failed", "request_ref", requestLogRef(requestID), "error_type", "GenerationError")
			emitError("AI_GENERATION_ERROR", "AI 输出未完成，请重新生成")
			return
		}
	}
}

// validationIssues mirrors _validation_issues.
func validationIssues(err error, scope string) []map[string]any {
	var resourceErr *schema.ResourceContractError
	if errors.As(err, &resourceErr) {
		return []map[string]any{resourceErr.Issue.AsDict()}
	}
	var programErr *schema.ProgramContractError
	if errors.As(err, &programErr) {
		issues := make([]map[string]any, 0, len(programErr.Issues))
		for _, issue := range programErr.Issues {
			issues = append(issues, map[string]any{
				"code":         "PROGRAM_CONTRACT_INVALID",
				"path":         schema.ToJSONPointer(issue.Path),
				"operation":    "program",
				"actual":       nil,
				"expected":     issue.Message,
				"repair_scope": "program",
				"repair_id":    nil,
				"message":      issue.Message,
			})
		}
		return issues
	}
	var validationErr *schema.SchemaValidationError
	if errors.As(err, &validationErr) {
		path := schema.ToJSONPointer(validationErr.Path)
		if path == "" {
			path = "/output"
		}
		return []map[string]any{{
			"code":         "SCHEMA_VALIDATION_ERROR",
			"path":         path,
			"operation":    scope,
			"actual":       nil,
			"expected":     validationErr.Message,
			"repair_scope": scope,
			"repair_id":    nil,
			"message":      validationErr.Message,
		}}
	}
	message := err.Error()
	return []map[string]any{{
		"code":         "OUTPUT_VALIDATION_ERROR",
		"path":         "/output",
		"operation":    "validation",
		"actual":       nil,
		"expected":     message,
		"repair_scope": "validation",
		"repair_id":    nil,
		"message":      message,
	}}
}

func (s *Server) handleAIBlocksValidate(writer http.ResponseWriter, request *http.Request) {
	body, ok := s.ReadBody(writer, request)
	if !ok {
		return
	}
	var decoded schema.ValidationRequest
	if err := schema.DecodeStrict(body, &decoded); err != nil {
		s.writeDecodeError(writer, err)
		return
	}
	if decoded.Kind == "program" {
		program := &schema.ProgramIR{}
		raw, err := json.Marshal(decoded.Payload)
		if err != nil {
			WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
			return
		}
		if err := schema.DecodeStrict(raw, program); err != nil {
			// The Python route catches ValidationError and reports it in the
			// response body with HTTP 200 instead of failing the request.
			WriteJSON(writer, http.StatusOK, map[string]any{"valid": false, "issues": validationIssues(err, "program")})
			return
		}
		if _, err := schema.ValidateProgramContracts(program, schema.ProgramContractOptions{TargetKind: decoded.TargetKind}); err != nil {
			WriteJSON(writer, http.StatusOK, map[string]any{"valid": false, "issues": validationIssues(err, "program")})
			return
		}
		if _, err := schema.ValidateProgramForMode(program, decoded.Mode, decoded.CurrentProgram); err != nil {
			WriteJSON(writer, http.StatusOK, map[string]any{"valid": false, "issues": validationIssues(err, "program")})
			return
		}
		WriteJSON(writer, http.StatusOK, map[string]any{"valid": true, "issues": []any{}})
		return
	}

	normalized, normalizations, err := schema.NormalizeProjectManifestPayload(decoded.Payload)
	if err != nil {
		WriteJSON(writer, http.StatusOK, map[string]any{"valid": false, "issues": validationIssues(err, "manifest")})
		return
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		WriteJSON(writer, http.StatusOK, map[string]any{"valid": false, "issues": validationIssues(err, "manifest")})
		return
	}
	manifest := &schema.ProjectManifest{}
	if err := schema.DecodeStrict(raw, manifest); err != nil {
		WriteJSON(writer, http.StatusOK, map[string]any{"valid": false, "issues": validationIssues(err, "manifest")})
		return
	}
	items := make([]map[string]any, 0, len(normalizations))
	for _, item := range normalizations {
		items = append(items, item.AsDict())
	}
	WriteJSON(writer, http.StatusOK, map[string]any{"valid": true, "issues": []any{}, "normalizations": items})
}

// manifestToUnified projects the flattened project_manifest request onto the
// shared unified request shape used by the orchestrator.
func manifestToUnified(manifest *schema.ProjectManifestGenerationRequest) *schema.UnifiedGenerationRequest {
	return manifest.AsUnifiedGenerationRequest()
}

var _ = resourcecontract.BoundedActual

func (s *Server) handleAIBlocksGenerateSourceProgram(writer http.ResponseWriter, request *http.Request) {
	if !s.Settings.AIBlocksSourcePipelineEnabled {
		WriteError(writer, http.StatusConflict, "SOURCE_PIPELINE_DISABLED", "新积木流水线当前未启用")
		return
	}
	body, ok := s.ReadBody(writer, request)
	if !ok {
		return
	}
	if err := authoring.CheckJSON(body, authoring.Limits{MaxBytes: s.Settings.AIBlocksMaxRequestBytes}); err != nil {
		s.writeDecodeError(writer, err)
		return
	}
	var decoded orchestration.SourceProgramRequest
	if err := schema.DecodeStrict(body, &decoded); err != nil {
		s.writeDecodeError(writer, err)
		return
	}
	selected, ok := s.requireProvider(writer, string(decoded.Request.ModelTier))
	if !ok {
		return
	}
	s.streamGeneration(writer, request, decoded.Request.RequestID, func(ctx context.Context) (<-chan orchestration.GenerationEvent, <-chan error) {
		return s.deps().orchestrator.GenerateSourceProgram(ctx, &decoded, selected)
	})
}
