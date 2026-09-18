import asyncio
import json
import logging
import re

import httpx
import pytest
from pydantic import TypeAdapter, ValidationError

from app.main import app
from app.orchestration.cache_manager import PromptCache
from app.orchestration.error_messages import repair_hint
from app.orchestration.orchestrator import GenerationOrchestrator, _formatted_model_output
from app.orchestration.streaming_parser import StreamProtocolError, UnifiedFrameParser
from app.prompt_builder import PromptBuilder
from app.providers.agnes_text_provider import AgnesStreamDelta
from app.routes.ai_blocks import get_ai_blocks_orchestrator, get_ai_blocks_provider
from app.schemas.ai_blocks import (
    ProgramIR,
    ProjectSharedState,
    RequirementsResponse,
    ResourceContractError,
    ResourceOp,
    UnifiedGenerationRequest,
    ValidationRequest,
    canonical_contract_json,
    normalize_resource_op,
    validate_project_program_shared_state,
)


def test_canonical_contract_json_matches_browser_integral_number_spelling():
    assert canonical_contract_json({'confidence': 1.0, 'nested': [2.0, 2.5]}) == '{"confidence":1,"nested":[2,2.5]}'


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


class UsageProvider(MockProvider):
    async def stream_with_reasoning(self, *args, **kwargs):
        yield AgnesStreamDelta(kind='usage', usage={
            'input_tokens': 120,
            'output_tokens': 40,
            'total_tokens': 160,
            'cached_input_tokens': 72,
        })
        async for delta in super().stream_with_reasoning(*args, **kwargs):
            yield delta


class RoutedSegmentProvider(MockProvider):
    def __init__(self, routes, delays=None):
        super().__init__('')
        self.routes = {key: list(values) for key, values in routes.items()}
        self.delays = delays or {}

    @staticmethod
    def _key_for(text, call_count):
        if call_count == 1:
            return 'requirements'
        if call_count == 2:
            return 'plan'
        if call_count == 3 and 'resource_op_index=' not in text:
            return 'manifest'
        if 'resource_op_index=' in text:
            return 'resource_op'
        if re.search(r'target_index=0 的完整', text):
            return 'program:0'
        if re.search(r'target_index=1 的完整', text):
            return 'program:1'
        return 'manifest'

    async def stream_with_reasoning(self, messages, *, thinking_enabled, thinking_strength, json_output):
        assert json_output is False
        self.calls.append(messages)
        key = self._key_for(messages[-1]['content'], len(self.calls))
        values = self.routes[key]
        output = values[0] if len(values) == 1 else values.pop(0)
        await asyncio.sleep(self.delays.get(key, 0))
        for chunk in (output[index:index + 7] for index in range(0, len(output), 7)):
            yield AgnesStreamDelta(kind='content', text=chunk)


class StructuredSegmentProvider(RoutedSegmentProvider):
    def __init__(self, routes):
        super().__init__(routes)
        self.json_flags = []

    def supports_structured_output(self, segment=None):
        return segment in {'requirements', 'plan', 'manifest', 'resource_op'}

    async def stream_with_reasoning(self, messages, *, thinking_enabled, thinking_strength, json_output):
        self.calls.append(messages)
        text = messages[-1]['content']
        key = self._key_for(text, len(self.calls))
        self.json_flags.append((key, json_output))
        values = self.routes[key]
        output = values[0] if len(values) == 1 else values.pop(0)
        if json_output:
            match = re.search(r'<[^>]+>(.*?)</[^>]+>', output, re.S)
            assert match is not None
            output = match.group(1)
        for chunk in (output[index:index + 7] for index in range(0, len(output), 7)):
            yield AgnesStreamDelta(kind='content', text=chunk)


