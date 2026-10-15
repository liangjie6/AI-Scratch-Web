// Package provider implements the upstream text and image providers used by the
// AI Scratch Web backend. It mirrors backend/app/providers/*.py.
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ai-scratch-web/backend/internal/config"
)

// StopSequences mirror _STOP_SEQUENCES in agnes_text_provider.py.
var StopSequences = []string{"\n---", "\n现在是 **Agnes**", "\n现在是 Agnes"}

// DeltaKind identifies the kind of a streamed provider delta.
type DeltaKind string

// Streamed delta kinds.
const (
	DeltaReasoning DeltaKind = "reasoning"
	DeltaContent   DeltaKind = "content"
	DeltaUsage     DeltaKind = "usage"
)

// StreamDelta is one normalized provider stream delta.
type StreamDelta struct {
	Kind  DeltaKind
	Text  string
	Usage map[string]int
}

// TextProviderError reports a provider transport or protocol failure.
type TextProviderError struct{ Message string }

func (e *TextProviderError) Error() string { return e.Message }

func newTextProviderError(format string, args ...any) *TextProviderError {
	return &TextProviderError{Message: fmt.Sprintf(format, args...)}
}

// TextProvider streams completions from Agnes, DeepSeek or the ChatGPT Responses API.
type TextProvider struct {
	Settings *config.Settings
	Provider string
	Client   *http.Client
}

// NewTextProvider builds a provider for the given backend id (agnes, deepseek or gpt).
func NewTextProvider(settings *config.Settings, provider string) *TextProvider {
	return &TextProvider{Settings: settings, Provider: provider}
}

// ForTier returns a provider selected by the request model tier.
func (p *TextProvider) ForTier(tier string) *TextProvider {
	return &TextProvider{Settings: p.Settings, Provider: ProviderForTier(tier), Client: p.Client}
}

func (p *TextProvider) apiKey() (string, error) {
	switch p.Provider {
	case "deepseek":
		return p.Settings.RequireDeepSeekAPIKey()
	case "gpt":
		return p.Settings.RequireGPTAPIKey()
	default:
		return p.Settings.RequireAgnesAPIKey()
	}
}

func (p *TextProvider) baseURL() string {
	switch p.Provider {
	case "deepseek":
		return p.Settings.DeepSeekAPIBase
	case "gpt":
		return p.Settings.GPTAPIBase
	default:
		return p.Settings.AgnesAPIBase
	}
}

func (p *TextProvider) model() string {
	switch p.Provider {
	case "deepseek":
		return p.Settings.DeepSeekChatModel
	case "gpt":
		return p.Settings.GPTChatModel
	default:
		return p.Settings.AgnesChatModel
	}
}

// EnsureConfigured verifies the provider API key is present.
func (p *TextProvider) EnsureConfigured() error {
	_, err := p.apiKey()
	return err
}

// SupportsStructuredOutput mirrors the Python transport capability probe.
func (p *TextProvider) SupportsStructuredOutput(segment string) bool {
	if p.Provider != "deepseek" && p.Provider != "gpt" {
		return false
	}
	switch segment {
	case "requirements", "task_spec", "blueprint", "plan", "manifest", "resource_op", "program":
		return true
	}
	return false
}

// SupportsJSONSchema mirrors the Python strict JSON Schema capability probe.
func (p *TextProvider) SupportsJSONSchema(segment string) bool {
	if p.Provider != "gpt" {
		return false
	}
	switch segment {
	case "requirements", "task_spec", "blueprint", "plan":
		return true
	}
	return false
}

// Endpoint returns the transport endpoint used for a request shape.
func (p *TextProvider) Endpoint(thinkingEnabled bool) string {
	if thinkingEnabled && p.Provider == "agnes" {
		return "messages"
	}
	if p.Provider == "gpt" {
		return "responses"
	}
	return "chat/completions"
}

// LogMetadata describes a request without persisting prompts or project content.
type LogMetadata struct {
	Provider     string
	Model        string
	BaseURL      string
	MessageCount int
	ContentChars int
}

// DescribeRequest returns bounded request metadata for logging.
func (p *TextProvider) DescribeRequest(messages []ChatMessage) LogMetadata {
	total := 0
	for _, message := range messages {
		total += len(message.Content)
	}
	return LogMetadata{
		Provider:     p.Provider,
		Model:        p.model(),
		BaseURL:      p.baseURL(),
		MessageCount: len(messages),
		ContentChars: total,
	}
}

