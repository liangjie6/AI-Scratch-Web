from dataclasses import dataclass
from typing import Protocol


@dataclass(frozen=True)
class ProviderImage:
    request_id: str
    mime_type: str
    data: bytes


class ImageProvider(Protocol):
    async def generate(self, prompt: str) -> ProviderImage:
        ...