class StructuredProjectProvider:
    provider = 'gpt'

    def __init__(self, outputs):
        self.outputs = outputs
        self.calls = []
        self.transport_calls = []

    def supports_structured_output(self, segment=None):
        return segment in {'requirements', 'plan', 'manifest', 'resource_op', 'program'}

    def supports_json_schema(self, segment=None):
        return segment in {'requirements', 'plan', 'manifest', 'resource_op', 'program'}

    async def stream_with_reasoning(self, messages, *, thinking_enabled, thinking_strength, json_output, json_schema=None):
        assert json_output is True
        self.calls.append(messages)
        text = messages[-1]['content']
        if len(self.calls) == 1:
            segment = 'requirements'
        elif len(self.calls) == 2:
            segment = 'plan'
        elif len(self.calls) == 3:
            segment = 'manifest'
        else:
            target_index = re.search(r'target_index=(\d+)', text).group(1)
            segment = f'program:{target_index}'
        self.transport_calls.append((segment, json_output, json_schema))
        output = json.dumps(self.outputs[segment])
        for chunk in (output[index:index + 7] for index in range(0, len(output), 7)):
            yield AgnesStreamDelta(kind='content', text=chunk)


def structured_program_payload(tagged):
    metadata = json.loads(re.search(r'<metadata>(.*?)</metadata>', tagged, re.S).group(1))
    state = json.loads(re.search(r'<shared_state>(.*?)</shared_state>', tagged, re.S).group(1))
    scripts = [json.loads(match.group(1)) for match in re.finditer(r'<script index="\d+" total="\d+">(.*?)</script>', tagged, re.S)]
    return {**metadata, **state, 'scripts': scripts}


def routed_project_provider(segments, *, manifest=None, resource_ops=None, program0=None, program1=None, delays=None):
    return RoutedSegmentProvider({
        'requirements': [segments[0]],
        'plan': [segments[1]],
        'manifest': list(manifest or [segments[2]]),
        'resource_op': list(resource_ops or []),
        'program:0': list(program0 or [segments[3]]),
        'program:1': list(program1 or [segments[4]]),
    }, delays=delays)


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
        'schema_version': 2, 'scope': 'project', 'summary': 'Update project', 'resource_ops': [],
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


def project_segment_outputs(output=None):
    value = output or project_stream_output()
    requirements = re.search(r'<requirements>.*?</requirements>', value, re.S).group(0)
    plan = re.search(r'<plan>.*?</plan>', value, re.S).group(0)
    manifest = re.search(r'<manifest>.*?</manifest>', value, re.S).group(0)
    programs = [match.group(0) for match in re.finditer(r'<program target_index="(\d+)">.*?</program>', value, re.S)]
    programs.sort(key=lambda item: int(re.search(r'target_index="(\d+)"', item).group(1)))
    return [requirements, plan, manifest, *programs]


class FailingProvider(MockProvider):
    async def stream_with_reasoning(self, messages, *, thinking_enabled, thinking_strength, json_output):
        self.calls.append(messages)
        raise RuntimeError('provider secret should not reach client')
        if False:
            yield AgnesStreamDelta(kind='content', text='')


