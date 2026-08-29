import json

import httpx
import pytest
from pydantic import ValidationError

from app.main import app
from app.prompts_ai_blocks import (
    SCRATCH_CODE_GENERATION_PROMPT,
    build_program_messages,
    build_plan_messages,
    build_project_manifest_messages,
)
from app.providers.agnes_text_provider import AgnesStreamDelta, AgnesTextProviderError
from app.routes.ai_blocks import _collect_resilient, _timed_events, get_ai_blocks_provider
from app.schemas.ai_blocks import (
    GenerateRequest, MAX_BLOCKS, NATIVE_OPCODES, ProgramIR, ThinkingSettings, extract_single_json,
    validate_program_contracts, validate_program_for_mode,
)


class MockProvider:
    def __init__(self, responses=None, configured=True):
        self.responses = list(responses or [])
        self.configured = configured
        self.calls = []

    def ensure_configured(self):
        if not self.configured:
            raise RuntimeError('secret missing key')

    async def stream_with_reasoning(
        self, messages, *, thinking_enabled=True, thinking_strength='medium',
    ):
        self.calls.append(messages)
        response = self.responses.pop(0) if self.responses else []
        if isinstance(response, Exception):
            raise response
        for chunk in response:
            if isinstance(chunk, AgnesStreamDelta):
                yield chunk
            else:
                yield AgnesStreamDelta(kind='content', text=chunk)

    async def stream(self, messages):
        async for delta in self.stream_with_reasoning(messages):
            if delta.kind == 'content':
                yield delta.text


def test_project_manifest_prompt_defines_only_valid_target_refs():
    request = GenerateRequest.model_validate({
        **payload(),
        'requirements': requirements(),
        'output_contract': 'project_manifest',
        'scope': 'project',
        'asset_policy': 'existing_only',
    })
    prompt = '\n'.join(item['content'] for item in build_project_manifest_messages(request, ['添加角色']))
    assert 'TargetRef.kind 只能是 current_target、stage、existing_sprite、created_sprite' in prompt
    assert '绝对不能写 sprite、ref_type 或其他值' in prompt
    assert 'target={\"kind\":\"created_sprite\",\"op_id\":\"该角色的op_id\"}' in prompt
    assert 'program_targets 的每项且只能有 target、mode、intent，绝对不能有 programs' in prompt
    assert 'variables 每项且只能有 name、value、scope，scope 必须为 global' in prompt
    assert '禁止 type、variable、list、id 等字段' in prompt
    assert 'width 和 height 只能是 512、768 或 1024' in prompt
    assert 'sheet_layout 必须是字符串 "1x1"' in prompt
    assert 'add_backdrop 只能有 op、op_id、name、tags、source、select_after' in prompt
    assert '所有 stage、existing_sprite、created_sprite 的 mode 都必须为 append' in prompt
    assert 'add_builtin_sprite 只能有 op、op_id、name、x、y、size、tags、source，禁止 target' in prompt
    assert 'stage 只能是 {\"kind\":\"stage\"}' in prompt
    assert 'delete_sprite 只能有 op、op_id、name、target' in prompt
    assert 'add_costume 的 source.asset_type 必须是 costume，绝对不能是 sprite' in prompt
    assert 'add_backdrop 的 source.asset_type 必须是 backdrop' in prompt
    assert '本次素材策略是 existing_only（仅已有素材）' in prompt
    assert 'resource_ops 的所有 source.kind 必须是 scratch_library，严禁 image_generation' in prompt
    assert '素材策略：existing_only' in prompt


def payload(mode='modify', is_stage=False):
    return {
        'session_id': 'session-1',
        'request_id': 'request-1',
        'messages': [{'role': 'user', 'content': '让角色走动'}],
        'target': {
            'name': 'Sprite1', 'is_stage': is_stage, 'has_scripts': True, 'mode': mode,
            'costumes': ['costume1'], 'sounds': ['Meow'], 'sprite_names': ['Sprite1', 'Ball'],
            'current_program': json.dumps({'scripts': [{'id': 'script_1', 'code': 'when flag clicked -> move 10 steps'}]}),
        },
    }


