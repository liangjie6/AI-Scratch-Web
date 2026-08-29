from __future__ import annotations

import json
import re
from copy import deepcopy
from typing import Annotated, Any, Literal

from pydantic import BaseModel, ConfigDict, Field, StringConstraints, field_validator, model_validator

from ..scratch_block_catalog import BLOCK_CONTRACTS

MAX_BLOCKS = 120
MAX_BLOCK_DEPTH = 12
MAX_SCRIPTS = 12
MAX_STRING_LENGTH = 2000
MAX_SUMMARY_LENGTH = 1000

ShortString = Annotated[str, StringConstraints(min_length=1, max_length=120)]
LongString = Annotated[str, StringConstraints(min_length=1, max_length=MAX_STRING_LENGTH)]

NATIVE_OPCODES = frozenset(BLOCK_CONTRACTS)


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class ChatMessage(StrictModel):
    role: Literal['user', 'assistant']
    content: LongString


class TargetContext(StrictModel):
    name: ShortString
    is_stage: bool
    has_scripts: bool
    mode: Literal['modify', 'append', 'rewrite']
    costumes: list[ShortString] = Field(default_factory=list, max_length=64)
    sounds: list[ShortString] = Field(default_factory=list, max_length=64)
    sprite_names: list[ShortString] = Field(default_factory=list, max_length=128)
    current_program: str = Field(default='', max_length=40_000)

    @field_validator('current_program')
    @classmethod
    def validate_current_program(cls, value: str) -> str:
        if not value:
            return value
        try:
            parsed = json.loads(value)
        except json.JSONDecodeError as exc:
            raise ValueError('current_program must be valid JSON') from exc
        if not isinstance(parsed, dict):
            raise ValueError('current_program must contain a JSON object')
        return value


class ThinkingSettings(StrictModel):
    enabled: bool = True
    strength: Literal['low', 'medium', 'high', 'xhigh', 'max'] = 'medium'


class AIBlocksRequestBase(StrictModel):
    model_tier: Literal['normal', 'advanced', 'super'] = 'normal'
    session_id: ShortString
    request_id: ShortString
    messages: list[ChatMessage] = Field(min_length=1, max_length=40)
    target: TargetContext
    thinking: ThinkingSettings = Field(default_factory=ThinkingSettings)


class ChatRequest(AIBlocksRequestBase):
    pass


class RequirementsRequest(AIBlocksRequestBase):
    proceed_with_defaults: bool = False


class RequirementsResponse(StrictModel):
    ready: bool
    title: str = Field(max_length=200)
    goal: str = Field(max_length=2000)
    behavior_steps: list[ShortString] = Field(max_length=40)
    triggers: list[ShortString] = Field(max_length=30)
    controls: list[ShortString] = Field(max_length=30)
    success_criteria: list[ShortString] = Field(max_length=30)
    missing_questions: list[ShortString] = Field(max_length=20)


class GenerateRequest(AIBlocksRequestBase):
    requirements: RequirementsResponse
    plan: Plan | None = None
    protocol_version: Literal[1, 2] = 1
    output_contract: Literal['program_ir', 'project_manifest', 'project_program'] = 'program_ir'
    scope: Literal['single_sprite', 'project'] = 'single_sprite'
    project_manifest: ProjectManifest | None = None
    program_target: ProjectProgramTarget | None = None
    asset_policy: Literal['builtin_first', 'existing_only', 'allow_generation'] = 'builtin_first'
    asset_match_evidence: list[AssetMatchEvidence] = Field(default_factory=list, max_length=40)


class Plan(StrictModel):
    steps: list[ShortString] = Field(min_length=1, max_length=40)


class ProjectProgramTarget(StrictModel):
    target: TargetRef
    mode: Literal['append', 'modify', 'rewrite']
    intent: ShortString


class ProjectSharedState(StrictModel):
    variables: list[VariableDefinition] = Field(default_factory=list, max_length=100)
    lists: list[ListDefinition] = Field(default_factory=list, max_length=50)
    broadcasts: list[ShortString] = Field(default_factory=list, max_length=100)

    @model_validator(mode='after')
    def validate_shared_state(self) -> ProjectSharedState:
        for values, label in ((self.variables, 'variables'), (self.lists, 'lists')):
            names = [item.name for item in values]
            if len(names) != len(set(names)):
                raise ValueError(f'{label} names must be unique')
            if any(item.scope != 'global' for item in values):
                raise ValueError(f'shared {label} must use global scope')
        if len(self.broadcasts) != len(set(self.broadcasts)):
            raise ValueError('broadcast names must be unique')
        return self