def valid_manifest_payload():
    return {
        'schema_version': 2,
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
async def test_orchestrator_forwards_provider_usage_event():
    provider = UsageProvider(stream_output())
    request = UnifiedGenerationRequest.model_validate(request_payload())

    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    usage = next(event for event in events if event.type == 'usage')
    assert usage.data == {
        'input_tokens': 120,
        'output_tokens': 40,
        'total_tokens': 160,
        'cached_input_tokens': 72,
    }


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


def test_json_object_segment_prompt_has_no_tagged_transport_instruction():
    builder = PromptBuilder(knowledge_base='fixed knowledge')
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    messages, _ = builder.build_unified_prompt(
        None,
        request.target.model_dump(),
        request,
        transport='json_object',
        segment='program',
    )
    contract = messages[0]['content'][messages[0]['content'].rfind('FINAL OUTPUT CONTRACT'):]
    assert 'one JSON object, not a tagged stream' in contract
    assert 'Output tags in this order' not in contract
    assert 'complete ProgramIR object' in contract

    segment_messages, _ = builder.build_segment_prompt(
        None,
        request.target.model_dump(),
        request,
        'program',
        {'transport': 'json_object'},
        target_index=0,
    )
    assert '只输出一个 JSON object' in segment_messages[-1]['content']
    assert 'JSON object 之外的文本' in segment_messages[-1]['content']
    assert '<program>' not in segment_messages[-1]['content']


def test_task_spec_segment_prompt_spells_out_assumption_contract():
    builder = PromptBuilder(knowledge_base='fixed knowledge')
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    messages, _ = builder.build_unified_prompt(
        None,
        request.target.model_dump(),
        request,
        transport='json_object',
        segment='task_spec',
    )
    contract = messages[0]['content'][messages[0]['content'].rfind('FINAL OUTPUT CONTRACT'):]

    assert 'title, goal, assumptions, entities, state, behaviors, success_criteria, unsupported' in contract
    assert 'confidence:number 0..1' in contract
    assert 'requires_confirmation:boolean' in contract
    assert 'never high/medium/low' in contract
    assert 'TaskEntity.role: "stage" | "sprite"' in contract
    assert 'TriggerSpec.kind: "green_flag" | "key_pressed"' in contract
    assert 'ActionSpec.kind: "move_steps" | "set_axis"' in contract
    assert 'ProgramIR.schema_version: 1 | 2' in contract
    assert 'CANONICAL_OUTPUT_JSON_SCHEMA' in contract
    assert 'set_axis(axis,value)' in contract
    assert 'change_axis(axis,delta)' in contract
    assert 'loop(mode,actions, plus count for repeat or until for repeat_until)' in contract
    assert 'Never replace an OperandSpec with a bare number/string/boolean' in contract
    schema_text = contract.split('CANONICAL_OUTPUT_JSON_SCHEMA', 1)[1]
    assert '"required":["behavior_id","owner_entity_id","trigger","actions"]' in schema_text
    assert '"required":["criterion_id","description","related_behavior_ids","priority","verification"]' in schema_text


@pytest.mark.parametrize('segment', ['requirements', 'task_spec', 'blueprint', 'plan', 'manifest', 'resource_op', 'program'])
def test_each_segment_prompt_contains_its_canonical_schema(segment):
    builder = PromptBuilder(knowledge_base='fixed knowledge')
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    messages, _ = builder.build_unified_prompt(
        None,
        request.target.model_dump(),
        request,
        transport='json_object',
        segment=segment,
    )

    contract = messages[0]['content'][messages[0]['content'].rfind('FINAL OUTPUT CONTRACT'):]
    assert 'CANONICAL_OUTPUT_JSON_SCHEMA' in contract
    assert '"required"' in contract


def test_model_output_log_formats_json_with_real_line_breaks():
    rendered = _formatted_model_output('{\n  "schema_version": 2,\n  "title": "小猫接红包计分游戏"\n}')

    assert rendered.startswith('{\n  "schema_version": 2,')
    assert '\\n' not in rendered


def test_resource_op_prompt_schema_follows_destructive_feature_flag():
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    disabled, _ = PromptBuilder(knowledge_base='fixed').build_unified_prompt(
        None, request.target.model_dump(), request, transport='json_object', segment='resource_op',
    )
    enabled, _ = PromptBuilder(knowledge_base='fixed', allow_destructive_resource_ops=True).build_unified_prompt(
        None, request.target.model_dump(), request, transport='json_object', segment='resource_op',
    )
    disabled_schema = disabled[0]['content'].split('CANONICAL_OUTPUT_JSON_SCHEMA', 1)[1].split('RESOURCE_OPERATION_CONTRACT', 1)[0]
    enabled_schema = enabled[0]['content'].split('CANONICAL_OUTPUT_JSON_SCHEMA', 1)[1].split('RESOURCE_OPERATION_CONTRACT', 1)[0]

    assert 'DeleteSpriteOp' not in disabled_schema
    assert 'DeleteSoundOp' not in disabled_schema
    assert 'DeleteSpriteOp' in enabled_schema
    assert 'DeleteSoundOp' in enabled_schema


def test_structured_empty_output_repair_requires_non_empty_json_body():
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    messages, _ = PromptBuilder(knowledge_base='fixed').build_segment_prompt(
        None,
        request.target.model_dump(),
        request,
        'blueprint',
        {'transport': 'json_object', 'failure': {'path': '/output', 'code': 'OUTPUT_VALIDATION_ERROR'}},
    )

    assert '正文为空或不是 JSON' in messages[-1]['content']
    assert '不能只输出 reasoning' in messages[-1]['content']


def test_task_spec_assumption_validation_has_specific_repair_hint():
    hint = repair_hint(
        'task_spec',
        '/assumptions/0/confidence',
        '/assumptions/0/confidence: Input should be a valid number',
    )

    assert '0 到 1 的数字' in hint
    assert 'requires_confirmation' in hint


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
    bad_segments = project_segment_outputs(bad)
    provider = routed_project_provider(bad_segments, manifest=[bad_segments[2]], program0=[], program1=[])
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
    assert '| add_builtin_sprite | op, op_id, name, x, y, size, source | tags | target, select_after, costumes |' in contract
    assert '| add_sprite | op, op_id, name, x, y, size, costumes | tags | target, source, select_after |' in contract
    assert '| delete_sprite | op, op_id, name, target | none | source, x, y, size, tags, select_after, costumes |' in contract
    assert '| add_costume | op, op_id, target, name, source | tags, select_after (select_after=False) | x, y, size, costumes |' in contract
    assert '| add_backdrop | op, op_id, name, source | tags, select_after (select_after=False) | target, x, y, size, costumes |' in contract
    assert '| select_backdrop | op, op_id, name | none | none |' in contract
    assert 'RESOURCE_SOURCE_CONTRACT_JSON=' in contract
    assert 'sheet_layout' in contract
    assert 'add_sprite=costume' in contract
    assert 'ONLY current_target may use modify or rewrite' in contract
    assert 'stage, existing_sprite and created_sprite entries MUST use exactly "append"' in contract


def test_knowledge_base_has_one_transport_and_input_protocol():
    knowledge = PromptBuilder()._build_knowledge_base()
    assert 'UNSUPPORTED_SCRATCH_REQUEST' not in knowledge
    assert 'kind":"reporter"' not in knowledge
    assert '不存在独立的 boolean 或 reporter wire kind' in knowledge
    assert '统一生成传输由 system prompt 的 FINAL OUTPUT CONTRACT 选择 tagged stream 或 JSON object' in knowledge


@pytest.mark.asyncio
async def test_manifest_resource_op_validation_reports_rule_detail(caplog):
    caplog.set_level(logging.WARNING, logger='app.orchestration.orchestrator')
    bad = project_stream_output().replace(
        '"resource_ops": []',
        '"resource_ops": [{"op":"add_backdrop","op_id":"backdrop_bad","name":"森林","source":{"kind":"scratch_library","asset_type":"costume","search_terms":["森林"]}}]',
    )
    bad_segments = project_segment_outputs(bad)
    repaired_op = backdrop_resource()
    repaired_op['op_id'] = 'backdrop_bad'
    good_segments = project_segment_outputs()
    resource_output = '<resource_op>' + json.dumps(repaired_op, ensure_ascii=False) + '</resource_op>'
    provider = routed_project_provider(good_segments, manifest=[bad_segments[2]], resource_ops=[resource_output])
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    repair = next(event for event in events if event.type == 'repair')
    assert repair.phase == 'plan'
    assert repair.data['path'] == '/resource_ops/0/source/asset_type'
    assert repair.data['code'] == 'INVALID_BACKDROP_SOURCE'
    assert repair.data['operation'] == 'add_backdrop'
    assert repair.data['expected'].endswith('asset_type=backdrop')
    assert repair.data['resume'] == 'resource_op'
    assert events[-1].type == 'complete'
    assert any('INVALID_BACKDROP_SOURCE' in record.getMessage() for record in caplog.records)


def backdrop_resource(select_after_marker=True):
    value = {
        'op': 'add_backdrop',
        'op_id': 'backdrop_1',
        'name': '森林',
        'source': {'kind': 'scratch_library', 'asset_type': 'backdrop', 'search_terms': ['森林']},
    }
    if select_after_marker:
        value['select_after'] = False
    return value


@pytest.mark.asyncio
async def test_resource_op_repair_has_two_local_rounds():
    bad = project_stream_output().replace(
        '"resource_ops": []',
        '"resource_ops": [{"op":"add_backdrop","op_id":"backdrop_bad","name":"森林","source":{"kind":"scratch_library","asset_type":"costume","search_terms":["森林"]}}]',
    )
    bad_segments = project_segment_outputs(bad)
    good_segments = project_segment_outputs()
    invalid_op = backdrop_resource()
    invalid_op['op_id'] = 'backdrop_bad'
    invalid_op['source']['asset_type'] = 'costume'
    valid_op = backdrop_resource()
    valid_op['op_id'] = 'backdrop_bad'
    provider = routed_project_provider(
        good_segments,
        manifest=[bad_segments[2]],
        resource_ops=[
            '<resource_op>' + json.dumps(invalid_op) + '</resource_op>',
            '<resource_op>' + json.dumps(valid_op) + '</resource_op>',
        ],
    )
    request = UnifiedGenerationRequest.model_validate(project_request_payload())

    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    repairs = [event for event in events if event.type == 'repair']
    assert [event.data['attempt'] for event in repairs] == [1, 2]
    assert all(event.data['repair_scope'] == 'resource_op' for event in repairs)
    assert len(provider.calls) == 7
    assert events[-1].type == 'complete'


def test_add_backdrop_normalizes_compatibility_fields_and_defaults():
    raw = backdrop_resource(select_after_marker=False)
    raw.update({'target': {'kind': 'stage'}, 'x': None, 'y': None, 'size': None})
    normalized, diagnostics = normalize_resource_op(raw, path='/resource_ops/0')
    assert normalized['select_after'] is False
    assert 'target' not in normalized
    assert all(field not in normalized for field in ('x', 'y', 'size'))
    assert [item.code for item in diagnostics] == [
        'DEFAULT_APPLIED', 'REDUNDANT_STAGE_TARGET_REMOVED', 'NULL_FIELD_REMOVED',
        'NULL_FIELD_REMOVED', 'NULL_FIELD_REMOVED',
    ]
    operation = TypeAdapter(ResourceOp).validate_python(raw)
    assert operation.select_after is False
    assert 'target' not in operation.model_dump()


def test_add_costume_defaults_select_after_to_false():
    operation = TypeAdapter(ResourceOp).validate_python({
        'op': 'add_costume',
        'op_id': 'costume_1',
        'target': {'kind': 'current_target'},
        'name': '走路',
        'source': {'kind': 'scratch_library', 'asset_type': 'costume', 'search_terms': ['走路']},
    })
    assert operation.select_after is False


@pytest.mark.parametrize(('field', 'value', 'code'), [
    ('target', {'kind': 'existing_sprite', 'name': '猫'}, 'INVALID_BACKDROP_TARGET'),
    ('x', 10, 'INVALID_BACKDROP_PLACEMENT'),
    ('source', {'kind': 'scratch_library', 'asset_type': 'costume', 'search_terms': ['森林']}, 'INVALID_BACKDROP_SOURCE'),
])
def test_add_backdrop_reports_independent_normalization_errors(field, value, code):
    raw = backdrop_resource()
    raw[field] = value
    with pytest.raises(ResourceContractError) as caught:
        normalize_resource_op(raw, path='/resource_ops/0')
    issue = caught.value.issue.as_dict()
    assert issue['code'] == code
    assert issue['path'].startswith('/resource_ops/0/')
    assert issue['operation'] == 'add_backdrop'
    assert issue['repair_scope'] == 'resource_op'
    assert issue['repair_id'] == 'backdrop_1'


@pytest.mark.asyncio
async def test_manifest_phase_reports_backdrop_normalizations():
    raw = project_stream_output().replace(
        '"resource_ops": []',
        '"resource_ops": [{"op":"add_backdrop","op_id":"backdrop_1","name":"森林","target":{"kind":"stage"},"source":{"kind":"scratch_library","asset_type":"backdrop","search_terms":["森林"]}}]',
    )
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, MockProvider(raw))]
    plan = next(event for event in events if event.type == 'phase_complete' and event.phase == 'plan')
    assert [item['code'] for item in plan.data['normalizations']] == ['DEFAULT_APPLIED', 'REDUNDANT_STAGE_TARGET_REMOVED']
    assert plan.data['manifest']['resource_ops'][0]['select_after'] is False


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
    provider = routed_project_provider(project_segment_outputs(), delays={'program:0': 0.03})
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    assert len(provider.calls) == 5
    assert events[-1].type == 'complete', [(event.type, event.phase, event.data) for event in events]
    assert events[-1].data['program_count'] == 2
    completed = [event for event in events if event.type == 'phase_complete' and event.phase == 'program']
    assert [event.data['target_index'] for event in completed] == [0, 1]
    assert 'stage-1' in provider.calls[0][0]['content']
    assert 'requirements' in provider.calls[1][-1]['content']
    assert 'manifest' in provider.calls[2][-1]['content']


