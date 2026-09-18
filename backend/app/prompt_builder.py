from __future__ import annotations

from dataclasses import dataclass
import hashlib
import json
from pathlib import Path
from typing import Any, Literal

from pydantic import TypeAdapter

from .orchestration.cache_manager import PromptCache
from .orchestration.error_messages import repair_hint
from .resource_contract import render_resource_operation_contract
from .schemas.ai_blocks import (
    Plan,
    ProgramIR,
    ProjectBlueprint,
    ProjectManifest,
    RequirementsResponse,
    ResourceOp,
    ScratchTaskSpec,
    UnifiedGenerationRequest,
)
from .scratch_block_catalog import BLOCK_CONTRACTS, extension_for_opcode


TASK_SPEC_ENUMERATION_CONTRACT = '''
TASKSPEC ENUMERATION CONTRACT (use exactly these values; do not invent synonyms)
- TaskEntity.role: "stage" | "sprite". Each item requires entity_id, role, description; existing_target_ref is optional.
- TaskTargetRef exact forms: {"kind":"current_target"}; {"kind":"stage"}; {"kind":"existing_sprite","name":string}. Never use a string in existing_target_ref.
- TaskState requires state_id, kind, scope. kind: "variable" | "list" | "broadcast"; scope: "global" | "local". A local item also requires owner_entity_id. A global item must omit owner_entity_id. A broadcast must be global and omit owner_entity_id and initial_value. A list initial_value is an array; a variable initial_value is a scalar.
- OperandSpec exact forms: literal={"kind":"literal","value":number|string|boolean}; state={"kind":"state","state_id":string}; attribute={"kind":"attribute","entity_id":string,"attribute":"x"|"y"|"direction"|"size"|"visible"}; timer={"kind":"timer"}; random={"kind":"random","min":number,"max":number}. Never replace an OperandSpec with a bare number/string/boolean.
- TriggerSpec.kind: "green_flag" | "key_pressed" | "sprite_clicked" | "broadcast_received" | "backdrop_switched" | "clone_started" | "timer_reached". Exact forms: {"kind":"green_flag"}; {"kind":"key_pressed","key":string}; {"kind":"sprite_clicked"}; {"kind":"broadcast_received","state_id":string}; {"kind":"backdrop_switched","asset_purpose":string}; {"kind":"clone_started"}; {"kind":"timer_reached","seconds":number}.
- ConditionSpec exact forms: compare requires kind,left,operator,right where operator is "eq"|"gt"|"gte"|"lt"|"lte"; touching requires kind,entity_id; touching_edge is {"kind":"touching_edge"}; key_down requires kind,key; logical requires kind,operator ("and"|"or"),operands (2..12 ConditionSpec items); not requires kind,operand (one ConditionSpec). Conditions are only entries in a behavior's conditions array or a loop's until field. Never put a condition object in actions, never use a condition kind as ActionSpec.kind, and never invent condition/body fields; loop children go only in loop.actions.
- ActionSpec.kind: "move_steps" | "set_axis" | "change_axis" | "go_to_entity" | "turn" | "point_towards" | "set_state" | "change_state" | "broadcast" | "use_asset" | "set_visibility" | "say" | "ask" | "create_clone" | "delete_this_clone" | "wait" | "loop" | "stop".
- ActionSpec exact required fields by kind: move_steps(steps); set_axis(axis,value); change_axis(axis,delta); go_to_entity(entity_id); turn(degrees); point_towards(entity_id); set_state(state_id,value); change_state(state_id,delta); broadcast(state_id); use_asset(asset_kind,purpose, optional wait); set_visibility(visible); say(text, optional seconds); ask(question); create_clone(entity_id); delete_this_clone(no other fields); wait(seconds); loop(mode,actions, plus count for repeat or until for repeat_until); stop(scope). axis is "x"|"y"; asset_kind is "costume"|"backdrop"|"sound"; loop mode is "forever"|"repeat"|"repeat_until"; stop scope is "this_script"|"all". All steps/value/delta/degrees/seconds/count fields are OperandSpec objects, but say.text, ask.question, use_asset.purpose and other text fields are plain strings; never wrap those strings in an OperandSpec.
- TaskBehavior requires behavior_id, owner_entity_id, trigger, actions (non-empty array); conditions, reads and writes are optional arrays. Nested actions belong only in a loop's actions field.
- SuccessCriterionSpec requires criterion_id, description, related_behavior_ids (non-empty), priority and verification. UnsupportedRequirement requires requirement and reason_code.
- SuccessCriterionSpec.priority: "must" | "should"; verification: "executable" | "structural" | "manual" | "unsupported".
- ProgramIR.schema_version: 1 | 2. Program input kind: "number" | "text" | "angle" | "color" | "boolean" | "any". Block shape: "hat" | "stack" | "cap" | "reporter" | "boolean".
- Program input reference kind: "block" | "resource_ref" | "existing_asset_ref" | "state_ref". Resource asset type: "sprite" | "costume" | "backdrop" | "sound". State reference kind: "variable" | "list" | "broadcast".
- Manifest.required_extensions: "pen" | "music" | "text2speech". Manifest scope: "project". Manifest program target mode: "append" | "modify" | "rewrite" (only current_target may use modify/rewrite). Resource source kind: "scratch_library" | "image_generation"; image sheet_layout: "1x1".
- Resource operation op: "add_builtin_sprite" | "add_sprite" | "delete_sprite" | "add_costume" | "add_backdrop" | "add_sound" | "select_backdrop" | "delete_sound". Scratch library source asset_type: "sprite" | "costume" | "backdrop" | "sound". Audio source kind: "existing_sound"; audio generation kind: "audio_generation", audio_kind: "sound_effect" | "speech" | "music".
- Acceptance trigger kind: "green_flag" | "key" | "sprite_click" | "broadcast"; arrange kind: "set_position" | "set_variable" | "set_list" | "set_timer"; expect kind: "variable" | "position_range" | "costume" | "backdrop" | "visibility" | "list" | "clone_count" | "say_text" | "question_asked" | "broadcast_emitted" | "sound_invoked" | "scripts_stopped". Expect comparator: "eq" | "gt" | "gte" | "lt" | "lte" | "equals" | "contains" | "length".
Do not emit description-only behaviors or fields not declared by the selected object kind.
'''.strip()


