import json
import logging

import httpx
import pytest
from pydantic import ValidationError

from app.main import app
from app.orchestration.cache_manager import PromptCache
from app.orchestration.orchestrator import GenerationOrchestrator
from app.orchestration.streaming_parser import StreamProtocolError, UnifiedFrameParser
from app.prompt_builder import PromptBuilder
from app.providers.agnes_text_provider import AgnesStreamDelta
from app.routes.ai_blocks import get_ai_blocks_orchestrator, get_ai_blocks_provider
from app.schemas.ai_blocks import RequirementsResponse, UnifiedGenerationRequest, ValidationRequest


class MockProvider:
    provider = 'mock'

    def __init__(self, output: str | list[str] = '', configured: bool = True):
        self.outputs = [output] if isinstance(output, str) else list(output)
        self.configured = configured
        self.calls = []

    def ensure_configured(self):
        if not self.configured:
            raise RuntimeError('missing provider key')

    async def stream_with_reasoning(self, messages, *, thinking_enabled, thinking_strength, json_output):
        assert json_output is False
        self.calls.append(messages)
        output = self.outputs[0] if len(self.outputs) == 1 else self.outputs.pop(0)
        for chunk in (output[index:index + 7] for index in range(0, len(output), 7)):
            yield AgnesStreamDelta(kind='content', text=chunk)


def target(mode='append', is_stage=False):
    return {
        'name': 'Stage' if is_stage else 'Sprite1',
        'is_stage': is_stage,
        'has_scripts': False,
        'mode': mode,
        'costumes': ['costume1'],
        'sounds': [],
        'sprite_names': ['Sprite1'],
        'current_program': '',
    }


def script(script_id='script_new_1'):
    return {
        'id': script_id,
        'purpose': 'Move',
        'x': 20,
        'y': 20,
        'blocks': [
            {'opcode': 'event_whenflagclicked', 'fields': {}, 'inputs': {}, 'substacks': {}},
            {'opcode': 'motion_movesteps', 'fields': {}, 'inputs': {'STEPS': {'kind': 'number', 'value': 10}}, 'substacks': {}},
        ],
    }


def stream_output():
    requirements = {
        'ready': True,
        'title': 'Move sprite',
        'goal': 'Move after green flag',
        'behavior_steps': ['Move ten steps'],
        'triggers': ['Green flag'],
        'controls': [],
        'success_criteria': ['Sprite moves'],
        'missing_questions': [],
    }
    plan = {'steps': ['Listen for the green flag', 'Move ten steps']}
    metadata = {'schema_version': 1, 'summary': 'Move when green flag is clicked', 'remove_script_ids': []}
    state = {'variables': [], 'lists': [], 'broadcasts': []}
    return ''.join([
        '<requirements>', json.dumps(requirements, ensure_ascii=False), '</requirements>',
        '<plan>', json.dumps(plan, ensure_ascii=False), '</plan>',
        '<program target_index="0"><metadata>', json.dumps(metadata, ensure_ascii=False), '</metadata><shared_state>',
        json.dumps(state, ensure_ascii=False), '</shared_state><script index="0" total="1">',
        json.dumps(script(), ensure_ascii=False), '</script></program>',
    ])


def project_request_payload():
    stage = target(is_stage=True)
    stage.update(target_id='stage-1', revision=2, has_scripts=True)
    sprite = target()
    sprite.update(target_id='sprite-1', revision=3)
    return request_payload(
        scope='project',
        project_context={'project_id': 'project-1', 'version': 4, 'targets': [stage, sprite], 'shared_state': {}},
    )