// ChatMessage is one provider conversation message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Stream yields only assistant content deltas.
func (p *TextProvider) Stream(ctx context.Context, messages []ChatMessage) (<-chan StreamDelta, <-chan error) {
	if p.Provider == "gpt" {
		return p.StreamWithReasoning(ctx, messages, false, "low", false, nil)
	}
	return p.streamOpenAI(ctx, messages, nil, "low", false, nil)
}

// StreamWithReasoning yields reasoning, content and usage deltas.
func (p *TextProvider) StreamWithReasoning(
	ctx context.Context,
	messages []ChatMessage,
	thinkingEnabled bool,
	thinkingStrength string,
	jsonOutput bool,
	jsonSchema map[string]any,
) (<-chan StreamDelta, <-chan error) {
	if thinkingEnabled && p.Provider == "agnes" {
		return p.streamAnthropic(ctx, messages, thinkingStrength)
	}
	if p.Provider == "gpt" {
		return p.streamResponses(ctx, messages, thinkingEnabled, thinkingStrength, jsonOutput, jsonSchema)
	}
	return p.streamOpenAI(ctx, messages, &thinkingEnabled, thinkingStrength, jsonOutput, jsonSchema)
}

func (p *TextProvider) streamOpenAI(
	ctx context.Context,
	messages []ChatMessage,
	enableThinking *bool,
	thinkingStrength string,
	jsonOutput bool,
	jsonSchema map[string]any,
) (<-chan StreamDelta, <-chan error) {
	payload := map[string]any{
		"model":          p.model(),
		"messages":       messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"stop":           StopSequences,
	}
	if enableThinking != nil && p.Provider == "agnes" {
		payload["chat_template_kwargs"] = map[string]any{"enable_thinking": *enableThinking}
	}
	if p.Provider == "deepseek" {
		if enableThinking != nil && *enableThinking {
			payload["reasoning_effort"] = DeepSeekReasoningEffort(thinkingStrength)
		}
		if jsonOutput {
			if jsonSchema != nil {
				payload["response_format"] = map[string]any{
					"type": "json_schema",
					"json_schema": map[string]any{
						"name":   "ai_blocks_segment",
						"strict": true,
						"schema": jsonSchema,
					},
				}
			} else {
				payload["response_format"] = map[string]any{"type": "json_object"}
			}
		}
	}
	return p.requestStream(ctx, "chat/completions", payload, parseOpenAILines)
}

func (p *TextProvider) streamResponses(
	ctx context.Context,
	messages []ChatMessage,
	thinkingEnabled bool,
	thinkingStrength string,
	jsonOutput bool,
	jsonSchema map[string]any,
) (<-chan StreamDelta, <-chan error) {
	payload := map[string]any{"model": p.model(), "input": messages, "stream": true}
	if thinkingEnabled {
		payload["reasoning"] = map[string]any{"effort": thinkingStrength, "summary": "auto"}
	}
	if jsonOutput {
		var format map[string]any
		if jsonSchema != nil {
			format = map[string]any{"type": "json_schema", "name": "ai_blocks_segment", "strict": true, "schema": jsonSchema}
		} else {
			format = map[string]any{"type": "json_object"}
		}
		payload["text"] = map[string]any{"format": format}
	}
	return p.requestStream(ctx, "responses", payload, parseResponsesLines)
}

func (p *TextProvider) streamAnthropic(ctx context.Context, messages []ChatMessage, strength string) (<-chan StreamDelta, <-chan error) {
	system := make([]string, 0, 1)
	rest := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message.Role == "system" {
			system = append(system, message.Content)
			continue
		}
		rest = append(rest, message)
	}
	payload := map[string]any{
		"model":          p.Settings.AgnesChatModel,
		"messages":       rest,
		"max_tokens":     32768,
		"stream":         true,
		"stop_sequences": StopSequences,
		"thinking":       map[string]any{"type": "enabled", "budget_tokens": ThinkingBudget(strength)},
	}
	if len(system) > 0 {
		payload["system"] = strings.Join(system, "\n\n")
	}
	key, err := p.Settings.RequireAgnesAPIKey()
	if err != nil {
		return failedStream(err)
	}
	headers := map[string]string{
		"x-api-key":         key,
		"anthropic-version": "2023-06-01",
		"Content-Type":      "application/json",
	}
	return p.requestStreamWithHeaders(ctx, "messages", payload, headers, parseAnthropicLines)
}

