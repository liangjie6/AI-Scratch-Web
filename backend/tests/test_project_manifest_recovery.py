import asyncio
import json
import pytest
from pydantic import ValidationError

from app.config import Settings
from app.providers.agnes_text_provider import AgnesTextProvider, AgnesStreamDelta
from app.schemas.ai_blocks import ProjectManifest, ProjectSharedState, normalize_project_manifest_input


def builtin_manifest():
    return {
        'schema_version': 1, 'scope': 'project', 'summary': 'project',
        'resource_ops': [{
            'op': 'add_builtin_sprite', 'op_id': 'cat_1', 'name': 'Cat',
            'source': {'kind': 'scratch_library', 'asset_type': 'sprite', 'search_terms': ['cat']},
            'x': 0, 'y': 0, 'size': 100,
        }],
        'program_targets': [{
            'target': {'kind': 'created_sprite', 'op_id': 'cat_1'},
            'mode': 'append', 'intent': 'move',
        }],
        'shared_state': {'variables': [], 'lists': [], 'broadcasts': []},
    }


def test_manifest_normalizer_removes_only_legacy_target_aliases():
    value = builtin_manifest()
    value['resource_ops'][0]['target'] = {'kind': 'stage', 'name': '舞台'}
    value['program_targets'][0]['target'] = {'kind': 'stage', 'name': '舞台', 'op_id': 'ignored'}
    normalized = normalize_project_manifest_input(value)
    assert 'target' not in normalized['resource_ops'][0]
    assert normalized['program_targets'][0]['target'] == {'kind': 'stage'}
    assert value['resource_ops'][0]['target']['name'] == '舞台'


def test_manifest_normalizer_repairs_costume_source_and_boolean():
    value = {'resource_ops': [{'op': 'add_costume', 'source': {'kind': 'scratch_library', 'asset_type': 'sprite'}, 'select_after': 'true'}]}
    normalized = normalize_project_manifest_input(value)
    assert normalized['resource_ops'][0]['source']['asset_type'] == 'costume'
    assert normalized['resource_ops'][0]['select_after'] is True


def test_manifest_accepts_delete_existing_sprite_operation():
    value = builtin_manifest()
    value['resource_ops'] = [{
        'op': 'delete_sprite', 'op_id': 'delete_ball', 'name': 'Ball',
        'target': {'kind': 'existing_sprite', 'name': 'Ball'},
    }]
    value['program_targets'] = []
    assert ProjectManifest.model_validate(value).resource_ops[0].op == 'delete_sprite'


def test_manifest_accepts_builtin_created_sprite_reference():
    assert ProjectManifest.model_validate(builtin_manifest()).program_targets[0].target.op_id == 'cat_1'


def test_manifest_rejects_forward_created_sprite_reference():
    value = builtin_manifest()
    value['program_targets'][0]['target']['op_id'] = 'missing'
    with pytest.raises(ValidationError, match='created_sprite'):
        ProjectManifest.model_validate(value)
def test_shared_state_requires_global_unique_names():
    value = {'variables': [{'name': 'score', 'scope': 'local'}], 'lists': [], 'broadcasts': []}
    with pytest.raises(ValidationError, match='global'):
        ProjectSharedState.model_validate(value)


def test_manifest_accepts_mixed_nested_costume_sources():
    value = builtin_manifest()
    value['resource_ops'] = [{
        'op': 'add_sprite', 'op_id': 'hero_1', 'name': 'Hero', 'x': 0, 'y': 0, 'size': 100,
        'source': {'kind': 'image_generation', 'reason': 'custom', 'prompt': 'hero', 'style': 'cartoon', 'width': 512, 'height': 512, 'sheet_layout': '1x1'},
        'costumes': [
            {'name': 'local', 'source': {'kind': 'scratch_library', 'asset_type': 'costume', 'search_terms': ['hat']}, 'tags': []},
            {'name': 'generated', 'source': {'kind': 'image_generation', 'reason': 'custom', 'prompt': 'hero', 'style': 'cartoon', 'width': 512, 'height': 512, 'sheet_layout': '1x1'}, 'tags': []},
        ],
    }]
    value['program_targets'][0]['target']['op_id'] = 'hero_1'
    assert ProjectManifest.model_validate(value).resource_ops[0].costumes[1].name == 'generated'


