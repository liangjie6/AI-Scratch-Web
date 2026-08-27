import json

import httpx
import pytest

from app.config import Settings
from app.main import app
from app.providers.agnes_text_provider import AgnesTextProvider
from app.routes.text_expansion import get_text_provider


class MockTextProvider:
    def __init__(self, chunks=None, configured=True):
        self.chunks = chunks or []
        self.configured = configured
        self.messages = None

    def ensure_configured(self):
        if not self.configured:
            raise RuntimeError('missing key')

    async def stream(self, messages):
        self.messages = messages
        for chunk in self.chunks:
            yield chunk


@pytest.mark.asyncio
async def test_expand_route_streams_scene_prompt_and_uses_preset():
    provider = MockTextProvider(['夜晚的森林小屋，', '暖光从窗户洒出。'])
    app.dependency_overrides[get_text_provider] = lambda: provider
    try:
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            response = await client.post('/api/text/expand', json={
                'kind': 'scene',
                'text': '森林小屋',
            })
        assert response.status_code == 200
        assert response.headers['content-type'].startswith('text/event-stream')
        assert 'event: content_delta' in response.text
        assert '暖光从窗户洒出。' in response.text
        assert 'event: message_complete' in response.text
        assert provider.messages[0]['role'] == 'system'
        assert '画面描述' in provider.messages[0]['content']
        assert '禁止自我介绍' in provider.messages[0]['content']
        assert '禁止提及 Agnes' in provider.messages[0]['content']
        assert provider.messages[1] == {
            'role': 'user',
            'content': '严格遵守输出规则，只返回扩写后的正文，不要附带任何其他内容。\n待扩写内容：\n森林小屋',
        }
    finally:
        app.dependency_overrides.clear()


@pytest.mark.asyncio
async def test_expand_route_rejects_missing_agnes_configuration():
    app.dependency_overrides[get_text_provider] = lambda: MockTextProvider(configured=False)
    try:
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            response = await client.post('/api/text/expand', json={'kind': 'style', 'text': '木刻'})
        assert response.status_code == 503
        assert response.json()['detail']['code'] == 'AGNES_NOT_CONFIGURED'
    finally:
        app.dependency_overrides.clear()


@pytest.mark.asyncio
async def test_agnes_provider_uses_expected_model_and_parses_content_only():
    captured = {}

    async def handler(request):
        captured['authorization'] = request.headers.get('authorization')
        captured['payload'] = json.loads(request.content)
        body = (
            'data: {"choices":[{"delta":{"reasoning_content":"思考"}}]}\n\n'
            'data: {"choices":[{"delta":{"content":"扩写结果"}}]}\n\n'
            'data: [DONE]\n\n'
        )
        return httpx.Response(200, text=body, headers={'content-type': 'text/event-stream'})

    async with httpx.AsyncClient(
        transport=httpx.MockTransport(handler),
        base_url='https://api.agnes-ai.cn/v1/',
    ) as client:
        provider = AgnesTextProvider(Settings(agnes_api_key='agnes-test-key'), client)
        chunks = [chunk async for chunk in provider.stream([{'role': 'user', 'content': '测试'}])]

    assert chunks == ['扩写结果']
    assert captured['authorization'] == 'Bearer agnes-test-key'
    assert captured['payload']['model'] == 'agnes-2.5-flash'
    assert captured['payload']['stream'] is True
    assert '\n---' in captured['payload']['stop']
    assert '\n现在是 **Agnes**' in captured['payload']['stop']