BLUEPRINT_ENUMERATION_CONTRACT = '''
BLUEPRINT CONDITIONAL FIELD CONTRACT (copy exact fields; omit unrelated fields)
- BlueprintAssetNeed requires need_id, kind ("sprite"|"costume"|"backdrop"|"sound"), purpose, non-empty search_terms, non-empty unique fallback_order ("project_asset"|"scratch_library"|"user_asset"|"image_generation"|"audio_generation"). A sprite need for a new_sprite binding MUST have owner_entity_id equal to that entity_id. constraints keys are only transparent, max_bytes, max_duration_ms, animation_frames.
- BlueprintEntityResolution exact forms: existing_target={"kind":"existing_target","target_ref":TaskTargetRef}; new_sprite={"kind":"new_sprite","need_id":string}. New-sprite need_id must reference a sprite need whose owner_entity_id is the bound entity_id.
- BlueprintStatePlan requires ref_id, kind ("variable"|"list"|"broadcast"), scope ("global"|"local"), name; local requires owner_entity_id, global omits owner_entity_id; broadcast is global and omits owner_entity_id and initial_value; list initial_value is an array, variable initial_value is scalar. Every TaskSpec state must appear in state_plan exactly once with ref_id equal to the exact TaskSpec state_id (copy state_id directly; never invent new IDs such as state_score or ref_1; name matches ref_id).
- BlueprintProgramJob requires job_id, owner_entity_id, mode ("append"|"modify"|"rewrite"; in project generation, all jobs for stage, existing_sprite and created_sprite must strictly use "append"), non-empty behavior_ids. Every TaskSpec behavior must occur in exactly one job. behavior_graph consumes_broadcasts and produces_broadcasts, when present, must contain the exact TaskSpec state_id values of broadcast actions/triggers (not broadcast display names, labels, or invented IDs), and must match the TaskSpec behavior's broadcast edges exactly. CRITICAL DEPENDENCY RULE: behavior_graph depends_on MUST form a strictly acyclic DAG across program_jobs! NEVER create mutual cross-job dependencies (e.g. entity_cat behaviors depending on entity_milk behaviors while entity_milk behaviors depend on entity_cat behaviors); cross-sprite interactions MUST decouple via state_plan or broadcasts, and depends_on should primarily express causal ordering within the same entity.
- AcceptanceTrigger exact required forms: green_flag={"kind":"green_flag"}; key={"kind":"key","key":string}; sprite_click={"kind":"sprite_click","entity_id":string}; broadcast={"kind":"broadcast","state_id":string}. AcceptanceArrange: set_position requires entity_id,x,y; set_variable requires state_id,value (scalar); set_list requires state_id,value (array); set_timer requires seconds.
- AcceptanceExpectation required fields: variable=(state_id,comparator,value), comparator "eq"|"gt"|"gte"|"lt"|"lte"; position_range=(entity_id,axis,min,max), axis "x"|"y"; costume=(entity_id,asset_need_id); backdrop=(asset_need_id); visibility=(entity_id,visible); list=(state_id,comparator,value), comparator "equals"|"contains"|"length"; clone_count=(entity_id,comparator,value), comparator "eq"|"gte"|"lte"; say_text=(entity_id,text); question_asked=(text); broadcast_emitted=(state_id,min_count); sound_invoked=(entity_id,asset_need_id,min_count); scripts_stopped has no extra required field. Never use min_count alone for clone_count: it must include comparator and numeric value.
- BlueprintAcceptanceTest requires test_id, source_criterion_id, non-empty related_behavior_ids, trigger, expect (non-empty), timeout_ms; total timeout across tests is at most 4000. Build a coverage checklist from TaskSpec.success_criteria before writing JSON: every criterion with verification exactly "executable" must appear exactly once as source_criterion_id, so the set and count of executable criterion IDs and acceptance_tests.source_criterion_id values must be identical. Never omit an executable criterion (including reset, collision, scoring, boundary, or game-over criteria). Never create an acceptance test for a manual, structural, or unsupported criterion; if there are no executable criteria, output acceptance_tests as [].
'''.strip()


_SEGMENT_SCHEMA_ADAPTERS: dict[str, TypeAdapter[Any]] = {
    'requirements': TypeAdapter(RequirementsResponse),
    'task_spec': TypeAdapter(ScratchTaskSpec),
    'blueprint': TypeAdapter(ProjectBlueprint),
    'plan': TypeAdapter(Plan),
    'manifest': TypeAdapter(ProjectManifest),
    'resource_op': TypeAdapter(ResourceOp),
    'program': TypeAdapter(ProgramIR),
}