@pytest.mark.asyncio
async def test_structured_project_transport_forwards_schemas_and_decodes_programs():
    segments = project_segment_outputs()
    outputs = {
        'requirements': json.loads(re.search(r'<requirements>(.*?)</requirements>', segments[0], re.S).group(1)),
        'plan': json.loads(re.search(r'<plan>(.*?)</plan>', segments[1], re.S).group(1)),
        'manifest': json.loads(re.search(r'<manifest>(.*?)</manifest>', segments[2], re.S).group(1)),
        'program:0': structured_program_payload(segments[3]),
        'program:1': structured_program_payload(segments[4]),
    }
    provider = StructuredProjectProvider(outputs)
    request = UnifiedGenerationRequest.model_validate(project_request_payload())

    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    assert len(provider.calls) == 5
    assert events[-1].type == 'complete'
    assert events[-1].data['program_count'] == 2
    assert all(json_output is True and schema is not None for _, json_output, schema in provider.transport_calls)
    schemas = {segment: schema for segment, _, schema in provider.transport_calls}
    assert schemas['manifest']['properties']['schema_version']['const'] == 2
    assert 'scripts' in schemas['program:0']['properties']
    assert any(event.type == 'script_ready' and event.data['target_index'] == 1 for event in events)


def test_structured_output_schemas_include_every_property_in_required():
    provider = StructuredProjectProvider({})

    def assert_chatgpt_compatible(value):
        if isinstance(value, dict):
            assert 'oneOf' not in value
            assert 'discriminator' not in value
            for child in value.values():
                assert_chatgpt_compatible(child)
        elif isinstance(value, list):
            for child in value:
                assert_chatgpt_compatible(child)

    def assert_required(value):
        if isinstance(value, dict):
            properties = value.get('properties')
            if isinstance(properties, dict):
                assert set(value.get('required', [])) == set(properties)
            for child in value.values():
                assert_required(child)
        elif isinstance(value, list):
            for child in value:
                assert_required(child)

    for segment in ('requirements', 'plan', 'manifest', 'program'):
        schema = GenerationOrchestrator._structured_output_schema(provider, segment)
        assert schema is not None
        assert_chatgpt_compatible(schema)
        assert_required(schema)

    requirements = GenerationOrchestrator._structured_output_schema(provider, 'requirements')
    assert requirements is not None
    assert 'missing_questions' in requirements['required']

    manifest = GenerationOrchestrator._structured_output_schema(provider, 'manifest')
    assert manifest is not None
    assert 'anyOf' in manifest['$defs']['AddBackdropOp']['properties']['source']

    # ResourceOp is a root union, which Responses structured output does not accept.
    assert GenerationOrchestrator._structured_output_schema(provider, 'resource_op') is None