class ProjectManifest(StrictModel):
    schema_version: Literal[1]
    scope: Literal['project']
    summary: str = Field(min_length=1, max_length=MAX_SUMMARY_LENGTH)
    resource_ops: list[ResourceOp] = Field(default_factory=list, max_length=24)
    program_targets: list[ProjectProgramTarget] = Field(default_factory=list, max_length=12)
    shared_state: ProjectSharedState = Field(default_factory=ProjectSharedState)

    @model_validator(mode='after')
    def validate_manifest(self) -> ProjectManifest:
        ids: set[str] = set()
        created: set[str] = set()
        for operation in self.resource_ops:
            if operation.op_id in ids:
                raise ValueError('resource op_id values must be unique')
            ids.add(operation.op_id)
            if operation.op in {'add_sprite', 'add_builtin_sprite'}:
                created.add(operation.op_id)
            target = operation.target
            if target and target.kind == 'created_sprite' and target.op_id not in created:
                raise ValueError('created_sprite must reference a prior add_sprite or add_builtin_sprite operation')
        keys = [(item.target.kind, item.target.name, item.target.op_id) for item in self.program_targets]
        if len(keys) != len(set(keys)):
            raise ValueError('program targets must be unique')
        for item in self.program_targets:
            if item.target.kind == 'created_sprite' and item.target.op_id not in created:
                raise ValueError('created_sprite must reference a prior add_sprite or add_builtin_sprite operation')
        if any(item.target.kind in {'stage', 'existing_sprite', 'created_sprite'} and item.mode != 'append' for item in self.program_targets):
            raise ValueError('stage, existing_sprite and created_sprite programs must use append')
        return self


class ProjectProgramResponse(StrictModel):
    target: ProjectProgramTarget
    program: ProgramIR


class LiteralInput(StrictModel):
    kind: Literal['number', 'text', 'angle', 'color']
    value: float | str

    @model_validator(mode='after')
    def validate_value(self) -> LiteralInput:
        if self.kind in {'number', 'angle'}:
            if not isinstance(self.value, (int, float)) or isinstance(self.value, bool):
                raise ValueError(f'{self.kind} input requires a numeric value')
        elif not isinstance(self.value, str):
            raise ValueError(f'{self.kind} input requires a string value')
        if isinstance(self.value, str) and len(self.value) > MAX_STRING_LENGTH:
            raise ValueError('input string is too long')
        return self


class BlockInput(StrictModel):
    kind: Literal['block']
    block: Block


InputValue = Annotated[LiteralInput | BlockInput, Field(discriminator='kind')]


class Block(StrictModel):
    opcode: str
    fields: dict[str, str | float | int | bool] = Field(default_factory=dict, max_length=32)
    inputs: dict[str, InputValue] = Field(default_factory=dict, max_length=32)
    substacks: dict[str, list[Block]] = Field(default_factory=dict, max_length=4)

    @field_validator('opcode')
    @classmethod
    def native_opcode_only(cls, value: str) -> str:
        if value not in NATIVE_OPCODES:
            raise ValueError('opcode is not an allowed native Scratch opcode')
        return value

    @field_validator('substacks')
    @classmethod
    def validate_substacks(cls, value: dict[str, list[Block]]) -> dict[str, list[Block]]:
        if any(key not in {'SUBSTACK', 'SUBSTACK2'} for key in value):
            raise ValueError('invalid substack name')
        return value

    @field_validator('fields')
    @classmethod
    def validate_fields(cls, value: dict[str, str | float | int | bool]) -> dict[str, str | float | int | bool]:
        if any(len(key) > 120 for key in value):
            raise ValueError('field name is too long')
        if any(isinstance(item, str) and len(item) > MAX_STRING_LENGTH for item in value.values()):
            raise ValueError('field string is too long')
        return value


class VariableDefinition(StrictModel):
    name: ShortString
    value: float | str | bool | None = None
    scope: Literal['local', 'global'] | None = None


class ListDefinition(StrictModel):
    name: ShortString
    value: list[str | float | bool] | None = Field(default=None, max_length=500)
    scope: Literal['local', 'global'] | None = None


class Script(StrictModel):
    id: str = Field(pattern=r'^script_[A-Za-z0-9_-]+$', max_length=100)
    purpose: ShortString
    x: int = Field(ge=-10000, le=10000)
    y: int = Field(ge=-10000, le=10000)
    blocks: list[Block] = Field(min_length=1, max_length=MAX_BLOCKS)


