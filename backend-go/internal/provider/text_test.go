package provider

import (
	"reflect"
	"testing"
)

func TestResolveURLMatchesHTTPXBaseJoin(t *testing.T) {
	cases := []struct {
		base     string
		endpoint string
		want     string
	}{
		{"https://api.agnes-ai.cn/v1/", "chat/completions", "https://api.agnes-ai.cn/v1/chat/completions"},
		{"https://api.deepseek.com", "chat/completions", "https://api.deepseek.com/chat/completions"},
		{"https://sui-xiang.com/v1", "responses", "https://sui-xiang.com/v1/responses"},
		{"https://api.agnes-ai.cn/v1/", "/messages", "https://api.agnes-ai.cn/v1/messages"},
	}
	for _, item := range cases {
		if got := ResolveURL(item.base, item.endpoint); got != item.want {
			t.Errorf("ResolveURL(%q, %q) = %q, want %q", item.base, item.endpoint, got, item.want)
		}
	}
}

func TestNormalizeUsageMatchesPythonReference(t *testing.T) {
	openai := NormalizeUsage(map[string]any{
		"prompt_tokens":     float64(11),
		"completion_tokens": float64(7),
		"total_tokens":      float64(18),
		"prompt_tokens_details": map[string]any{
			"cached_tokens": float64(3),
		},
	})
	want := map[string]int{"input_tokens": 11, "output_tokens": 7, "total_tokens": 18, "cached_input_tokens": 3}
	if !reflect.DeepEqual(openai, want) {
		t.Fatalf("openai usage = %v, want %v", openai, want)
	}

	anthropic := NormalizeUsage(map[string]any{
		"input_tokens":            float64(5),
		"output_tokens":           float64(2),
		"cache_read_input_tokens": float64(9),
	})
	wantAnthropic := map[string]int{"input_tokens": 5, "output_tokens": 2, "total_tokens": 7, "cached_input_tokens": 9}
	if !reflect.DeepEqual(anthropic, wantAnthropic) {
		t.Fatalf("anthropic usage = %v, want %v", anthropic, wantAnthropic)
	}

	derived := NormalizeUsage(map[string]any{"input_tokens": float64(4), "output_tokens": float64(6)})
	if derived["total_tokens"] != 10 {
		t.Fatalf("total_tokens must default to input+output, got %v", derived)
	}
}

func TestParseOpenAILines(t *testing.T) {
	deltas, stop, err := parseOpenAILines(`data: {"choices":[{"delta":{"reasoning_content":"think","content":"hi"}}]}`)
	if err != nil || stop {
		t.Fatalf("parse failed: err=%v stop=%v", err, stop)
	}
	if len(deltas) != 2 || deltas[0].Kind != DeltaReasoning || deltas[1].Kind != DeltaContent {
		t.Fatalf("deltas = %#v", deltas)
	}
	if _, stop, err := parseOpenAILines("data: [DONE]"); err != nil || !stop {
		t.Fatalf("[DONE] must stop the stream: err=%v stop=%v", err, stop)
	}
	if deltas, _, err := parseOpenAILines(": keepalive"); err != nil || deltas != nil {
		t.Fatalf("comments must be ignored: %#v %v", deltas, err)
	}
	if _, _, err := parseOpenAILines("data: not-json"); err == nil {
		t.Fatal("invalid JSON must raise a provider error")
	}
	if _, _, err := parseOpenAILines(`data: {"error":{"message":"boom"}}`); err == nil {
		t.Fatal("an error payload must raise a provider error")
	}
}

func TestParseResponsesLines(t *testing.T) {
	deltas, stop, err := parseResponsesLines(`data: {"type":"response.output_text.delta","delta":"hello"}`)
	if err != nil || stop || len(deltas) != 1 || deltas[0].Kind != DeltaContent {
		t.Fatalf("deltas = %#v stop=%v err=%v", deltas, stop, err)
	}
	deltas, stop, err = parseResponsesLines(`data: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":4}}}`)
	if err != nil || !stop {
		t.Fatalf("response.completed must terminate the stream: stop=%v err=%v", stop, err)
	}
	if len(deltas) != 1 || deltas[0].Kind != DeltaUsage || deltas[0].Usage["total_tokens"] != 7 {
		t.Fatalf("usage delta = %#v", deltas)
	}
	if _, _, err := parseResponsesLines(`data: {"type":"response.failed"}`); err == nil {
		t.Fatal("response.failed must raise a provider error")
	}
}

func TestParseAnthropicLines(t *testing.T) {
	deltas, _, err := parseAnthropicLines(`data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hmm"}}`)
	if err != nil || len(deltas) != 1 || deltas[0].Kind != DeltaReasoning {
		t.Fatalf("deltas = %#v err=%v", deltas, err)
	}
	deltas, _, err = parseAnthropicLines(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"yo"}}`)
	if err != nil || len(deltas) != 1 || deltas[0].Kind != DeltaContent || deltas[0].Text != "yo" {
		t.Fatalf("deltas = %#v err=%v", deltas, err)
	}
	deltas, _, err = parseAnthropicLines(`data: {"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}`)
	if err != nil || len(deltas) != 1 || deltas[0].Kind != DeltaUsage {
		t.Fatalf("deltas = %#v err=%v", deltas, err)
	}
	if _, _, err := parseAnthropicLines(`data: {"type":"error"}`); err == nil {
		t.Fatal("an error event must raise a provider error")
	}
}

func TestProviderCapabilityProbes(t *testing.T) {
	deepseek := &TextProvider{Provider: "deepseek"}
	if !deepseek.SupportsStructuredOutput("program") {
		t.Fatal("deepseek must support structured output")
	}
	if deepseek.SupportsJSONSchema("program") {
		t.Fatal("deepseek must not claim strict JSON schema support")
	}
	gpt := &TextProvider{Provider: "gpt"}
	if !gpt.SupportsJSONSchema("blueprint") {
		t.Fatal("gpt must support strict JSON schema for blueprint")
	}
	if gpt.SupportsJSONSchema("program") {
		t.Fatal("gpt must not claim JSON schema support for program")
	}
	agnes := &TextProvider{Provider: "agnes"}
	if agnes.SupportsStructuredOutput("program") {
		t.Fatal("agnes must not claim structured output support")
	}
}

func TestProviderForTierAndThinkingBudgets(t *testing.T) {
	if ProviderForTier("super") != "gpt" || ProviderForTier("advanced") != "deepseek" || ProviderForTier("normal") != "agnes" {
		t.Fatal("tier to provider mapping is wrong")
	}
	if ProviderForTier("unknown") != "agnes" {
		t.Fatal("unknown tiers must fall back to agnes")
	}
	if ThinkingBudget("max") != 16384 || ThinkingBudget("unknown") != 2048 {
		t.Fatal("thinking budget mapping is wrong")
	}
	if DeepSeekReasoningEffort("max") != "high" || DeepSeekReasoningEffort("low") != "low" {
		t.Fatal("deepseek reasoning effort mapping is wrong")
	}
}
