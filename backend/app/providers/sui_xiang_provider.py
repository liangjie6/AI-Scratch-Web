import asyncio
import logging
import time
from typing import Any

from openai import OpenAI

from ..config import Settings
from .base import ProviderImage


class ProviderError(RuntimeError):
    pass


logger = logging.getLogger(__name__)


class SuiXiangProvider:
    def __init__(self, settings: Settings, http_client: Any | None = None) -> None:
        self.settings = settings
        self.http_client = http_client

    async def generate(self, prompt: str) -> ProviderImage:
        started_at = time.perf_counter()
        logger.info(
            'sui_xiang_generate_started base_url=%s model=%s prompt_length=%s timeout_seconds=%s',
            self.settings.provider_base_url, self.settings.provider_model, len(prompt),
            self.settings.image_request_timeout_seconds,
        )
        api_key = self.settings.require_api_key()
        client = OpenAI(
            api_key=api_key,
            base_url=self.settings.provider_base_url,
            timeout=self.settings.image_request_timeout_seconds,
        )
        try:
            response = await asyncio.to_thread(
                client.images.generate,
                model=self.settings.provider_model,
                prompt=prompt,
            )
        except Exception as exc:  # provider SDK errors vary by transport/version
            status_code = getattr(exc, 'status_code', None)
            upstream_request_id = getattr(exc, 'request_id', None)
            logger.error(
                'sui_xiang_generate_failed duration_ms=%s error_type=%s status_code=%s upstream_request_id=%s',
                round((time.perf_counter() - started_at) * 1000), type(exc).__name__,
                status_code, upstream_request_id,
            )
            raise ProviderError('provider request failed') from exc

        data = getattr(response, 'data', None) or []
        logger.info(
            'sui_xiang_generate_response duration_ms=%s upstream_request_id=%s item_count=%s',
            round((time.perf_counter() - started_at) * 1000), getattr(response, 'id', None), len(data),
        )
        if not data:
            logger.error('sui_xiang_generate_empty_response upstream_request_id=%s', getattr(response, 'id', None))
            raise ProviderError('provider returned no image')
        item = data[0]
        request_id = str(getattr(response, 'id', '') or 'provider-request')
        b64 = getattr(item, 'b64_json', None)
        if b64:
            import base64
            try:
                image_data = base64.b64decode(b64, validate=True)
                logger.info(
                    'sui_xiang_generate_base64_received upstream_request_id=%s bytes=%s duration_ms=%s',
                    request_id, len(image_data), round((time.perf_counter() - started_at) * 1000),
                )
                return ProviderImage(request_id, 'image/png', image_data)
            except Exception as exc:
                logger.error(
                    'sui_xiang_generate_invalid_base64 upstream_request_id=%s error_type=%s',
                    request_id, type(exc).__name__,
                )
                raise ProviderError('provider returned invalid base64 image') from exc

        url = getattr(item, 'url', None)
        if not url or not isinstance(url, str) or not url.startswith('https://'):
            logger.error('sui_xiang_generate_unsafe_image_url upstream_request_id=%s has_url=%s', request_id, bool(url))
            raise ProviderError('provider returned an unsafe image URL')
        try:
            import httpx
            async with httpx.AsyncClient(
                timeout=self.settings.image_request_timeout_seconds,
                follow_redirects=False,
            ) as client_http:
                response_http = await client_http.get(url)
                response_http.raise_for_status()
                content_type = response_http.headers.get('content-type', '').split(';', 1)[0].lower()
                if not content_type.startswith('image/'):
                    raise ProviderError('provider URL did not return an image')
                if len(response_http.content) > self.settings.image_max_download_bytes:
                    raise ProviderError('provider image is too large')
                return ProviderImage(request_id, content_type, response_http.content)
        except ProviderError:
            raise
        except Exception as exc:
            raise ProviderError('provider image download failed') from exc