def project_stream_output():
    requirements = {'ready': True, 'title': 'Project', 'goal': 'Update two targets', 'behavior_steps': ['React'], 'triggers': ['Green flag'], 'controls': [], 'success_criteria': ['Both targets updated'], 'missing_questions': []}
    plan = {'steps': ['Update stage', 'Update sprite']}
    manifest = {
        'schema_version': 1, 'scope': 'project', 'summary': 'Update project', 'resource_ops': [],
        'program_targets': [
            {'target': {'kind': 'stage'}, 'mode': 'append', 'intent': 'Update stage'},
            {'target': {'kind': 'existing_sprite', 'name': 'Sprite1'}, 'mode': 'append', 'intent': 'Update sprite'},
        ],
        'shared_state': {'variables': [], 'lists': [], 'broadcasts': []},
    }
    metadata = {'schema_version': 1, 'summary': 'Update target', 'remove_script_ids': []}
    state = {'variables': [], 'lists': [], 'broadcasts': []}

    def program(index, script_id):
        value = script(script_id)
        if index == 0:
            value['blocks'] = value['blocks'][:1]
        return ''.join([
            f'<program target_index="{index}"><metadata>', json.dumps(metadata), '</metadata><shared_state>',
            json.dumps(state), f'</shared_state><script index="0" total="1">',
            json.dumps(value), '</script></program>',
        ])

    return ''.join([
        '<requirements>', json.dumps(requirements), '</requirements>',
        '<plan>', json.dumps(plan), '</plan>',
        '<manifest>', json.dumps(manifest), '</manifest>',
        program(0, 'script_stage'), program(1, 'script_sprite'),
    ])


class FailingProvider(MockProvider):
    async def stream_with_reasoning(self, messages, *, thinking_enabled, thinking_strength, json_output):
        self.calls.append(messages)
        raise RuntimeError('provider secret should not reach client')
        if False:
            yield AgnesStreamDelta(kind='content', text='')


def valid_manifest_payload():
    return {
        'schema_version': 1,
        'scope': 'project',
        'summary': 'Create a sprite',
        'resource_ops': [],
        'program_targets': [],
        'shared_state': {'variables': [], 'lists': [], 'broadcasts': []},
    }


def request_payload(**overrides):
    value = {
        'session_id': 'session-1',
        'request_id': 'request-1',
        'messages': [{'role': 'user', 'content': '让角色走十步'}],
        'target': target(),
        'thinking': {'enabled': False, 'strength': 'low'},
        'scope': 'single_sprite',
        'asset_policy': 'builtin_first',
        'proceed_with_defaults': True,
    }
    value.update(overrides)
    return value


@pytest.mark.asyncio
async def test_orchestrator_uses_one_stream_and_emits_ordered_events(caplog):
    caplog.set_level(logging.INFO, logger='app.orchestration.orchestrator')
    provider = MockProvider(stream_output())
    request = UnifiedGenerationRequest.model_validate(request_payload())
    orchestrator = GenerationOrchestrator(PromptBuilder(knowledge_base='fixed knowledge'))

    events = [event async for event in orchestrator.generate_unified(request, provider)]

    assert len(provider.calls) == 1
    event_types = [event.type for event in events if event.type != 'content_delta']
    assert event_types == [
        'cache_hit', 'phase_start', 'phase_complete', 'phase_start', 'phase_complete',
        'phase_start', 'metadata', 'shared_state', 'script_ready', 'phase_complete', 'complete',
    ]
    assert events[-1].type == 'complete'
    assert events[-1].data['program_count'] == 1
    assert any('统一生成 需求完成: title=Move sprite' in record.getMessage() for record in caplog.records)
    assert not any(record.getMessage().startswith('<requir') for record in caplog.records)
    assert events[-3].data['script_count'] == 1


@pytest.mark.asyncio
async def test_orchestrator_rejects_plain_json_without_legacy_fallback():
    provider = MockProvider(json.dumps({'schema_version': 1}))
    request = UnifiedGenerationRequest.model_validate(request_payload())
    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]
    assert events[-1].type == 'error'
    assert events[-1].data['code'] == 'OUTPUT_PROTOCOL_ERROR'


def test_prompt_cache_expires_and_prompt_layers_hit():
    now = [0.0]
    cache = PromptCache(clock=lambda: now[0])
    builder = PromptBuilder(cache=cache, knowledge_base='fixed knowledge')
    request = UnifiedGenerationRequest.model_validate(request_payload())
    first, first_meta = builder.build_unified_prompt(None, request.target.model_dump(), request)
    second, second_meta = builder.build_unified_prompt(None, request.target.model_dump(), request)
    assert first[0]['content'] == second[0]['content']
    assert first_meta.estimated_hit_rate == 0
    assert second_meta.estimated_hit_rate == 1
    now[0] = 301
    _, expired_meta = builder.build_unified_prompt(None, request.target.model_dump(), request)
    assert expired_meta.estimated_hit_rate == 0