def requirements():
    return {
        'ready': True, 'title': 'Move sprite', 'goal': 'Move after green flag',
        'behavior_steps': ['Move ten steps'], 'triggers': ['Green flag'],
        'controls': [], 'success_criteria': ['Sprite moves'], 'missing_questions': [],
    }


def valid_program(remove=None):
    return {
        'schema_version': 1,
        'summary': 'Move when green flag is clicked',
        'remove_script_ids': remove or [],
        'variables': [], 'lists': [], 'broadcasts': [],
        'scripts': [{
            'id': 'script_new_1', 'purpose': 'Move', 'x': 10, 'y': 20,
            'blocks': [
                {'opcode': 'event_whenflagclicked', 'fields': {}, 'inputs': {}, 'substacks': {}},
                {'opcode': 'motion_movesteps', 'fields': {},
                 'inputs': {'STEPS': {'kind': 'number', 'value': 10}}, 'substacks': {}},
            ],
        }],
    }


def edge_bounce_program():
    program = valid_program()
    program['summary'] = 'Move forever and bounce at the edge'
    program['scripts'][0]['purpose'] = 'Move and bounce'
    program['scripts'][0]['blocks'] = [
        {'opcode': 'event_whenflagclicked', 'fields': {}, 'inputs': {}, 'substacks': {}},
        {
            'opcode': 'control_forever', 'fields': {}, 'inputs': {},
            'substacks': {'SUBSTACK': [
                {
                    'opcode': 'motion_movesteps', 'fields': {},
                    'inputs': {'STEPS': {'kind': 'number', 'value': 10}}, 'substacks': {},
                },
                {'opcode': 'motion_ifonedgebounce', 'fields': {}, 'inputs': {}, 'substacks': {}},
            ]},
        },
    ]
    return program


def point_direction_program(value):
    program = valid_program()
    program['summary'] = 'Point in a numeric direction'
    program['scripts'][0]['blocks'].append({
        'opcode': 'motion_pointindirection', 'fields': {},
        'inputs': {'DIRECTION': {'kind': 'angle', 'value': value}}, 'substacks': {},
    })
    return program


def test_program_prompt_includes_canonical_edge_bounce_contract():
    request = payload(mode='append')
    request['requirements'] = requirements()
    messages = build_program_messages(
        GenerateRequest.model_validate(request),
        ['move forever', 'bounce at the edge'],
    )
    prompt = '\n'.join(message['content'] for message in messages)
    assert 'Scratch 代码规范' in prompt
    assert '禁止 SUBSTACK1' in prompt
    assert '禁止虚构 sensing_touchingedge' in prompt
    assert '循环表达持续关系' in prompt
    assert '条件积木表达前置/分支关系' in prompt
    assert 'number/angle 的 value 必须是 JSON 数字' in prompt
    assert '{"kind":"angle","value":90}' in prompt
    assert SCRATCH_CODE_GENERATION_PROMPT.splitlines()[0] in prompt
    assert '现在只输出符合上述协议的 ProgramIR JSON。' in prompt


@pytest.mark.asyncio
async def test_timed_events_prints_only_content(capsys):
    provider = MockProvider([[
        AgnesStreamDelta(kind='reasoning', text='内部推理'),
        AgnesStreamDelta(kind='content', text='第一段'),
        AgnesStreamDelta(kind='usage', usage={'total_tokens': 3}),
        AgnesStreamDelta(kind='content', text='第二段'),
    ]])

    events = [event async for event in _timed_events(
        provider, [{'role': 'user', 'content': 'test'}], ThinkingSettings(enabled=False, strength='low')
    )]

    assert [event.kind for event in events] == ['reasoning', 'content', 'usage', 'content']
    assert capsys.readouterr().out == '第一段第二段'


