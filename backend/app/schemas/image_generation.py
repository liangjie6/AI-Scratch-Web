from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


ALLOWED_SIZES = {
    (480, 360),
    (640, 360),
    (360, 360),
    (360, 480),
}


class ImageGenerationRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')

    prompt: str = Field(min_length=2, max_length=800)
    style: Literal['cartoon', 'scratch', 'paper-cut', 'pixel', 'watercolor', 'custom']
    custom_style: str | None = Field(default=None, min_length=2, max_length=240)
    width: int = Field(ge=1, le=2_000)
    height: int = Field(ge=1, le=2_000)
    target: Literal['backdrop', 'sprite']
    sheet_layout: Literal['1x1']

    @field_validator('prompt')
    @classmethod
    def normalize_prompt(cls, value: str) -> str:
        value = ' '.join(value.split())
        if not value:
            raise ValueError('prompt must not be empty')
        return value

    @field_validator('custom_style')
    @classmethod
    def normalize_custom_style(cls, value: str | None) -> str | None:
        if value is None:
            return None
        value = ' '.join(value.split())
        if len(value) < 2:
            raise ValueError('custom_style must contain at least 2 characters')
        return value

    @model_validator(mode='after')
    def validate_custom_style(self) -> 'ImageGenerationRequest':
        if self.style == 'custom' and not self.custom_style:
            raise ValueError('custom_style is required when style is custom')
        return self

    @field_validator('width')
    @classmethod
    def validate_size_pair_placeholder(cls, value: int) -> int:
        return value

    @model_validator(mode='after')
    def validate_canvas_size(self) -> 'ImageGenerationRequest':
        if (self.width, self.height) not in ALLOWED_SIZES:
            raise ValueError('width and height must be an allowed canvas pair')
        return self


class SheetLayout(BaseModel):
    columns: Literal[1] = 1
    rows: Literal[1] = 1


class RequestedCanvas(BaseModel):
    width: int
    height: int


class ImageGenerationResponse(BaseModel):
    request_id: str
    mime_type: Literal['image/png'] = 'image/png'
    sheet_width: int
    sheet_height: int
    sheet_layout: SheetLayout = SheetLayout()
    image_base64: str
    target: Literal['backdrop', 'sprite']
    requested_canvas: RequestedCanvas