class ProgramIR(StrictModel):
    schema_version: Literal[1]
    summary: str = Field(min_length=1, max_length=MAX_SUMMARY_LENGTH)
    remove_script_ids: list[str] = Field(default_factory=list, max_length=MAX_SCRIPTS)
    variables: list[VariableDefinition] = Field(default_factory=list, max_length=100)
    lists: list[ListDefinition] = Field(default_factory=list, max_length=50)
    broadcasts: list[ShortString] = Field(default_factory=list, max_length=100)
    scripts: list[Script] = Field(min_length=1, max_length=MAX_SCRIPTS)

    @field_validator('remove_script_ids')
    @classmethod
    def validate_removed_ids(cls, value: list[str]) -> list[str]:
        pattern = re.compile(r'^script_[A-Za-z0-9_-]+$')
        if any(not pattern.fullmatch(item) for item in value):
            raise ValueError('remove_script_ids must use script_N style ids')
        if len(value) != len(set(value)):
            raise ValueError('remove_script_ids must be unique')
        return value

    @model_validator(mode='after')
    def validate_program_size(self) -> ProgramIR:
        for values, label in ((self.variables, 'variables'), (self.lists, 'lists')):
            names = [item.name for item in values]
            if len(names) != len(set(names)):
                raise ValueError(f'{label} names must be unique')
        if len(self.broadcasts) != len(set(self.broadcasts)):
            raise ValueError('broadcast names must be unique')

        count = 0

        def visit(block: Block, depth: int) -> None:
            nonlocal count
            if depth > MAX_BLOCK_DEPTH:
                raise ValueError('block nesting is too deep')
            count += 1
            if count > MAX_BLOCKS:
                raise ValueError('program has too many blocks')
            for item in block.inputs.values():
                if isinstance(item, BlockInput):
                    visit(item.block, depth + 1)
            for stack in block.substacks.values():
                for child in stack:
                    visit(child, depth + 1)

        for script in self.scripts:
            for block in script.blocks:
                visit(block, 1)
        return self


def validate_program_for_mode(
    program: ProgramIR,
    mode: str,
    current_program: str = '',
) -> ProgramIR:
    if mode in {'append', 'rewrite'} and program.remove_script_ids:
        raise ValueError(f'{mode} mode cannot remove scripts by id')
    if mode == 'modify' and program.remove_script_ids and current_program:
        context = json.loads(current_program)
        protected = {
            item.get('id') for item in context.get('scripts', [])
            if isinstance(item, dict) and item.get('protected') is True
        }
        if protected.intersection(program.remove_script_ids):
            raise ValueError('modify mode cannot remove protected scripts')
    return program


class ProgramContractError(ValueError):
    def __init__(self, issues: list[tuple[str, str]]) -> None:
        self.issues = issues
        super().__init__('; '.join(f'{path}: {message}' for path, message in issues))


def validate_program_contracts(
    program: ProgramIR,
    target_kind: Literal['stage', 'sprite'] = 'sprite',
) -> ProgramIR:
    """Match the frontend compiler contract before a ProgramIR leaves the server."""
    issues: list[tuple[str, str]] = []

    def issue(path: str, message: str) -> None:
        issues.append((path, message))

    def visit_sequence(blocks: list[Block], path: str, *, script: bool) -> None:
        for index, block in enumerate(blocks):
            block_path = f'{path}[{index}]'
            shape = BLOCK_CONTRACTS[block.opcode].shape
            if script and index == 0 and shape != 'hat':
                issue(block_path, 'script must start with an event hat block')
            if shape == 'hat' and ((script and index > 0) or not script):
                issue(block_path, 'event hat block cannot appear here')
            if shape == 'cap' and index < len(blocks) - 1:
                issue(block_path, 'cap block must be the last block in its stack')
            visit(block, block_path, nested_reporter=False)

    def visit(
        block: Block,
        path: str,
        *,
        nested_reporter: bool,
        expected_reporter: str | None = None,
    ) -> None:
        contract = BLOCK_CONTRACTS[block.opcode]
        if target_kind not in contract.targets:
            issue(f'{path}.opcode', f'not valid for {target_kind}')
        if nested_reporter:
            if contract.shape not in {'reporter', 'boolean'}:
                issue(path, 'nested input block must be a reporter or boolean')
            elif expected_reporter == 'boolean' and contract.shape != 'boolean':
                issue(path, 'input requires a boolean block')
        elif contract.shape in {'reporter', 'boolean'}:
            issue(path, 'reporter blocks must be nested in an input')

        for name in block.fields:
            if name not in contract.fields:
                issue(f'{path}.fields.{name}', 'unknown field')
        for name in contract.fields:
            if name not in block.fields:
                issue(f'{path}.fields.{name}', 'required field is missing')
        for name in contract.resolved_fields:
            if name in block.fields and not isinstance(block.fields[name], str):
                issue(f'{path}.fields.{name}', 'resolved field must use a name string')

        for name, value in block.inputs.items():
            expected = contract.inputs.get(name)
            if expected is None:
                issue(f'{path}.inputs.{name}', 'unknown input')
                continue
            input_path = f'{path}.inputs.{name}'
            if isinstance(value, BlockInput):
                visit(
                    value.block,
                    f'{input_path}.block',
                    nested_reporter=True,
                    expected_reporter=expected,
                )
            elif expected == 'boolean':
                issue(input_path, 'boolean input requires a nested boolean block')
            elif expected != 'any' and value.kind != expected:
                issue(f'{input_path}.kind', f'expected {expected}')
        for name in contract.inputs:
            if name not in block.inputs:
                issue(f'{path}.inputs.{name}', 'required input is missing')

        for name in block.substacks:
            if name not in contract.substacks:
                issue(f'{path}.substacks.{name}', 'substack not allowed for opcode')
        for name in contract.substacks:
            if name not in block.substacks:
                issue(f'{path}.substacks.{name}', 'required substack is missing')
        for name, stack in block.substacks.items():
            visit_sequence(stack, f'{path}.substacks.{name}', script=False)

    for script_index, script_value in enumerate(program.scripts):
        visit_sequence(script_value.blocks, f'$.scripts[{script_index}].blocks', script=True)
    if issues:
        raise ProgramContractError(issues)
    return program