@pytest.mark.asyncio
async def test_provider_timeout_retries_without_thinking():
    class RetryProvider(MockProvider):
        async def stream_with_reasoning(self, messages, *, thinking_enabled=True, thinking_strength='medium'):
            self.calls.append((messages, thinking_enabled, thinking_strength))
            if len(self.calls) == 1:
                raise AgnesTextProviderError('transient timeout')
            yield AgnesStreamDelta(kind='content', text='{"ok":true}')

    provider = RetryProvider()
    result = await _collect_resilient(
        provider,
        [{'role': 'user', 'content': 'test'}],
        ThinkingSettings(enabled=True, strength='high'),
    )
    assert result[0] == '{"ok":true}'
    assert provider.calls[1][1:] == (False, 'low')


async def post(path, data):
    transport = httpx.ASGITransport(app=app)
    async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
        return await client.post(path, json=data)


@pytest.mark.asyncio
async def test_chat_streams_events_and_prompt_uses_defaults_without_questions():
    provider = MockProvider([['请问希望用方向键，', '还是自动移动？']])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    try:
        response = await post('/api/ai-blocks/chat', payload(is_stage=True))
    finally:
        app.dependency_overrides.clear()
    assert response.status_code == 200
    assert response.headers['content-type'].startswith('text/event-stream')
    assert response.text.count('event: assistant_delta') == 2
    assert 'event: message_complete' in response.text
    system = provider.calls[0][0]['content']
    assert '根据用户输入直接确定可实现的角色或舞台行为' in system
    assert '绝不提问、绝不请求确认' in system
    assert '未说明细节一律使用最简单且符合 Scratch 习惯的默认值' in system
    assert '禁止输出代码' in system
    prompt = provider.calls[0][-1]['content']
    assert '"is_stage":true' in prompt
    assert 'script_1' in prompt


def test_plan_prompt_requires_steps_only():
    request = GenerateRequest.model_validate({**payload(), 'requirements': requirements()})
    prompt = '\n'.join(item['content'] for item in build_plan_messages(request))
    assert '且只能包含唯一字段 steps' in prompt
    assert '禁止输出 success_criteria、summary、triggers、controls、plan' in prompt


def test_program_prompt_includes_general_behavior_contract():
    request = payload(mode='append')
    request['requirements'] = requirements()
    messages = build_program_messages(
        GenerateRequest.model_validate(request),
        ['识别触发条件、持续关系和动作前置条件', '将受条件控制的动作放入对应分支', '核对所有行为断言'],
    )
    prompt = '\n'.join(message['content'] for message in messages)
    assert '可验证的行为断言' in prompt
    assert '每个核心断言都必须在规划和 ProgramIR 中有对应实现' in prompt
    assert '条件体只放受该条件控制的动作' in prompt
    assert '发现需求无法由当前原生积木表达时输出 UNSUPPORTED_SCRATCH_REQUEST' in prompt
    assert '禁止根据自然语言臆造 opcode' in prompt


@pytest.mark.asyncio
async def test_requirements_accumulates_chunks_and_accepts_one_code_fence():
    result = {
        'ready': True, 'title': '移动角色', 'goal': '按方向键移动',
        'behavior_steps': ['读取方向键', '改变坐标'], 'triggers': ['绿旗'],
        'controls': ['方向键'], 'success_criteria': ['角色可移动'], 'missing_questions': [],
    }
    raw = json.dumps(result, ensure_ascii=False)
    provider = MockProvider([['```json\n' + raw[:20], raw[20:] + '\n```']])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    try:
        response = await post('/api/ai-blocks/requirements', payload())
    finally:
        app.dependency_overrides.clear()
    assert response.status_code == 200
    assert response.json() == result
    system = provider.calls[0][0]['content']
    assert '未说明的触发方式默认点击绿旗' in system
    assert '必须 ready=true，missing_questions 必须始终为空数组' in system


@pytest.mark.asyncio
async def test_requirements_retries_with_exact_parse_error_in_context():
    valid = requirements()
    provider = MockProvider([
        ['{"ready": tru'],
        [json.dumps(valid, ensure_ascii=False)],
    ])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    try:
        response = await post('/api/ai-blocks/requirements', payload())
    finally:
        app.dependency_overrides.clear()
    assert response.status_code == 200
    assert response.json() == valid
    assert len(provider.calls) == 2
    repair_context = '\n'.join(message['content'] for message in provider.calls[1])
    assert '{"ready": tru' in repair_context
    assert 'invalid JSON response at line 1 column 11' in repair_context


