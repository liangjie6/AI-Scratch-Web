import pytest
from app.schemas.ai_blocks import (
    ProjectBlueprint,
    ProjectManifest,
    normalize_project_blueprint_payload,
    validate_blueprint_manifest_lineage,
)
from app.prompt_builder import PromptBuilder
from app.orchestration.error_messages import repair_hint


def sample_blueprint_payload():
    return {
        "schema_version": 2,
        "task_spec_hash": "a" * 64,
        "asset_needs": [
            {
                "need_id": "need_milk",
                "kind": "sprite",
                "purpose": "红包角色",
                "owner_entity_id": "entity_packet",
                "search_terms": ["Milk", "红包"],
                "fallback_order": ["scratch_library"],
            }
        ],
        "entity_bindings": [
            {
                "entity_id": "entity_cat",
                "resolution": {
                    "kind": "existing_target",
                    "target_ref": {"kind": "existing_sprite", "name": "角色1"},
                },
            },
            {
                "entity_id": "entity_packet",
                "resolution": {
                    "kind": "new_sprite",
                    "need_id": "need_milk",
                },
            },
        ],
        "state_plan": [
            {
                "ref_id": "state_score",
                "name": "state_score",
                "kind": "variable",
                "scope": "global",
                "initial_value": 0,
            }
        ],
        "behavior_graph": [
            {
                "behavior_id": "b_cat_init",
                "owner_entity_id": "entity_cat",
                "depends_on": [],
            },
            {
                "behavior_id": "b_packet_spawn",
                "owner_entity_id": "entity_packet",
                "depends_on": [],
            },
        ],
        "program_jobs": [
            {
                "job_id": "job_cat",
                "owner_entity_id": "entity_cat",
                "mode": "rewrite",  # 模拟大模型输出 rewrite
                "behavior_ids": ["b_cat_init"],
            },
            {
                "job_id": "job_packet",
                "owner_entity_id": "entity_packet",
                "mode": "modify",   # 模拟大模型输出 modify
                "behavior_ids": ["b_packet_spawn"],
            },
        ],
        "acceptance_tests": [],
    }


def sample_manifest():
    return ProjectManifest.model_validate({
        "schema_version": 3,
        "scope": "project",
        "summary": "接红包小游戏",
        "assumptions": [],
        "required_extensions": [],
        "resource_ops": [
            {
                "op": "add_builtin_sprite",
                "op_id": "op_milk",
                "source_need_id": "need_milk",
                "owner_entity_id": "entity_packet",
                "name": "Milk",
                "x": 0,
                "y": 175,
                "size": 100,
                "source": {
                    "kind": "scratch_library",
                    "asset_type": "sprite",
                    "search_terms": ["Milk"],
                },
                "include_costumes": "all",
            }
        ],
        "shared_state": {
            "variables": [
                {
                    "ref_id": "state_score",
                    "name": "得分",
                    "scope": "global",
                    "value": 0,
                }
            ],
            "lists": [],
            "broadcasts": [],
        },
        "local_state": [],
        "program_targets": [
            {
                "job_id": "job_cat",
                "behavior_ids": ["b_cat_init"],
                "owner_entity_id": "entity_cat",
                "target": {"kind": "existing_sprite", "name": "角色1"},
                "mode": "append",
                "intent": "小猫初始化与移动",
            },
            {
                "job_id": "job_packet",
                "behavior_ids": ["b_packet_spawn"],
                "owner_entity_id": "entity_packet",
                "target": {"kind": "created_sprite", "op_id": "op_milk"},
                "mode": "append",
                "intent": "红包生成与飘落",
            },
        ],
    })


def test_normalize_project_blueprint_payload_converts_modes_to_append():
    raw = sample_blueprint_payload()
    normalized = normalize_project_blueprint_payload(raw)
    assert normalized["program_jobs"][0]["mode"] == "append"
    assert normalized["program_jobs"][1]["mode"] == "append"
    # 验证规范化后可通过 ProjectBlueprint 校验且模式已为 append
    blueprint = ProjectBlueprint.model_validate(normalized)
    assert blueprint.program_jobs[0].mode == "append"
    assert blueprint.program_jobs[1].mode == "append"


def test_lineage_validation_accepts_append_when_blueprint_had_rewrite():
    raw = sample_blueprint_payload()
    # 直接验证未经规范化的原始 blueprint（保留 mode: rewrite/modify）
    raw_blueprint = ProjectBlueprint.model_validate(raw)
    assert raw_blueprint.program_jobs[0].mode == "rewrite"
    assert raw_blueprint.program_jobs[1].mode == "modify"

    manifest = sample_manifest()
    # 即使 Blueprint 曾是 rewrite/modify，只要 Manifest 目标是合法且符合规范的 append，血缘检查必须兼容通过
    validate_blueprint_manifest_lineage(raw_blueprint, manifest)


def test_repair_hints_for_mode_conflicts():
    hint1 = repair_hint("manifest", "/output", "manifest program target must preserve Blueprint job fields")
    assert "mode 必须全部为 \"append\"" in hint1

    hint2 = repair_hint("manifest", "/program_targets/0/mode", "stage, existing_sprite and created_sprite programs must use append")
    assert "只能为 \"append\"" in hint2


def test_blueprint_prompt_contains_append_instruction():
    builder = PromptBuilder()
    from app.schemas.ai_blocks import UnifiedGenerationRequest, TargetContext
    request = UnifiedGenerationRequest(
        session_id="s1",
        request_id="r1",
        messages=[{"role": "user", "content": "接红包"}],
        scope="project",
        target=TargetContext(name="角色1", is_stage=False, has_scripts=False, mode="append"),
        project_context={
            "project_id": "p1",
            "version": 1,
            "targets": [
                {"target_id": "stage", "revision": 1, "name": "Stage", "is_stage": True, "has_scripts": False, "mode": "append"},
                {"target_id": "sprite1", "revision": 1, "name": "角色1", "is_stage": False, "has_scripts": False, "mode": "append"},
            ],
            "shared_state": {},
        },
    )
    messages_tagged, _ = builder.build_segment_prompt(None, {"name": "角色1", "is_stage": False}, request, "blueprint")
    assert "program_jobs 的 mode 必须严格为 \"append\"" in messages_tagged[-1]["content"]

    messages_json, _ = builder.build_segment_prompt(
        None, {"name": "角色1", "is_stage": False}, request, "blueprint", context={"transport": "json_object"}
    )
    assert "program_jobs 的 mode 必须严格为 \"append\"" in messages_json[-1]["content"]