type lineParser func(line string) ([]StreamDelta, bool, error)

func (p *TextProvider) requestStream(ctx context.Context, endpoint string, payload map[string]any, parser lineParser) (<-chan StreamDelta, <-chan error) {
	key, err := p.apiKey()
	if err != nil {
		return failedStream(err)
	}
	headers := map[string]string{
		"Authorization": "Bearer " + key,
		"Content-Type":  "application/json",
	}
	return p.requestStreamWithHeaders(ctx, endpoint, payload, headers, parser)
}

func failedStream(err error) (<-chan StreamDelta, <-chan error) {
	deltas := make(chan StreamDelta)
	errs := make(chan error, 1)
	errs <- err
	close(errs)
	close(deltas)
	return deltas, errs
}

// ResolveURL mirrors httpx base_url joining: the base path always ends with a
// slash before the relative endpoint is appended.
func ResolveURL(base, endpoint string) string {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + strings.TrimPrefix(endpoint, "/")
}

func (p *TextProvider) requestStreamWithHeaders(
	ctx context.Context,
	endpoint string,
	payload map[string]any,
	headers map[string]string,
	parser lineParser,
) (<-chan StreamDelta, <-chan error) {
	deltas := make(chan StreamDelta, 16)
	errs := make(chan error, 1)

	body, err := json.Marshal(payload)
	if err != nil {
		return failedStream(newTextProviderError("AI provider request could not be encoded"))
	}

	client := p.Client
	if client == nil {
		client = &http.Client{
			Timeout: time.Duration(p.Settings.AIBlocksProviderTimeoutSeconds * float64(time.Second)),
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, ResolveURL(p.baseURL(), endpoint), bytes.NewReader(body))
	if err != nil {
		return failedStream(newTextProviderError("AI provider request could not be created"))
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	go func() {
		defer close(deltas)
		defer close(errs)
		response, err := client.Do(request)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				errs <- err
			} else {
				errs <- newTextProviderError("AI provider connection failed")
			}
			return
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			errs <- newTextProviderError("AI provider rejected request with status %d", response.StatusCode)
			return
		}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			items, stop, err := parser(scanner.Text())
			if err != nil {
				errs <- err
				return
			}
			for _, item := range items {
				select {
				case deltas <- item:
				case <-ctx.Done():
					errs <- ctx.Err()
					return
				}
			}
			if stop {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				errs <- err
				return
			}
			errs <- newTextProviderError("AI provider connection failed")
		}
	}()
	return deltas, errs
}

func parseOpenAILines(raw string) ([]StreamDelta, bool, error) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
		return nil, false, nil
	}
	payload := line
	if strings.HasPrefix(line, "data:") {
		payload = strings.TrimSpace(line[5:])
	}
	if payload == "[DONE]" {
		return nil, true, nil
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		return nil, false, newTextProviderError("AI provider returned invalid stream")
	}
	if item == nil || item["error"] != nil {
		return nil, false, newTextProviderError("AI provider stream failed")
	}
	result := make([]StreamDelta, 0, 2)
	if usage, ok := item["usage"].(map[string]any); ok && usage != nil {
		result = append(result, StreamDelta{Kind: DeltaUsage, Usage: NormalizeUsage(usage)})
	}
	var delta map[string]any
	if choices, ok := item["choices"].([]any); ok && len(choices) > 0 {
		if first, ok := choices[0].(map[string]any); ok {
			if candidate, ok := first["delta"].(map[string]any); ok {
				delta = candidate
			}
		}
	}
	if delta == nil {
		return result, false, nil
	}
	if reasoning := firstString(delta, "reasoning_content", "reasoning"); reasoning != "" {
		result = append(result, StreamDelta{Kind: DeltaReasoning, Text: reasoning})
	}
	if content, ok := delta["content"].(string); ok && content != "" {
		result = append(result, StreamDelta{Kind: DeltaContent, Text: content})
	}
	return result, false, nil
}

