package httpapi

import (
	"net/http"
	"strings"

	"github.com/ai-scratch-web/backend/internal/config"

	"github.com/ai-scratch-web/backend/internal/imageservice"
	"github.com/ai-scratch-web/backend/internal/prompt"
	"github.com/ai-scratch-web/backend/internal/provider"
)

func (s *Server) handleHealth(writer http.ResponseWriter, _ *http.Request) {
	WriteJSON(writer, http.StatusOK, map[string]any{
		"status":              "ok",
		"provider_configured": s.Settings.SuiXiangAPIKey != "",
		"agnes_configured":    s.Settings.AgnesAPIKey != "",
		"deepseek_configured": s.Settings.DeepSeekAPIKey != "",
		"gpt_configured":      s.Settings.GPTAPIKey != "",
	})
}

func (s *Server) handleCapabilities(writer http.ResponseWriter, request *http.Request) {
	settings := s.Settings
	scope := strings.TrimSpace(request.Header.Get("X-AI-Run-Scope"))
	if len(scope) > 512 {
		scope = ""
	}
	durableRunsAvailable := settings.AIDurableRunsEnabled && settings.AIDurableRunsRolloutPercent >= 100
	if scope != "" {
		durableRunsAvailable = settings.DurableRunsEnabledForScope(scope)
	}
	WriteJSON(writer, http.StatusOK, map[string]any{
		"ai_blocks_protocol": []int{2, 3},
		"ai_runs_protocol":   "ai-scratch-run/1",
		"project_manifest":   []int{2, 3},
		"program_ir":         []int{1, 2},
		"features": map[string]any{
			"ai_commit":                   settings.AIBlocksCommitEnabled,
			"project_assets":              settings.AIProjectAssetsEnabled,
			"sound_assets":                settings.AISoundAssetsEnabled,
			"audio_generation":            settings.AIAudioGenerationEnabled,
			"asset_auto_match":            settings.AIAssetAutoMatchEnabled,
			"asset_auto_match_min_score":  settings.AIAssetAutoMatchMinScore,
			"asset_auto_match_min_margin": settings.AIAssetAutoMatchMinMargin,
			"destructive_sprite_ops":      settings.AIDestructiveResourceOpsEnabled,
			"destructive_sound_ops":       settings.AIDestructiveResourceOpsEnabled,
			"logical_resource_refs":       settings.AILogicalResourceRefsEnabled,
			"short_commit":                settings.AIShortCommitEnabled,
			"durable_runs":                durableRunsAvailable,
			"crash_checkpoint":            settings.AICrashCheckpointEnabled,
			"pen_extension":               settings.AIPenExtensionEnabled,
			"music_extension":             settings.AIMusicExtensionEnabled,
			"text_to_speech_extension":    settings.AITextToSpeechExtensionEnabled,
		},
	})
}

type imageRequest struct {
	Prompt      string  `json:"prompt"`
	Style       string  `json:"style"`
	CustomStyle *string `json:"custom_style"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Target      string  `json:"target"`
	SheetLayout string  `json:"sheet_layout"`
	Model       *string `json:"model,omitempty"`
}

var imageStyles = map[string]bool{
	"cartoon": true, "scratch": true, "paper-cut": true, "pixel": true, "watercolor": true, "custom": true,
}

func normalizeSpaces(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (s *Server) handleImageGenerate(writer http.ResponseWriter, request *http.Request) {
	var body imageRequest
	if !s.DecodeBody(writer, request, &body) {
		return
	}
	promptText := normalizeSpaces(body.Prompt)
	if len([]rune(promptText)) < 2 || len([]rune(promptText)) > 800 {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	if !imageStyles[body.Style] {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	customStyle := ""
	if body.CustomStyle != nil {
		customStyle = normalizeSpaces(*body.CustomStyle)
		if len([]rune(customStyle)) < 2 || len([]rune(customStyle)) > 240 {
			WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
			return
		}
	}
	if body.Style == "custom" && customStyle == "" {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	if body.Width < 1 || body.Width > 2000 || body.Height < 1 || body.Height > 2000 {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	if !imageservice.AllowedSizes[[2]int{body.Width, body.Height}] {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	if body.Target != "backdrop" && body.Target != "sprite" {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	if body.SheetLayout != "1x1" {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	model := imageservice.DefaultModel
	if body.Model != nil && strings.TrimSpace(*body.Model) != "" {
		model = strings.TrimSpace(*body.Model)
	}
	if !imageservice.AllowedModels[model] {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}

	service := s.deps().imageService
	response, err := service.Generate(request.Context(), imageservice.Request{
		Prompt:      promptText,
		Style:       body.Style,
		CustomStyle: customStyle,
		Width:       body.Width,
		Height:      body.Height,
		Target:      body.Target,
		SheetLayout: body.SheetLayout,
		Model:       model,
	})
	if err != nil {
		writeImageError(writer, err)
		return
	}
	WriteJSON(writer, http.StatusOK, response)
}

func writeImageError(writer http.ResponseWriter, err error) {
	switch typed := err.(type) {
	case *provider.ProviderError:
		_ = typed
		WriteError(writer, http.StatusBadGateway, "PROVIDER_ERROR", "生图服务暂时不可用")
	case *config.ConfigurationError:
		WriteError(writer, http.StatusServiceUnavailable, "CONFIGURATION_ERROR", "生图服务尚未配置")
	default:
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
	}
}

type textExpansionRequest struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func (s *Server) handleTextExpand(writer http.ResponseWriter, request *http.Request) {
	var body textExpansionRequest
	if !s.DecodeBody(writer, request, &body) {
		return
	}
	if body.Kind != "scene" && body.Kind != "style" {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || len([]rune(text)) > 800 {
		WriteError(writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "请求参数不正确")
		return
	}

	textProvider := provider.NewTextProvider(s.Settings, "agnes")
	if err := textProvider.EnsureConfigured(); err != nil {
		WriteError(writer, http.StatusServiceUnavailable, "AGNES_NOT_CONFIGURED", "未配置 Agnes 对话服务")
		return
	}

	messages := make([]provider.ChatMessage, 0, 2)
	for _, item := range prompt.BuildTextExpansionMessages(body.Kind, text) {
		messages = append(messages, provider.ChatMessage{Role: item["role"], Content: item["content"]})
	}

	stream, streamErr := textProvider.Stream(request.Context(), messages)
	sse, ok := NewSSEWriter(writer)
	if !ok {
		WriteError(writer, http.StatusInternalServerError, "STREAM_UNAVAILABLE", "服务端不支持流式响应")
		return
	}
	received := false
	for {
		select {
		case <-request.Context().Done():
			return
		case delta, open := <-stream:
			if !open {
				if !received {
					sse.Event("message_error", map[string]any{
						"code":    "AGNES_STREAM_ERROR",
						"message": "AI 扩写暂时不可用，请稍后重试",
					})
					return
				}
				sse.Event("message_complete", map[string]any{})
				return
			}
			if delta.Kind == provider.DeltaContent && delta.Text != "" {
				received = true
				sse.Event("content_delta", map[string]any{"text": delta.Text})
			}
		case err, open := <-streamErr:
			if !open {
				continue
			}
			_ = err
			sse.Event("message_error", map[string]any{
				"code":    "AGNES_STREAM_ERROR",
				"message": "AI 扩写暂时不可用，请稍后重试",
			})
			return
		}
	}
}