@pytest.mark.asyncio
async def test_requirements_proceed_command_forces_defaults_instead_of_another_question():
    incomplete = {
        'ready': False, 'title': '', 'goal': '', 'behavior_steps': [], 'triggers': [],
        'controls': [], 'success_criteria': [], 'missing_questions': ['还要移动多少步？'],
    }
    provider = MockProvider([[json.dumps(incomplete, ensure_ascii=False)]])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload()
    request['messages'] = [
        {'role': 'user', 'content': '让角色移动'},
        {'role': 'assistant', 'content': '还要移动多少步？'},
        {'role': 'user', 'content': '开始编写，按默认即可'},
    ]
    request['proceed_with_defaults'] = True
    try:
        response = await post('/api/ai-blocks/requirements', request)
    finally:
        app.dependency_overrides.clear()

    assert response.status_code == 200
    result = response.json()
    assert result['ready'] is True
    assert result['goal'] == '让角色移动'
    assert result['behavior_steps'] == ['让角色移动']
    assert result['triggers'] == ['点击绿旗']
    assert result['missing_questions'] == []
    assert '必须 ready=true' in provider.calls[0][0]['content']
    assert '立即使用默认值开始：true' in provider.calls[0][-1]['content']


@pytest.mark.asyncio
async def test_requirements_rejects_bad_json_without_leaking_provider_output():
    provider = MockProvider([['{"ready":true} trailing SECRET_TOKEN']])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    try:
        response = await post('/api/ai-blocks/requirements', payload())
    finally:
        app.dependency_overrides.clear()
    assert response.status_code == 502
    assert response.json()['detail']['code'] == 'INVALID_REQUIREMENTS_RESPONSE'
    assert 'SECRET_TOKEN' not in response.text


@pytest.mark.asyncio
async def test_generate_requires_structured_requirements():
    request = payload()
    request['requirements'] = 'legacy string requirements'
    response = await post('/api/ai-blocks/generate', request)
    assert response.status_code == 422


@pytest.mark.asyncio
async def test_generate_calls_agnes_twice_and_streams_plan_then_program():
    plan = json.dumps({'steps': ['添加绿旗事件', '移动十步']}, ensure_ascii=False)
    program = json.dumps(valid_program(), ensure_ascii=False)
    provider = MockProvider([[plan[:10], plan[10:]], [program]])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload(mode='append')
    request['requirements'] = requirements()
    try:
        response = await post('/api/ai-blocks/generate', request)
    finally:
        app.dependency_overrides.clear()
    assert response.status_code == 200
    events = [line for line in response.text.splitlines() if line.startswith('event:')]
    assert events == [
        'event: phase', 'event: plan_ready', 'event: phase',
        'event: program_ready', 'event: message_complete',
    ]
    assert '"message":"正在规划积木步骤"' in response.text
    assert '"message":"正在生成积木程序"' in response.text
    assert len(provider.calls) == 2
    assert '"ready": true' in provider.calls[0][1]['content']
    assert 'append: 只新增脚本' in provider.calls[1][0]['content']
    assert 'remove_script_ids 必须为空' in provider.calls[1][0]['content']


@pytest.mark.asyncio
async def test_generate_repairs_invalid_plan_before_program_generation():
    provider = MockProvider([
        ['not json'],
        [json.dumps({'steps': ['move']})],
        [json.dumps(valid_program())],
    ])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload(mode='append')
    request['requirements'] = requirements()
    try:
        response = await post('/api/ai-blocks/generate', request)
    finally:
        app.dependency_overrides.clear()
    assert 'event: program_ready' in response.text
    assert '正在校正规划结构' in response.text
    assert len(provider.calls) == 3
    repair_context = '\n'.join(message['content'] for message in provider.calls[1])
    assert 'not json' in repair_context
    assert 'invalid JSON response at line 1 column 1: Expecting value' in repair_context


