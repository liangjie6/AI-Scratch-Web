import json

import httpx
import pytest

from app.config import Settings
from app.providers.agnes_text_provider import AgnesTextProvider


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ('provider_name', 'expected_endpoint'),
    [('deepseek', 'chat/completions'), ('gpt', 'responses')],
)
async def test_structured_output_uses_provider_json_object_transport(provider_name, expected_endpoint):
    requests = []

    async def handler(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content)
        requests.append((request.url.path, payload))
        if provider_name == 'deepseek':
            body = 'data: ' + json.dumps({'choices': [{'delta': {'content': '{"ok":true}'}}]}) + '\n\ndata: [DONE]\n\n'
        else:
            body = 'data: ' + json.dumps({'type': 'response.output_text.delta', 'delta': '{"ok":true}'}) + '\n\ndata: [DONE]\n\n'
        return httpx.Response(200, headers={'content-type': 'text/event-stream'}, content=body.encode())

    settings = Settings(deepseek_api_key='deepseek-key', gpt_api_key='gpt-key')
    async with httpx.AsyncClient(transport=httpx.MockTransport(handler), base_url='https://provider.test') as client:
        provider = AgnesTextProvider(settings, http_client=client, provider=provider_name)
        deltas = [
            delta async for delta in provider.stream_with_reasoning(
                [{'role': 'system', 'content': 'return json'}],
                thinking_enabled=False,
                thinking_strength='low',
                json_output=True,
            )
        ]

    assert requests[0][0].endswith('/' + expected_endpoint)
    payload = requests[0][1]
    if provider_name == 'deepseek':
        assert payload['response_format'] == {'type': 'json_object'}
        assert payload['messages'][0]['content'] == 'return json'
    else:
        assert payload['text'] == {'format': {'type': 'json_object'}}
        assert payload['input'][0]['content'] == 'return json'
    assert [delta.text for delta in deltas if delta.kind == 'content'] == ['{"ok":true}']


@pytest.mark.asyncio
async def test_agnes_does_not_receive_structured_output_flag():
    requests = []

    async def handler(request: httpx.Request) -> httpx.Response:
        requests.append(json.loads(request.content))
        body = 'data: ' + json.dumps({'choices': [{'delta': {'content': 'plain'}}]}) + '\n\ndata: [DONE]\n\n'
        return httpx.Response(200, headers={'content-type': 'text/event-stream'}, content=body.encode())

    settings = Settings(agnes_api_key='agnes-key')
    async with httpx.AsyncClient(transport=httpx.MockTransport(handler), base_url='https://provider.test') as client:
        provider = AgnesTextProvider(settings, http_client=client, provider='agnes')
        deltas = [
            delta async for delta in provider.stream_with_reasoning(
                [{'role': 'system', 'content': 'tagged'}],
                thinking_enabled=False,
                thinking_strength='low',
                json_output=False,
            )
        ]

    assert 'response_format' not in requests[0]
    assert [delta.text for delta in deltas if delta.kind == 'content'] == ['plain']


@pytest.mark.asyncio
async def test_gpt_structured_output_forwards_json_schema():
    requests = []

    async def handler(request: httpx.Request) -> httpx.Response:
        requests.append(json.loads(request.content))
        body = 'data: ' + json.dumps({'type': 'response.output_text.delta', 'delta': '{"ok":true}'}) + '\n\ndata: [DONE]\n\n'
        return httpx.Response(200, headers={'content-type': 'text/event-stream'}, content=body.encode())

    schema = {'type': 'object', 'additionalProperties': False, 'properties': {'ok': {'type': 'boolean'}}, 'required': ['ok']}
    settings = Settings(gpt_api_key='gpt-key')
    async with httpx.AsyncClient(transport=httpx.MockTransport(handler), base_url='https://provider.test') as client:
        provider = AgnesTextProvider(settings, http_client=client, provider='gpt')
        [
            delta async for delta in provider.stream_with_reasoning(
                [{'role': 'system', 'content': 'return schema'}],
                thinking_enabled=False,
                thinking_strength='low',
                json_output=True,
                json_schema=schema,
            )
        ]

    assert requests[0]['text']['format'] == {
        'type': 'json_schema',
        'name': 'ai_blocks_segment',
        'strict': True,
        'schema': schema,
    }


def test_gpt_json_schema_only_uses_chatgpt_safe_segments():
    provider = AgnesTextProvider(Settings(gpt_api_key='gpt-key'), provider='gpt')

    assert provider.supports_json_schema('requirements')
    assert provider.supports_json_schema('plan')
    assert not provider.supports_json_schema('manifest')
    assert not provider.supports_json_schema('resource_op')
    assert not provider.supports_json_schema('program')


@pytest.mark.asyncio
async def test_responses_parser_stops_at_response_completed_without_waiting_for_eof():
    async def lines():
        yield 'data: ' + json.dumps({
            'type': 'response.output_text.delta',
            'delta': '{"ready":true}',
        })
        yield 'data: ' + json.dumps({
            'type': 'response.completed',
            'response': {'usage': {'input_tokens': 3, 'output_tokens': 2}},
        })
        raise AssertionError('parser waited for a keep-alive stream to close')

    deltas = [delta async for delta in AgnesTextProvider._parse_responses_lines(lines())]

    assert [delta.text for delta in deltas if delta.kind == 'content'] == ['{"ready":true}']
    assert any(delta.kind == 'usage' for delta in deltas)
