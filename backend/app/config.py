from functools import lru_cache

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
    ai_blocks_max_repair_rounds: int = Field(
        default=2, ge=0, le=3, validation_alias='AI_BLOCKS_MAX_REPAIR_ROUNDS',
    )
    provider_base_url: str = 'https://sui-xiang.com/v1'
    provider_model: str = 'gpt-image-2'
    agnes_api_base: str = 'https://api.agnes-ai.cn/v1/'
    agnes_chat_model: str = 'agnes-2.5-flash'
    deepseek_api_base: str = 'https://api.deepseek.com'
    deepseek_chat_model: str = 'deepseek-v4-flash'
    gpt_api_base: str = 'https://apexapi.roixw.com'
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


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()