def one_target_project_segments():
    segments = project_segment_outputs()
    manifest = json.loads(re.search(r'<manifest>(.*?)</manifest>', segments[2], re.S).group(1))
    manifest['program_targets'] = manifest['program_targets'][1:2]
    program = segments[4].replace('target_index="1"', 'target_index="0"')
    return [segments[0], segments[1], '<manifest>' + json.dumps(manifest) + '</manifest>', program]


@pytest.mark.asyncio
async def test_project_flow_runs_four_segments_for_one_target():
    segments = one_target_project_segments()
    provider = RoutedSegmentProvider({
        'requirements': [segments[0]],
        'plan': [segments[1]],
        'manifest': [segments[2]],
        'resource_op': [],
        'program:0': [segments[3]],
        'program:1': [],
    })
    payload = project_request_payload()
    payload['project_context']['targets'] = payload['project_context']['targets'][1:2]
    request = UnifiedGenerationRequest.model_validate(payload)

    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    assert len(provider.calls) == 4
    assert events[-1].type == 'complete'
    assert events[-1].data['program_count'] == 1


@pytest.mark.asyncio
async def test_capable_provider_uses_json_objects_for_root_segments():
    segments = project_segment_outputs()
    provider = StructuredSegmentProvider({
        'requirements': [segments[0]],
        'plan': [segments[1]],
        'manifest': [segments[2]],
        'resource_op': [],
        'program:0': [segments[3]],
        'program:1': [segments[4]],
    })
    request = UnifiedGenerationRequest.model_validate(project_request_payload())

    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    assert provider.json_flags[:3] == [('requirements', True), ('plan', True), ('manifest', True)]
    assert provider.json_flags[3:] == [('program:0', False), ('program:1', False)]
    assert events[-1].type == 'complete'
    assert not any(event.type == 'validation_error' for event in events)