@pytest.mark.asyncio
async def test_generate_repairs_one_invalid_program_response():
    provider = MockProvider([
        [json.dumps({'steps': ['move']})],
        [json.dumps({'schema_version': 1})],
        [json.dumps(valid_program())],
    ])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload(mode='append')
    request['requirements'] = requirements()
    try:
        response = await post('/api/ai-blocks/generate', request)
    finally:
        app.dependency_overrides.clear()
    assert 'event: program_ready' in response.text
    assert '"phase":"repairing"' in response.text
    assert len(provider.calls) == 3
    assert provider.calls[2][-2]['role'] == 'assistant'
    assert '未通过服务器解析或校验' in provider.calls[2][-1]['content']



@pytest.mark.asyncio
async def test_generate_repairs_string_angle_before_frontend_compilation():
    provider = MockProvider([
        [json.dumps({'steps': ['point right']})],
        [json.dumps(point_direction_program('90'))],
        [json.dumps(point_direction_program(90))],
    ])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload(mode='append')
    request['requirements'] = requirements()
    try:
        response = await post('/api/ai-blocks/generate', request)
    finally:
        app.dependency_overrides.clear()

    assert 'event: program_ready' in response.text
    assert '"phase":"repairing"' in response.text
    assert len(provider.calls) == 3
    repair_context = '\n'.join(message['content'] for message in provider.calls[2])
    assert 'angle input requires a numeric value' in repair_context
    assert '"value": "90"' in repair_context


@pytest.mark.asyncio
async def test_generate_repairs_frontend_incompatible_event_substack():
    invalid = edge_bounce_program()
    event, forever = invalid['scripts'][0]['blocks']
    event['substacks'] = {'SUBSTACK': [forever]}
    invalid['scripts'][0]['blocks'] = [event]
    provider = MockProvider([
        [json.dumps({'steps': ['move forever', 'bounce']})],
        [json.dumps(invalid)],
        [json.dumps(edge_bounce_program())],
    ])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload(mode='append')
    request['requirements'] = requirements()
    try:
        response = await post('/api/ai-blocks/generate', request)
    finally:
        app.dependency_overrides.clear()
    assert 'event: program_ready' in response.text
    assert '"phase":"repairing"' in response.text
    assert len(provider.calls) == 3
    repair_context = '\n'.join(message['content'] for message in provider.calls[2])
    assert 'event_whenflagclicked' in repair_context
    assert '$.scripts[0].blocks[0].substacks.SUBSTACK: substack not allowed for opcode' in repair_context


@pytest.mark.asyncio
async def test_generate_uses_a_second_program_repair_attempt():
    provider = MockProvider([
        [json.dumps({'steps': ['move']})],
        [json.dumps({'schema_version': 1})],
        ['not json'],
        [json.dumps(valid_program())],
    ])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload(mode='append')
    request['requirements'] = requirements()
    try:
        response = await post('/api/ai-blocks/generate', request)
    finally:
        app.dependency_overrides.clear()
    assert 'event: program_ready' in response.text
    assert response.text.count('\"phase\":\"repairing\"') == 2
    assert len(provider.calls) == 4
    repair_context = '\n'.join(message['content'] for message in provider.calls[3])
    assert '第 1 次 ProgramIR' in repair_context
    assert '第 2 次 ProgramIR' in repair_context
    assert 'not json' in repair_context
    assert 'invalid JSON response at line 1 column 1: Expecting value' in repair_context


@pytest.mark.asyncio
async def test_generate_uses_a_fourth_program_repair_attempt():
    provider = MockProvider([
        [json.dumps({'steps': ['move']})],
        [json.dumps({'schema_version': 1})],
        ['not json'],
        [json.dumps({'schema_version': 1})],
        ['still not json'],
        [json.dumps(valid_program())],
    ])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload(mode='append')
    request['requirements'] = requirements()
    try:
        response = await post('/api/ai-blocks/generate', request)
    finally:
        app.dependency_overrides.clear()
    assert 'event: program_ready' in response.text
    assert response.text.count('\"phase\":\"repairing\"') == 4
    assert len(provider.calls) == 6
    repair_context = '\n'.join(message['content'] for message in provider.calls[5])
    assert '第 4 次 ProgramIR' in repair_context
    assert 'still not json' in repair_context