def render_segment_json_schema(segment: str, *, allow_destructive_resource_ops: bool = False) -> str:
    """Render the canonical schema so prompt contracts cannot drift from validation."""
    adapter = _SEGMENT_SCHEMA_ADAPTERS.get(segment)
    if adapter is None:
        return ''
    schema = adapter.json_schema(mode='validation')
    if segment == 'resource_op' and not allow_destructive_resource_ops:
        blocked_refs = {'#/$defs/DeleteSpriteOp', '#/$defs/DeleteSoundOp'}
        schema['oneOf'] = [item for item in schema.get('oneOf', []) if item.get('$ref') not in blocked_refs]
        mapping = schema.get('discriminator', {}).get('mapping', {})
        for operation in ('delete_sprite', 'delete_sound'):
            mapping.pop(operation, None)
        for definition in ('DeleteSpriteOp', 'DeleteSoundOp'):
            schema.get('$defs', {}).pop(definition, None)
    return 'CANONICAL_OUTPUT_JSON_SCHEMA (all properties, enum values, required arrays and limits are authoritative):\n' + json.dumps(
        schema,
        ensure_ascii=False,
        sort_keys=True,
        separators=(',', ':'),
    )


UNIFIED_OUTPUT_CONTRACT = '''
FINAL OUTPUT CONTRACT (HIGHEST PRIORITY; OVERRIDES EARLIER TEXT)
The transport protocol for this request is a tagged stream, not one JSON document. Ignore any earlier sentence that says to output a single JSON object or a bare ProgramIR; that describes an internal payload, not the transport. Output no markdown, code fence, explanation, reasoning, or text outside the tags.

Output tags in this order: requirements, then task_spec, blueprint, plan, manifest only for project scope, then program tags. Every tag body is exactly one JSON object. The backend creates phase events; do not output phase_start or phase_complete tags.

The requirements object has exactly these keys: ready boolean, title string, goal string, behavior_steps string array, triggers string array, controls string array, success_criteria string array, and missing_questions string array. Always include all eight keys, using [] for an empty array. Do not wrap it in another property.
The task_spec object is ScratchTaskSpec v2. It is the execution meaning of the request. Required top-level keys are exactly schema_version, title, goal, assumptions, entities, state, behaviors, success_criteria, and unsupported (schema_version must be 2; use [] for optional arrays). Use stable entity_id, state_id, behavior_id and criterion_id values. Do not substitute UI-shaped fields such as name, entity_type, target, states, type, allowed_values, or entity_type_hint for the schema fields. Each assumptions item MUST be exactly {"id": string, "text": string, "confidence": number from 0 to 1, "requires_confirmation": boolean}; use numbers such as 0.95, never labels such as high/medium/low, and never omit requires_confirmation. Every success criterion must set verification to exactly one of executable, structural, manual or unsupported. Only executable criteria may have a behavior acceptance test; every executable criterion must have one. Its actions, triggers, conditions and operands must use only the schema's controlled vocabulary listed in TASKSPEC ENUMERATION CONTRACT. Put unsupported requests in unsupported, never in free-form action arguments.
'''+TASK_SPEC_ENUMERATION_CONTRACT+'''
The blueprint object is ProjectBlueprint v2. It preserves all TaskSpec IDs (specifically, state_plan must project every TaskSpec state exactly once with ref_id equal to the TaskSpec state_id, never invent new state IDs; name matches ref_id), has task_spec_hash supplied in context, assigns each entity exactly once, creates asset needs and one ProgramJob coverage for each behavior. Do not choose catalogue IDs, VM IDs, URLs, or binary assets. acceptance_tests use only the schema's discriminated trigger, arrange and expect kinds, and may reference executable TaskSpec criteria only; never emit trigger_kind, arbitrary expressions, JavaScript, target IDs, asset IDs or URLs. Reference TaskSpec entity_id/state_id and Blueprint need_id only. The combined acceptance timeout is at most 4000ms.
'''+BLUEPRINT_ENUMERATION_CONTRACT+'''
The plan object has exactly one key, steps, whose value is a non-empty array of strings and is display-only.
For project scope, the manifest object is ProjectManifest v3. It contains assumptions, required_extensions, resource_ops, shared_state, local_state, and program_targets. Every new resource has source_need_id; a created sprite also has owner_entity_id. Every program target has job_id, behavior_ids, owner_entity_id, target, mode, and intent exactly matching the Blueprint. It must not contain programs or VM block graphs. required_extensions may only contain IDs explicitly permitted by the runtime context below; otherwise it is [].

Resource operations follow the generated RESOURCE_OPERATION_CONTRACT appended below. Use schema_version 3 operation shapes exactly. Never invent URLs, hashes, filenames, catalogue IDs, or sound bytes. Resource op_id values must be unique; a created_sprite may only reference an earlier add_sprite or add_builtin_sprite; stage, existing_sprite and created_sprite program_targets must use mode "append". Audio generation is recognized by schema but disabled: use Scratch library sound sources.

Each program tag is <program target_index=N>...</program>. Its children, in order, are metadata, shared_state, and script tags. The metadata object has schema_version 1 for standalone programs or 2 for a project program using logical resources, plus summary string, remove_script_ids string array, and procedures array. A procedure has id procedure_N, label without %, parameters (id arg_N, name, kind string_number|boolean, optional scalar default), warp boolean and a non-empty body block array. Procedures cannot call procedures, and argument reporters can only appear in their own body. A ProgramIR v2 uses {"kind":"resource_ref","ref":{"op_id":"<manifest resource op_id>","value":"installed_name"}} for manifest resources and {"kind":"state_ref","state_kind":"variable"|"list"|"broadcast","ref_id":"<manifest state ref_id>"} in a resolved block field. It may use existing_asset_ref only for an existing menu item. The shared_state object in a project v2 program MUST be {"variables":[],"lists":[],"broadcasts":[]}; all state comes from the manifest. Each script tag has index and total attributes; its JSON object has exactly id, purpose, x, y, and blocks. The id must match script_[A-Za-z0-9_-]+.

Each normal block object may contain only opcode, fields, inputs, and substacks. A procedure call uses opcode procedures_call, procedure_id and inputs keyed by declared arg_N IDs. A procedure argument reporter uses opcode argument_reporter_string_number or argument_reporter_boolean plus argument_id, and must be nested in its own procedure body. Never output mutation, proccode, next, parent, shadow, topLevel, generated block IDs, or any other VM/project.json fields. fields, inputs, and substacks are JSON objects; when empty, write {} rather than [] or null. In particular, a block with no substacks must use substacks: {}, never substacks: []. Substack values are arrays and their keys are only SUBSTACK or SUBSTACK2. Use only input kinds and opcode contracts in BLOCK_CATALOG. The first block of every script is an event hat; reporter and boolean blocks appear only as nested input blocks. Output formatted JSON with indentation and newlines (never single-line compressed JSON). Keep condition logic flat: never nest compound boolean operators deeply inside inputs.CONDITION (split multiple conditions into nested control_if blocks); before writing 'substacks', carefully verify that all nested block objects and inputs have fully closed their '}' braces.

In TARGET_CONTEXT and PROJECT_CONTEXT, costumes lists are existing role costume names, backdrops lists are existing stage backdrop names, sounds lists are existing sounds owned by that target, and sprite_names lists are existing original role names. Use existing_asset_ref only with an exact name from the correct list and asset_type; use resource_ref only for a resource operation declared in the project Manifest.

Start with <requirements> and end with the final </program>.
'''