func parseResponsesLines(raw string) ([]StreamDelta, bool, error) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
		return nil, false, nil
	}
	if !strings.HasPrefix(line, "data:") {
		return nil, false, nil
	}
	payload := strings.TrimSpace(line[5:])
	if payload == "[DONE]" {
		return nil, true, nil
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		return nil, false, newTextProviderError("GPT Responses stream is invalid")
	}
	if item == nil {
		return nil, false, nil
	}
	eventType, _ := item["type"].(string)
	if eventType == "response.failed" || eventType == "response.incomplete" || item["error"] != nil {
		return nil, false, newTextProviderError("GPT Responses stream failed")
	}
	result := make([]StreamDelta, 0, 2)
	switch eventType {
	case "response.output_text.delta":
		if text, ok := item["delta"].(string); ok && text != "" {
			result = append(result, StreamDelta{Kind: DeltaContent, Text: text})
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if text, ok := item["delta"].(string); ok && text != "" {
			result = append(result, StreamDelta{Kind: DeltaReasoning, Text: text})
		}
	case "response.completed":
		var usage map[string]any
		if response, ok := item["response"].(map[string]any); ok {
			if candidate, ok := response["usage"].(map[string]any); ok {
				usage = candidate
			}
		}
		if usage == nil {
			if candidate, ok := item["usage"].(map[string]any); ok {
				usage = candidate
			}
		}
		if usage != nil {
			result = append(result, StreamDelta{Kind: DeltaUsage, Usage: NormalizeUsage(usage)})
		}
		// The Responses SSE stream may stay open after the terminal event, so
		// stop at the protocol terminator instead of waiting for EOF.
		return result, true, nil
	}
	return result, false, nil
}

func parseAnthropicLines(raw string) ([]StreamDelta, bool, error) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
		return nil, false, nil
	}
	payload := line
	if strings.HasPrefix(line, "data:") {
		payload = strings.TrimSpace(line[5:])
	}
	if payload == "[DONE]" {
		return nil, true, nil
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		return nil, false, newTextProviderError("AI provider returned invalid stream")
	}
	if item == nil || item["type"] == "error" || item["error"] != nil {
		return nil, false, newTextProviderError("AI provider stream failed")
	}
	eventType, _ := item["type"].(string)
	result := make([]StreamDelta, 0, 1)
	if eventType == "message_start" || eventType == "message_delta" {
		var usage map[string]any
		if message, ok := item["message"].(map[string]any); ok {
			if candidate, ok := message["usage"].(map[string]any); ok {
				usage = candidate
			}
		}
		if usage == nil {
			if candidate, ok := item["usage"].(map[string]any); ok {
				usage = candidate
			}
		}
		if usage != nil {
			result = append(result, StreamDelta{Kind: DeltaUsage, Usage: NormalizeUsage(usage)})
		}
		return result, false, nil
	}
	if eventType != "content_block_delta" {
		return result, false, nil
	}
	delta, ok := item["delta"].(map[string]any)
	if !ok {
		return result, false, nil
	}
	switch delta["type"] {
	case "thinking_delta":
		if text, ok := delta["thinking"].(string); ok {
			result = append(result, StreamDelta{Kind: DeltaReasoning, Text: text})
		}
	case "text_delta":
		if text, ok := delta["text"].(string); ok {
			result = append(result, StreamDelta{Kind: DeltaContent, Text: text})
		}
	}
	return result, false, nil
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func intFrom(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0
		}
		return int(parsed)
	default:
		return 0
	}
}

// NormalizeUsage mirrors AgnesTextProvider._normalize_usage.
func NormalizeUsage(usage map[string]any) map[string]int {
	input := intFrom(firstPresent(usage, "input_tokens", "prompt_tokens"))
	output := intFrom(firstPresent(usage, "output_tokens", "completion_tokens"))
	total := intFrom(usage["total_tokens"])
	if total == 0 {
		total = input + output
	}
	normalized := map[string]int{
		"input_tokens":  input,
		"output_tokens": output,
		"total_tokens":  total,
	}
	var details map[string]any
	if candidate, ok := usage["input_tokens_details"].(map[string]any); ok {
		details = candidate
	} else if candidate, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		details = candidate
	}
	var cached any
	if details != nil {
		cached = details["cached_tokens"]
	}
	if cached == nil {
		cached = firstPresent(usage, "cached_input_tokens", "cache_read_input_tokens")
	}
	if cached != nil {
		normalized["cached_input_tokens"] = intFrom(cached)
	}
	return normalized
}

func firstPresent(value map[string]any, keys ...string) any {
	for _, key := range keys {
		if item, ok := value[key]; ok && item != nil {
			return item
		}
	}
	return nil
}