def test_unified_prompt_places_one_transport_contract_after_context():
    builder = PromptBuilder(knowledge_base='fixed knowledge')
    request = UnifiedGenerationRequest.model_validate(request_payload())
    messages, _ = builder.build_unified_prompt(None, request.target.model_dump(), request)
    content = messages[0]['content']

    assert content.rfind('FINAL OUTPUT CONTRACT') > content.rfind('[REQUEST_CONTEXT]')
    assert 'tagged stream, not one JSON document' in content
    assert 'Ignore any earlier sentence that says to output a single JSON object' in content
    assert 'requirements, plan, then manifest only for project scope' in content
    assert 'Never output next, parent, shadow, topLevel' in content


def test_prompt_cache_key_changes_when_context_content_changes():
    cache = PromptCache()
    builder = PromptBuilder(cache=cache, knowledge_base='fixed knowledge')
    request = UnifiedGenerationRequest.model_validate(request_payload())
    first, first_meta = builder.build_unified_prompt(None, request.target.model_dump(), request)
    changed_target = request.target.model_copy(update={'name': '另一角色'})
    second, second_meta = builder.build_unified_prompt(None, changed_target.model_dump(), request)
    assert first[0]['content'] != second[0]['content']
    assert first_meta.hits['target'] is False
    assert second_meta.hits['target'] is False


def test_requirements_missing_questions_defaults_to_empty():
    value = RequirementsResponse.model_validate({
        'ready': True,
        'title': '标题',
        'goal': '目标',
        'behavior_steps': ['步骤一'],
        'triggers': ['点击绿旗'],
        'controls': [],
        'success_criteria': ['完成'],
    })
    assert value.missing_questions == []


def test_tagged_parser_rejects_script_target_alias():
    parser = UnifiedFrameParser()
    with pytest.raises(StreamProtocolError):
        parser.feed('<program target_index="0"><metadata>{}</metadata><shared_state>{}</shared_state><script target_index="1" index="0" total="1">{}</script>')


def test_validation_request_rejects_program_fields_for_manifest():
    with pytest.raises(ValidationError):
        ValidationRequest.model_validate({
            'kind': 'manifest',
            'payload': valid_manifest_payload(),
            'mode': 'append',
        })


@pytest.mark.asyncio
async def test_chat_route_accepts_only_chat_contract_and_streams_completion(caplog):
    caplog.set_level(logging.INFO, logger='app.routes.ai_blocks')
    provider = MockProvider('你好')
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    try:
        chat_payload = request_payload()
        for key in ('scope', 'asset_policy', 'proceed_with_defaults'):
            chat_payload.pop(key)
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            response = await client.post('/api/ai-blocks/chat', json=chat_payload)
            rejected = await client.post('/api/ai-blocks/chat', json=request_payload())
        assert response.status_code == 200
        assert 'event: assistant_delta' in response.text
        assert 'event: message_complete' in response.text
        assert rejected.status_code == 422
        assert len(provider.calls) == 1
        assert 'Sprite1' in provider.calls[0][0]['content']
        assert any(record.getMessage() == 'ai_chat 完成 request_id=request-1 内容块=1' for record in caplog.records)
        assert not any(record.getMessage() == '你好' for record in caplog.records)
    finally:
        app.dependency_overrides.clear()


@pytest.mark.asyncio
async def test_ai_provider_configuration_failure_is_reported_before_streaming():
    provider = MockProvider(stream_output(), configured=False)
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    try:
        chat_payload = request_payload()
        for key in ('scope', 'asset_policy', 'proceed_with_defaults'):
            chat_payload.pop(key)
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            response = await client.post('/api/ai-blocks/chat', json=chat_payload)
        assert response.status_code == 503
        assert response.json()['detail']['code'] == 'AI_PROVIDER_NOT_CONFIGURED'
        assert provider.calls == []
        assert 'missing provider key' not in response.text
    finally:
        app.dependency_overrides.clear()


@pytest.mark.asyncio
async def test_unified_route_is_only_generation_entrypoint():
    provider = MockProvider(stream_output())
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    app.dependency_overrides[get_ai_blocks_orchestrator] = lambda: GenerationOrchestrator(PromptBuilder(knowledge_base='fixed'))
    try:
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            response = await client.post('/api/ai-blocks/generate-unified', json=request_payload())
            old_requirements = await client.post('/api/ai-blocks/requirements', json=request_payload())
            old_generate = await client.post('/api/ai-blocks/generate', json=request_payload())
        assert response.status_code == 200
        assert 'text/event-stream' in response.headers['content-type']
        assert 'event: complete' in response.text
        assert old_requirements.status_code == 404
        assert old_generate.status_code == 404
        assert len(provider.calls) == 1
    finally:
        app.dependency_overrides.clear()