@pytest.mark.asyncio
async def test_manifest_v1_reports_unsupported_schema_version():
    segments = project_segment_outputs()
    segments[2] = segments[2].replace('"schema_version": 2', '"schema_version": 1', 1)
    provider = RoutedSegmentProvider({
        'requirements': [segments[0]],
        'plan': [segments[1]],
        'manifest': [segments[2]],
        'resource_op': [],
        'program:0': [segments[3]],
        'program:1': [segments[4]],
    })
    request = UnifiedGenerationRequest.model_validate(project_request_payload())

    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    failure = next(event for event in events if event.type == 'validation_error')
    assert failure.data['code'] == 'UNSUPPORTED_SCHEMA_VERSION'
    assert failure.data['path'] == '/schema_version'


def test_project_program_shared_state_normalizes_omitted_list_values():
    shared_state = ProjectSharedState.model_validate({'variables': [], 'lists': [{'name': 'items', 'value': [], 'scope': 'global'}], 'broadcasts': []})
    program = ProgramIR.model_validate({'schema_version': 1, 'summary': 'list parity', 'remove_script_ids': [], 'variables': [], 'lists': [{'name': 'items', 'scope': 'global'}], 'broadcasts': [], 'scripts': [script()]})

    assert validate_project_program_shared_state(program, shared_state) is program


