from __future__ import annotations

from dataclasses import dataclass
import hashlib
import json
from pathlib import Path
from typing import Any, Literal

from .orchestration.cache_manager import PromptCache
from .resource_contract import render_resource_operation_contract
from .schemas.ai_blocks import UnifiedGenerationRequest
from .scratch_block_catalog import BLOCK_CONTRACTS


UNIFIED_OUTPUT_CONTRACT = '''
FINAL OUTPUT CONTRACT (HIGHEST PRIORITY; OVERRIDES EARLIER TEXT)
The transport protocol for this request is a tagged stream, not one JSON document. Ignore any earlier sentence that says to output a single JSON object or a bare ProgramIR; that describes an internal payload, not the transport. Output no markdown, code fence, explanation, reasoning, or text outside the tags.

Output tags in this order: requirements, plan, then manifest only for project scope, then program tags. Every tag body is exactly one JSON object. The backend creates phase events; do not output phase_start or phase_complete tags.

The requirements object has exactly these keys: ready boolean, title string, goal string, behavior_steps string array, triggers string array, controls string array, success_criteria string array, and missing_questions string array. Always include all eight keys, using [] for an empty array. Do not wrap it in another property.
The plan object has exactly one key, steps, whose value is a non-empty array of strings.
For project scope, the manifest object has exactly these keys: schema_version equal to 2, scope equal to project, summary string, resource_ops array, program_targets array, and shared_state object. Do not use legacy change_set, target_index, target_id-only entries, or programs fields. Each program_targets entry has exactly target, mode, and intent. The target field is an OBJECT, never a plain string: {"kind": "stage"} for the stage, {"kind": "existing_sprite", "name": "<sprite name from sprite_names>"} for an existing sprite, {"kind": "created_sprite", "op_id": "<op_id from a prior add_sprite/add_builtin_sprite resource op>"} for a newly created sprite, or {"kind": "current_target"} for the request's current target. mode must be one of append, modify, or rewrite, and ONLY current_target may use modify or rewrite; stage, existing_sprite and created_sprite entries MUST use exactly "append" — never modify or rewrite, because those targets have no editable program context from this request (violating this hard rule makes the manifest fail validation). manifest.shared_state declares the complete project-global variables, lists, and broadcasts; every variable/list entry must declare scope "global". Each project program shared_state must repeat those same global declarations exactly, and any additional target-local variable/list must declare scope "local".

Resource operations follow the generated RESOURCE_OPERATION_CONTRACT appended below. Use schema_version 2 operation shapes exactly; do not add fields, use top-level source on add_sprite, or omit the required add_sprite costumes array. Resource op_id values must be unique; a created_sprite may only reference an earlier add_sprite or add_builtin_sprite; stage, existing_sprite and created_sprite program_targets must use mode "append".

Each program tag is <program target_index=N>...</program>. Its children, in order, are metadata, shared_state, and script tags. The metadata object has exactly schema_version equal to 1, summary string, and remove_script_ids string array. The shared_state object has exactly variables array, lists array, and broadcasts string array. In project scope it must repeat manifest.shared_state's global declarations exactly; target-local definitions must use scope "local". Each script tag has index and total attributes; its JSON object has exactly id, purpose, x, y, and blocks. The id must match script_[A-Za-z0-9_-]+.

Each block object may contain only opcode, fields, inputs, and substacks. fields, inputs, and substacks are JSON objects; when empty, write {} rather than [] or null. In particular, a block with no substacks must use substacks: {}, never substacks: []. Substack values are arrays and their keys are only SUBSTACK or SUBSTACK2. Use only input kinds and opcode contracts in BLOCK_CATALOG. Never output next, parent, shadow, topLevel, generated block IDs, or any other VM/project.json fields. The first block of every script is an event hat; reporter and boolean blocks appear only as nested input blocks.

Start with <requirements> and end with the final </program>.
'''