@pytest.mark.asyncio
async def test_generate_rejects_append_removals_as_clean_sse_error():
    provider = MockProvider([
        [json.dumps({'steps': ['replace']})],
        [json.dumps(valid_program(['script_1']))],
    ])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload(mode='append')
    request['requirements'] = requirements()
    try:
        response = await post('/api/ai-blocks/generate', request)
    finally:
        app.dependency_overrides.clear()
    assert 'event: message_error' in response.text
    assert 'event: program_ready' not in response.text
    assert 'script_1' not in response.text


def test_modify_mode_rejects_protected_script_removal():
    program = ProgramIR.model_validate(valid_program(['script_2']))
    current = json.dumps({'scripts': [
        {'id': 'script_1', 'protected': False},
        {'id': 'script_2', 'protected': True},
    ]})
    with pytest.raises(ValueError, match='protected'):
        validate_program_for_mode(program, 'modify', current)


def test_json_extractor_rejects_multiple_objects_and_trailing_garbage():
    assert extract_single_json('{"ok":true}') == {'ok': True}
    assert extract_single_json('```json\n{"ok":true}\n```') == {'ok': True}
    with pytest.raises(ValueError):
        extract_single_json('{"ok":true}{"second":true}')
    with pytest.raises(ValueError):
        extract_single_json('prefix {"ok":true}')
    with pytest.raises(ValueError):
        extract_single_json('```json\n{}\n``` extra')


def test_program_ir_rejects_unknown_opcode_and_extra_fields():
    program = valid_program()
    program['scripts'][0]['blocks'][0]['opcode'] = 'pen_clear'
    with pytest.raises(ValidationError):
        ProgramIR.model_validate(program)
    program = valid_program()
    program['secret'] = 'not allowed'
    with pytest.raises(ValidationError):
        ProgramIR.model_validate(program)


def test_program_contracts_match_frontend_for_edge_bounce():
    validate_program_contracts(ProgramIR.model_validate(edge_bounce_program()))

    invalid_event = edge_bounce_program()
    event, forever = invalid_event['scripts'][0]['blocks']
    event['substacks'] = {'SUBSTACK': [forever]}
    invalid_event['scripts'][0]['blocks'] = [event]
    with pytest.raises(
        ValueError,
        match=r'\$\.scripts\[0\]\.blocks\[0\]\.substacks\.SUBSTACK: substack not allowed',
    ):
        validate_program_contracts(ProgramIR.model_validate(invalid_event))

    invalid_sensing = edge_bounce_program()
    forever = invalid_sensing['scripts'][0]['blocks'][1]
    forever['substacks']['SUBSTACK'][1] = {
        'opcode': 'control_if', 'fields': {},
        'inputs': {'CONDITION': {'kind': 'block', 'block': {
            'opcode': 'sensing_touchingobject',
            'fields': {'OBJECTIVE': 'edge'}, 'inputs': {}, 'substacks': {},
        }}},
        'substacks': {'SUBSTACK': [
            {'opcode': 'motion_ifonedgebounce', 'fields': {}, 'inputs': {}, 'substacks': {}},
        ]},
    }
    with pytest.raises(
        ValueError,
        match=r'inputs\.CONDITION\.block\.fields\.OBJECTIVE: unknown field',
    ):
        validate_program_contracts(ProgramIR.model_validate(invalid_sensing))