def extract_single_json(text: str) -> Any:
    stripped = text.strip()
    fence = re.fullmatch(r'\s*`{3}(?:json)?\s*\n([\s\S]*?)\n`{3}\s*', stripped, re.IGNORECASE)
    if fence:
        stripped = fence.group(1).strip()
    elif '```' in stripped:
        raise ValueError('invalid JSON wrapper')
    decoder = json.JSONDecoder()
    try:
        value, end = decoder.raw_decode(stripped)
    except json.JSONDecodeError as exc:
        raise ValueError(
            f'invalid JSON response at line {exc.lineno} column {exc.colno}: {exc.msg}'
        ) from exc
    if stripped[end:].strip():
        raise ValueError('trailing content after JSON')
    return value


BlockInput.model_rebuild()
Block.model_rebuild()

class AssetMatchCandidate(StrictModel):
    name: ShortString
    tags: list[ShortString] = Field(default_factory=list, max_length=20)


class AssetMatchEvidence(StrictModel):
    requirement_id: ShortString
    asset_type: Literal['sprite', 'costume', 'backdrop']
    manifest_version: Literal['scratch-gui-15.0.1']
    match_count: int = Field(ge=0, le=1000)
    candidates: list[AssetMatchCandidate] = Field(default_factory=list, max_length=5)

    @model_validator(mode='after')
    def validate_count(self) -> AssetMatchEvidence:
        if self.match_count < len(self.candidates):
            raise ValueError('match_count cannot be smaller than candidates length')
        return self


class TargetRef(StrictModel):
    kind: Literal['current_target', 'stage', 'existing_sprite', 'created_sprite']
    name: ShortString | None = None
    op_id: ShortString | None = None

    @model_validator(mode='after')
    def validate_reference(self) -> TargetRef:
        if self.kind == 'existing_sprite' and not self.name:
            raise ValueError('existing_sprite requires name')
        if self.kind == 'created_sprite' and not self.op_id:
            raise ValueError('created_sprite requires op_id')
        if self.kind in {'current_target', 'stage'} and (self.name or self.op_id):
            raise ValueError(f'{self.kind} cannot contain name or op_id')
        return self


class ScratchLibrarySource(StrictModel):
    kind: Literal['scratch_library']
    asset_type: Literal['sprite', 'costume', 'backdrop']
    search_terms: list[ShortString] = Field(min_length=1, max_length=12)


class ImageGenerationSource(StrictModel):
    kind: Literal['image_generation']
    reason: ShortString
    prompt: LongString
    style: ShortString
    custom_style: ShortString | None = None
    width: Literal[512, 768, 1024]
    height: Literal[512, 768, 1024]
    sheet_layout: Literal['1x1']


ProjectManifestResourceSource = Annotated[ScratchLibrarySource | ImageGenerationSource, Field(discriminator='kind')]


