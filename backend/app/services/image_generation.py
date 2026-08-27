import base64
import io
import uuid
from asyncio import Semaphore

from PIL import Image, UnidentifiedImageError

from ..config import Settings
from ..providers.base import ImageProvider
from ..providers.sui_xiang_provider import ProviderError
from ..schemas.image_generation import ImageGenerationRequest, ImageGenerationResponse, RequestedCanvas, SheetLayout
from ..prompts import build_image_prompt


class ImageGenerationService:
    def __init__(self, settings: Settings, provider: ImageProvider) -> None:
        self.settings = settings
        self.provider = provider
        self.semaphore = Semaphore(settings.image_max_concurrency)

    async def generate(self, request: ImageGenerationRequest) -> ImageGenerationResponse:
        request.validate_canvas_size()
        prompt = build_image_prompt(request)
        async with self.semaphore:
            try:
                provider_image = await self.provider.generate(prompt)
            except ProviderError:
                raise
            except TimeoutError as exc:
                raise ProviderError('provider request timed out') from exc
        png_bytes, width, height = self._normalize_image(provider_image.data, provider_image.mime_type)
        return ImageGenerationResponse(
            request_id=provider_image.request_id or str(uuid.uuid4()),
            sheet_width=width,
            sheet_height=height,
            image_base64=base64.b64encode(png_bytes).decode('ascii'),
            target=request.target,
            requested_canvas=RequestedCanvas(width=request.width, height=request.height),
            sheet_layout=SheetLayout(),
        )

    def _normalize_image(self, data: bytes, mime_type: str) -> tuple[bytes, int, int]:
        if not data or len(data) > self.settings.image_max_download_bytes:
            raise ProviderError('image is missing or too large')
        if not mime_type.startswith('image/'):
            raise ProviderError('provider returned a non-image MIME type')
        try:
            with Image.open(io.BytesIO(data)) as image:
                image.verify()
            with Image.open(io.BytesIO(data)) as image:
                width, height = image.size
                if width > 4096 or height > 4096:
                    raise ProviderError('image dimensions exceed the safety limit')
                image = image.convert('RGBA')
                output = io.BytesIO()
                image.save(output, format='PNG', optimize=True)
                return output.getvalue(), width, height
        except UnidentifiedImageError as exc:
            raise ProviderError('provider returned invalid image data') from exc
