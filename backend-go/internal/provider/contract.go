package provider

import "github.com/ai-scratch-web/backend/internal/config"

// ModelTier identifiers accepted by the AI blocks API.
var ModelTiers = []string{"normal", "advanced", "super"}

// ModelTierProviders maps a tier to its upstream provider id.
var ModelTierProviders = map[string]string{
	"normal":   "agnes",
	"advanced": "deepseek",
	"super":    "gpt",
}

// ThinkingStrengths lists the five-level UI thinking contract.
var ThinkingStrengths = []string{"low", "medium", "high", "xhigh", "max"}

// ThinkingBudgetTokens maps a thinking strength to an Anthropic thinking budget.
var ThinkingBudgetTokensByStrength = map[string]int{
	"low":    1024,
	"medium": 2048,
	"high":   4096,
	"xhigh":  8192,
	"max":    16384,
}

// DeepSeekReasoningEffort collapses the five-level UI contract onto the three
// reasoning efforts DeepSeek currently accepts.
var DeepSeekReasoningEffortByStrength = map[string]string{
	"low":    "low",
	"medium": "medium",
	"high":   "high",
	"xhigh":  "high",
	"max":    "high",
}

// ProviderForTier returns the provider id for a tier, defaulting to agnes.
func ProviderForTier(tier string) string {
	if provider, ok := ModelTierProviders[tier]; ok {
		return provider
	}
	return "agnes"
}

// ThinkingBudget returns the Anthropic thinking budget for a strength.
func ThinkingBudget(strength string) int {
	if budget, ok := ThinkingBudgetTokensByStrength[strength]; ok {
		return budget
	}
	return ThinkingBudgetTokensByStrength["medium"]
}

// DeepSeekReasoningEffort returns the DeepSeek reasoning effort for a strength.
func DeepSeekReasoningEffort(strength string) string {
	if effort, ok := DeepSeekReasoningEffortByStrength[strength]; ok {
		return effort
	}
	return DeepSeekReasoningEffortByStrength["medium"]
}

var _ = config.Get
