from __future__ import annotations

from typing import Final, Literal


ModelTier = Literal['normal', 'advanced', 'super']
ThinkingStrength = Literal['low', 'medium', 'high', 'xhigh', 'max']

MODEL_TIERS: Final[tuple[ModelTier, ...]] = ('normal', 'advanced', 'super')
MODEL_TIER_PROVIDERS: Final[dict[ModelTier, str]] = {
    'normal': 'agnes',
    'advanced': 'deepseek',
    'super': 'gpt',
}
THINKING_STRENGTHS: Final[tuple[ThinkingStrength, ...]] = ('low', 'medium', 'high', 'xhigh', 'max')
THINKING_BUDGET_TOKENS: Final[dict[ThinkingStrength, int]] = {
    'low': 1024,
    'medium': 2048,
    'high': 4096,
    'xhigh': 8192,
    'max': 16384,
}

# DeepSeek currently accepts low, medium, and high reasoning effort. Keep the
# five-level UI contract and collapse the two highest levels for this provider.
DEEPSEEK_REASONING_EFFORT: Final[dict[ThinkingStrength, str]] = {
    'low': 'low',
    'medium': 'medium',
    'high': 'high',
    'xhigh': 'high',
    'max': 'high',
}


def provider_for_tier(tier: str) -> str:
    return MODEL_TIER_PROVIDERS.get(tier, 'agnes')  # type: ignore[arg-type]


def thinking_budget(strength: str) -> int:
    return THINKING_BUDGET_TOKENS.get(strength, THINKING_BUDGET_TOKENS['medium'])  # type: ignore[arg-type]
