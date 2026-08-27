import logging
import time
import uuid
from functools import lru_cache

from fastapi import APIRouter, Depends, HTTPException

from ..config import ConfigurationError, get_settings
from ..providers.sui_xiang_provider import ProviderError, SuiXiangProvider
from ..schemas.image_generation import ImageGenerationRequest, ImageGenerationResponse
from ..services.image_generation import ImageGenerationService


router = APIRouter(prefix='/api/image', tags=['image-generation'])
logger = logging.getLogger(__name__)


@lru_cache(maxsize=1)
def get_service() -> ImageGenerationService:
    settings = get_settings()
    return ImageGenerationService(settings, SuiXiangProvider(settings))


@router.post('/generate', response_model=ImageGenerationResponse)
async def generate_image(
    request: ImageGenerationRequest,
    service: ImageGenerationService = Depends(get_service),
) -> ImageGenerationResponse:
    request_id = str(uuid.uuid4())
    started_at = time.perf_counter()
    logger.info(
        'image_generate_started request_id=%s style=%s target=%s size=%sx%s sheet_layout=%s prompt_length=%s',
        request_id, request.style, request.target, request.width, request.height,
        request.sheet_layout, len(request.prompt),
    )
    try:
        response = await service.generate(request)
        logger.info(
            'image_generate_succeeded request_id=%s provider_request_id=%s output_size=%sx%s duration_ms=%s',
            request_id, response.request_id, response.sheet_width, response.sheet_height,
            round((time.perf_counter() - started_at) * 1000),
        )
        return response
    except ValueError as exc:
        logger.warning(
            'image_generate_invalid_request request_id=%s duration_ms=%s error=%s',
            request_id, round((time.perf_counter() - started_at) * 1000), str(exc)[:300],
        )
        raise HTTPException(status_code=422, detail={'code': 'INVALID_REQUEST', 'message': str(exc)}) from exc
    except ConfigurationError as exc:
        logger.error(
            'image_generate_configuration_error request_id=%s duration_ms=%s error_type=%s',
            request_id, round((time.perf_counter() - started_at) * 1000), type(exc).__name__,
        )
        raise HTTPException(
            status_code=503,
            detail={'code': 'CONFIGURATION_ERROR', 'message': '生图服务尚未配置'},
        ) from exc
    except ProviderError as exc:
        message = str(exc)
        code = 'TIMEOUT' if 'timed out' in message else 'PROVIDER_ERROR'
        logger.error(
            'image_generate_provider_error request_id=%s duration_ms=%s code=%s error_type=%s error=%s',
            request_id, round((time.perf_counter() - started_at) * 1000), code,
            type(exc).__name__, message[:500], exc_info=True,
        )
        raise HTTPException(status_code=502, detail={'code': code, 'message': '生图服务暂时不可用'}) from exc