@pytest.mark.asyncio
async def test_manifest_validation_error_uses_plan_phase():
    bad = project_stream_output().replace('{"kind": "stage"}', '"stage"')
    provider = MockProvider(bad)
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    validation = [event for event in events if event.type == 'validation_error']
    assert validation and validation[0].phase == 'plan'
    assert events[-1].type == 'error'
    assert not any(event.phase == 'manifest' for event in events if event.type == 'validation_error')


def test_unified_prompt_describes_resource_operation_contract():
    builder = PromptBuilder(knowledge_base='fixed knowledge')
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    messages, _ = builder.build_unified_prompt(
        {'project_id': 'project-1', 'version': 4, 'targets': [request.project_context.targets[0].model_dump()], 'shared_state': {}},
        request.target.model_dump(), request,
    )
    content = messages[0]['content']
    contract = content[content.rfind('FINAL OUTPUT CONTRACT'):]
    assert 'add_builtin_sprite (op, op_id, name, x' in contract
    assert 'add_sprite (op, op_id, name, x' in contract
    assert 'delete_sprite (only op, op_id, name, target' in contract
    assert 'add_costume (op, op_id, target current_target/existing_sprite/created_sprite but never stage' in contract
    assert 'add_backdrop (op, op_id, name' in contract
    assert 'select_backdrop (only op, op_id, name)' in contract
    assert 'sheet_layout":"1x1"' in contract
    assert 'asset_type "costume", never "sprite"' in contract
    assert 'ONLY current_target may use modify or rewrite' in contract
    assert 'stage, existing_sprite and created_sprite entries MUST use exactly "append"' in contract


@pytest.mark.asyncio
async def test_manifest_resource_op_validation_reports_rule_detail(caplog):
    caplog.set_level(logging.WARNING, logger='app.orchestration.orchestrator')
    bad = project_stream_output().replace(
        '"resource_ops": []',
        '"resource_ops": [{"op": "add_sprite", "op_id": "op_ball", "name": "ball", "x": 0, "y": 0, "size": 60, "tags": [], "source": {"kind": "scratch_library", "asset_type": "sprite", "search_terms": ["ball"]}}]',
    )
    provider = MockProvider(bad)
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    validation = [event for event in events if event.type == 'validation_error']
    assert validation and validation[0].phase == 'plan'
    assert validation[0].data['path'] == 'resource_ops.0'
    assert 'costume source cannot be a sprite' in validation[0].data['detail']
    assert events[-1].type == 'error'
    assert events[-1].data['code'] == 'OUTPUT_VALIDATION_ERROR'
    assert any('costume source cannot be a sprite' in record.getMessage() for record in caplog.records)


@pytest.mark.asyncio
async def test_protocol_failure_emits_single_error_event():
    provider = MockProvider(json.dumps({'schema_version': 1}))
    request = UnifiedGenerationRequest.model_validate(request_payload())
    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    assert [event.type for event in events if event.type != 'cache_hit'] == ['error']
    assert events[-1].data['code'] == 'OUTPUT_PROTOCOL_ERROR'
    assert not any(event.type == 'validation_error' for event in events)


@pytest.mark.asyncio
async def test_orchestrator_resolves_each_project_program_from_project_context():
    provider = MockProvider(project_stream_output())
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    assert len(provider.calls) == 1
    assert events[-1].type == 'complete', [(event.type, event.phase, event.data) for event in events]
    assert events[-1].data['program_count'] == 2
    completed = [event for event in events if event.type == 'phase_complete' and event.phase == 'program']
    assert [event.data['target_index'] for event in completed] == [0, 1]
    assert 'stage-1' in provider.calls[0][0]['content']


def test_tagged_parser_rejects_malformed_program_name():
    parser = UnifiedFrameParser()
    with pytest.raises(StreamProtocolError):
        parser.feed('<programX target_index="0">')