@pytest.mark.asyncio
async def test_manifest_missing_schema_version_reports_schema_error():
    segments = project_segment_outputs()
    segments[2] = segments[2].replace('"schema_version": 2,', '', 1)
    provider = RoutedSegmentProvider({
        'requirements': [segments[0]],
        'plan': [segments[1]],
        'manifest': [segments[2]],
        'resource_op': [],
        'program:0': [segments[3]],
        'program:1': [segments[4]],
    })
    request = UnifiedGenerationRequest.model_validate(project_request_payload())

    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    failure = next(event for event in events if event.type == 'validation_error')
    assert failure.data['code'] == 'SCHEMA_VALIDATION_ERROR'
    assert failure.data['path'] == '/schema_version'


@pytest.mark.asyncio
async def test_project_program_global_state_must_match_manifest():
    segments = project_segment_outputs()
    manifest = json.loads(segments[2].split('<manifest>', 1)[1].split('</manifest>', 1)[0])
    manifest['shared_state']['variables'] = [{'name': 'score', 'value': 0, 'scope': 'global'}]
    segments[2] = segments[2].split('<manifest>', 1)[0] + '<manifest>' + json.dumps(manifest) + '</manifest>' + segments[2].split('</manifest>', 1)[1]
    provider = RoutedSegmentProvider({
        'requirements': [segments[0]],
        'plan': [segments[1]],
        'manifest': [segments[2]],
        'resource_op': [],
        'program:0': [segments[3]],
        'program:1': [segments[4]],
    })
    request = UnifiedGenerationRequest.model_validate(project_request_payload())

    events = [event async for event in GenerationOrchestrator(PromptBuilder(knowledge_base='fixed')).generate_unified(request, provider)]

    failure = next(event for event in events if event.type == 'validation_error')
    assert failure.data['code'] == 'PROGRAM_CONTRACT_INVALID'
    assert failure.data['path'] == '/variables'


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
    assert response.json()['issues'][0]['path'] == '/scripts/0/blocks/0'
    assert response.json()['issues'][0]['code'] == 'PROGRAM_CONTRACT_INVALID'


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
    bad_segments = project_segment_outputs(bad_mode_manifest_output())
    good_segments = project_segment_outputs()
    provider = routed_project_provider(good_segments, manifest=[bad_segments[2], good_segments[2]])
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    orchestrator = GenerationOrchestrator(PromptBuilder(knowledge_base='fixed'), max_repair_rounds=1)

    events = [event async for event in orchestrator.generate_unified(request, provider)]

    assert len(provider.calls) == 6
    repair = [event for event in events if event.type == 'repair']
    assert repair and repair[0].data['attempt'] == 1
    assert repair[0].data['resume'] == 'manifest'
    assert 'must use append' in repair[0].data['detail']
    assert events[-1].type == 'complete'
    assert events[-1].data['program_count'] == 2
    assert not any(event.type == 'validation_error' for event in events)
    repair_messages = provider.calls[3]
    repair_prompt = repair_messages[-1]['content']
    assert 'must use append' in repair_prompt
    assert 'failed_object' in repair_prompt
    assert '"mode":"modify"' in repair_prompt