JSON_OBJECT_SEGMENT_CONTRACTS: dict[str, str] = {
    'requirements': 'The JSON object is RequirementsResponse. It must contain exactly ready, title, goal, behavior_steps, triggers, controls, success_criteria, and missing_questions; all array fields are arrays of strings.',
    'task_spec': 'The JSON object is ScratchTaskSpec schema_version=2. Required top-level keys are exactly schema_version, title, goal, assumptions, entities, state, behaviors, success_criteria, unsupported. Each assumptions item is exactly {id:string, text:string, confidence:number 0..1, requires_confirmation:boolean}; confidence is numeric (for example 0.95), never high/medium/low. Use schema field names, not legacy name/entity_type/target/states/type/allowed_values/entity_type_hint. All enum values and required object fields are listed in TASKSPEC ENUMERATION CONTRACT in the system contract; copy those values exactly. Each success criterion declares verification as executable, structural, manual or unsupported; do not use VM or asset IDs.',
    'blueprint': 'The JSON object is ProjectBlueprint schema_version=2. task_spec_hash must equal the canonical TaskSpec hash in context. It must preserve TaskSpec IDs and exactly cover entities, state, and behaviors (every TaskSpec state must appear in state_plan exactly once, where ref_id equals the exact TaskSpec state_id; never invent IDs like state_score; name matches ref_id). All program_jobs must use mode="append" (stage, existing_sprite and created_sprite must use append). CRITICAL DEPENDENCY RULE: behavior_graph depends_on MUST form a strictly acyclic DAG across program_jobs (no cross-job circular dependencies where Job A depends on Job B and Job B depends on Job A; decouple cross-entity interactions via state_plan or broadcasts). Before output, build a criterion checklist: every acceptance_test.source_criterion_id must reference a criterion whose related_behavior_ids contain every acceptance_test.related_behavior_ids item; never include an unrelated behavior. acceptance_tests must use the typed trigger/arrange/expect DSL only and can reference executable TaskSpec criteria only, with no trigger_kind or arbitrary objects. Follow BLUEPRINT CONDITIONAL FIELD CONTRACT and the canonical schema exactly.',
    'plan': 'The JSON object is Plan. It must contain exactly steps, a non-empty array of strings. Each step must be no more than 120 characters; summarize implementation details instead of copying the full behavior.',
    'manifest': 'The JSON object is ProjectManifest v3. It contains schema_version=3, scope=project, summary, assumptions, required_extensions, resource_ops, shared_state, local_state, and program_targets. Preserve Blueprint need_id, job_id, behavior_ids, owner_entity_id, and state ref_id lineage exactly.',
    'resource_op': 'The JSON object is exactly one resource operation to replace the selected resource_ops entry. It must match RESOURCE_OPERATION_CONTRACT with no extra or legacy fields.',
    'program': 'The JSON object is the complete ProgramIR object for this segment. It has schema_version=1 or schema_version=2, summary, remove_script_ids, variables, lists, broadcasts, procedures, and scripts. Procedures are constrained declarations; the runtime builds their Scratch mutation. Project ProgramIR v2 has empty variables/lists/broadcasts and uses resource_ref.ref plus state_ref block fields instead of guessed names. Scripts contain id, purpose, x, y, and blocks. CRITICAL FORMAT & AST RULES: Output pretty-printed JSON with indentation and newlines; DO NOT compress into a single line. Keep block logic flat: DO NOT deeply nest compound operators (e.g. no operator_and containing multiple nested comparator blocks inside inputs.CONDITION); split multiple conditions into nested control_if blocks. Double-check all closing "}" braces in inputs and nested blocks before writing the "substacks" key. Before output, run this checklist: every input object has an explicit kind; numeric literals use kind="number" and text literals use kind="text" (never null, omitted, or a guessed kind). For project v2, variables/lists/broadcasts MUST each be [] and every shared/local state access MUST use a state_ref with the exact manifest ref_id; never repeat manifest names in those arrays. Re-check the complete object against the canonical schema before sending.',
}


