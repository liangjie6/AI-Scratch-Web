import pytest

from app.providers.agnes_text_provider import AgnesTextProvider


async def stream_lines(*items: str):
    for item in items:
        yield item


@pytest.mark.asyncio
async def test_openai_usage_normalizes_cached_prompt_tokens():
    lines = stream_lines(
        'data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":25,"total_tokens":125,"prompt_tokens_details":{"cached_tokens":60}}}',
        'data: [DONE]',
    )

    deltas = [delta async for delta in AgnesTextProvider._parse_openai_lines(lines)]

    assert deltas[0].usage == {
        'input_tokens': 100,
        'output_tokens': 25,
        'total_tokens': 125,
        'cached_input_tokens': 60,
    }


@pytest.mark.asyncio
async def test_anthropic_usage_normalizes_cache_read_tokens():
    lines = stream_lines(
        'data: {"type":"message_start","message":{"usage":{"input_tokens":90,"cache_read_input_tokens":45}}}',
        'data: {"type":"message_delta","usage":{"output_tokens":30}}',
    )

    deltas = [delta async for delta in AgnesTextProvider._parse_anthropic_lines(lines)]

    assert deltas[0].usage == {
        'input_tokens': 90,
        'output_tokens': 0,
        'total_tokens': 90,
        'cached_input_tokens': 45,
    }
    assert deltas[1].usage == {'input_tokens': 0, 'output_tokens': 30, 'total_tokens': 30}
