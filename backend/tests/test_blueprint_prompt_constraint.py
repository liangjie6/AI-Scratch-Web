import pytest
from app.orchestration.error_messages import repair_hint
from app.prompt_builder import PromptBuilder
from app.schemas.ai_blocks import UnifiedGenerationRequest, TargetContext


def test_repair_hint_for_blueprint_lineage_errors():
    hint1 = repair_hint('blueprint', '/output', 'blueprint state_plan must project every TaskSpec state exactly once')
    assert 'Blueprint 的 state_plan 必须且只能投影 TaskSpec.state' in hint1
    assert 'ref_id 必须严格等于对应 TaskSpec 状态的 state_id' in hint1
    assert 'state_score' in hint1

    hint2 = repair_hint('blueprint', '/output', 'blueprint state_plan must preserve TaskSpec state semantics')
    assert 'kind、scope 和 owner_entity_id 必须与 TaskSpec 对应 state 完全一致' in hint2

    hint3 = repair_hint('blueprint', '/output', 'blueprint must bind every TaskSpec entity exactly once')
    assert 'entity_bindings 必须且只能绑定 TaskSpec 中的每个实体一次' in hint3


def test_prompt_builder_blueprint_prompt_contains_state_plan_constraints():
    builder = PromptBuilder()
    request = UnifiedGenerationRequest(
        session_id='s1',
        request_id='r1',
        messages=[{'role': 'user', 'content': '小猫接红包'}],
        scope='project',
        target=TargetContext(name='角色1', is_stage=False, has_scripts=False, mode='append'),
        project_context={
            'project_id': 'p1',
            'version': 1,
            'targets': [
                {'target_id': 'stage', 'revision': 1, 'name': 'Stage', 'is_stage': True, 'has_scripts': False, 'mode': 'append'},
                {'target_id': 'sprite1', 'revision': 1, 'name': '角色1', 'is_stage': False, 'has_scripts': False, 'mode': 'append'},
            ],
            'shared_state': {},
        },
    )

    # 1. 验证 blueprint 的 tagged 模式提示词
    messages, _ = builder.build_segment_prompt(None, {'name': '角色1', 'is_stage': False}, request, 'blueprint')
    content = messages[-1]['content']
    assert 'state_plan 必须且只能投影 TaskSpec.state 中的每一个状态' in content
    assert 'ref_id 必须严格等于对应 TaskSpec 的 state_id' in content
    assert 'state_score' in content

    # 2. 验证 blueprint 的 json_object 模式提示词
    messages_json, _ = builder.build_segment_prompt(
        None, {'name': '角色1', 'is_stage': False}, request, 'blueprint', context={'transport': 'json_object'}
    )
    content_json = messages_json[-1]['content']
    assert 'state_plan 必须且只能投影 TaskSpec.state 中的每一个状态' in content_json
    assert 'ref_id 必须严格等于对应 TaskSpec 的 state_id' in content_json

    # 3. 验证 failure 重试时，准确注入具体的 lineage 错误详情和修复指导，而不是误提示为空
    messages_retry, _ = builder.build_segment_prompt(
        None,
        {'name': '角色1', 'is_stage': False},
        request,
        'blueprint',
        context={
            'transport': 'json_object',
            'failure': {
                'code': 'OUTPUT_VALIDATION_ERROR',
                'path': '/output',
                'message': 'structured output validation failed',
                'detail': 'blueprint state_plan must project every TaskSpec state exactly once',
            },
        },
    )
    content_retry = messages_retry[-1]['content']
    assert '上一轮输出未通过校验：blueprint state_plan must project every TaskSpec state exactly once' in content_retry
    assert '修复指导：Blueprint 的 state_plan 必须且只能投影 TaskSpec.state' in content_retry
    assert '上一轮结构化响应正文为空' not in content_retry