def test_program_contracts_report_all_exact_input_and_sound_errors():
    program = valid_program()
    program['variables'] = [{'name': 'score', 'value': 0, 'scope': 'local'}]
    program['scripts'][0]['blocks'].extend([
        {
            'opcode': 'looks_say', 'fields': {},
            'inputs': {'MESSAGE': {'kind': 'text', 'value': 'go'}}, 'substacks': {},
        },
        {
            'opcode': 'data_setvariableto', 'fields': {'VARIABLE': 'score'},
            'inputs': {'VALUE': {'kind': 'number', 'value': 1}}, 'substacks': {},
        },
        {
            'opcode': 'control_forever', 'fields': {}, 'inputs': {},
            'substacks': {'SUBSTACK': [
                {'opcode': 'motion_movesteps', 'fields': {}, 'inputs': {'STEPS': {'kind': 'number', 'value': 10}}, 'substacks': {}},
                {'opcode': 'motion_ifonedgebounce', 'fields': {}, 'inputs': {}, 'substacks': {}},
                {'opcode': 'sound_play', 'fields': {'SOUND': 'Meow'}, 'inputs': {}, 'substacks': {}},
            ]},
        },
    ])
    with pytest.raises(ValueError) as exc_info:
        validate_program_contracts(ProgramIR.model_validate(program))
    error = str(exc_info.value)
    assert '$.scripts[0].blocks[3].inputs.VALUE.kind: expected text' in error
    assert '$.scripts[0].blocks[4].substacks.SUBSTACK[2].fields.SOUND: unknown field' in error
    assert '$.scripts[0].blocks[4].substacks.SUBSTACK[2].inputs.SOUND_MENU: required input is missing' in error


def nested_block(depth):
    block = {'opcode': 'operator_not', 'fields': {}, 'inputs': {}, 'substacks': {}}
    for _ in range(depth):
        block = {
            'opcode': 'operator_not', 'fields': {},
            'inputs': {'OPERAND': {'kind': 'block', 'block': block}}, 'substacks': {},
        }
    return block