JSON_OBJECT_SEGMENT_CONTRACTS: dict[str, str] = {
    'requirements': 'The JSON object is RequirementsResponse. It must contain exactly ready, title, goal, behavior_steps, triggers, controls, success_criteria, and missing_questions; all array fields are arrays of strings.',
    'plan': 'The JSON object is Plan. It must contain exactly steps, a non-empty array of strings.',
    'manifest': 'The JSON object is ProjectManifest. It must contain exactly schema_version=2, scope=project, summary, resource_ops, program_targets, and shared_state. Every resource operation must match RESOURCE_OPERATION_CONTRACT; add_sprite requires a non-empty costumes array and forbids top-level source. Each program target has target, mode, and intent, and each project program must repeat shared_state global declarations exactly.',
    'resource_op': 'The JSON object is exactly one resource operation to replace the selected resource_ops entry. It must match RESOURCE_OPERATION_CONTRACT with no extra or legacy fields.',
    'program': 'The JSON object is the complete ProgramIR object for this segment. It has exactly schema_version=1, summary, remove_script_ids, variables, lists, broadcasts, and scripts; scripts contain id, purpose, x, y, and blocks, and blocks use only ProgramIR fields.',
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


SegmentName = Literal['requirements', 'plan', 'manifest', 'resource_op', 'program']


class PromptBuilder:
    """Build one stable system prefix plus request-specific conversation data."""

    KNOWLEDGE_BASE_VERSION = 'scratch-ai-v2'
    CACHE_TTL_SECONDS = 300

    def __init__(self, cache: PromptCache | None = None, knowledge_base: str | None = None) -> None:
        self.cache = cache if cache is not None else PromptCache()
        self._knowledge_base_override = knowledge_base

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
        level1_key = f'knowledge:{self.KNOWLEDGE_BASE_VERSION}'
        level1, level1_hit = self.cache.get_or_build(level1_key, self._build_knowledge_base, self.CACHE_TTL_SECONDS)
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
            self._build_unified_output_contract(request, transport=transport, segment=segment),
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
            'plan': '输出且只输出一个 <plan>JSON</plan> 标签，不要重出 requirements。',
            'manifest': '输出且只输出一个 <manifest>JSON</manifest> 标签，不要输出 requirements、plan 或 program。',
            'resource_op': '输出且只输出一个 <resource_op>JSON</resource_op> 标签。该对象替换 resource_ops 中的指定位置。',
            'program': f'输出且只输出一个 target_index={target_index if target_index is not None else 0} 的完整 <program> 标签，不要输出其他目标。',
        }
        if context and context.get('transport') == 'json_object':
            details.update({
                'requirements': '只输出一个 JSON object，内容是 requirements 对象。',
                'plan': '只输出一个 JSON object，内容是 plan 对象，不要重出 requirements。',
                'manifest': '只输出一个 JSON object，内容是 manifest 对象，不要输出 requirements、plan 或 program。',
                'resource_op': '只输出一个 JSON object，内容是要替换的 resource_op 对象。',
                'program': '只输出一个 JSON object，内容是当前 target_index 的完整 ProgramIR 对象，不要输出其他目标。',
            })
        target_note = '' if target_index is None else f' target_index={target_index}.'
        operation_note = '' if resource_op_index is None else f' resource_op_index={resource_op_index}.'
        output_boundary = '标签外文本' if transport == 'tagged' else 'JSON object 之外的文本'
        instruction = (
            'SEGMENT OUTPUT CONTRACT\n'
            + details[segment]
            + target_note
            + operation_note
            + '\n必要的已通过上下文（只用于生成本段）：'
            + context_json
            + f'\n禁止 Markdown、解释、代码围栏和{output_boundary}。'
        )
        messages.append({'role': 'system', 'content': instruction})
        return messages, cache

    def build_chat_messages(self, request: Any) -> list[dict[str, str]]:
        system = (
            '你是 Scratch 项目需求助手。只回答用户问题，不生成 ProgramIR，不修改项目。'
            '回答简洁、具体，并指出当前 Scratch 原生能力的限制。'
        )
        target = request.target.model_dump(mode='json')
        target_context = '[TARGET_CONTEXT]' + chr(10) + json.dumps(target, ensure_ascii=False, separators=(',', ':'))
        return [{'role': 'system', 'content': system + chr(10) + target_context}] + [
            {'role': item.role, 'content': item.content} for item in request.messages
        ]

    @staticmethod
    def _build_unified_output_contract(
        request: UnifiedGenerationRequest,
        *,
        transport: Literal['tagged', 'json_object'] = 'tagged',
        segment: SegmentName | None = None,
    ) -> str:
        if request.scope == 'project':
            scope_note = (
                'This request has scope=project. Emit one manifest after the plan, then one program for each entry in manifest.program_targets. '
                'Use target_index values 0, 1, 2, ... in exactly that order.'
            )
        else:
            scope_note = (
                'This request has scope=single_sprite. Do not emit a manifest. Emit exactly one program and its target_index must be 0.'
            )
        if transport == 'json_object':
            segment_note = segment or 'current'
            segment_contract = JSON_OBJECT_SEGMENT_CONTRACTS.get(segment, 'The JSON object must match the schema for the current segment.')
            return chr(10).join((
                'FINAL OUTPUT CONTRACT (HIGHEST PRIORITY; OVERRIDES EARLIER TEXT)',
                f'The transport for this {segment_note} segment is one JSON object, not a tagged stream. Output exactly one JSON object for this segment and nothing else.',
                'Do not output XML-like tags, Markdown, code fences, explanations, reasoning, or any text outside the JSON object.',
                segment_contract,
                render_resource_operation_contract(),
                scope_note + ' Generate only the current segment; do not emit any other segment.',
            ))
        return chr(10).join((UNIFIED_OUTPUT_CONTRACT.strip(), render_resource_operation_contract(), scope_note))

    def _build_knowledge_base(self) -> str:
        if self._knowledge_base_override is not None:
            source = self._knowledge_base_override
        else:
            path = Path(__file__).resolve().parents[2] / 'docs' / 'scratch代码生成提示词.md'
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
        }
        protocol = '''
统一生成内容协议（实际传输由 system prompt 末尾的 FINAL OUTPUT CONTRACT 决定）：
- tagged stream：按 requirements、plan、manifest（仅项目模式）和 program 标签顺序输出；每个标签内部是一个严格 JSON 对象。
- JSON object：当前 segment 只输出一个严格 JSON 对象，不输出 XML-like 标签；requirements、plan、manifest、resource_op 和 program 共用同一套对象字段约束。
项目模式的 manifest 对象为 {"schema_version":2,"scope":"project","summary":"...","resource_ops":[],"program_targets":[{"target":{"kind":"existing_sprite","name":"Sprite1"},"mode":"append","intent":"..."}],"shared_state":{"variables":[],"lists":[],"broadcasts":[]}}。target 必须是对象：{"kind":"stage"}、{"kind":"existing_sprite","name":"角色名"}、{"kind":"created_sprite","op_id":"op_id"} 或 {"kind":"current_target"}，禁止写成字符串。program_targets 的 mode 只能取 append/modify/rewrite；只有 current_target 可用 modify 或 rewrite，stage、existing_sprite、created_sprite 必须严格为 "append"（写错会校验失败）。resource_ops 必须遵循上方生成的 RESOURCE_OPERATION_CONTRACT。
每个程序的对象字段对应 ProgramIR：metadata 的 schema_version=1、summary、remove_script_ids，与 variables、lists、broadcasts、scripts 合并；tagged stream 将它们分别放在 metadata、shared_state 和 script 标签中，JSON object 的 program segment 直接输出完整对象。项目模式下 shared_state 必须完整重复 manifest.shared_state 的全局声明；额外的目标局部变量和列表必须声明 scope="local"。
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
            'mode': request.target.mode,
            'request_id': request.request_id,
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
            sounds=tuple(value.get('sounds', ())),
            sprite_names=tuple(value.get('sprite_names', ())),
            current_program=str(value.get('current_program', '{}')),
        )