@pytest.mark.asyncio
async def test_validate_route_returns_structured_contract_paths():
    invalid = script()
    invalid['blocks'][0]['opcode'] = 'motion_movesteps'
    transport = httpx.ASGITransport(app=app)
    async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
        response = await client.post('/api/ai-blocks/validate', json={
            'kind': 'program', 'payload': {'schema_version': 1, 'summary': 'bad', 'remove_script_ids': [], 'variables': [], 'lists': [], 'broadcasts': [], 'scripts': [invalid]},
            'target_kind': 'sprite', 'mode': 'append',
        })
    assert response.status_code == 200
    assert response.json()['valid'] is False
    assert response.json()['issues'][0]['path'] == '$.scripts[0].blocks[0]'


@pytest.mark.asyncio
async def test_unified_route_emits_one_terminal_error_without_complete():
    provider = FailingProvider('')
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    try:
        transport = httpx.ASGITransport(app=app)
        async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
            response = await client.post('/api/ai-blocks/generate-unified', json=request_payload())
        assert response.status_code == 200
        assert response.text.count('event: error') == 1
        assert 'event: complete' not in response.text
        assert 'provider secret should not reach client' not in response.text
    finally:
        app.dependency_overrides.clear()


def bad_mode_manifest_output():
    bad = project_stream_output().replace(
        '{"kind": "stage"}, "mode": "append"',
        '{"kind": "stage"}, "mode": "modify"',
    )
    assert '{"kind": "stage"}, "mode": "modify"' in bad
    return bad


@pytest.mark.asyncio
async def test_orchestrator_repairs_manifest_mode_violation():
    provider = MockProvider([bad_mode_manifest_output(), project_stream_output()])
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    orchestrator = GenerationOrchestrator(PromptBuilder(knowledge_base='fixed'), max_repair_rounds=1)

    events = [event async for event in orchestrator.generate_unified(request, provider)]

    assert len(provider.calls) == 2
    repair = [event for event in events if event.type == 'repair']
    assert repair and repair[0].data['attempt'] == 1
    assert repair[0].data['resume'] == 'manifest'
    assert 'must use append' in repair[0].data['detail']
    assert events[-1].type == 'complete'
    assert events[-1].data['program_count'] == 2
    assert not any(event.type == 'validation_error' for event in events)
    repair_messages = provider.calls[1]
    assert any(item['role'] == 'assistant' and '<manifest>' in item['content'] for item in repair_messages)
    assert any(item['role'] == 'user' and 'must use append' in item['content'] and '从 <manifest> 标签开始' in item['content'] for item in repair_messages)


@pytest.mark.asyncio
async def test_orchestrator_repairs_program_validation_error():
    bad = project_stream_output().replace('opcode": "event_whenflagclicked"', 'opcode": "motion_movesteps"', 1)
    provider = MockProvider([bad, project_stream_output()])
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    orchestrator = GenerationOrchestrator(PromptBuilder(knowledge_base='fixed'), max_repair_rounds=1)

    events = [event async for event in orchestrator.generate_unified(request, provider)]

    repair = [event for event in events if event.type == 'repair']
    assert repair and repair[0].data['resume'] == 'program'
    assert events[-1].type == 'complete'
    assert events[-1].data['program_count'] == 2


@pytest.mark.asyncio
async def test_orchestrator_repair_exhaustion_yields_one_terminal_error():
    provider = MockProvider(bad_mode_manifest_output())
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    orchestrator = GenerationOrchestrator(PromptBuilder(knowledge_base='fixed'), max_repair_rounds=1)

    events = [event async for event in orchestrator.generate_unified(request, provider)]

    assert len(provider.calls) == 2
    repairs = [event for event in events if event.type == 'repair']
    assert len(repairs) == 1
    validation = [event for event in events if event.type == 'validation_error']
    assert len(validation) == 1
    assert events[-1].type == 'error'
    assert events[-1].data['code'] == 'OUTPUT_VALIDATION_ERROR'
    assert events[-1].data['message'] == 'AI 输出未通过协议校验'


@pytest.mark.asyncio
async def test_orchestrator_repairs_second_round_when_first_repair_fails():
    provider = MockProvider([bad_mode_manifest_output(), bad_mode_manifest_output(), project_stream_output()])
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    orchestrator = GenerationOrchestrator(PromptBuilder(knowledge_base='fixed'), max_repair_rounds=2)

    events = [event async for event in orchestrator.generate_unified(request, provider)]

    assert len(provider.calls) == 3
    repairs = [event for event in events if event.type == 'repair']
    assert [item.data['attempt'] for item in repairs] == [1, 2]
    assert events[-1].type == 'complete'