def test_program_ir_rejects_excessive_depth_and_total_blocks():
    program = valid_program()
    program['scripts'][0]['blocks'] = [nested_block(20)]
    with pytest.raises(ValidationError, match='nesting'):
        ProgramIR.model_validate(program)
    program = valid_program()
    program['scripts'] = [{
        'id': f'script_{i}', 'purpose': 'many', 'x': 0, 'y': 0,
        'blocks': [
            {'opcode': 'motion_movesteps', 'fields': {}, 'inputs': {}, 'substacks': {}}
            for _ in range(MAX_BLOCKS // 2)
        ],
    } for i in range(3)]
    with pytest.raises(ValidationError, match='too many blocks'):
        ProgramIR.model_validate(program)



def test_program_ir_matches_frontend_input_and_data_shapes():
    program = valid_program()
    program['variables'] = [
        {'name': 'score'},
        {'name': 'speed', 'value': 3, 'scope': 'local'},
    ]
    program['lists'] = [
        {'name': 'items'},
        {'name': 'levels', 'value': ['one', 2, True], 'scope': 'global'},
    ]
    program['scripts'][0]['blocks'][0]['fields'] = {
        'text': 'value', 'number': 3, 'enabled': True,
    }
    block_input = {
        'opcode': 'operator_not', 'fields': {},
        'inputs': {
            'OPERAND': {
                'kind': 'block',
                'block': {'opcode': 'sensing_mousedown', 'fields': {}, 'inputs': {}, 'substacks': {}},
            },
        },
        'substacks': {},
    }
    program['scripts'][0]['blocks'].append(block_input)
    dumped = ProgramIR.model_validate(program).model_dump(mode='json', exclude_none=True)
    assert dumped['scripts'][0]['blocks'][2]['inputs']['OPERAND'] == {
        'kind': 'block',
        'block': {'opcode': 'sensing_mousedown', 'fields': {}, 'inputs': {}, 'substacks': {}},
    }
    assert dumped['variables'][0] == {'name': 'score'}
    assert dumped['variables'][1] == {'name': 'speed', 'value': 3.0, 'scope': 'local'}
    assert dumped['lists'][0] == {'name': 'items'}
    assert dumped['lists'][1] == {
        'name': 'levels', 'value': ['one', 2.0, True], 'scope': 'global',
    }
    assert 'is_cloud' not in json.dumps(dumped)
    assert 'items' not in dumped['lists'][0]


def test_program_ir_rejects_legacy_input_shape_and_unsupported_catalog_opcodes():
    program = valid_program()
    program['scripts'][0]['blocks'][1]['inputs']['STEPS'] = {'type': 'number', 'value': 10}
    with pytest.raises(ValidationError):
        ProgramIR.model_validate(program)
    assert 'looks_switchbackdroptoandwait' not in NATIVE_OPCODES
    assert 'sensing_of' not in NATIVE_OPCODES
    for opcode in ('looks_switchbackdroptoandwait', 'sensing_of'):
        program = valid_program()
        program['scripts'][0]['blocks'][0]['opcode'] = opcode
        with pytest.raises(ValidationError):
            ProgramIR.model_validate(program)


def test_program_ir_frontend_limits_are_enforced():
    program = valid_program()
    program['scripts'] = [
        {
            'id': f'script_{index}', 'purpose': 'limit', 'x': 0, 'y': 0,
            'blocks': [{'opcode': 'motion_movesteps', 'fields': {}, 'inputs': {}, 'substacks': {}}],
        }
        for index in range(13)
    ]
    with pytest.raises(ValidationError):
        ProgramIR.model_validate(program)
    program = valid_program()
    program['summary'] = 'x' * 1001
    with pytest.raises(ValidationError):
        ProgramIR.model_validate(program)
    program = valid_program()
    program['scripts'][0]['purpose'] = 'x' * 121
    with pytest.raises(ValidationError):
        ProgramIR.model_validate(program)


@pytest.mark.asyncio
async def test_missing_key_is_503_for_all_routes_and_does_not_leak():
    provider = MockProvider(configured=False)
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    try:
        chat_response = await post('/api/ai-blocks/chat', payload())
        requirements_response = await post('/api/ai-blocks/requirements', payload())
        generate_payload = payload()
        generate_payload['requirements'] = requirements()
        generate_response = await post('/api/ai-blocks/generate', generate_payload)
    finally:
        app.dependency_overrides.clear()
    for response in (chat_response, requirements_response, generate_response):
        assert response.status_code == 503
        assert response.json()['detail']['code'] == 'AGNES_NOT_CONFIGURED'
        assert 'secret' not in response.text


@pytest.mark.asyncio
async def test_ai_blocks_request_size_limit():
    transport = httpx.ASGITransport(app=app)
    async with httpx.AsyncClient(transport=transport, base_url='http://test') as client:
        response = await client.post(
            '/api/ai-blocks/chat', content=b'x' * 270_000,
            headers={'content-type': 'application/json'},
        )
    assert response.status_code == 413
    assert response.json()['detail']['code'] == 'REQUEST_TOO_LARGE'

@pytest.mark.asyncio
async def test_generate_project_manifest_streams_manifest_ready():
    plan = json.dumps({'steps': ['选择已有背景']}, ensure_ascii=False)
    manifest = json.dumps({
        'schema_version': 1,
        'scope': 'project',
        'summary': '选择已有背景',
        'resource_ops': [{'op': 'select_backdrop', 'op_id': 'select_1', 'name': '舞台'}],
        'program_targets': [],
        'shared_state': {'variables': [], 'lists': [], 'broadcasts': []},
    }, ensure_ascii=False)
    provider = MockProvider([[plan], [manifest]])
    app.dependency_overrides[get_ai_blocks_provider] = lambda: provider
    request = payload()
    request['requirements'] = requirements()
    request['output_contract'] = 'project_manifest'
    request['scope'] = 'project'
    try:
        response = await post('/api/ai-blocks/generate', request)
    finally:
        app.dependency_overrides.clear()
    assert response.status_code == 200
    events = [line for line in response.text.splitlines() if line.startswith('event:')]
    assert events == ['event: phase', 'event: plan_ready', 'event: phase', 'event: manifest_ready', 'event: message_complete']
    assert '"scope":"project"' in response.text
    assert 'manifest_ready' in response.text
    assert '"target":null' not in response.text
    assert '"name":null' not in response.text
    assert '"op_id":null' not in response.text