def test_manifest_rejects_unknown_nested_costume_fields():
    value = builtin_manifest()
    value['resource_ops'] = [{
        'op': 'add_sprite', 'op_id': 'hero_1', 'name': 'Hero', 'x': 0, 'y': 0, 'size': 100,
        'source': {'kind': 'image_generation', 'reason': 'custom', 'prompt': 'hero', 'style': 'cartoon', 'width': 512, 'height': 512, 'sheet_layout': '1x1'},
        'costumes': [{'name': 'local', 'source': {'kind': 'scratch_library', 'asset_type': 'costume', 'search_terms': ['hat']}, 'tags': [], 'unexpected': True}],
    }]
    with pytest.raises(ValidationError, match='unexpected'):
        ProjectManifest.model_validate(value)


class FakeResponse:
    def __init__(self, lines):
        self.status_code = 200
        self._lines = lines

    async def aiter_lines(self):
        for line in self._lines:
            yield line


class FakeStreamContext:
    def __init__(self, response):
        self.response = response

    async def __aenter__(self):
        return self.response

    async def __aexit__(self, exc_type, exc, traceback):
        return False


class FakeStreamClient:
    def __init__(self, lines):
        self.lines = lines
        self.calls = []

    def stream(self, method, endpoint, *, json, headers):
        self.calls.append({'method': method, 'endpoint': endpoint, 'json': json, 'headers': headers})
        return FakeStreamContext(FakeResponse(self.lines))


@pytest.mark.asyncio
async def test_provider_builds_gpt_responses_json_payload():
    client = FakeStreamClient([
        'data: {"type":"response.output_text.delta","delta":"{\\"ok\\":true}"}',
        'data: {"type":"response.completed"}',
    ])
    provider = AgnesTextProvider(Settings(gpt_api_key='gpt-key'), client).for_tier('super')
    items = [item async for item in provider.stream_with_reasoning(
        [{'role': 'user', 'content': 'test'}], thinking_enabled=True, thinking_strength='xhigh', json_output=True,
    )]
    request = client.calls[0]
    assert request['endpoint'] == 'responses'
    assert request['json']['model'] == 'gpt-5.6-sol'
    assert request['json']['reasoning'] == {'effort': 'xhigh', 'summary': 'auto'}
    assert request['json']['text'] == {'format': {'type': 'json_object'}}
    assert [(item.kind, item.text) for item in items] == [('content', '{"ok":true}')]


@pytest.mark.asyncio
async def test_provider_builds_deepseek_chat_json_payload():
    client = FakeStreamClient([
        'data: {"choices":[{"delta":{"content":"{\\"ok\\":true}"}}]}',
        'data: [DONE]',
    ])
    provider = AgnesTextProvider(Settings(deepseek_api_key='deepseek-key'), client).for_tier('advanced')
    items = [item async for item in provider.stream_with_reasoning(
        [{'role': 'user', 'content': 'test'}], thinking_enabled=True, thinking_strength='high', json_output=True,
    )]
    request = client.calls[0]
    assert request['endpoint'] == 'chat/completions'
    assert request['json']['model'] == 'deepseek-v4-flash'
    assert request['json']['reasoning_effort'] == 'high'
    assert request['json']['response_format'] == {'type': 'json_object'}
    assert [(item.kind, item.text) for item in items] == [('content', '{"ok":true}')]


def test_responses_parser_emits_reasoning_content_and_usage():
    async def lines():
        for line in [
            'event: response.reasoning_summary_text.delta',
            'data: {"type":"response.reasoning_summary_text.delta","delta":"thinking"}',
            'data: {"type":"response.output_text.delta","delta":"ok"}',
            'data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}',
        ]:
            yield line
    async def collect():
        return [item async for item in AgnesTextProvider._parse_responses_lines(lines())]
    items = asyncio.run(collect())
    assert [(item.kind, item.text) for item in items[:2]] == [('reasoning', 'thinking'), ('content', 'ok')]
    assert items[-1].usage == {'input_tokens': 10, 'output_tokens': 2, 'total_tokens': 12}

