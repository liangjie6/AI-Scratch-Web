from functools import lru_cache
from hashlib import sha256
from pathlib import Path

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class ConfigurationError(RuntimeError):
    pass


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file='.env', extra='ignore')

    sui_xiang_api_key: str | None = Field(default=None, validation_alias='SUI_XIANG_API_KEY')
    agnes_api_key: str | None = Field(default=None, validation_alias='AGNES_API_KEY')
    deepseek_api_key: str | None = Field(default=None, validation_alias='DEEPSEEK_API_KEY')
    gpt_api_key: str | None = Field(default=None, validation_alias='GPT_API_KEY')
    image_request_timeout_seconds: float = Field(default=90, ge=5, le=180, validation_alias='IMAGE_REQUEST_TIMEOUT_SECONDS')
    image_max_download_bytes: int = Field(default=12_582_912, ge=1_048_576, le=50_000_000, validation_alias='IMAGE_MAX_DOWNLOAD_BYTES')
    image_max_request_bytes: int = Field(default=16_384, ge=2_048, le=1_048_576, validation_alias='IMAGE_MAX_REQUEST_BYTES')
    image_max_concurrency: int = Field(default=2, ge=1, le=8, validation_alias='IMAGE_MAX_CONCURRENCY')
    ai_blocks_max_request_bytes: int = Field(default=262_144, ge=16_384, le=1_048_576, validation_alias='AI_BLOCKS_MAX_REQUEST_BYTES')
    ai_blocks_max_concurrency: int = Field(default=2, ge=1, le=8, validation_alias='AI_BLOCKS_MAX_CONCURRENCY')
    ai_blocks_provider_timeout_seconds: float = Field(
        default=480, ge=10, le=720, validation_alias='AI_BLOCKS_PROVIDER_TIMEOUT_SECONDS',
    )
    # Local start-all runs may opt into bounded model-output diagnostics. Keep
    # this disabled for normal deployments because model output can contain
    # user-authored project text.
    ai_blocks_verbose_logging: bool = Field(default=False, validation_alias='AI_BLOCKS_VERBOSE_LOGGING')
    ai_blocks_max_repair_rounds: int = Field(
        default=3, ge=0, le=3, validation_alias='AI_BLOCKS_MAX_REPAIR_ROUNDS',
    )
    ai_blocks_commit_enabled: bool = Field(default=True, validation_alias='AI_BLOCKS_COMMIT_ENABLED')
    ai_project_assets_enabled: bool = Field(default=True, validation_alias='AI_PROJECT_ASSETS_ENABLED')
    ai_sound_assets_enabled: bool = Field(default=True, validation_alias='AI_SOUND_ASSETS_ENABLED')
    ai_logical_resource_refs_enabled: bool = Field(default=True, validation_alias='AI_LOGICAL_RESOURCE_REFS_ENABLED')
    ai_short_commit_enabled: bool = Field(default=True, validation_alias='AI_SHORT_COMMIT_ENABLED')
    ai_audio_generation_enabled: bool = Field(default=False, validation_alias='AI_AUDIO_GENERATION_ENABLED')
    ai_asset_auto_match_enabled: bool = Field(default=False, validation_alias='AI_ASSET_AUTO_MATCH_ENABLED')
    ai_asset_auto_match_min_score: int = Field(default=8, ge=8, le=100, validation_alias='AI_ASSET_AUTO_MATCH_MIN_SCORE')
    ai_asset_auto_match_min_margin: int = Field(default=5, ge=5, le=100, validation_alias='AI_ASSET_AUTO_MATCH_MIN_MARGIN')
    ai_runs_database_path: Path = Field(default=Path('data/ai-runs.sqlite3'), validation_alias='AI_RUNS_DATABASE_PATH')
    ai_runs_event_retention_hours: int = Field(default=24, ge=1, le=24 * 30, validation_alias='AI_RUNS_EVENT_RETENTION_HOURS')
    ai_runs_retention_days: int = Field(default=7, ge=1, le=90, validation_alias='AI_RUNS_RETENTION_DAYS')
    ai_durable_runs_enabled: bool = Field(default=False, validation_alias='AI_DURABLE_RUNS_ENABLED')
    ai_durable_runs_rollout_percent: int = Field(
        default=100, ge=0, le=100, validation_alias='AI_DURABLE_RUNS_ROLLOUT_PERCENT',
    )
    ai_crash_checkpoint_enabled: bool = Field(default=True, validation_alias='AI_CRASH_CHECKPOINT_ENABLED')
    ai_pen_extension_enabled: bool = Field(default=False, validation_alias='AI_PEN_EXTENSION_ENABLED')
    ai_music_extension_enabled: bool = Field(default=False, validation_alias='AI_MUSIC_EXTENSION_ENABLED')
    ai_text_to_speech_extension_enabled: bool = Field(default=False, validation_alias='AI_TEXT_TO_SPEECH_EXTENSION_ENABLED')
    # Deletion requires a separate approval and compensation implementation.
    # Keep generated manifests fail-closed until that path is released.
    ai_destructive_resource_ops_enabled: bool = Field(default=False, validation_alias='AI_DESTRUCTIVE_RESOURCE_OPS_ENABLED')
    provider_base_url: str = 'https://sui-xiang.com/v1'
    provider_model: str = 'gpt-image-2'
    agnes_api_base: str = 'https://api.agnes-ai.cn/v1/'
    agnes_chat_model: str = 'agnes-2.5-flash'
    deepseek_api_base: str = 'https://api.deepseek.com'
    deepseek_chat_model: str = 'deepseek-v4.1-flash-expires-on-0910'
    gpt_api_base: str = 'https://sui-xiang.com/v1'
    gpt_chat_model: str = 'gpt-5.6-sol'

    def require_api_key(self) -> str:
        if not self.sui_xiang_api_key:
            raise ConfigurationError('SUI_XIANG_API_KEY is required for image generation')
        return self.sui_xiang_api_key

    def require_agnes_api_key(self) -> str:
        if not self.agnes_api_key:
            raise RuntimeError('AGNES_API_KEY is required for text expansion')
        return self.agnes_api_key

    def require_deepseek_api_key(self) -> str:
        if not self.deepseek_api_key:
            raise RuntimeError('DEEPSEEK_API_KEY is required for advanced AI block generation')
        return self.deepseek_api_key

    def require_gpt_api_key(self) -> str:
        if not self.gpt_api_key:
            raise RuntimeError('GPT_API_KEY is required for super AI block generation')
        return self.gpt_api_key

    def durable_runs_enabled_for_scope(self, scope: str) -> bool:
        """Use a stable opaque scope bucket so rollout does not expose project data."""
        return self.durable_runs_enabled_for_scope_hash(sha256(scope.encode('utf-8')).hexdigest())

    def durable_runs_enabled_for_scope_hash(self, scope_hash: str) -> bool:
        """Evaluate a previously normalized scope without retaining the raw value."""
        if not self.ai_durable_runs_enabled or self.ai_durable_runs_rollout_percent <= 0:
            return False
        if self.ai_durable_runs_rollout_percent >= 100:
            return True
        bucket = int(scope_hash[:8], 16) % 100
        return bucket < self.ai_durable_runs_rollout_percent

    def enabled_ai_extensions(self) -> frozenset[str]:
        enabled: set[str] = set()
        if self.ai_pen_extension_enabled:
            enabled.add('pen')
        if self.ai_music_extension_enabled:
            enabled.add('music')
        if self.ai_text_to_speech_extension_enabled:
            enabled.add('text2speech')
        return frozenset(enabled)


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()
