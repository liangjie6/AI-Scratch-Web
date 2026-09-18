from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.orchestration.error_messages import repair_hint
from app.orchestration.run_manager import _program_generation_layers
from app.prompt_builder import PromptBuilder
from app.schemas.ai_blocks import (
    ProjectBlueprint,
    ProjectManifest,
    TargetRef,
    UnifiedGenerationRequest,
    TargetContext,
)


def make_blueprint_payload(cyclic: bool = False):
    # 模拟用户线上生成的 Blueprint
    behavior_graph = [
        {"behavior_id": "b_init_cat", "owner_entity_id": "entity_cat"},
        {"behavior_id": "b_cat_left", "owner_entity_id": "entity_cat"},
        {"behavior_id": "b_cat_right", "owner_entity_id": "entity_cat"},
        {"behavior_id": "b_milk_spawn", "owner_entity_id": "entity_milk"},
        {"behavior_id": "b_milk_fall", "owner_entity_id": "entity_milk", "depends_on": ["b_milk_spawn"]},
        # 如果 cyclic=True，则 b_milk_catch 依赖 b_init_cat（Milk 依赖 Cat）
        # 同时 b_game_over 依赖 b_milk_catch（Cat 依赖 Milk） -> 产生 Job 环！
        {"behavior_id": "b_milk_catch", "owner_entity_id": "entity_milk", "depends_on": ["b_milk_spawn", "b_init_cat"] if cyclic else ["b_milk_spawn"]},
        {"behavior_id": "b_game_over", "owner_entity_id": "entity_cat", "depends_on": ["b_milk_catch"] if cyclic else ["b_init_cat"]},
    ]
    return {
        "schema_version": 2,
        "task_spec_hash": "a" * 64,
        "entity_bindings": [
            {"entity_id": "entity_stage", "resolution": {"kind": "existing_target", "target_ref": {"kind": "stage"}}},
            {"entity_id": "entity_cat", "resolution": {"kind": "existing_target", "target_ref": {"kind": "current_target"}}},
            {"entity_id": "entity_milk", "resolution": {"kind": "new_sprite", "need_id": "need_milk_sprite"}},
        ],
        "asset_needs": [
            {
                "need_id": "need_milk_sprite",
                "kind": "sprite",
                "purpose": "Milk",
                "search_terms": ["Milk"],
                "fallback_order": ["scratch_library"],
                "owner_entity_id": "entity_milk",
            }
        ],
        "state_plan": [
            {"ref_id": "score", "kind": "variable", "scope": "global", "name": "score", "initial_value": 0}
        ],
        "behavior_graph": behavior_graph,
        "program_jobs": [
            {"job_id": "job_cat", "owner_entity_id": "entity_cat", "mode": "rewrite", "behavior_ids": ["b_init_cat", "b_cat_left", "b_cat_right", "b_game_over"]},
            {"job_id": "job_milk", "owner_entity_id": "entity_milk", "mode": "append", "behavior_ids": ["b_milk_spawn", "b_milk_fall", "b_milk_catch"]},
        ],
        "acceptance_tests": [],
    }


def test_blueprint_detects_job_level_cyclic_dependency():
    # 有环时应该被 ProjectBlueprint 校验拦截
    cyclic_payload = make_blueprint_payload(cyclic=True)
    with pytest.raises(ValidationError) as exc_info:
        ProjectBlueprint.model_validate(cyclic_payload)
    assert "blueprint program jobs dependencies cannot contain a cycle" in str(exc_info.value)

    # 无环时正常通过校验
    acyclic_payload = make_blueprint_payload(cyclic=False)
    blueprint = ProjectBlueprint.model_validate(acyclic_payload)
    assert blueprint.schema_version == 2


def test_repair_hint_for_job_cycle_error():
    hint1 = repair_hint("blueprint", "/output", "blueprint program jobs dependencies cannot contain a cycle")
    assert "循环依赖" in hint1
    assert "删除跨角色的 depends_on" in hint1

    hint2 = repair_hint("blueprint", "/output", "项目程序行为依赖存在循环")
    assert "循环依赖" in hint2
    assert "共享变量" in hint2 or "广播" in hint2


def test_program_generation_layers_self_heals_when_cycle_occurs():
    # 构造含环的 manifest 与 blueprint
    # 模拟极端情况下下游接收到了有环 Blueprint
    # 验证 _program_generation_layers 能够降级自愈返回包含所有 target 的 layers
    class DummyNode:
        def __init__(self, b_id, deps):
            self.behavior_id = b_id
            self.depends_on = deps

    class DummyBlueprint:
        behavior_graph = [
            DummyNode("b_cat", ["b_milk"]),
            DummyNode("b_milk", ["b_cat"]),
        ]

    class DummyTarget:
        def __init__(self, behavior_ids):
            self.behavior_ids = behavior_ids

    class DummyManifest:
        program_targets = [
            DummyTarget(["b_cat"]),
            DummyTarget(["b_milk"]),
        ]

    layers = _program_generation_layers(DummyBlueprint(), DummyManifest())
    # 验证自愈后所有 target 索引 0, 1 均被包含在图层中
    flattened = [idx for layer in layers for idx in layer]
    assert sorted(flattened) == [0, 1]


def test_prompt_builder_contains_cross_role_cycle_constraint():
    builder = PromptBuilder()
    request = UnifiedGenerationRequest(
        session_id="s1",
        request_id="r1",
        messages=[{"role": "user", "content": "接红包小游戏"}],
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
        None, {"name": "Cat", "is_stage": False}, request, "blueprint", context={"transport": "json_object"}
    )
    all_content = "\n".join(m["content"] for m in messages)
    assert "严禁在不同角色" in all_content or "cross-job" in all_content or "循环依赖" in all_content