class CostumeResource(StrictModel):
    name: ShortString
    source: ProjectManifestResourceSource
    tags: list[ShortString] = Field(default_factory=list, max_length=20)


class ResourceOp(StrictModel):
    op: Literal['add_builtin_sprite', 'add_sprite', 'delete_sprite', 'add_costume', 'add_backdrop', 'select_backdrop']
    op_id: ShortString
    target: TargetRef | None = None
    name: ShortString
    source: ProjectManifestResourceSource | None = None
    tags: list[ShortString] = Field(default_factory=list, max_length=20)
    x: int | None = Field(default=None, ge=-10000, le=10000)
    y: int | None = Field(default=None, ge=-10000, le=10000)
    size: int | None = Field(default=None, ge=1, le=1000)
    select_after: bool | None = None
    costumes: list[CostumeResource] = Field(default_factory=list, max_length=12)

    @model_validator(mode='after')
    def validate_operation(self) -> ResourceOp:
        positional = (self.x, self.y, self.size)
        if self.op == 'select_backdrop':
            if self.source is not None or self.target is not None or any(v is not None for v in positional) or self.tags or self.select_after is not None or self.costumes:
                raise ValueError('select_backdrop only permits op, op_id and name')
            return self
        if self.op == 'delete_sprite':
            if self.source is not None or self.target is None or self.target.kind != 'existing_sprite' or any(v is not None for v in positional) or self.tags or self.select_after is not None or self.costumes:
                raise ValueError('delete_sprite requires an existing_sprite target and no resource fields')
            return self
        if self.source is None:
            raise ValueError(f'{self.op} requires source')
        if self.op in {'add_builtin_sprite', 'add_sprite'}:
            if self.target is not None or any(v is None for v in positional) or self.select_after is not None:
                raise ValueError(f'{self.op} requires x, y, size and no target/select_after')
        elif self.op == 'add_costume':
            if self.target is None or self.target.kind == 'stage' or any(v is not None for v in positional) or self.select_after is None:
                raise ValueError('add_costume requires a non-stage target and select_after')
        elif self.op == 'add_backdrop':
            if self.target is not None or any(v is not None for v in positional) or self.select_after is None:
                raise ValueError('add_backdrop requires select_after and no target/placement fields')
        if self.op == 'add_builtin_sprite' and (self.source.kind != 'scratch_library' or self.source.asset_type != 'sprite'):
            raise ValueError('add_builtin_sprite requires a sprite scratch_library source')
        if self.op == 'add_backdrop' and self.source.kind == 'scratch_library' and self.source.asset_type != 'backdrop':
            raise ValueError('add_backdrop requires a backdrop source')
        if self.op in {'add_costume', 'add_sprite'} and self.source.kind == 'scratch_library' and self.source.asset_type == 'sprite':
            raise ValueError('costume source cannot be a sprite')
        if self.op == 'add_sprite' and any(item.source.kind == 'scratch_library' and item.source.asset_type == 'sprite' for item in self.costumes):
            raise ValueError('nested costume source cannot be a sprite')
        return self


def normalize_project_manifest_input(value: object) -> object:
    """Remove only harmless legacy aliases before strict manifest validation."""
    if not isinstance(value, dict):
        return value
    normalized = deepcopy(value)
    for operation in normalized.get('resource_ops', []):
        if not isinstance(operation, dict):
            continue
        op = operation.get('op')
        if op in {'add_builtin_sprite', 'add_sprite', 'add_backdrop', 'select_backdrop'}:
            operation.pop('target', None)
        if op in {'add_costume', 'add_backdrop'}:
            if operation.get('select_after') == 'true': operation['select_after'] = True
            elif operation.get('select_after') == 'false': operation['select_after'] = False
        if op == 'add_costume':
            source = operation.get('source')
            if isinstance(source, dict) and source.get('kind') == 'scratch_library' and source.get('asset_type') == 'sprite':
                source['asset_type'] = 'costume'
        costumes = operation.get('costumes')
        if isinstance(costumes, list):
            for costume in costumes:
                if isinstance(costume, dict) and isinstance(costume.get('source'), dict) and costume['source'].get('kind') == 'scratch_library' and costume['source'].get('asset_type') == 'sprite':
                    costume['source']['asset_type'] = 'costume'
    for target in normalized.get('program_targets', []):
        if isinstance(target, dict) and isinstance(target.get('target'), dict) and target['target'].get('kind') in {'stage', 'current_target'}:
            target['target'].pop('name', None)
            target['target'].pop('op_id', None)
    return normalized


GenerateRequest.model_rebuild()