@pytest.mark.asyncio
async def test_orchestrator_repairs_program_validation_error():
    good_segments = project_segment_outputs()
    bad_segments = project_segment_outputs(project_stream_output().replace('opcode": "event_whenflagclicked"', 'opcode": "motion_movesteps"', 1))
    provider = routed_project_provider(good_segments, program0=[bad_segments[3], good_segments[3]], program1=[good_segments[4]])
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    orchestrator = GenerationOrchestrator(PromptBuilder(knowledge_base='fixed'), max_repair_rounds=1)

    events = [event async for event in orchestrator.generate_unified(request, provider)]

    repair = [event for event in events if event.type == 'repair']
    assert repair and repair[0].data['resume'] == 'program'
    assert events[-1].type == 'complete'
    assert events[-1].data['program_count'] == 2
    assert [event.data['target_index'] for event in events if event.type == 'phase_complete' and event.phase == 'program'] == [0, 1]
    repair_prompt = provider.calls[5][-1]['content']
    assert 'failed_object' in repair_prompt
    assert '"scripts"' in repair_prompt
    assert bad_segments[3] not in repair_prompt


@pytest.mark.asyncio
async def test_orchestrator_repair_exhaustion_yields_one_terminal_error():
    bad_segments = project_segment_outputs(bad_mode_manifest_output())
    provider = routed_project_provider(bad_segments, manifest=[bad_segments[2], bad_segments[2]], program0=[], program1=[])
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    orchestrator = GenerationOrchestrator(PromptBuilder(knowledge_base='fixed'), max_repair_rounds=1)

    events = [event async for event in orchestrator.generate_unified(request, provider)]

    assert len(provider.calls) == 4
    repairs = [event for event in events if event.type == 'repair']
    assert len(repairs) == 1
    validation = [event for event in events if event.type == 'validation_error']
    assert len(validation) == 1
    assert events[-1].type == 'error'
    assert events[-1].data['code'] == 'OUTPUT_VALIDATION_ERROR'
    assert events[-1].data['message'] == 'AI 输出未通过协议校验'


@pytest.mark.asyncio
async def test_orchestrator_repairs_second_round_when_first_repair_fails():
    good_segments = project_segment_outputs()
    bad_program = project_segment_outputs(project_stream_output().replace('opcode": "event_whenflagclicked"', 'opcode": "motion_movesteps"', 1))[3]
    provider = routed_project_provider(good_segments, program0=[bad_program, bad_program, good_segments[3]], program1=[good_segments[4]])
    request = UnifiedGenerationRequest.model_validate(project_request_payload())
    orchestrator = GenerationOrchestrator(PromptBuilder(knowledge_base='fixed'), max_repair_rounds=2)

    events = [event async for event in orchestrator.generate_unified(request, provider)]

    assert len(provider.calls) == 7
    repairs = [event for event in events if event.type == 'repair']
    assert [item.data['attempt'] for item in repairs] == [1, 2]
    assert events[-1].type == 'complete'
    assert events[-1].type == 'complete'
