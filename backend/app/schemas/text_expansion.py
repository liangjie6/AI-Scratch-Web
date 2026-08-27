from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator


class TextExpansionRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')

    kind: Literal['scene', 'style']
    text: str = Field(min_length=1, max_length=800)

    @field_validator('text')
    @classmethod
    def normalize_text(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError('text must not be empty')
        return value
