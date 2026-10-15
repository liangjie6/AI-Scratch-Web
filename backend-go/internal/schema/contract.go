// Package schema ports backend/app/schemas/ai_blocks.py and
// backend/app/ai_blocks_contract.py into Go.
//
// The Python implementation is the source of truth: every model, field
// constraint, validator and module-level function is reproduced here with the
// same names, limits, diagnostic codes and messages. Where Go cannot express a
// pydantic concept directly (strict decoding, discriminated unions, defaults on
// absent fields) the package provides explicit helpers that mirror pydantic
// behaviour.
//
// Deviations that are inherent to Go:
//
//   - The StrictModel base class has no Go type; DecodeStrict implements its
//     extra='forbid' + strict semantics (JSON object root, unknown fields
//     rejected at every level, required fields enforced, defaults applied).
//   - Union aliases (ResourceOp, InputValue, FieldValue, ProjectGenerationRequest
//     and the resource sources) are modelled as any and validated by the
//     exported Validate*/Decode* helpers instead of a sealed union type.
//   - SchemaValidationError.Path is a JSON pointer. Required-field, unknown-field
//     and null errors carry the full path from the decoded root; errors raised by
//     validator bodies use the path of the model that raised them.
//   - Optional list fields (ConditionSpec.operands, ActionSpec.actions) carry an
//     optional:"true" tag so an absent/null value stays nil and skips
//     min_length, exactly like the Python "| None = None" default.
//   - canonical_contract_json matches Python byte-for-byte for the bounded
//     contract payloads this service produces. Extreme float spellings (for
//     example 1e20 or 1e-5) and U+2028/U+2029 use Go's encoder spelling, which
//     differs from Python's repr in those cases.
package schema

import (
	"github.com/ai-scratch-web/backend/internal/blockcatalog"
	"github.com/ai-scratch-web/backend/internal/resourcecontract"
)

// Limits from backend/app/schemas/ai_blocks.py.
const (
	MaxBlocks                     = 120
	MaxBlockDepth                 = 12
	MaxScripts                    = 12
	MaxStringLength               = 2000
	MaxSummaryLength              = 1000
	ProjectContextSoftBudgetBytes = 64 * 1024

	// MaxCurrentProgramLength is the max_length of TargetContext.current_program
	// and ValidationRequest.current_program (40_000).
	MaxCurrentProgramLength = 40_000

	// MaxShortStringLength is the max_length of ShortString.
	MaxShortStringLength = 120

	// DefaultRepairScope is the default repair_scope of ResourceContractIssue.
	DefaultRepairScope = "resource_op"

	// DefaultResourceOpPath is the keyword-only default path of
	// normalize_resource_op.
	DefaultResourceOpPath = "/resource_ops/0"

	// MaxBoundedActual is the default limit of bounded_actual.
	MaxBoundedActual = 2048
)

// ModelTier mirrors ModelTier from ai_blocks_contract.py.
type ModelTier string

// Model tiers.
const (
	ModelTierNormal   ModelTier = "normal"
	ModelTierAdvanced ModelTier = "advanced"
	ModelTierSuper    ModelTier = "super"
)

// ThinkingStrength mirrors ThinkingStrength from ai_blocks_contract.py.
type ThinkingStrength string

// Thinking strengths.
const (
	ThinkingStrengthLow    ThinkingStrength = "low"
	ThinkingStrengthMedium ThinkingStrength = "medium"
	ThinkingStrengthHigh   ThinkingStrength = "high"
	ThinkingStrengthXHigh  ThinkingStrength = "xhigh"
	ThinkingStrengthMax    ThinkingStrength = "max"
)

// ModelTiers is MODEL_TIERS.
var ModelTiers = []ModelTier{ModelTierNormal, ModelTierAdvanced, ModelTierSuper}

// ModelTierProviders is MODEL_TIER_PROVIDERS.
var ModelTierProviders = map[ModelTier]string{
	ModelTierNormal:   "agnes",
	ModelTierAdvanced: "deepseek",
	ModelTierSuper:    "gpt",
}

// ThinkingStrengths is THINKING_STRENGTHS.
var ThinkingStrengths = []ThinkingStrength{
	ThinkingStrengthLow,
	ThinkingStrengthMedium,
	ThinkingStrengthHigh,
	ThinkingStrengthXHigh,
	ThinkingStrengthMax,
}

// ThinkingBudgetTokens is THINKING_BUDGET_TOKENS.
var ThinkingBudgetTokens = map[ThinkingStrength]int{
	ThinkingStrengthLow:    1024,
	ThinkingStrengthMedium: 2048,
	ThinkingStrengthHigh:   4096,
	ThinkingStrengthXHigh:  8192,
	ThinkingStrengthMax:    16384,
}

// DeepSeekReasoningEffort is DEEPSEEK_REASONING_EFFORT.
var DeepSeekReasoningEffort = map[ThinkingStrength]string{
	ThinkingStrengthLow:    "low",
	ThinkingStrengthMedium: "medium",
	ThinkingStrengthHigh:   "high",
	ThinkingStrengthXHigh:  "high",
	ThinkingStrengthMax:    "high",
}

// ProviderForTier ports provider_for_tier: unknown tiers fall back to agnes.
func ProviderForTier(tier string) string {
	if provider, ok := ModelTierProviders[ModelTier(tier)]; ok {
		return provider
	}
	return "agnes"
}

// ThinkingBudget ports thinking_budget: unknown strengths fall back to medium.
func ThinkingBudget(strength string) int {
	if budget, ok := ThinkingBudgetTokens[ThinkingStrength(strength)]; ok {
		return budget
	}
	return ThinkingBudgetTokens[ThinkingStrengthMedium]
}

// ResourceDiagnosticCodes is RESOURCE_DIAGNOSTIC_CODES, re-exported because
// schemas/ai_blocks.py imports it from the resource contract module.
var ResourceDiagnosticCodes = resourcecontract.DiagnosticCodes

// AllowedSizes is ALLOWED_SIZES from schemas/image_generation.py: the allowed
// (width, height) canvas pairs.
var AllowedSizes = map[[2]int]bool{
	{480, 360}: true,
	{640, 360}: true,
	{360, 360}: true,
	{360, 480}: true,
}

// ProcedureOpcodes is PROCEDURE_OPCODES.
var ProcedureOpcodes = map[string]bool{
	"procedures_call":                 true,
	"argument_reporter_string_number": true,
	"argument_reporter_boolean":       true,
}

// IsNativeOpcode reports whether opcode exists in BLOCK_CONTRACTS
// (NATIVE_OPCODES = frozenset(BLOCK_CONTRACTS)).
func IsNativeOpcode(opcode string) bool {
	_, ok := blockcatalog.BlockContracts[opcode]
	return ok
}

// ExtensionForOpcode mirrors extension_for_opcode: it returns the owning
// extension, or "" when the opcode is a core block.
func ExtensionForOpcode(opcode string) string {
	return blockcatalog.ExtensionForOpcode(opcode)
}