@dataclass(frozen=True)
class ProjectContext:
    project_id: str = 'scratch-project'
    version: int = 0
    targets: tuple[dict[str, Any], ...] = ()
    shared_state: dict[str, Any] | None = None


@dataclass(frozen=True)
class TargetContext:
    target_id: str = 'current-target'
    revision: int = 0
    name: str = ''
    is_stage: bool = False
    has_scripts: bool = False
    mode: str = 'append'
    costumes: tuple[str, ...] = ()
    backdrops: tuple[str, ...] = ()
    sounds: tuple[str, ...] = ()
    sprite_names: tuple[str, ...] = ()
    current_program: str = '{}'


@dataclass(frozen=True)
class CacheMetadata:
    keys: dict[str, str]
    hits: dict[str, bool]

    @property
    def estimated_hit_rate(self) -> float:
        return sum(self.hits.values()) / len(self.hits) if self.hits else 0.0

    def as_dict(self) -> dict[str, Any]:
        return {
            'keys': dict(self.keys),
            'hits': dict(self.hits),
            'estimated_hit_rate': self.estimated_hit_rate,
        }


SegmentName = Literal['requirements', 'task_spec', 'blueprint', 'plan', 'manifest', 'resource_op', 'program']


class PromptBuilder:
    """Build one stable system prefix plus request-specific conversation data."""

    KNOWLEDGE_BASE_VERSION = 'scratch-ai-v2'
    CACHE_TTL_SECONDS = 300

    def __init__(
        self,
        cache: PromptCache | None = None,
        knowledge_base: str | None = None,
        enabled_extensions: frozenset[str] = frozenset(),
        allow_destructive_resource_ops: bool = False,
    ) -> None:
        self.cache = cache if cache is not None else PromptCache()
        self._knowledge_base_override = knowledge_base
        self._enabled_extensions = enabled_extensions
        self._allow_destructive_resource_ops = allow_destructive_resource_ops

    def build_unified_prompt(
        self,
        project_context: ProjectContext | dict[str, Any] | None,
        target_context: TargetContext | dict[str, Any],
        request: UnifiedGenerationRequest,
        *,
        transport: Literal['tagged', 'json_object'] = 'tagged',
        segment: SegmentName | None = None,
    ) -> tuple[list[dict[str, str]], CacheMetadata]:
        project = self._project(project_context)
        target = self._target(target_context)
        enabled_extensions = self._enabled_extensions_for_request(request)
        level1_key = f'knowledge:{self.KNOWLEDGE_BASE_VERSION}:extensions={",".join(sorted(enabled_extensions))}'
        level1, level1_hit = self.cache.get_or_build(level1_key, lambda: self._build_knowledge_base(enabled_extensions), self.CACHE_TTL_SECONDS)
        level2_key = self._context_key('project', project.project_id, project.version, project)
        level2, level2_hit = self.cache.get_or_build(level2_key, lambda: self._build_project_context(project), self.CACHE_TTL_SECONDS)
        level3_key = self._context_key('target', target.target_id, target.revision, target)
        level3, level3_hit = self.cache.get_or_build(level3_key, lambda: self._build_target_context(target), self.CACHE_TTL_SECONDS)
        level4 = self._build_request_context(request)
        system = chr(10).join((
            level1,
            level2,
            level3,
            level4,
            self._build_unified_output_contract(request, enabled_extensions, transport=transport, segment=segment),
        ))
        messages = [{'role': 'system', 'content': system}]
        messages.extend({'role': item.role, 'content': item.content} for item in request.messages)
        return messages, CacheMetadata(
            keys={'knowledge_base': level1_key, 'project': level2_key, 'target': level3_key},
            hits={'knowledge_base': level1_hit, 'project': level2_hit, 'target': level3_hit},
        )


    def build_segment_prompt(
        self,
        project_context: ProjectContext | dict[str, Any] | None,
        target_context: TargetContext | dict[str, Any],
        request: UnifiedGenerationRequest,
        segment: SegmentName,
        context: dict[str, Any] | None = None,
        *,
        target_index: int | None = None,
        resource_op_index: int | None = None,
    ) -> tuple[list[dict[str, str]], CacheMetadata]:
        transport = 'json_object' if context and context.get('transport') == 'json_object' else 'tagged'
        messages, cache = self.build_unified_prompt(project_context, target_context, request, transport=transport, segment=segment)
        context_json = json.dumps(context or {}, ensure_ascii=False, separators=(',', ':'))
        details = {
            'requirements': '输出且只输出一个 <requirements>JSON</requirements> 标签。',
            'task_spec': '输出且只输出一个 <task_spec>JSON</task_spec> 标签；不要重出 requirements、blueprint、plan 或其他标签。TaskSpec 中条件只能出现在 behavior.conditions 或 loop.until，touching_edge 不是 action，禁止把它放入 actions。',
            'blueprint': '输出且只输出一个 <blueprint>JSON</blueprint> 标签；先建立成功标准覆盖清单：每个 verification=executable 的 criterion_id 必须在 acceptance_tests.source_criterion_id 中恰好出现一次，数量和集合必须完全相等，不能遗漏结束、碰撞、计分等标准；manual/structural/unsupported 标准禁止生成验收测试。behavior_graph 的 consumes_broadcasts/produces_broadcasts 只能填写 TaskSpec 广播的 state_id，并且必须与对应 broadcast action/trigger 完全匹配，不能填写广播显示名称。state_plan 必须且只能投影 TaskSpec.state 中的每一个状态，集合与数量必须严格完全一致；每个 state_plan 项的 ref_id 必须严格等于对应 TaskSpec 的 state_id（例如 state_id 为“得分”，则 ref_id 必须直接填“得分”，禁止自创新英文 ID 如 state_score 或 ref_score；name 与 ref_id 保持一致），kind、scope、owner_entity_id 必须与 TaskSpec 保持完全一致。严禁在不同角色行为之间形成互相循环依赖（例如角色1行为依赖角色2，角色2行为又依赖角色1；跨角色玩法联动一律通过共享变量或广播解耦，depends_on 仅用于同一角色内部前后关系，确保 program_jobs 之间拓扑无环）。program_jobs 的 mode 必须严格为 "append"（项目模式下舞台、已有角色和新建角色一律使用 "append"，严禁使用 rewrite 或 modify）。',
            'plan': '输出且只输出一个 <plan>JSON</plan> 标签，不要重出 requirements。steps 每项最多 120 个字符。',
            'manifest': '输出且只输出一个 <manifest>JSON</manifest> 标签，不要输出 requirements、plan 或 program。',
            'resource_op': '输出且只输出一个 <resource_op>JSON</resource_op> 标签。该对象替换 resource_ops 中的指定位置。',
            'program': f'输出且只输出一个 target_index={target_index if target_index is not None else 0} 的完整 <program> 标签，不要输出其他目标。必须使用带换行与标准缩进的格式化 JSON 输出，严禁单行压缩；严禁在 inputs.CONDITION 中嵌套多层复合逻辑（多条件必须拆为嵌套 control_if 或分步判断）；在输出 substacks 前仔细核对 inputs 嵌套 block 的所有闭合花括号。',
        }
        if context and context.get('transport') == 'json_object':
            details.update({
                'requirements': '只输出一个 JSON object，内容是 requirements 对象。',
                'task_spec': '只输出一个 JSON object，内容是 ScratchTaskSpec 对象；不要重出其他 segment。TaskSpec 中条件只能出现在 behavior.conditions 或 loop.until，touching_edge 不是 action，禁止把它放入 actions。',
                'blueprint': '只输出一个 JSON object，内容是 ProjectBlueprint 对象；先建立成功标准覆盖清单：每个 verification=executable 的 criterion_id 必须在 acceptance_tests.source_criterion_id 中恰好出现一次，数量和集合必须完全相等，不能遗漏结束、碰撞、计分等标准；manual/structural/unsupported 标准禁止生成验收测试。behavior_graph 的 consumes_broadcasts/produces_broadcasts 只能填写 TaskSpec 广播的 state_id，并且必须与对应 broadcast action/trigger 完全匹配，不能填写广播显示名称。state_plan 必须且只能投影 TaskSpec.state 中的每一个状态，集合与数量必须严格完全一致；每个 state_plan 项的 ref_id 必须严格等于对应 TaskSpec 的 state_id（例如 state_id 为“得分”，则 ref_id 必须直接填“得分”，禁止自创新英文 ID 如 state_score 或 ref_score；name 与 ref_id 保持一致），kind、scope、owner_entity_id 必须与 TaskSpec 保持完全一致。严禁在不同角色行为之间形成互相循环依赖（例如角色1行为依赖角色2，角色2行为又依赖角色1；跨角色玩法联动一律通过共享变量或广播解耦，depends_on 仅用于同一角色内部前后关系，确保 program_jobs 之间拓扑无环）。program_jobs 的 mode 必须严格为 "append"（项目模式下舞台、已有角色和新建角色一律使用 "append"，严禁使用 rewrite 或 modify）。',
                'plan': '只输出一个 JSON object，内容是 plan 对象，不要重出 requirements。steps 每项最多 120 个字符。',
                'manifest': '只输出一个 JSON object，内容是 manifest 对象，不要输出 requirements、plan 或 program。',
                'resource_op': '只输出一个 JSON object，内容是要替换的 resource_op 对象。',
                'program': '只输出一个 JSON object，内容是当前 target_index 的完整 ProgramIR 对象，不要输出其他目标。必须使用带换行与标准缩进的漂亮格式（Pretty-printed JSON）输出，禁止单行无换行压缩；保持积木结构扁平直观，严禁在 inputs.CONDITION 中进行深层复合逻辑嵌套（多条件必须拆分为嵌套 control_if）；输出 "substacks" 前仔细核查 inputs 嵌套积木的所有闭合花括号是否完整。',
            })
        target_note = '' if target_index is None else f' target_index={target_index}.'
        operation_note = '' if resource_op_index is None else f' resource_op_index={resource_op_index}.'
        output_boundary = '标签外文本' if transport == 'tagged' else 'JSON object 之外的文本'
        failure = context.get('failure') if isinstance(context, dict) else None
        failure_note = ''
        if isinstance(failure, dict):
            detail = failure.get('detail')
            message = failure.get('message')
            hint = repair_hint(segment, failure.get('path'), detail)
            if failure.get('path') == '/output' and not detail:
                failure_note = '\n上一轮结构化响应正文为空或不是 JSON；本轮必须实际输出非空 JSON object，不能只输出 reasoning。'
            elif hint:
                failure_note = f'\n上一轮输出未通过校验：{detail or message}。\n修复指导：{hint}'
            elif detail or message:
                failure_note = f'\n上一轮输出未通过校验：{detail or message}。请严格对照契约修正。'
        instruction = (
            'SEGMENT OUTPUT CONTRACT\n'
            + details[segment]
            + target_note
            + operation_note
            + '\n必要的已通过上下文（只用于生成本段）：'
            + context_json
            + f'\n禁止 Markdown、解释、代码围栏和{output_boundary}。'
            + failure_note
        )
        messages.append({'role': 'system', 'content': instruction})
        return messages, cache

    def build_chat_messages(self, request: Any) -> list[dict[str, str]]:
        system = (
            '你是 Scratch 项目需求助手。只回答用户问题，不生成 ProgramIR，不修改项目。'
            '回答简洁,具体，并指出当前 Scratch 原生能力的限制。'
        )
        target = request.target.model_dump(mode='json')
        target_context = '[TARGET_CONTEXT]' + chr(10) + json.dumps(target, ensure_ascii=False, separators=(',', ':'))
        return [{'role': 'system', 'content': system + chr(10) + target_context}] + [
            {'role': item.role, 'content': item.content} for item in request.messages
        ]

    def _enabled_extensions_for_request(self, request: UnifiedGenerationRequest) -> frozenset[str]:
        return frozenset(
            extension for extension in self._enabled_extensions
            if extension != 'text2speech' or extension in request.permitted_extensions
        )

    def _build_unified_output_contract(
        self,
        request: UnifiedGenerationRequest,
        enabled_extensions: frozenset[str],
        *,
        transport: Literal['tagged', 'json_object'] = 'tagged',
        segment: SegmentName | None = None,
    ) -> str:
        if request.scope == 'single_sprite' and transport == 'tagged':
            return chr(10).join((
                'FINAL OUTPUT CONTRACT (HIGHEST PRIORITY; OVERRIDES EARLIER TEXT)',
                'Output tags in this exact order: requirements, plan, then one <program target_index=0>. Every tag body is exactly one JSON object. Output no text outside tags.',
                JSON_OBJECT_SEGMENT_CONTRACTS['requirements'],
                JSON_OBJECT_SEGMENT_CONTRACTS['plan'],
                JSON_OBJECT_SEGMENT_CONTRACTS['program'],
                render_resource_operation_contract(self._allow_destructive_resource_ops),
            ))
        if request.scope == 'project':
            scope_note = (
                'This request has scope=project. Emit task_spec, blueprint, display plan, then manifest; afterwards emit one program for each entry in manifest.program_targets. '
                'Use target_index values 0, 1, 2, ... in exactly that order.'
            )
        else:
            scope_note = (
                'This request has scope=single_sprite. Do not emit a manifest. Emit exactly one program and its target_index must be 0.'
            )
        if transport == 'json_object':
            segment_note = segment or 'current'
            segment_contract = JSON_OBJECT_SEGMENT_CONTRACTS.get(segment, 'The JSON object must match the schema for the current segment.')
            if segment == 'task_spec':
                segment_contract = segment_contract + chr(10) + TASK_SPEC_ENUMERATION_CONTRACT
            if segment == 'blueprint':
                segment_contract = segment_contract + chr(10) + BLUEPRINT_ENUMERATION_CONTRACT
            return chr(10).join((
                'FINAL OUTPUT CONTRACT (HIGHEST PRIORITY; OVERRIDES EARLIER TEXT)',
                f'The transport for this {segment_note} segment is one JSON object, not a tagged stream. Output exactly one JSON object for this segment and nothing else.',
                'Do not output XML-like tags, Markdown, code fences, explanations, reasoning, or any text outside the JSON object.',
                segment_contract,
                render_segment_json_schema(
                    segment_note,
                    allow_destructive_resource_ops=self._allow_destructive_resource_ops,
                ),
                render_resource_operation_contract(self._allow_destructive_resource_ops),
                scope_note + ' Generate only the current segment; do not emit any other segment.',
            ))
        extension_note = (
            'Enabled built-in extension IDs for this request: ' + json.dumps(sorted(enabled_extensions))
            + '. Do not use any other extension ID or extension opcode.'
        )
        schema_contract = render_segment_json_schema(
            segment,
            allow_destructive_resource_ops=self._allow_destructive_resource_ops,
        ) if segment else ''
        return chr(10).join((UNIFIED_OUTPUT_CONTRACT.strip(), schema_contract, render_resource_operation_contract(self._allow_destructive_resource_ops), extension_note, scope_note))

    def _build_knowledge_base(self, enabled_extensions: frozenset[str]) -> str:
        if self._knowledge_base_override is not None:
            source = self._knowledge_base_override
        else:
            path = Path(__file__).resolve().parents[2] / 'docs' / '参考资料' / 'ProgramIR-v1生成提示词.md'
            source = path.read_text(encoding='utf-8').strip() if path.exists() else ''
        catalog = {
            opcode: {
                'shape': contract.shape,
                'targets': sorted(contract.targets),
                'fields': sorted(contract.fields),
                'resolved_fields': sorted(contract.resolved_fields),
                'inputs': dict(contract.inputs),
                'substacks': sorted(contract.substacks),
            }
            for opcode, contract in sorted(BLOCK_CONTRACTS.items())
            if extension_for_opcode(opcode) is None or extension_for_opcode(opcode) in enabled_extensions
        }
        protocol = '''
统一生成内容协议（实际传输由 system prompt 末尾的 FINAL OUTPUT CONTRACT 决定）：
- tagged stream：项目模式按 requirements、task_spec、blueprint、plan、manifest 和 program 标签顺序输出；每个标签内部是一个严格 JSON 对象。
- JSON object：当前 segment 只输出一个严格 JSON 对象，不输出 XML-like 标签；requirements、task_spec、blueprint、plan、manifest、resource_op 和 program 共用同一套对象字段约束。
项目模式先输出 ScratchTaskSpec v2，再输出含 task_spec_hash 的 ProjectBlueprint v2，最后输出 ProjectManifest v3。TaskSpec 的 entity_id/state_id/behavior_id/criterion_id、Blueprint 的 need_id/job_id、Manifest 的 op_id/ref_id 必须各自稳定且遵守上游外键关系（特别注意：Blueprint 的 state_plan[].ref_id 必须严格等于对应 TaskSpec 的 state_id，严禁自创新 ID 如 state_score 或 ref_score，name 与 ref_id 一致；Blueprint 的 program_jobs 与 Manifest 的 program_targets 的 mode 必须全部严格为 "append"；behavior_graph 的 depends_on 严禁跨角色循环依赖，不同角色的行为原则上保持独立，跨角色状态联动一律通过共享变量或广播解耦，确保 program_jobs 之间拓扑无环）。target 必须是对象：{"kind":"stage"}、{"kind":"existing_sprite","name":"角色名"}、{"kind":"created_sprite","op_id":"op_id"} 或 {"kind":"current_target"}，禁止写成字符串。program_targets 的 mode 只能取 append/modify/rewrite；只有 current_target 可用 modify 或 rewrite，stage、existing_sprite、created_sprite 必须严格为 "append"。resource_ops 必须遵循上方生成的 RESOURCE_OPERATION_CONTRACT。
每个程序的对象字段对应 ProgramIR：metadata 的 schema_version=1 或 2、summary、remove_script_ids，与 variables、lists、broadcasts、scripts 合并；tagged stream 将它们分别放在 metadata、shared_state 和 script 标签中，JSON object 的 program segment 直接输出完整对象。项目 ProgramIR v2 的 shared_state 三个数组必须为空，变量、列表、广播通过 fields 中的 state_ref 引用 Manifest ref_id。积木输出必须使用带缩进与换行的标准漂亮格式（Pretty-printed JSON），禁止输出无换行的超长单行。积木逻辑保持扁平直观，严禁深层复合逻辑嵌套（禁止在 inputs.CONDITION 内部嵌套多层 operator_and/operator_or），多条件请使用嵌套 control_if 拆分，并在输出 substacks 前核对 inputs 嵌套 block 的所有花括号配对完整性。
单目标 target_index 固定为 0；项目 target_index 必须按 manifest.program_targets 顺序递增。
禁止解释、代码围栏、协议外文本和旧接口字段。
'''.strip()
        return chr(10).join((source, 'BLOCK_CATALOG=' + json.dumps(catalog, ensure_ascii=False, separators=(',', ':')), protocol))

    @staticmethod
    def _context_key(prefix: str, identity: str, revision: int, value: Any) -> str:
        serialized = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'), default=str)
        fingerprint = hashlib.sha256(serialized.encode('utf-8')).hexdigest()[:16]
        return f'{prefix}:{identity}:v{revision}:{fingerprint}'

    @staticmethod
    def _build_project_context(project: ProjectContext) -> str:
        value = {
            'project_id': project.project_id,
            'version': project.version,
            'targets': project.targets,
            'shared_state': project.shared_state or {},
        }
        return '[PROJECT_CONTEXT]' + chr(10) + json.dumps(value, ensure_ascii=False, separators=(',', ':'))

    @staticmethod
    def _build_target_context(target: TargetContext) -> str:
        value = {
            'target_id': target.target_id,
            'revision': target.revision,
            'name': target.name,
            'is_stage': target.is_stage,
            'has_scripts': target.has_scripts,
            'mode': target.mode,
            'costumes': list(target.costumes),
            'backdrops': list(target.backdrops),
            'sounds': list(target.sounds),
            'sprite_names': list(target.sprite_names),
            'current_program': PromptBuilder._parse_program(target.current_program),
        }
        return '[TARGET_CONTEXT]' + chr(10) + json.dumps(value, ensure_ascii=False, separators=(',', ':'))

    @staticmethod
    def _build_request_context(request: UnifiedGenerationRequest) -> str:
        value = {
            'scope': request.scope,
            'asset_policy': request.asset_policy,
            'proceed_with_defaults': request.proceed_with_defaults,
            'permitted_extensions': request.permitted_extensions,
            'mode': request.target.mode,
        }
        return '[REQUEST_CONTEXT]' + chr(10) + json.dumps(value, ensure_ascii=False, separators=(',', ':'))

    @staticmethod
    def _parse_program(value: str) -> Any:
        if not value:
            return {}
        try:
            return json.loads(value)
        except json.JSONDecodeError:
            return {'unavailable': True}

    @staticmethod
    def _project(value: ProjectContext | dict[str, Any] | None) -> ProjectContext:
        if isinstance(value, ProjectContext):
            return value
        if not value:
            return ProjectContext()
        return ProjectContext(
            project_id=str(value.get('project_id', 'scratch-project')),
            version=int(value.get('version', 0)),
            targets=tuple(value.get('targets', ())),
            shared_state=value.get('shared_state'),
        )

    @staticmethod
    def _target(value: TargetContext | dict[str, Any]) -> TargetContext:
        if isinstance(value, TargetContext):
            return value
        return TargetContext(
            target_id=str(value.get('target_id', 'current-target')),
            revision=int(value.get('revision', 0)),
            name=str(value.get('name', '')),
            is_stage=bool(value.get('is_stage', False)),
            has_scripts=bool(value.get('has_scripts', False)),
            mode=str(value.get('mode', 'append')),
            costumes=tuple(value.get('costumes', ())),
            backdrops=tuple(value.get('backdrops', ())),
            sounds=tuple(value.get('sounds', ())),
            sprite_names=tuple(value.get('sprite_names', ())),
            current_program=str(value.get('current_program', '{}')),
        )
