import base64
import io

import httpx
import pytest
from PIL import Image

from app.config import Settings
from app.main import app
from app.providers.base import ProviderImage
from app.providers.sui_xiang_provider import ProviderError, SuiXiangProvider
from app.routes.image_generation import get_service
from app.services.image_generation import ImageGenerationService


def png_bytes(width=512, height=512):
    image = Image.new('RGBA', (width, height), (80, 160, 220, 255))
    output = io.BytesIO()
    image.save(output, format='PNG')
    return output.getvalue()


class MockProvider:
    def __init__(self, value=None, error=None, mime_type='image/png'):
        self.value = value if value is not None else png_bytes()
        self.error = error
        self.mime_type = mime_type
        self.prompt = None

    async def generate(self, prompt):
        self.prompt = prompt
        if self.error:
            raise self.error
        return ProviderImage('mock-request', self.mime_type, self.value)


@pytest.mark.asyncio
async def test_generate_contract_and_prompt_mapping():
    provider = MockProvider()
    service = ImageGenerationService(Settings(sui_xiang_api_key='test-key'), provider)
    request = {
        'prompt': '夜晚森林小屋',
        'style': 'cartoon',
        'width': 480,
        'height': 360,
        'target': 'backdrop',
        'sheet_layout': '1x1',
    }
    response = await service.generate(__import__('app.schemas.image_generation', fromlist=['ImageGenerationRequest']).ImageGenerationRequest(**request))
    assert response.mime_type == 'image/png'
    assert response.sheet_layout.columns == 1
    assert response.sheet_layout.rows == 1
    assert response.requested_canvas.width == 480
    assert 'one complete image' in provider.prompt
    assert base64.b64decode(response.image_base64).startswith(b'\x89PNG')


@pytest.mark.asyncio
async def test_scratch_style_is_included_in_provider_prompt():
    from app.schemas.image_generation import ImageGenerationRequest

    provider = MockProvider()
    service = ImageGenerationService(Settings(sui_xiang_api_key='test-key'), provider)
    request = ImageGenerationRequest(
        prompt='会走路的小猫', style='scratch', width=480, height=360,
        target='sprite', sheet_layout='1x1'
    )

    await service.generate(request)

    assert 'Scratch-inspired 2D vector art' in provider.prompt


@pytest.mark.asyncio
async def test_custom_style_is_included_in_provider_prompt():
    from app.schemas.image_generation import ImageGenerationRequest

    provider = MockProvider()
    service = ImageGenerationService(Settings(sui_xiang_api_key='test-key'), provider)
    request = ImageGenerationRequest(
        prompt='夜晚森林小屋', style='custom', custom_style='复古木刻版画',
        width=480, height=360, target='backdrop', sheet_layout='1x1'
    )

    await service.generate(request)

    assert 'Style: 复古木刻版画.' in provider.prompt


@pytest.mark.asyncio
async def test_invalid_size_and_image_are_rejected():
    service = ImageGenerationService(Settings(sui_xiang_api_key='test-key'), MockProvider())
    from app.schemas.image_generation import ImageGenerationRequest
    request = ImageGenerationRequest(
        prompt='test', style='pixel', width=480, height=360,
        target='sprite', sheet_layout='1x1'
    )
    request.width = 500
    with pytest.raises(ValueError):
        await service.generate(request)

    invalid = ImageGenerationService(Settings(sui_xiang_api_key='test-key'), MockProvider(b'not-an-image'))
    with pytest.raises(ProviderError):
        await invalid.generate(ImageGenerationRequest(
            prompt='test', style='pixel', width=480, height=360,
            target='sprite', sheet_layout='1x1'
        ))


@pytest.mark.asyncio
async def test_route_uses_local_provider_dependency():
    provider = MockProvider()
    service = ImageGenerationService(Settings(sui_xiang_api_key='test-key'), provider)
    app.dependency_overrides[get_service] = lambda: service
    try:
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            response = await client.post('/api/image/generate', json={
                'prompt': '小屋', 'style': 'cartoon', 'width': 480,
                'height': 360, 'target': 'backdrop', 'sheet_layout': '1x1'
            })
        assert response.status_code == 200
        assert response.json()['target'] == 'backdrop'
    finally:
        app.dependency_overrides.clear()


@pytest.mark.asyncio
async def test_provider_error_is_not_leaked():
    service = ImageGenerationService(Settings(sui_xiang_api_key='test-key'), MockProvider(error=ProviderError('secret provider detail')))
    app.dependency_overrides[get_service] = lambda: service
    try:
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            response = await client.post('/api/image/generate', json={
                'prompt': '小屋', 'style': 'cartoon', 'width': 480,
                'height': 360, 'target': 'backdrop', 'sheet_layout': '1x1'
            })
        assert response.status_code == 502
        assert 'secret provider detail' not in response.text
    finally:
        app.dependency_overrides.clear()


@pytest.mark.asyncio
async def test_timeout_non_image_and_oversized_provider_data_are_rejected():
    from app.schemas.image_generation import ImageGenerationRequest
    request = ImageGenerationRequest(
        prompt='test', style='pixel', width=480, height=360,
        target='sprite', sheet_layout='1x1'
    )

    timeout_service = ImageGenerationService(
        Settings(sui_xiang_api_key='test-key'),
        MockProvider(error=TimeoutError()),
    )
    with pytest.raises(ProviderError, match='timed out'):
        await timeout_service.generate(request)

    non_image_service = ImageGenerationService(
        Settings(sui_xiang_api_key='test-key'),
        MockProvider(value=b'<html>', mime_type='text/html'),
    )
    with pytest.raises(ProviderError, match='non-image'):
        await non_image_service.generate(request)

    oversized_service = ImageGenerationService(
        Settings(sui_xiang_api_key='test-key', image_max_download_bytes=1_048_576),
        MockProvider(value=b'x' * 1_048_577),
    )
    with pytest.raises(ProviderError, match='too large'):
        await oversized_service.generate(request)


def test_missing_api_key_fails_the_production_entrypoint_check():
    with pytest.raises(RuntimeError, match='SUI_XIANG_API_KEY is required'):
        Settings(sui_xiang_api_key=None).require_api_key()


@pytest.mark.asyncio
async def test_route_reports_missing_image_provider_configuration():
    settings = Settings(sui_xiang_api_key=None)
    service = ImageGenerationService(settings, SuiXiangProvider(settings))
    app.dependency_overrides[get_service] = lambda: service
    try:
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            response = await client.post('/api/image/generate', json={
                'prompt': '小屋', 'style': 'cartoon', 'width': 480,
                'height': 360, 'target': 'backdrop', 'sheet_layout': '1x1'
            })
        assert response.status_code == 503
        assert response.json()['detail'] == {
            'code': 'CONFIGURATION_ERROR', 'message': '生图服务尚未配置'
        }
    finally:
        app.dependency_overrides.clear()


@pytest.mark.asyncio
async def test_route_rejects_oversized_request_body_before_provider_use():
    transport = httpx.ASGITransport(app=app)
    async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
        response = await client.post(
            '/api/image/generate',
            content=b'x' * 20_000,
            headers={'content-type': 'application/json'},
        )
    assert response.status_code == 413
    assert response.json()['detail']['code'] == 'REQUEST_TOO_LARGE'
