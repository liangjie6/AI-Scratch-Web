from __future__ import annotations

import json
import pytest

from app.orchestration.error_messages import repair_hint
from app.orchestration.streaming_parser import robust_json_parse, StreamProtocolError
from app.prompt_builder import JSON_OBJECT_SEGMENT_CONTRACTS, PromptBuilder
from app.schemas.ai_blocks import UnifiedGenerationRequest, TargetContext


# 用户线上实际报错的 DeepSeek 单行 ProgramIR 样本（在两处 substacks 之前各漏 1 个 }）
MALFORMED_PROGRAM_JSON = (
    '{"schema_version":2,"summary":"在角色1（小猫）上重写游戏主逻辑","remove_script_ids":[],"variables":[],"lists":[],"broadcasts":[],"procedures":[],"scripts":['
    '{"id":"script_cat_init","purpose":"初始化","x":40,"y":40,"blocks":[{"opcode":"event_whenflagclicked"},{"opcode":"looks_show"}]},'
    '{"id":"script_cat_left","purpose":"左移","x":40,"y":280,"blocks":[{"opcode":"event_whenkeypressed","fields":{"KEY_OPTION":"left arrow"}},'
    '{"opcode":"control_if","inputs":{"CONDITION":{"kind":"block","block":{"opcode":"operator_and","inputs":{"OPERAND1":{"kind":"block","block":{"opcode":"operator_equals","inputs":{"OPERAND1":{"kind":"block","block":{"opcode":"data_variable","fields":{"VARIABLE":{"kind":"state_ref","state_kind":"variable","ref_id":"state_game_over"}}}},"OPERAND2":{"kind":"text","value":"false"}}}},"OPERAND2":{"kind":"block","block":{"opcode":"operator_gt","inputs":{"OPERAND1":{"kind":"block","block":{"opcode":"motion_xposition"}},"OPERAND2":{"kind":"number","value":-200}}}}}}},"substacks":{"SUBSTACK":[{"opcode":"motion_changexby","inputs":{"DX":{"kind":"number","value":-20}}}]}}]},'
    '{"id":"script_cat_right","purpose":"右移","x":420,"y":280,"blocks":[{"opcode":"event_whenkeypressed","fields":{"KEY_OPTION":"right arrow"}},'
    '{"opcode":"control_if","inputs":{"CONDITION":{"kind":"block","block":{"opcode":"operator_and","inputs":{"OPERAND1":{"kind":"block","block":{"opcode":"operator_equals","inputs":{"OPERAND1":{"kind":"block","block":{"opcode":"data_variable","fields":{"VARIABLE":{"kind":"state_ref","state_kind":"variable","ref_id":"state_game_over"}}}},"OPERAND2":{"kind":"text","value":"false"}}}},"OPERAND2":{"kind":"block","block":{"opcode":"operator_lt","inputs":{"OPERAND1":{"kind":"block","block":{"opcode":"motion_xposition"}},"OPERAND2":{"kind":"number","value":200}}}}}}},"substacks":{"SUBSTACK":[{"opcode":"motion_changexby","inputs":{"DX":{"kind":"number","value":20}}}]}}]},'
    '{"id":"script_cat_catch","purpose":"碰到红包加分","x":40,"y":520,"blocks":[{"opcode":"event_whenflagclicked"},{"opcode":"looks_show"}]}'
    ']}'
)


def test_robust_json_parse_valid():
    sample = '{"schema_version": 2, "summary": "test", "scripts": []}'
    parsed = robust_json_parse(sample)
    assert parsed["schema_version"] == 2
    assert parsed["summary"] == "test"


def test_robust_json_parse_recovers_missing_substacks_braces():
    # 验证原生 json.loads 会报错
    with pytest.raises(json.JSONDecodeError):
        json.loads(MALFORMED_PROGRAM_JSON)

    # 验证 robust_json_parse 能够自愈解析出全部数据
    repaired = robust_json_parse(MALFORMED_PROGRAM_JSON)
    assert isinstance(repaired, dict)
    assert repaired["schema_version"] == 2
    assert len(repaired["scripts"]) == 4
    script_ids = [s["id"] for s in repaired["scripts"]]
    assert script_ids == ["script_cat_init", "script_cat_left", "script_cat_right", "script_cat_catch"]


def test_robust_json_parse_recovers_unclosed_tail():
    truncated = '{"schema_version": 2, "summary": "incomplete", "scripts": [{"id": "s1"'
    repaired = robust_json_parse(truncated)
    assert isinstance(repaired, dict)
    assert repaired["schema_version"] == 2
    assert repaired["scripts"][0]["id"] == "s1"


def test_repair_hint_for_json_decode_error():
    # 模拟实际出现的 Expecting ',' delimiter
    err_detail = "Expecting ',' delimiter: line 1 column 2097 (char 2096)"
    hint = repair_hint("program", "/output", err_detail)
    assert "花括号 '}' 闭合不足或缺失逗号" in hint
    assert "Pretty-printed JSON" in hint
    assert "嵌套 control_if" in hint


def test_program_prompt_constraints():
    builder = PromptBuilder()
    request = UnifiedGenerationRequest(
        session_id="s1",
        request_id="r1",
        messages=[{"role": "user", "content": "做小猫接红包"}],
        scope="project",
        target=TargetContext(name="Cat", is_stage=False, has_scripts=False, mode="append"),
        project_context={
            "project_id": "p1",
            "version": 1,
            "targets": [
                {"target_id": "stage", "revision": 1, "name": "Stage", "is_stage": True, "has_scripts": False, "mode": "append"},
                {"target_id": "sprite1", "revision": 1, "name": "Cat", "is_stage": False, "has_scripts": False, "mode": "append"},
            ],
            "shared_state": {},
        },
    )
    messages, _ = builder.build_segment_prompt(
        None, {"name": "Cat", "is_stage": False}, request, "program", context={"transport": "json_object"}, target_index=0
    )
    all_content = "\n".join(m["content"] for m in messages)
    assert "Pretty-printed JSON" in all_content or "带缩进与换行" in all_content
    assert "control_if" in all_content
    assert "substacks" in all_content
