from __future__ import annotations

from dataclasses import dataclass
import json
import re
from collections.abc import Collection, Mapping
from typing import Annotated, Any, Literal
import unicodedata

from pydantic import BeforeValidator, BaseModel, ConfigDict, Field, StringConstraints, field_validator, model_validator

from ..ai_blocks_contract import ModelTier, ThinkingStrength
from ..config import get_settings
from ..resource_contract import RESOURCE_DIAGNOSTIC_CODES, bounded_actual
from ..scratch_block_catalog import BLOCK_CONTRACTS, extension_for_opcode
from .image_generation import ALLOWED_SIZES

MAX_BLOCKS = 120
MAX_BLOCK_DEPTH = 12
MAX_SCRIPTS = 12
MAX_STRING_LENGTH = 2000
MAX_SUMMARY_LENGTH = 1000
PROJECT_CONTEXT_SOFT_BUDGET_BYTES = 64 * 1024

ShortString = Annotated[str, StringConstraints(min_length=1, max_length=120)]
LongString = Annotated[str, StringConstraints(min_length=1, max_length=MAX_STRING_LENGTH)]

NATIVE_OPCODES = frozenset(BLOCK_CONTRACTS)
PROCEDURE_OPCODES = frozenset({
    'procedures_call',
    'argument_reporter_string_number',
    'argument_reporter_boolean',
})


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


def normalize_contract_text(value: str) -> str:
    """Normalize logical IDs and display names before they become contract keys."""
    normalized = unicodedata.normalize('NFKC', value).strip()
    if any(ord(char) < 32 or ord(char) == 127 for char in normalized):
        raise ValueError('strings cannot contain control characters')
    if not normalized:
        raise ValueError('string must not be empty after normalization')
    return normalized


def logical_key(value: str) -> str:
    return normalize_contract_text(value).casefold()


class ChatMessage(StrictModel):
    role: Literal['user', 'assistant']
    content: LongString


class TargetContext(StrictModel):
    name: ShortString
    is_stage: bool
    has_scripts: bool
    mode: Literal['modify', 'append', 'rewrite']
    costumes: list[ShortString] = Field(default_factory=list, max_length=64)
    backdrops: list[ShortString] = Field(default_factory=list, max_length=64)
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


class ProjectTargetContext(TargetContext):
    target_id: ShortString
    revision: int = Field(ge=0)


class ProjectContext(StrictModel):
    project_id: ShortString
    version: int = Field(ge=0)
    targets: list[ProjectTargetContext] = Field(min_length=1, max_length=12)
    shared_state: dict[str, Any] = Field(default_factory=dict, max_length=16)


class ThinkingSettings(StrictModel):
    enabled: bool = True
    strength: ThinkingStrength = 'medium'


class AIBlocksRequestBase(StrictModel):
    model_tier: ModelTier = 'normal'
    session_id: ShortString
    request_id: ShortString
    messages: list[ChatMessage] = Field(min_length=1, max_length=40)
    target: TargetContext
    thinking: ThinkingSettings = Field(default_factory=ThinkingSettings)


class ChatRequest(AIBlocksRequestBase):
    pass


class RequirementsResponse(StrictModel):
    ready: bool
    title: str = Field(max_length=200)
    goal: str = Field(max_length=2000)
    behavior_steps: list[ShortString] = Field(max_length=40)
    triggers: list[ShortString] = Field(max_length=30)
    controls: list[ShortString] = Field(max_length=30)
    success_criteria: list[ShortString] = Field(max_length=30)
    # The model sometimes omits this when there are no open questions; treat it as optional.
    missing_questions: list[ShortString] = Field(default_factory=list, max_length=20)


class Plan(StrictModel):
    steps: list[ShortString] = Field(min_length=1, max_length=40)


class ProjectProgramTarget(StrictModel):
    job_id: ShortString
    behavior_ids: list[ShortString] = Field(min_length=1, max_length=40)
    owner_entity_id: ShortString
    target: TargetRef
    mode: Literal['append', 'modify', 'rewrite']
    intent: ShortString


class ProjectSharedState(StrictModel):
    variables: list['ProjectVariableDefinition'] = Field(max_length=100)
    lists: list['ProjectListDefinition'] = Field(max_length=50)
    broadcasts: list['ProjectBroadcastDefinition'] = Field(max_length=100)

    @model_validator(mode='after')
    def validate_shared_state(self) -> ProjectSharedState:
        for values, label in ((self.variables, 'variables'), (self.lists, 'lists')):
            names = [logical_key(item.name) for item in values]
            if len(names) != len(set(names)):
                raise ValueError(f'{label} names must be unique')
            if any(item.scope != 'global' for item in values):
                raise ValueError(f'shared {label} must use global scope')
        if len([logical_key(item.name) for item in self.broadcasts]) != len({logical_key(item.name) for item in self.broadcasts}):
            raise ValueError('broadcast names must be unique')
        refs = [item.ref_id for item in [*self.variables, *self.lists, *self.broadcasts]]
        if len(refs) != len(set(refs)):
            raise ValueError('shared state ref_id values must be unique')
        return self


class ProjectManifest(StrictModel):
    schema_version: Literal[3]
    scope: Literal['project']
    summary: str = Field(min_length=1, max_length=MAX_SUMMARY_LENGTH)
    assumptions: list[ShortString] = Field(default_factory=list, max_length=10)
    required_extensions: list[Literal['pen', 'music', 'text2speech']] = Field(default_factory=list, max_length=3)
    resource_ops: list[ResourceOp] = Field(max_length=24)
    local_state: list['ProjectLocalState'] = Field(default_factory=list, max_length=100)
    program_targets: list[ProjectProgramTarget] = Field(max_length=12)
    shared_state: ProjectSharedState

    @model_validator(mode='after')
    def validate_manifest(self) -> ProjectManifest:
        settings = get_settings()
        enabled_extensions = settings.enabled_ai_extensions()
        if len(self.required_extensions) != len(set(self.required_extensions)):
            raise ValueError('required_extensions must be unique')
        if any(extension not in enabled_extensions for extension in self.required_extensions):
            raise ValueError('required_extensions contains an extension disabled by this deployment')
        if not get_settings().ai_destructive_resource_ops_enabled and any(
            isinstance(operation, (DeleteSpriteOp, DeleteSoundOp)) for operation in self.resource_ops
        ):
            raise ValueError('destructive resource operations are disabled by this deployment')
        if not settings.ai_project_assets_enabled and self.resource_ops:
            raise ValueError('project asset operations are disabled by this deployment')
        if not settings.ai_sound_assets_enabled and any(isinstance(operation, AddSoundOp) for operation in self.resource_ops):
            raise ValueError('sound asset operations are disabled by this deployment')
        if not settings.ai_audio_generation_enabled and any(
            isinstance(operation, AddSoundOp) and operation.source.kind == 'audio_generation'
            for operation in self.resource_ops
        ):
            raise ValueError('audio generation is disabled by this deployment')
        ids: set[str] = set()
        created: set[str] = set()
        for operation in self.resource_ops:
            if operation.op_id in ids:
                raise ValueError('resource op_id values must be unique')
            ids.add(operation.op_id)
            if operation.op in {'add_sprite', 'add_builtin_sprite'}:
                created.add(operation.op_id)
            target = getattr(operation, 'target', None)
            if target and target.kind == 'created_sprite' and target.op_id not in created:
                raise ValueError('created_sprite must reference a prior add_sprite or add_builtin_sprite operation')
        keys = [(item.target.kind, item.target.name, item.target.op_id) for item in self.program_targets]
        if len(keys) != len(set(keys)):
            raise ValueError('program targets must be unique')
        job_ids = [item.job_id for item in self.program_targets]
        if len(job_ids) != len(set(job_ids)):
            raise ValueError('program job_id values must be unique')
        for item in self.program_targets:
            if item.target.kind == 'created_sprite' and item.target.op_id not in created:
                raise ValueError('created_sprite must reference a prior add_sprite or add_builtin_sprite operation')
        if any(item.target.kind in {'stage', 'existing_sprite', 'created_sprite'} and item.mode != 'append' for item in self.program_targets):
            raise ValueError('stage, existing_sprite and created_sprite programs must use append')
        local_refs = [item.ref_id for item in self.local_state]
        shared_refs = [item.ref_id for item in [*self.shared_state.variables, *self.shared_state.lists, *self.shared_state.broadcasts]]
        if len(local_refs) != len(set(local_refs)) or set(local_refs).intersection(shared_refs):
            raise ValueError('local state ref_id values must be unique and distinct from shared state')
        return self


class ProjectProgramResponse(StrictModel):
    target: ProjectProgramTarget
    program: ProgramIR


class UnifiedGenerationRequest(AIBlocksRequestBase):
    scope: Literal['single_sprite', 'project'] = 'single_sprite'
    asset_policy: Literal['builtin_first', 'existing_only', 'allow_generation'] = 'builtin_first'
    project_context: ProjectContext | None = None
    proceed_with_defaults: bool = True
    permitted_extensions: list[Literal['text2speech']] = Field(default_factory=list, max_length=1)

    @model_validator(mode='after')
    def validate_scope_context(self) -> UnifiedGenerationRequest:
        if self.scope == 'project' and self.project_context is None:
            raise ValueError('project_context is required for project generation')
        if self.scope == 'single_sprite' and self.project_context is not None:
            raise ValueError('project_context is only valid for project generation')
        if self.project_context is not None:
            encoded = json.dumps(
                self.project_context.model_dump(mode='json'),
                ensure_ascii=False,
                separators=(',', ':'),
            ).encode('utf-8')
            if len(encoded) > PROJECT_CONTEXT_SOFT_BUDGET_BYTES:
                raise ValueError('project_context exceeds the 64 KiB soft budget')
        return self


def validate_request_extension_permissions(request: UnifiedGenerationRequest, manifest: ProjectManifest) -> None:
    """Keep externally speaking extensions behind an explicit user grant."""
    if 'text2speech' in manifest.required_extensions and 'text2speech' not in request.permitted_extensions:
        raise ValueError('text2speech requires explicit request permission')


class ProjectManifestGenerationRequest(UnifiedGenerationRequest):
    output_contract: Literal['project_manifest'] = 'project_manifest'

    @model_validator(mode='after')
    def validate_project_scope(self) -> 'ProjectManifestGenerationRequest':
        if self.scope != 'project':
            raise ValueError('project_manifest requires project scope')
        return self


class ProjectProgramGenerationRequest(UnifiedGenerationRequest):
    output_contract: Literal['project_program'] = 'project_program'
    requirements: RequirementsResponse
    task_spec: ScratchTaskSpec
    blueprint: ProjectBlueprint
    plan: Plan
    manifest: ProjectManifest
    program_target: ProjectProgramTarget
    target_index: int = Field(ge=0)

    @model_validator(mode='after')
    def validate_program_target(self) -> 'ProjectProgramGenerationRequest':
        if self.scope != 'project':
            raise ValueError('project_program requires project scope')
        if self.target_index >= len(self.manifest.program_targets):
            raise ValueError('target_index is outside manifest.program_targets')
        if self.manifest.program_targets[self.target_index] != self.program_target:
            raise ValueError('program_target must match manifest.program_targets[target_index]')
        validate_task_spec_blueprint_lineage(self.task_spec, self.blueprint)
        validate_blueprint_manifest_lineage(self.blueprint, self.manifest)
        return self


ProjectGenerationRequest = Annotated[
    ProjectManifestGenerationRequest | ProjectProgramGenerationRequest,
    Field(discriminator='output_contract'),
]


class ValidationRequest(StrictModel):
    kind: Literal['program', 'manifest']
    payload: dict[str, Any]
    target_kind: Literal['stage', 'sprite'] = 'sprite'
    mode: Literal['modify', 'append', 'rewrite'] = 'append'
    current_program: str = Field(default='', max_length=40_000)

    @model_validator(mode='after')
    def validate_endpoint_contract(self) -> ValidationRequest:
        if self.current_program:
            try:
                parsed = json.loads(self.current_program)
            except json.JSONDecodeError as exc:
                raise ValueError('current_program must be valid JSON') from exc
            if not isinstance(parsed, dict):
                raise ValueError('current_program must contain a JSON object')
        if self.kind == 'manifest' and {'target_kind', 'mode', 'current_program'} & self.model_fields_set:
            raise ValueError('manifest validation does not accept program-only fields')
        return self


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


class ResourceRefInput(StrictModel):
    kind: Literal['resource_ref']
    resource_type: Literal['sprite', 'costume', 'backdrop', 'sound'] | None = None
    op_id: ShortString | None = None
    ref: 'InstalledResourceRef | None' = None

    @model_validator(mode='after')
    def validate_resource_reference(self) -> 'ResourceRefInput':
        if self.ref is not None and self.op_id is None and self.resource_type is None:
            return self
        if self.ref is None and self.op_id is not None and self.resource_type is not None:
            return self
        raise ValueError('resource_ref requires ref or legacy resource_type/op_id pair')


class InstalledResourceRef(StrictModel):
    op_id: ShortString
    value: Literal['installed_name']


class ExistingAssetRefInput(StrictModel):
    kind: Literal['existing_asset_ref']
    asset_type: Literal['sprite', 'costume', 'backdrop', 'sound']
    name: ShortString


InputValue = Annotated[LiteralInput | BlockInput | ResourceRefInput | ExistingAssetRefInput, Field(discriminator='kind')]


class StateRefField(StrictModel):
    kind: Literal['state_ref']
    state_kind: Literal['variable', 'list', 'broadcast']
    ref_id: ShortString


FieldValue = str | float | int | bool | StateRefField | ResourceRefInput | ExistingAssetRefInput


class Block(StrictModel):
    opcode: str
    procedure_id: ShortString | None = None
    argument_id: ShortString | None = None
    fields: dict[str, FieldValue] = Field(default_factory=dict, max_length=32)
    inputs: dict[str, InputValue] = Field(default_factory=dict, max_length=32)
    substacks: dict[str, list[Block]] = Field(default_factory=dict, max_length=4)

    @field_validator('opcode')
    @classmethod
    def native_opcode_only(cls, value: str) -> str:
        if value not in NATIVE_OPCODES and value not in PROCEDURE_OPCODES:
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
    def validate_fields(cls, value: dict[str, FieldValue]) -> dict[str, FieldValue]:
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


class ProjectVariableDefinition(StrictModel):
    ref_id: ShortString
    name: ShortString
    value: float | str | bool | None = None
    scope: Literal['global']


class ProjectListDefinition(StrictModel):
    ref_id: ShortString
    name: ShortString
    value: list[str | float | bool] | None = Field(default=None, max_length=500)
    scope: Literal['global']


class ProjectBroadcastDefinition(StrictModel):
    ref_id: ShortString
    name: ShortString


class ProjectLocalState(StrictModel):
    ref_id: ShortString
    kind: Literal['variable', 'list']
    target: 'TargetRef'
    name: ShortString
    value: float | str | bool | list[str | float | bool] | None = None

    @model_validator(mode='after')
    def validate_local_target_and_value(self) -> 'ProjectLocalState':
        if self.target.kind == 'stage':
            raise ValueError('local state cannot target the stage')
        if self.kind == 'list' and self.value is not None and not isinstance(self.value, list):
            raise ValueError('local list value must be an array')
        if self.kind == 'variable' and isinstance(self.value, list):
            raise ValueError('local variable value cannot be an array')
        return self


class Script(StrictModel):
    id: str = Field(pattern=r'^script_[A-Za-z0-9_-]+$', max_length=100)
    purpose: ShortString
    x: int = Field(ge=-10000, le=10000)
    y: int = Field(ge=-10000, le=10000)
    blocks: list[Block] = Field(min_length=1, max_length=MAX_BLOCKS)


class ProcedureParameter(StrictModel):
    id: str = Field(pattern=r'^arg_[A-Za-z0-9_-]+$', max_length=100)
    name: ShortString
    kind: Literal['string_number', 'boolean']
    default: str | float | int | bool | None = None


class Procedure(StrictModel):
    id: str = Field(pattern=r'^procedure_[A-Za-z0-9_-]+$', max_length=100)
    label: ShortString
    parameters: list[ProcedureParameter] = Field(default_factory=list, max_length=8)
    warp: bool = False
    body: list[Block] = Field(min_length=1, max_length=MAX_BLOCKS)

    @model_validator(mode='after')
    def validate_procedure(self) -> 'Procedure':
        if '%' in self.label:
            raise ValueError('procedure label cannot contain Scratch placeholder characters')
        parameter_ids = [item.id for item in self.parameters]
        if len(parameter_ids) != len(set(parameter_ids)):
            raise ValueError('procedure parameter ids must be unique')
        return self


class ProgramMetadata(StrictModel):
    schema_version: Literal[1, 2] = 1
    summary: str = Field(min_length=1, max_length=MAX_SUMMARY_LENGTH)
    remove_script_ids: list[str] = Field(default_factory=list, max_length=MAX_SCRIPTS)
    procedures: list[Procedure] = Field(default_factory=list, max_length=12)

    @field_validator('remove_script_ids')
    @classmethod
    def validate_removed_ids(cls, value: list[str]) -> list[str]:
        pattern = re.compile(r'^script_[A-Za-z0-9_-]+$')
        if any(not pattern.fullmatch(item) for item in value):
            raise ValueError('remove_script_ids must use script_N style ids')
        if len(value) != len(set(value)):
            raise ValueError('remove_script_ids must be unique')
        return value


class ProgramState(StrictModel):
    variables: list[VariableDefinition] = Field(default_factory=list, max_length=100)
    lists: list[ListDefinition] = Field(default_factory=list, max_length=50)
    broadcasts: list[ShortString] = Field(default_factory=list, max_length=100)

    @model_validator(mode='after')
    def validate_names(self) -> ProgramState:
        for values, label in ((self.variables, 'variables'), (self.lists, 'lists')):
            names = [item.name for item in values]
            if len(names) != len(set(names)):
                raise ValueError(f'{label} names must be unique')
        if len(self.broadcasts) != len(set(self.broadcasts)):
            raise ValueError('broadcast names must be unique')
        return self


class ProgramIR(StrictModel):
    schema_version: Literal[1, 2]
    summary: str = Field(min_length=1, max_length=MAX_SUMMARY_LENGTH)
    remove_script_ids: list[str] = Field(default_factory=list, max_length=MAX_SCRIPTS)
    variables: list[VariableDefinition] = Field(default_factory=list, max_length=100)
    lists: list[ListDefinition] = Field(default_factory=list, max_length=50)
    broadcasts: list[ShortString] = Field(default_factory=list, max_length=100)
    procedures: list[Procedure] = Field(default_factory=list, max_length=12)
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
        procedure_ids = [item.id for item in self.procedures]
        if len(procedure_ids) != len(set(procedure_ids)):
            raise ValueError('procedure ids must be unique')
        procedure_codes = {
            f"{item.label} {' '.join('%b' if parameter.kind == 'boolean' else '%s' for parameter in item.parameters)}".strip()
            for item in self.procedures
        }
        if len(procedure_codes) != len(self.procedures):
            raise ValueError('procedure proccodes must be unique')

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
        for procedure in self.procedures:
            for block in procedure.body:
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
        scripts = context.get('scripts', [])
        if not isinstance(scripts, list):
            raise ValueError('current_program scripts must be a list')
        protected = {
            item.get('id') for item in scripts
            if isinstance(item, dict) and item.get('protected') is True
        }
        if protected.intersection(program.remove_script_ids):
            raise ValueError('modify mode cannot remove protected scripts')
    return program


def validate_project_program_shared_state(
    program: ProgramIR,
    shared_state: ProjectSharedState,
    target_kind: Literal['stage', 'sprite'] = 'sprite',
) -> ProgramIR:
    """V1 repeats declarations; v2 project programs use manifest state refs only."""
    issues: list[tuple[str, str]] = []

    if program.schema_version == 2:
        if program.variables or program.lists or program.broadcasts:
            raise ProgramContractError([('$.shared_state', 'ProgramIR v2 project programs must not declare variables, lists, or broadcasts')])
        return program

    def effective_scope(item: VariableDefinition | ListDefinition) -> Literal['local', 'global']:
        # Scratch stores all state on the stage when the target is the stage; on a
        # sprite, an omitted scope is the existing local-variable default.
        return 'global' if target_kind == 'stage' else (item.scope or 'local')

    def dump_state(
        item: VariableDefinition | ListDefinition,
        is_list: bool,
        scope: Literal['local', 'global'],
    ) -> dict[str, Any]:
        value = item.model_dump(mode='json')
        value['scope'] = scope
        if is_list and value['value'] is None:
            value['value'] = []
        return value

    expected_variables = sorted((dump_state(item, False, 'global') for item in shared_state.variables), key=lambda item: item['name'])
    expected_lists = sorted((dump_state(item, True, 'global') for item in shared_state.lists), key=lambda item: item['name'])
    actual_variables = sorted((dump_state(item, False, 'global') for item in program.variables if effective_scope(item) == 'global'), key=lambda item: item['name'])
    actual_lists = sorted((dump_state(item, True, 'global') for item in program.lists if effective_scope(item) == 'global'), key=lambda item: item['name'])
    if actual_variables != expected_variables:
        issues.append(('$.variables', 'project program global variables must exactly repeat manifest.shared_state.variables'))
    if actual_lists != expected_lists:
        issues.append(('$.lists', 'project program global lists must exactly repeat manifest.shared_state.lists'))
    if sorted(program.broadcasts) != sorted(item.name for item in shared_state.broadcasts):
        issues.append(('$.broadcasts', 'project program broadcasts must exactly repeat manifest.shared_state.broadcasts'))
    global_names = {item['name'] for item in expected_variables} | {item['name'] for item in expected_lists}
    for path, values in (('$.variables', program.variables), ('$.lists', program.lists)):
        if any(effective_scope(item) != 'global' and item.name in global_names for item in values):
            issues.append((path, 'target-local state cannot reuse a manifest global name'))
    if issues:
        raise ProgramContractError(issues)
    return program


def validate_project_program_resource_refs(
    program: ProgramIR,
    manifest: ProjectManifest,
    existing_assets: Mapping[str, Collection[str]] | None = None,
) -> ProgramIR:
    """Require ProgramIR v2 resource refs to bind to compatible manifest ops."""
    resource_types: dict[str, str] = {}
    for operation in manifest.resource_ops:
        if operation.op in {'add_sprite', 'add_builtin_sprite'}:
            resource_types[operation.op_id] = 'sprite'
        elif operation.op == 'add_costume':
            resource_types[operation.op_id] = 'costume'
        elif operation.op == 'add_backdrop':
            resource_types[operation.op_id] = 'backdrop'
        elif operation.op == 'add_sound':
            resource_types[operation.op_id] = 'sound'
    issues: list[tuple[str, str]] = []

    def validate_resource_ref(value: ResourceRefInput | ExistingAssetRefInput, required: str | None, path: str) -> None:
        if isinstance(value, ResourceRefInput):
            op_id = value.ref.op_id if value.ref is not None else value.op_id
            actual = resource_types.get(op_id or '')
            if actual is None:
                issues.append((f'{path}.ref.op_id', 'resource_ref must reference a manifest resource operation'))
            elif value.resource_type is not None and actual != value.resource_type:
                issues.append((f'{path}.resource_type', 'resource_ref type does not match the manifest resource'))
            if required is None:
                issues.append((path, 'resource_ref is only valid for a typed Scratch resource menu'))
            elif actual != required:
                issues.append((f'{path}.ref', 'resource_ref type is not valid for this Scratch menu'))
        elif required is None:
            issues.append((path, 'existing_asset_ref is only valid for a typed Scratch resource menu'))
        elif value.asset_type != required:
            issues.append((f'{path}.asset_type', 'existing_asset_ref type is not valid for this Scratch menu'))
        elif existing_assets is not None:
            names = tuple(existing_assets.get(required, ()))
            if value.name not in names:
                issues.append((f'{path}.name', 'existing_asset_ref must name an existing asset in the target snapshot'))
            elif names.count(value.name) != 1:
                issues.append((f'{path}.name', 'existing_asset_ref must resolve to exactly one asset in the target snapshot'))

    def visit(blocks: list[Block], path: str) -> None:
        for index, block in enumerate(blocks):
            block_path = f'{path}[{index}]'
            contract = BLOCK_CONTRACTS.get(block.opcode)
            for field_name, value in block.fields.items():
                if isinstance(value, (ResourceRefInput, ExistingAssetRefInput)):
                    required = contract.resource_field_types.get(field_name) if contract is not None else None
                    validate_resource_ref(value, required, f'{block_path}.fields.{field_name}')
            for input_name, value in block.inputs.items():
                if isinstance(value, ResourceRefInput):
                    required = contract.resource_input_types.get(input_name) if contract is not None else None
                    validate_resource_ref(value, required, f'{block_path}.inputs.{input_name}')
                elif isinstance(value, ExistingAssetRefInput):
                    required = contract.resource_input_types.get(input_name) if contract is not None else None
                    validate_resource_ref(value, required, f'{block_path}.inputs.{input_name}')
                elif isinstance(value, BlockInput):
                    visit([value.block], f'{block_path}.inputs.{input_name}.block')
            for stack_name, stack in block.substacks.items():
                visit(stack, f'{block_path}.substacks.{stack_name}')

    for script_index, script in enumerate(program.scripts):
        visit(script.blocks, f'$.scripts[{script_index}].blocks')
    for procedure_index, procedure in enumerate(program.procedures):
        visit(procedure.body, f'$.procedures[{procedure_index}].body')
    if issues:
        raise ProgramContractError(issues)
    return program


def validate_project_program_state_refs(program: ProgramIR, manifest: ProjectManifest) -> ProgramIR:
    """ProgramIR v2 fields may only point at the Manifest's immutable state ref IDs."""
    known: dict[str, Literal['variable', 'list', 'broadcast']] = {}
    known.update({item.ref_id: 'variable' for item in manifest.shared_state.variables})
    known.update({item.ref_id: 'list' for item in manifest.shared_state.lists})
    known.update({item.ref_id: 'broadcast' for item in manifest.shared_state.broadcasts})
    known.update({item.ref_id: item.kind for item in manifest.local_state})
    issues: list[tuple[str, str]] = []

    def visit(blocks: list[Block], path: str) -> None:
        for index, block in enumerate(blocks):
            block_path = f'{path}[{index}]'
            for field_name, value in block.fields.items():
                if isinstance(value, StateRefField):
                    actual = known.get(value.ref_id)
                    if actual is None:
                        issues.append((f'{block_path}.fields.{field_name}.ref_id', 'state_ref must reference manifest shared_state or local_state'))
                    elif actual != value.state_kind:
                        issues.append((f'{block_path}.fields.{field_name}.state_kind', 'state_ref type does not match manifest state'))
            for input_name, value in block.inputs.items():
                if isinstance(value, BlockInput):
                    visit([value.block], f'{block_path}.inputs.{input_name}.block')
            for stack_name, stack in block.substacks.items():
                visit(stack, f'{block_path}.substacks.{stack_name}')

    for script_index, script in enumerate(program.scripts):
        visit(script.blocks, f'$.scripts[{script_index}].blocks')
    for procedure_index, procedure in enumerate(program.procedures):
        visit(procedure.body, f'$.procedures[{procedure_index}].body')
    if issues:
        raise ProgramContractError(issues)
    return program


class ProgramContractError(ValueError):
    def __init__(self, issues: list[tuple[str, str]]) -> None:
        self.issues = issues
        super().__init__('; '.join(f'{path}: {message}' for path, message in issues))


def validate_program_contracts(
    program: ProgramIR,
    target_kind: Literal['stage', 'sprite'] = 'sprite',
    allowed_extensions: frozenset[str] = frozenset(),
    *,
    allow_resource_refs: bool = True,
) -> ProgramIR:
    """Match the frontend compiler contract before a ProgramIR leaves the server."""
    # Logical references are a deployment capability, not merely a schema
    # feature. Keep the server-side contract fail-closed if that capability is
    # rolled back while an older client still submits project programs.
    allow_resource_refs = allow_resource_refs and get_settings().ai_logical_resource_refs_enabled
    issues: list[tuple[str, str]] = []
    procedures = {procedure.id: procedure for procedure in program.procedures}

    def issue(path: str, message: str) -> None:
        issues.append((path, message))

    def visit_sequence(blocks: list[Block], path: str, *, script: bool) -> None:
        for index, block in enumerate(blocks):
            block_path = f'{path}[{index}]'
            contract = BLOCK_CONTRACTS.get(block.opcode)
            if contract is None:
                issue(f'{block_path}.opcode', 'unknown opcode')
                continue
            shape = contract.shape
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
        procedure_arguments: dict[str, ProcedureParameter] | None = None,
    ) -> None:
        if block.opcode == 'procedures_call':
            if nested_reporter:
                issue(path, 'procedure calls cannot be nested reporters')
            if block.argument_id is not None:
                issue(f'{path}.argument_id', 'procedure call cannot include argument_id')
            procedure = procedures.get(block.procedure_id or '')
            if procedure is None:
                issue(f'{path}.procedure_id', 'must reference a declared procedure')
                return
            expected_parameters = {parameter.id: parameter for parameter in procedure.parameters}
            for name, value in block.inputs.items():
                parameter = expected_parameters.get(name)
                if parameter is None:
                    issue(f'{path}.inputs.{name}', 'does not match a procedure parameter')
                    continue
                if isinstance(value, BlockInput):
                    visit(
                        value.block,
                        f'{path}.inputs.{name}.block',
                        nested_reporter=True,
                        expected_reporter='boolean' if parameter.kind == 'boolean' else 'text',
                        procedure_arguments=procedure_arguments,
                    )
                elif parameter.kind == 'boolean' or value.kind != 'text':
                    issue(f'{path}.inputs.{name}', 'does not match the procedure parameter type')
            for parameter in procedure.parameters:
                if parameter.id not in block.inputs:
                    issue(f'{path}.inputs.{parameter.id}', 'required procedure argument is missing')
            if block.fields:
                issue(f'{path}.fields', 'procedure call cannot include fields')
            if block.substacks:
                issue(f'{path}.substacks', 'procedure call cannot include substacks')
            return
        if block.opcode in {'argument_reporter_string_number', 'argument_reporter_boolean'}:
            if not nested_reporter:
                issue(path, 'procedure argument reporter must be nested in an input')
            if block.procedure_id is not None:
                issue(f'{path}.procedure_id', 'argument reporter cannot include procedure_id')
            parameter = (procedure_arguments or {}).get(block.argument_id or '')
            if parameter is None:
                issue(f'{path}.argument_id', 'must reference a parameter of the current procedure')
            else:
                expected_kind = 'boolean' if block.opcode == 'argument_reporter_boolean' else 'string_number'
                if parameter.kind != expected_kind:
                    issue(f'{path}.opcode', 'reporter kind does not match the procedure parameter')
                if expected_reporter == 'boolean' and parameter.kind != 'boolean':
                    issue(path, 'input requires a boolean reporter')
            if block.fields or block.inputs or block.substacks:
                issue(path, 'procedure argument reporter cannot include fields, inputs, or substacks')
            return
        contract = BLOCK_CONTRACTS.get(block.opcode)
        if contract is None:
            issue(f'{path}.opcode', 'unknown opcode')
            return
        if target_kind not in contract.targets:
            issue(f'{path}.opcode', f'not valid for {target_kind}')
        extension = extension_for_opcode(block.opcode)
        if extension is not None and extension not in allowed_extensions:
            issue(f'{path}.opcode', f'extension {extension} is not declared for this program')
        if block.procedure_id is not None:
            issue(f'{path}.procedure_id', 'is only valid for procedures_call')
        if block.argument_id is not None:
            issue(f'{path}.argument_id', 'is only valid for a procedure argument reporter')
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
        for name in contract.required_fields:
            if name not in block.fields:
                issue(f'{path}.fields.{name}', 'required field is missing')
        for name in contract.resolved_fields:
            if name in block.fields and not isinstance(block.fields[name], (str, StateRefField)):
                issue(f'{path}.fields.{name}', 'resolved field must use a name string or state_ref')
            if isinstance(block.fields.get(name), StateRefField):
                expected_kind = 'list' if name == 'LIST' else 'broadcast' if name == 'BROADCAST_OPTION' else 'variable'
                if block.fields[name].state_kind != expected_kind:
                    issue(f'{path}.fields.{name}.state_kind', f'resolved field requires {expected_kind} state_ref')
        for name, value in block.fields.items():
            resource_type = contract.resource_field_types.get(name)
            if isinstance(value, ResourceRefInput):
                if not allow_resource_refs:
                    issue(f'{path}.fields.{name}.kind', 'resource_ref is only valid for project programs with a Manifest binding table')
                elif resource_type is None:
                    issue(f'{path}.fields.{name}.kind', 'resource_ref is only valid for a typed Scratch resource field menu')
                elif value.resource_type is not None and value.resource_type != resource_type:
                    issue(f'{path}.fields.{name}.resource_type', f'resource_ref must be {resource_type} for this Scratch field menu')
            elif isinstance(value, ExistingAssetRefInput):
                if resource_type is None:
                    issue(f'{path}.fields.{name}.kind', 'existing_asset_ref is only valid for a typed Scratch resource field menu')
                elif value.asset_type != resource_type:
                    issue(f'{path}.fields.{name}.asset_type', f'existing_asset_ref must be {resource_type} for this Scratch field menu')

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
                    procedure_arguments=procedure_arguments,
                )
            elif isinstance(value, ResourceRefInput):
                resource_type = contract.resource_input_types.get(name)
                if not allow_resource_refs:
                    issue(f'{input_path}.kind', 'resource_ref is only valid for project programs with a Manifest binding table')
                elif expected != 'text' or resource_type is None:
                    issue(f'{input_path}.kind', 'resource_ref is only valid for a typed Scratch resource menu')
                elif value.resource_type is not None and value.resource_type != resource_type:
                    issue(f'{input_path}.resource_type', f'resource_ref must be {resource_type} for this Scratch menu')
            elif isinstance(value, ExistingAssetRefInput):
                resource_type = contract.resource_input_types.get(name)
                if expected != 'text' or resource_type is None:
                    issue(f'{input_path}.kind', 'existing_asset_ref is only valid for a typed Scratch resource menu')
                elif value.asset_type != resource_type:
                    issue(f'{input_path}.asset_type', f'existing_asset_ref must be {resource_type} for this Scratch menu')
            elif expected == 'boolean':
                issue(input_path, 'boolean input requires a nested boolean block')
            elif expected != 'any' and value.kind != expected:
                issue(f'{input_path}.kind', f'expected {expected}')
        for name in contract.required_inputs:
            if name not in block.inputs:
                issue(f'{path}.inputs.{name}', 'required input is missing')

        for name in block.substacks:
            if name not in contract.substacks:
                issue(f'{path}.substacks.{name}', 'substack not allowed for opcode')
        for name in contract.required_substacks:
            if name not in block.substacks:
                issue(f'{path}.substacks.{name}', 'required substack is missing')
        for name, stack in block.substacks.items():
            visit_sequence(stack, f'{path}.substacks.{name}', script=False)

    for script_index, script_value in enumerate(program.scripts):
        visit_sequence(script_value.blocks, f'$.scripts[{script_index}].blocks', script=True)
    for procedure_index, procedure in enumerate(program.procedures):
        arguments = {parameter.id: parameter for parameter in procedure.parameters}
        for block_index, block in enumerate(procedure.body):
            path = f'$.procedures[{procedure_index}].body[{block_index}]'
            if block.opcode.startswith('event_') or block.opcode == 'procedures_call':
                issue(f'{path}.opcode', 'procedure body cannot contain event hats or procedure calls')
            visit(block, path, nested_reporter=False, procedure_arguments=arguments)
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
    asset_type: Literal['sprite', 'costume', 'backdrop', 'sound']
    manifest_version: Literal['scratch-gui-15.1.1']
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
    asset_type: Literal['sprite', 'costume', 'backdrop', 'sound']
    search_terms: list[ShortString] = Field(min_length=1, max_length=12)


class ScratchSpriteLibrarySource(ScratchLibrarySource):
    asset_type: Literal['sprite']


class ScratchCostumeLibrarySource(ScratchLibrarySource):
    asset_type: Literal['costume']


class ScratchBackdropLibrarySource(ScratchLibrarySource):
    asset_type: Literal['backdrop']


class ScratchSoundLibrarySource(ScratchLibrarySource):
    asset_type: Literal['sound']


class ImageGenerationSource(StrictModel):
    kind: Literal['image_generation']
    reason: ShortString
    prompt: LongString
    style: ShortString
    custom_style: ShortString | None = None
    width: int = Field(ge=1, le=2_000)
    height: int = Field(ge=1, le=2_000)
    sheet_layout: Literal['1x1']

    @model_validator(mode='after')
    def validate_size_pair(self) -> 'ImageGenerationSource':
        if (self.width, self.height) not in ALLOWED_SIZES:
            raise ValueError('width and height must be an allowed image size pair')
        return self


class TaskTargetRef(StrictModel):
    """A TaskSpec can identify existing targets, never a future Manifest op."""
    kind: Literal['current_target', 'stage', 'existing_sprite']
    name: ShortString | None = None

    @model_validator(mode='after')
    def validate_reference(self) -> 'TaskTargetRef':
        if self.kind == 'existing_sprite' and not self.name:
            raise ValueError('existing_sprite requires name')
        if self.kind != 'existing_sprite' and self.name is not None:
            raise ValueError(f'{self.kind} cannot contain name')
        return self


class TaskAssumption(StrictModel):
    id: ShortString
    text: ShortString
    confidence: float = Field(ge=0, le=1)
    requires_confirmation: bool


class TaskEntity(StrictModel):
    entity_id: ShortString
    role: Literal['stage', 'sprite']
    description: ShortString
    existing_target_ref: TaskTargetRef | None = None

    @model_validator(mode='after')
    def validate_role_reference(self) -> 'TaskEntity':
        if self.role == 'stage' and self.existing_target_ref and self.existing_target_ref.kind != 'stage':
            raise ValueError('a stage entity can only bind to the stage')
        return self


class TaskState(StrictModel):
    state_id: ShortString
    kind: Literal['variable', 'list', 'broadcast']
    scope: Literal['global', 'local']
    owner_entity_id: ShortString | None = None
    initial_value: float | str | bool | list[str | float | bool] | None = None

    @model_validator(mode='after')
    def validate_state_semantics(self) -> 'TaskState':
        if self.kind == 'broadcast':
            if self.scope != 'global' or self.owner_entity_id is not None or self.initial_value is not None:
                raise ValueError('broadcast state must be global and has no owner or initial_value')
        if self.scope == 'local' and not self.owner_entity_id:
            raise ValueError('local state requires owner_entity_id')
        if self.scope == 'global' and self.owner_entity_id is not None:
            raise ValueError('global state cannot have owner_entity_id')
        if self.kind == 'list' and self.initial_value is not None and not isinstance(self.initial_value, list):
            raise ValueError('list initial_value must be an array')
        if self.kind == 'variable' and isinstance(self.initial_value, list):
            raise ValueError('variable initial_value cannot be an array')
        return self


class OperandSpec(StrictModel):
    kind: Literal['literal', 'state', 'attribute', 'timer', 'random']
    value: float | str | bool | None = None
    state_id: ShortString | None = None
    entity_id: ShortString | None = None
    attribute: Literal['x', 'y', 'direction', 'size', 'visible'] | None = None
    min: float | None = None
    max: float | None = None

    @model_validator(mode='after')
    def validate_operand(self) -> 'OperandSpec':
        if self.kind == 'literal' and self.value is None:
            raise ValueError('literal operand requires value')
        if self.kind == 'state' and not self.state_id:
            raise ValueError('state operand requires state_id')
        if self.kind == 'attribute' and (not self.entity_id or not self.attribute):
            raise ValueError('attribute operand requires entity_id and attribute')
        if self.kind == 'random' and (self.min is None or self.max is None or self.min > self.max):
            raise ValueError('random operand requires min <= max')
        forbidden = {
            'literal': ('state_id', 'entity_id', 'attribute', 'min', 'max'),
            'state': ('value', 'entity_id', 'attribute', 'min', 'max'),
            'attribute': ('value', 'state_id', 'min', 'max'),
            'timer': ('value', 'state_id', 'entity_id', 'attribute', 'min', 'max'),
            'random': ('value', 'state_id', 'entity_id', 'attribute'),
        }
        if any(getattr(self, field) is not None for field in forbidden[self.kind]):
            raise ValueError(f'{self.kind} operand contains unrelated fields')
        return self


class TriggerSpec(StrictModel):
    kind: Literal['green_flag', 'key_pressed', 'sprite_clicked', 'broadcast_received', 'backdrop_switched', 'clone_started', 'timer_reached']
    key: ShortString | None = None
    state_id: ShortString | None = None
    asset_purpose: ShortString | None = None
    seconds: float | None = Field(default=None, ge=0)

    @model_validator(mode='after')
    def validate_trigger(self) -> 'TriggerSpec':
        required = {'key_pressed': 'key', 'broadcast_received': 'state_id', 'backdrop_switched': 'asset_purpose', 'timer_reached': 'seconds'}
        field = required.get(self.kind)
        if field and getattr(self, field) is None:
            raise ValueError(f'{self.kind} requires {field}')
        allowed = {field} if field else set()
        if any(getattr(self, candidate) is not None for candidate in ('key', 'state_id', 'asset_purpose', 'seconds') if candidate not in allowed):
            raise ValueError(f'{self.kind} contains unrelated trigger fields')
        return self


class ConditionSpec(StrictModel):
    kind: Literal['compare', 'touching', 'touching_edge', 'key_down', 'logical', 'not']
    left: OperandSpec | None = None
    operator: Literal['eq', 'gt', 'gte', 'lt', 'lte', 'and', 'or'] | None = None
    right: OperandSpec | None = None
    entity_id: ShortString | None = None
    key: ShortString | None = None
    operands: list['ConditionSpec'] | None = Field(default=None, min_length=2, max_length=12)
    operand: 'ConditionSpec | None' = None

    @model_validator(mode='after')
    def validate_condition(self) -> 'ConditionSpec':
        if self.kind == 'compare' and (not self.left or not self.right or self.operator not in {'eq', 'gt', 'gte', 'lt', 'lte'}):
            raise ValueError('compare condition requires left, comparison operator, and right')
        if self.kind == 'touching' and not self.entity_id:
            raise ValueError('touching condition requires entity_id')
        if self.kind == 'key_down' and not self.key:
            raise ValueError('key_down condition requires key')
        if self.kind == 'logical' and (self.operator not in {'and', 'or'} or not self.operands):
            raise ValueError('logical condition requires and/or and operands')
        if self.kind == 'not' and not self.operand:
            raise ValueError('not condition requires operand')
        return self


class ActionSpec(StrictModel):
    """Closed TaskSpec action vocabulary. The validator enforces per-action fields."""
    kind: Literal['move_steps', 'set_axis', 'change_axis', 'go_to_entity', 'turn', 'point_towards', 'set_state', 'change_state', 'broadcast', 'use_asset', 'set_visibility', 'say', 'ask', 'create_clone', 'delete_this_clone', 'wait', 'loop', 'stop']
    steps: OperandSpec | None = None
    axis: Literal['x', 'y'] | None = None
    value: OperandSpec | None = None
    delta: OperandSpec | None = None
    entity_id: ShortString | None = None
    degrees: OperandSpec | None = None
    state_id: ShortString | None = None
    asset_kind: Literal['costume', 'backdrop', 'sound'] | None = None
    purpose: ShortString | None = None
    wait: bool | None = None
    visible: bool | None = None
    text: ShortString | None = None
    seconds: OperandSpec | None = None
    question: ShortString | None = None
    mode: Literal['forever', 'repeat', 'repeat_until'] | None = None
    count: OperandSpec | None = None
    until: ConditionSpec | None = None
    actions: list['ActionSpec'] | None = Field(default=None, min_length=1, max_length=40)
    scope: Literal['this_script', 'all'] | None = None

    @model_validator(mode='after')
    def validate_action(self) -> 'ActionSpec':
        required: dict[str, tuple[str, ...]] = {
            'move_steps': ('steps',), 'set_axis': ('axis', 'value'), 'change_axis': ('axis', 'delta'),
            'go_to_entity': ('entity_id',), 'turn': ('degrees',), 'point_towards': ('entity_id',),
            'set_state': ('state_id', 'value'), 'change_state': ('state_id', 'delta'), 'broadcast': ('state_id',),
            'use_asset': ('asset_kind', 'purpose'), 'set_visibility': ('visible',), 'say': ('text',),
            'ask': ('question',), 'create_clone': ('entity_id',), 'delete_this_clone': (), 'wait': ('seconds',),
            'loop': ('mode', 'actions'), 'stop': ('scope',),
        }
        missing = [field for field in required[self.kind] if getattr(self, field) is None]
        if missing:
            raise ValueError(f'{self.kind} requires ' + ', '.join(missing))
        if self.kind == 'loop':
            if self.mode == 'repeat' and self.count is None:
                raise ValueError('repeat loop requires count')
            if self.mode == 'repeat_until' and self.until is None:
                raise ValueError('repeat_until loop requires until')
            if self.mode == 'forever' and (self.count is not None or self.until is not None):
                raise ValueError('forever loop cannot contain count or until')
        return self


class TaskBehavior(StrictModel):
    behavior_id: ShortString
    owner_entity_id: ShortString
    trigger: TriggerSpec
    conditions: list[ConditionSpec] = Field(default_factory=list, max_length=12)
    actions: list[ActionSpec] = Field(min_length=1, max_length=40)
    reads: list[ShortString] = Field(default_factory=list, max_length=100)
    writes: list[ShortString] = Field(default_factory=list, max_length=100)


class SuccessCriterionSpec(StrictModel):
    criterion_id: ShortString
    description: ShortString
    related_behavior_ids: list[ShortString] = Field(min_length=1, max_length=40)
    priority: Literal['must', 'should']
    verification: Literal['executable', 'structural', 'manual', 'unsupported']


class UnsupportedRequirement(StrictModel):
    requirement: ShortString
    reason_code: ShortString


class ScratchTaskSpec(StrictModel):
    schema_version: Literal[2]
    title: ShortString
    goal: LongString
    assumptions: list[TaskAssumption] = Field(default_factory=list, max_length=10)
    entities: list[TaskEntity] = Field(min_length=1, max_length=12)
    state: list[TaskState] = Field(default_factory=list, max_length=150)
    behaviors: list[TaskBehavior] = Field(min_length=1, max_length=80)
    success_criteria: list[SuccessCriterionSpec] = Field(min_length=1, max_length=40)
    unsupported: list[UnsupportedRequirement] = Field(default_factory=list, max_length=40)

    @model_validator(mode='after')
    def validate_task_lineage(self) -> 'ScratchTaskSpec':
        def unique(values: list[str], label: str) -> None:
            if len(values) != len(set(values)):
                raise ValueError(f'{label} values must be unique')
        entity_ids = [item.entity_id for item in self.entities]
        state_ids = [item.state_id for item in self.state]
        behavior_ids = [item.behavior_id for item in self.behaviors]
        criterion_ids = [item.criterion_id for item in self.success_criteria]
        unique(entity_ids, 'entity_id'); unique(state_ids, 'state_id'); unique(behavior_ids, 'behavior_id'); unique(criterion_ids, 'criterion_id')
        entities = set(entity_ids); states = set(state_ids); behaviors = set(behavior_ids)
        state_by_id = {item.state_id: item for item in self.state}

        def validate_operand(operand: OperandSpec) -> None:
            if operand.kind == 'state' and operand.state_id not in states:
                raise ValueError('operand state_id must reference declared state')
            if operand.kind == 'attribute' and operand.entity_id not in entities:
                raise ValueError('operand entity_id must reference declared entity')

        def validate_condition(condition: ConditionSpec) -> None:
            if condition.kind == 'compare':
                assert condition.left is not None and condition.right is not None
                validate_operand(condition.left); validate_operand(condition.right)
            elif condition.kind == 'touching' and condition.entity_id not in entities:
                raise ValueError('touching condition entity_id must reference declared entity')
            elif condition.kind == 'logical':
                for nested in condition.operands or []: validate_condition(nested)
            elif condition.kind == 'not' and condition.operand is not None:
                validate_condition(condition.operand)

        def validate_action(action: ActionSpec) -> None:
            for operand in (action.steps, action.value, action.delta, action.degrees, action.seconds, action.count):
                if operand is not None: validate_operand(operand)
            if action.until is not None: validate_condition(action.until)
            if action.entity_id is not None and action.entity_id not in entities:
                raise ValueError('action entity_id must reference declared entity')
            if action.state_id is not None and action.state_id not in states:
                raise ValueError('action state_id must reference declared state')
            if action.kind == 'broadcast' and state_by_id[action.state_id or ''].kind != 'broadcast':
                raise ValueError('broadcast action must reference a broadcast state')
            for nested in action.actions or []: validate_action(nested)

        for item in self.state:
            if item.owner_entity_id and item.owner_entity_id not in entities:
                raise ValueError('state owner_entity_id must reference an entity')
        for behavior in self.behaviors:
            if behavior.owner_entity_id not in entities:
                raise ValueError('behavior owner_entity_id must reference an entity')
            if not set([*behavior.reads, *behavior.writes]).issubset(states):
                raise ValueError('behavior reads and writes must reference declared state')
            for state_id in [*behavior.reads, *behavior.writes]:
                local_state = state_by_id[state_id]
                if local_state.scope == 'local' and local_state.owner_entity_id != behavior.owner_entity_id:
                    raise ValueError('behavior cannot read or write another entity local state')
            if behavior.trigger.kind == 'broadcast_received':
                broadcast = state_by_id.get(behavior.trigger.state_id or '')
                if broadcast is None or broadcast.kind != 'broadcast':
                    raise ValueError('broadcast trigger must reference a broadcast state')
            for condition in behavior.conditions: validate_condition(condition)
            for action in behavior.actions: validate_action(action)
        for criterion in self.success_criteria:
            if not set(criterion.related_behavior_ids).issubset(behaviors):
                raise ValueError('success criterion must reference declared behaviors')
        return self


class BlueprintAssetNeed(StrictModel):
    need_id: ShortString
    kind: Literal['sprite', 'costume', 'backdrop', 'sound']
    owner_entity_id: ShortString | None = None
    purpose: ShortString
    search_terms: list[ShortString] = Field(min_length=1, max_length=12)
    tags: list[ShortString] = Field(default_factory=list, max_length=20)
    fallback_order: list[Literal['project_asset', 'scratch_library', 'user_asset', 'image_generation', 'audio_generation']] = Field(min_length=1, max_length=5)
    constraints: dict[str, int | bool] = Field(default_factory=dict, max_length=4)

    @model_validator(mode='after')
    def validate_need(self) -> 'BlueprintAssetNeed':
        if len(self.fallback_order) != len(set(self.fallback_order)):
            raise ValueError('asset fallback_order entries must be unique')
        if self.kind != 'sound' and 'audio_generation' in self.fallback_order:
            raise ValueError('audio_generation only applies to sound needs')
        if self.kind == 'sound' and 'image_generation' in self.fallback_order:
            raise ValueError('image_generation cannot satisfy a sound need')
        allowed = {'transparent', 'max_bytes', 'max_duration_ms', 'animation_frames'}
        if not set(self.constraints).issubset(allowed):
            raise ValueError('asset need constraints contain an unknown key')
        return self


class BlueprintEntityResolution(StrictModel):
    kind: Literal['existing_target', 'new_sprite']
    target_ref: TaskTargetRef | None = None
    need_id: ShortString | None = None

    @model_validator(mode='after')
    def validate_resolution(self) -> 'BlueprintEntityResolution':
        if self.kind == 'existing_target' and (self.target_ref is None or self.need_id is not None):
            raise ValueError('existing_target binding requires target_ref only')
        if self.kind == 'new_sprite' and (self.need_id is None or self.target_ref is not None):
            raise ValueError('new_sprite binding requires need_id only')
        return self


class BlueprintEntityBinding(StrictModel):
    entity_id: ShortString
    resolution: BlueprintEntityResolution


class BlueprintStatePlan(StrictModel):
    ref_id: ShortString
    kind: Literal['variable', 'list', 'broadcast']
    scope: Literal['global', 'local']
    owner_entity_id: ShortString | None = None
    name: ShortString
    initial_value: float | str | bool | list[str | float | bool] | None = None

    @model_validator(mode='after')
    def validate_state_plan(self) -> 'BlueprintStatePlan':
        if self.kind == 'broadcast' and (self.scope != 'global' or self.owner_entity_id is not None or self.initial_value is not None):
            raise ValueError('broadcast plan must be global and have no owner or initial value')
        if self.scope == 'local' and not self.owner_entity_id:
            raise ValueError('local state plan requires owner_entity_id')
        if self.scope == 'global' and self.owner_entity_id is not None:
            raise ValueError('global state plan cannot have owner_entity_id')
        return self


class BlueprintBehaviorGraphNode(StrictModel):
    behavior_id: ShortString
    owner_entity_id: ShortString
    depends_on: list[ShortString] = Field(default_factory=list, max_length=40)
    produces_broadcasts: list[ShortString] = Field(default_factory=list, max_length=40)
    consumes_broadcasts: list[ShortString] = Field(default_factory=list, max_length=40)


class BlueprintProgramJob(StrictModel):
    job_id: ShortString
    owner_entity_id: ShortString
    mode: Literal['append', 'modify', 'rewrite']
    behavior_ids: list[ShortString] = Field(min_length=1, max_length=40)


class AcceptanceTrigger(StrictModel):
    kind: Literal['green_flag', 'key', 'sprite_click', 'broadcast']
    key: ShortString | None = None
    entity_id: ShortString | None = None
    state_id: ShortString | None = None

    @model_validator(mode='after')
    def validate_trigger(self) -> 'AcceptanceTrigger':
        required = {'key': 'key', 'sprite_click': 'entity_id', 'broadcast': 'state_id'}
        field = required.get(self.kind)
        if field and getattr(self, field) is None:
            raise ValueError(f'{self.kind} acceptance trigger requires {field}')
        return self


class AcceptanceArrange(StrictModel):
    kind: Literal['set_position', 'set_variable', 'set_list', 'set_timer']
    entity_id: ShortString | None = None
    state_id: ShortString | None = None
    x: float | None = None
    y: float | None = None
    value: float | str | bool | list[str | float | bool] | None = None
    seconds: float | None = None

    @model_validator(mode='after')
    def validate_arrange(self) -> 'AcceptanceArrange':
        required = {
            'set_position': ('entity_id', 'x', 'y'),
            'set_variable': ('state_id', 'value'),
            'set_list': ('state_id', 'value'),
            'set_timer': ('seconds',),
        }
        missing = [field for field in required[self.kind] if getattr(self, field) is None]
        if missing:
            raise ValueError(f'{self.kind} arrangement requires ' + ', '.join(missing))
        if self.kind == 'set_list' and not isinstance(self.value, list):
            raise ValueError('set_list arrangement requires a list value')
        if self.kind == 'set_variable' and isinstance(self.value, list):
            raise ValueError('set_variable arrangement requires a scalar value')
        if self.kind == 'set_timer' and self.seconds is not None and not 0 <= self.seconds <= 86_400:
            raise ValueError('set_timer arrangement requires seconds from 0 to 86400')
        return self


class AcceptanceExpectation(StrictModel):
    kind: Literal['variable', 'position_range', 'costume', 'backdrop', 'visibility', 'list', 'clone_count', 'say_text', 'question_asked', 'broadcast_emitted', 'sound_invoked', 'scripts_stopped']
    state_id: ShortString | None = None
    entity_id: ShortString | None = None
    asset_need_id: ShortString | None = None
    comparator: Literal['eq', 'gt', 'gte', 'lt', 'lte', 'equals', 'contains', 'length'] | None = None
    value: float | str | bool | list[str | float | bool] | None = None
    axis: Literal['x', 'y'] | None = None
    min: float | None = None
    max: float | None = None
    visible: bool | None = None
    text: ShortString | None = None
    min_count: int | None = Field(default=None, ge=0)

    @model_validator(mode='after')
    def validate_expectation(self) -> 'AcceptanceExpectation':
        required = {
            'variable': ('state_id', 'comparator', 'value'),
            'position_range': ('entity_id', 'axis', 'min', 'max'),
            'costume': ('entity_id', 'asset_need_id'),
            'backdrop': ('asset_need_id',),
            'visibility': ('entity_id', 'visible'),
            'list': ('state_id', 'comparator', 'value'),
            'clone_count': ('entity_id', 'comparator', 'value'),
            'say_text': ('entity_id', 'text'),
            'question_asked': ('text',),
            'broadcast_emitted': ('state_id', 'min_count'),
            'sound_invoked': ('entity_id', 'asset_need_id', 'min_count'),
            'scripts_stopped': (),
        }
        missing = [field for field in required[self.kind] if getattr(self, field) is None]
        if missing:
            raise ValueError(f'{self.kind} expectation requires ' + ', '.join(missing))
        allowed_comparators = {
            'variable': {'eq', 'gt', 'gte', 'lt', 'lte'},
            'list': {'equals', 'contains', 'length'},
            'clone_count': {'eq', 'gte', 'lte'},
        }
        if self.kind in allowed_comparators and self.comparator not in allowed_comparators[self.kind]:
            raise ValueError(f'{self.kind} expectation has an invalid comparator')
        if self.kind == 'position_range' and self.min is not None and self.max is not None and self.min > self.max:
            raise ValueError('position_range expectation min cannot exceed max')
        if self.kind == 'clone_count' and (not isinstance(self.value, (int, float)) or isinstance(self.value, bool)):
            raise ValueError('clone_count expectation requires a numeric value')
        return self


class BlueprintAcceptanceTest(StrictModel):
    test_id: ShortString
    source_criterion_id: ShortString
    related_behavior_ids: list[ShortString] = Field(min_length=1, max_length=40)
    trigger: AcceptanceTrigger
    arrange: list[AcceptanceArrange] = Field(default_factory=list, max_length=20)
    expect: list[AcceptanceExpectation] = Field(min_length=1, max_length=20)
    timeout_ms: int = Field(ge=1, le=4_000)


class ProjectBlueprint(StrictModel):
    schema_version: Literal[2]
    task_spec_hash: str = Field(pattern=r'^[a-f0-9]{64}$')
    asset_needs: list[BlueprintAssetNeed] = Field(default_factory=list, max_length=24)
    entity_bindings: list[BlueprintEntityBinding] = Field(min_length=1, max_length=12)
    state_plan: list[BlueprintStatePlan] = Field(default_factory=list, max_length=150)
    behavior_graph: list[BlueprintBehaviorGraphNode] = Field(min_length=1, max_length=80)
    program_jobs: list[BlueprintProgramJob] = Field(min_length=1, max_length=12)
    acceptance_tests: list[BlueprintAcceptanceTest] = Field(default_factory=list, max_length=40)

    @model_validator(mode='after')
    def validate_blueprint(self) -> 'ProjectBlueprint':
        def unique(values: list[str], label: str) -> None:
            if len(values) != len(set(values)):
                raise ValueError(f'{label} values must be unique')
        need_ids = [item.need_id for item in self.asset_needs]
        binding_entities = [item.entity_id for item in self.entity_bindings]
        state_refs = [item.ref_id for item in self.state_plan]
        graph_behaviors = [item.behavior_id for item in self.behavior_graph]
        job_ids = [item.job_id for item in self.program_jobs]
        test_ids = [item.test_id for item in self.acceptance_tests]
        unique(need_ids, 'need_id'); unique(binding_entities, 'entity binding'); unique(state_refs, 'state ref_id'); unique(graph_behaviors, 'behavior graph'); unique(job_ids, 'job_id'); unique(test_ids, 'acceptance test')
        needs = {item.need_id: item for item in self.asset_needs}
        for binding in self.entity_bindings:
            if binding.resolution.kind == 'new_sprite':
                need = needs.get(binding.resolution.need_id or '')
                if need is None or need.kind != 'sprite' or need.owner_entity_id != binding.entity_id:
                    raise ValueError('new_sprite binding must reference its entity sprite need')
        graph = set(graph_behaviors)
        for node in self.behavior_graph:
            if not set(node.depends_on).issubset(graph):
                raise ValueError('behavior dependency must reference a declared behavior')
        assigned = [behavior for job in self.program_jobs for behavior in job.behavior_ids]
        if len(assigned) != len(set(assigned)) or set(assigned) != graph:
            raise ValueError('program jobs must cover every behavior exactly once')

        behavior_to_job = {b_id: job.job_id for job in self.program_jobs for b_id in job.behavior_ids}
        job_deps = {job.job_id: set() for job in self.program_jobs}
        for node in self.behavior_graph:
            node_job = behavior_to_job.get(node.behavior_id)
            if node_job is None:
                continue
            for dep_id in node.depends_on:
                dep_job = behavior_to_job.get(dep_id)
                if dep_job is not None and dep_job != node_job:
                    job_deps[node_job].add(dep_job)

        visiting_jobs: set[str] = set()
        visited_jobs: set[str] = set()

        def has_job_cycle(j_id: str) -> bool:
            if j_id in visiting_jobs:
                return True
            if j_id in visited_jobs:
                return False
            visiting_jobs.add(j_id)
            cyclic = any(has_job_cycle(dep) for dep in job_deps[j_id])
            visiting_jobs.remove(j_id)
            visited_jobs.add(j_id)
            return cyclic

        if any(has_job_cycle(j_id) for j_id in job_deps):
            raise ValueError('blueprint program jobs dependencies cannot contain a cycle')

        if sum(item.timeout_ms for item in self.acceptance_tests) > 4_000:
            raise ValueError('acceptance test total timeout must not exceed 4000ms')
        return self


def canonical_contract_json(value: BaseModel | dict[str, Any]) -> str:
    payload = value.model_dump(mode='json', exclude_none=True) if isinstance(value, BaseModel) else value

    # Match the browser representation used by JSON.parse/JSON.stringify.
    # In particular, JavaScript collapses integral floats (1.0) to 1 before
    # hashing; leaving Python's 1.0 spelling here makes client/server hashes
    # disagree after an otherwise lossless SSE round trip.
    def normalize(entry: Any) -> Any:
        if isinstance(entry, float) and entry.is_integer():
            return int(entry)
        if isinstance(entry, dict):
            return {key: normalize(item) for key, item in entry.items()}
        if isinstance(entry, list):
            return [normalize(item) for item in entry]
        return entry

    return json.dumps(normalize(payload), ensure_ascii=False, sort_keys=True, separators=(',', ':'))


def contract_sha256(value: BaseModel | dict[str, Any]) -> str:
    import hashlib
    return hashlib.sha256(canonical_contract_json(value).encode('utf-8')).hexdigest()


def validate_task_spec_blueprint_lineage(task_spec: ScratchTaskSpec, blueprint: ProjectBlueprint) -> None:
    if blueprint.task_spec_hash != contract_sha256(task_spec):
        raise ValueError('blueprint task_spec_hash does not match the canonical TaskSpec')
    entities = {item.entity_id: item for item in task_spec.entities}
    if set(item.entity_id for item in blueprint.entity_bindings) != set(entities):
        raise ValueError('blueprint must bind every TaskSpec entity exactly once')
    state = {item.state_id: item for item in task_spec.state}
    if set(item.ref_id for item in blueprint.state_plan) != set(state):
        raise ValueError('blueprint state_plan must project every TaskSpec state exactly once')
    for state_plan in blueprint.state_plan:
        source = state[state_plan.ref_id]
        if (source.kind, source.scope, source.owner_entity_id) != (state_plan.kind, state_plan.scope, state_plan.owner_entity_id):
            raise ValueError('blueprint state_plan must preserve TaskSpec state semantics')
    behaviors = {item.behavior_id: item for item in task_spec.behaviors}
    if set(item.behavior_id for item in blueprint.behavior_graph) != set(behaviors):
        raise ValueError('blueprint behavior graph must project every TaskSpec behavior')
    for node in blueprint.behavior_graph:
        if node.owner_entity_id != behaviors[node.behavior_id].owner_entity_id:
            raise ValueError('blueprint behavior owner must preserve TaskSpec owner')
    graph_by_behavior = {item.behavior_id: item for item in blueprint.behavior_graph}
    visiting: set[str] = set()
    visited: set[str] = set()

    def has_cycle(behavior_id: str) -> bool:
        if behavior_id in visiting:
            return True
        if behavior_id in visited:
            return False
        visiting.add(behavior_id)
        cyclic = any(has_cycle(dependency) for dependency in graph_by_behavior[behavior_id].depends_on)
        visiting.remove(behavior_id)
        visited.add(behavior_id)
        return cyclic

    if any(has_cycle(behavior_id) for behavior_id in graph_by_behavior):
        raise ValueError('blueprint behavior dependencies cannot contain a cycle')

    broadcasts = {state_id for state_id, item in state.items() if item.kind == 'broadcast'}

    def action_broadcasts(actions: list[ActionSpec]) -> set[str]:
        result: set[str] = set()
        for action in actions:
            if action.kind == 'broadcast' and action.state_id:
                result.add(action.state_id)
            if action.kind == 'loop':
                result.update(action_broadcasts(action.actions or []))
        return result

    for node in blueprint.behavior_graph:
        behavior = behaviors[node.behavior_id]
        if not set(node.produces_broadcasts).issubset(broadcasts) or not set(node.consumes_broadcasts).issubset(broadcasts):
            raise ValueError('blueprint behavior graph broadcast edges must reference TaskSpec broadcasts')
        expected_produces = action_broadcasts(behavior.actions)
        expected_consumes = {behavior.trigger.state_id} if behavior.trigger.kind == 'broadcast_received' and behavior.trigger.state_id else set()
        if set(node.produces_broadcasts) != expected_produces or set(node.consumes_broadcasts) != expected_consumes:
            raise ValueError('blueprint behavior graph broadcast edges must match TaskSpec behavior semantics')
    for broadcast in broadcasts:
        if not any(broadcast in node.produces_broadcasts for node in blueprint.behavior_graph) or not any(broadcast in node.consumes_broadcasts for node in blueprint.behavior_graph):
            raise ValueError('each declared broadcast requires a producer and consumer')
    for job in blueprint.program_jobs:
        if any(behaviors[behavior_id].owner_entity_id != job.owner_entity_id for behavior_id in job.behavior_ids):
            raise ValueError('program job owner must match every assigned behavior')
    criteria = {item.criterion_id for item in task_spec.success_criteria}
    if not {item.source_criterion_id for item in blueprint.acceptance_tests}.issubset(criteria):
        raise ValueError('blueprint acceptance test references an unknown success criterion')
    criteria_by_id = {item.criterion_id: item for item in task_spec.success_criteria}
    needs = {item.need_id: item for item in blueprint.asset_needs}
    for test in blueprint.acceptance_tests:
        criterion = criteria_by_id[test.source_criterion_id]
        if criterion.verification != 'executable':
            raise ValueError('blueprint acceptance tests may only reference executable success criteria')
        if not set(test.related_behavior_ids).issubset(criterion.related_behavior_ids):
            raise ValueError('acceptance test behaviors must belong to its source criterion')
        trigger = test.trigger
        if trigger.kind == 'sprite_click' and trigger.entity_id not in entities:
            raise ValueError('sprite_click acceptance trigger must reference a declared entity')
        if trigger.kind == 'broadcast' and (trigger.state_id not in state or state[trigger.state_id].kind != 'broadcast'):
            raise ValueError('broadcast acceptance trigger must reference a declared broadcast')
        for arrange in test.arrange:
            if arrange.kind == 'set_position' and arrange.entity_id not in entities:
                raise ValueError('acceptance arrangement references an unknown entity')
            if arrange.kind == 'set_variable' and (arrange.state_id not in state or state[arrange.state_id].kind != 'variable'):
                raise ValueError('set_variable arrangement must reference a variable')
            if arrange.kind == 'set_list' and (arrange.state_id not in state or state[arrange.state_id].kind != 'list'):
                raise ValueError('set_list arrangement must reference a list')
        for expectation in test.expect:
            if expectation.kind in {'variable', 'broadcast_emitted'}:
                expected_kind = 'variable' if expectation.kind == 'variable' else 'broadcast'
                if expectation.state_id not in state or state[expectation.state_id].kind != expected_kind:
                    raise ValueError('acceptance expectation references an invalid state kind')
            if expectation.kind == 'list' and (expectation.state_id not in state or state[expectation.state_id].kind != 'list'):
                raise ValueError('list acceptance expectation must reference a list')
            if expectation.kind in {'position_range', 'costume', 'visibility', 'clone_count', 'say_text', 'sound_invoked'} and expectation.entity_id not in entities:
                raise ValueError('acceptance expectation references an unknown entity')
            if expectation.kind in {'costume', 'backdrop', 'sound_invoked'}:
                expected_need_kind = 'costume' if expectation.kind == 'costume' else 'backdrop' if expectation.kind == 'backdrop' else 'sound'
                if expectation.asset_need_id not in needs or needs[expectation.asset_need_id].kind != expected_need_kind:
                    raise ValueError('acceptance expectation references an invalid asset need')
            if expectation.kind == 'scripts_stopped' and expectation.entity_id is not None and expectation.entity_id not in entities:
                raise ValueError('scripts_stopped acceptance expectation references an unknown entity')
    tested_criteria = {item.source_criterion_id for item in blueprint.acceptance_tests}
    if any(item.verification == 'executable' and item.criterion_id not in tested_criteria for item in task_spec.success_criteria):
        raise ValueError('every executable success criterion must have an acceptance test')


def validate_blueprint_manifest_lineage(blueprint: ProjectBlueprint, manifest: ProjectManifest) -> None:
    needs = {item.need_id: item for item in blueprint.asset_needs}
    bindings = {item.entity_id: item for item in blueprint.entity_bindings}
    for operation in manifest.resource_ops:
        if isinstance(operation, (AddBuiltinSpriteOp, AddSpriteOp, AddCostumeOp, AddBackdropOp, AddSoundOp)):
            if operation.source_need_id not in needs:
                raise ValueError('manifest resource source_need_id must reference a Blueprint asset need')
        if isinstance(operation, (AddBuiltinSpriteOp, AddSpriteOp)):
            binding = bindings.get(operation.owner_entity_id)
            if binding is None or binding.resolution.kind != 'new_sprite' or binding.resolution.need_id != operation.source_need_id:
                raise ValueError('manifest created sprite must match its Blueprint entity binding')
    jobs = {item.job_id: item for item in blueprint.program_jobs}
    if set(item.job_id for item in manifest.program_targets) != set(jobs):
        raise ValueError('manifest program targets must project every Blueprint program job')
    for target in manifest.program_targets:
        source = jobs[target.job_id]
        mode_compatible = (
            target.mode == source.mode
            or (target.target.kind in {'stage', 'existing_sprite', 'created_sprite'} and target.mode == 'append')
        )
        if target.owner_entity_id != source.owner_entity_id or target.behavior_ids != source.behavior_ids or not mode_compatible:
            raise ValueError('manifest program target must preserve Blueprint job fields')
    blueprint_state = {item.ref_id: item for item in blueprint.state_plan}
    manifest_state: list[tuple[str, str, str, str | None]] = []
    manifest_state.extend((item.ref_id, 'variable', 'global', None) for item in manifest.shared_state.variables)
    manifest_state.extend((item.ref_id, 'list', 'global', None) for item in manifest.shared_state.lists)
    manifest_state.extend((item.ref_id, 'broadcast', 'global', None) for item in manifest.shared_state.broadcasts)
    manifest_state.extend((item.ref_id, item.kind, 'local', None) for item in manifest.local_state)
    if {item[0] for item in manifest_state} != set(blueprint_state):
        raise ValueError('manifest state must project every Blueprint state exactly once')
    for ref_id, kind, scope, _ in manifest_state:
        source = blueprint_state[ref_id]
        if source.kind != kind or source.scope != scope:
            raise ValueError('manifest state must preserve Blueprint state kind and scope')


class AudioGenerationSource(StrictModel):
    kind: Literal['audio_generation']
    reason: ShortString
    prompt: LongString
    audio_kind: Literal['sound_effect', 'speech', 'music']
    max_duration_seconds: float = Field(gt=0, le=60)


CostumeSource = Annotated[ScratchCostumeLibrarySource | ImageGenerationSource, Field(discriminator='kind')]
BackdropSource = Annotated[ScratchBackdropLibrarySource | ImageGenerationSource, Field(discriminator='kind')]
SoundSource = Annotated[ScratchSoundLibrarySource | AudioGenerationSource, Field(discriminator='kind')]


class CostumeResource(StrictModel):
    name: ShortString
    source: CostumeSource
    tags: list[ShortString] = Field(default_factory=list, max_length=20)


def to_json_pointer(path: str | tuple[Any, ...]) -> str:
    if isinstance(path, tuple):
        parts = [str(item) for item in path]
    else:
        value = path.strip()
        if value in {'', '$'}:
            return ''
        if value.startswith('/'):
            return value
        if value.startswith('$'):
            value = value[1:]
        parts = re.findall(r'[^.\[\]/]+', value)
    escaped = [part.replace('~', '~0').replace('/', '~1') for part in parts if part]
    return '/' + '/'.join(escaped) if escaped else ''


@dataclass(frozen=True)
class ResourceContractIssue:
    code: str
    path: str
    operation: str
    actual: Any
    expected: str
    repair_scope: str = 'resource_op'
    repair_id: str | None = None

    def as_dict(self) -> dict[str, Any]:
        return {
            'code': self.code,
            'path': self.path,
            'operation': self.operation,
            'actual': bounded_actual(self.actual),
            'expected': self.expected,
            'repair_scope': self.repair_scope,
            'repair_id': self.repair_id,
        }


class ResourceContractError(ValueError):
    def __init__(self, issue: ResourceContractIssue) -> None:
        self.issue = issue
        super().__init__(f'{issue.code} at {issue.path}: {issue.expected}')


def _resource_issue(
    raw: dict[str, Any],
    code: str,
    path: str,
    actual: Any,
    expected: str,
) -> ResourceContractError:
    operation = str(raw.get('op', 'unknown'))
    repair_id = raw.get('op_id') if isinstance(raw.get('op_id'), str) else None
    return ResourceContractError(ResourceContractIssue(code, path, operation, actual, expected, repair_id=repair_id))


def normalize_resource_op(
    raw: Any,
    *,
    path: str = '/resource_ops/0',
) -> tuple[Any, list[ResourceContractIssue]]:
    """Apply only lossless compatibility fixes before strict resource validation."""
    if not isinstance(raw, dict) or raw.get('op') not in {'add_backdrop', 'add_costume'}:
        return raw, []

    normalized = dict(raw)
    diagnostics: list[ResourceContractIssue] = []
    operation = str(normalized.get('op'))
    repair_id = normalized.get('op_id') if isinstance(normalized.get('op_id'), str) else None

    if 'select_after' not in normalized:
        normalized['select_after'] = False
        diagnostics.append(ResourceContractIssue(
            'DEFAULT_APPLIED',
            f'{path}/select_after',
            operation,
            None,
            'missing select_after is equivalent to false',
            repair_id=repair_id,
        ))

    if operation != 'add_backdrop':
        return normalized, diagnostics

    if 'target' in normalized:
        target = normalized['target']
        if target == {'kind': 'stage'}:
            del normalized['target']
            diagnostics.append(ResourceContractIssue(
                'REDUNDANT_STAGE_TARGET_REMOVED',
                f'{path}/target',
                operation,
                target,
                'backdrop always targets the stage; omit target',
                repair_id=repair_id,
            ))
        else:
            raise _resource_issue(
                normalized,
                'INVALID_BACKDROP_TARGET',
                f'{path}/target',
                target,
                'target must be omitted or exactly {"kind":"stage"} as a redundant compatibility field',
            )

    for field in ('x', 'y', 'size'):
        if field not in normalized:
            continue
        actual = normalized[field]
        if actual is None:
            del normalized[field]
            diagnostics.append(ResourceContractIssue(
                'NULL_FIELD_REMOVED',
                f'{path}/{field}',
                operation,
                None,
                f'{field} must be omitted for a backdrop',
                repair_id=repair_id,
            ))
        else:
            raise _resource_issue(
                normalized,
                'INVALID_BACKDROP_PLACEMENT',
                f'{path}/{field}',
                actual,
                'backdrop operations do not accept x, y, or size',
            )

    source = normalized.get('source')
    if isinstance(source, dict) and source.get('kind') == 'scratch_library' and source.get('asset_type') != 'backdrop':
        raise _resource_issue(
            normalized,
            'INVALID_BACKDROP_SOURCE',
            f'{path}/source/asset_type',
            source.get('asset_type'),
            'scratch_library backdrop source must use asset_type=backdrop',
        )

    return normalized, diagnostics


def _normalize_target_reference(value: Any, path: str) -> tuple[Any, list[ResourceContractIssue]]:
    if not isinstance(value, dict):
        return value, []
    kind = value.get('kind')
    allowed = {'kind'}
    if kind == 'existing_sprite':
        allowed.add('name')
    elif kind == 'created_sprite':
        allowed.add('op_id')
    elif kind not in {'current_target', 'stage'}:
        return value, []
    removed = {key: value[key] for key in value if key not in allowed and key in {'name', 'op_id'}}
    if not removed:
        return value, []
    normalized = {key: item for key, item in value.items() if key not in removed}
    diagnostics = [
        ResourceContractIssue(
            'REDUNDANT_TARGET_FIELD_REMOVED',
            f'{path}/{key}',
            'manifest',
            item,
            f'target kind={kind} does not use {key}; the redundant field was removed',
            repair_scope='manifest',
        )
        for key, item in removed.items()
    ]
    return normalized, diagnostics


def normalize_project_blueprint_payload(raw: Any) -> Any:
    """Normalize Blueprint input before validation, ensuring project program_jobs use append."""
    if not isinstance(raw, dict):
        return raw
    normalized = dict(raw)
    if isinstance(raw.get('program_jobs'), list):
        jobs: list[Any] = []
        for job in raw['program_jobs']:
            if isinstance(job, dict):
                job_copy = dict(job)
                if job_copy.get('mode') in {'rewrite', 'modify'}:
                    job_copy['mode'] = 'append'
                jobs.append(job_copy)
            else:
                jobs.append(job)
        normalized['program_jobs'] = jobs
    return normalized


def normalize_project_manifest_payload(raw: Any) -> tuple[Any, list[ResourceContractIssue]]:
    if not isinstance(raw, dict):
        return raw, []
    normalized = dict(raw)
    diagnostics: list[ResourceContractIssue] = []
    if isinstance(raw.get('resource_ops'), list):
        operations: list[Any] = []
        for index, operation in enumerate(raw['resource_ops']):
            operation_input = operation
            if isinstance(operation, dict) and 'target' in operation:
                target, target_diagnostics = _normalize_target_reference(operation['target'], f'/resource_ops/{index}/target')
                operation_input = dict(operation)
                operation_input['target'] = target
                diagnostics.extend(target_diagnostics)
            value, operation_diagnostics = normalize_resource_op(operation_input, path=f'/resource_ops/{index}')
            operations.append(value)
            diagnostics.extend(operation_diagnostics)
        normalized['resource_ops'] = operations
    if isinstance(raw.get('program_targets'), list):
        targets: list[Any] = []
        for index, program_target in enumerate(raw['program_targets']):
            if not isinstance(program_target, dict) or 'target' not in program_target:
                targets.append(program_target)
                continue
            target, target_diagnostics = _normalize_target_reference(program_target['target'], f'/program_targets/{index}/target')
            item = dict(program_target)
            item['target'] = target
            targets.append(item)
            diagnostics.extend(target_diagnostics)
        normalized['program_targets'] = targets
    return normalized, diagnostics


class AddBuiltinSpriteOp(StrictModel):
    op: Literal['add_builtin_sprite']
    op_id: ShortString
    source_need_id: ShortString
    owner_entity_id: ShortString
    name: ShortString
    x: int = Field(ge=-10000, le=10000)
    y: int = Field(ge=-10000, le=10000)
    size: int = Field(ge=1, le=1000)
    tags: list[ShortString] = Field(default_factory=list, max_length=20)
    source: ScratchSpriteLibrarySource
    include_costumes: Literal['all']
    include_sounds: Literal['all', 'none'] = 'all'


class AddSpriteOp(StrictModel):
    op: Literal['add_sprite']
    op_id: ShortString
    source_need_id: ShortString
    owner_entity_id: ShortString
    name: ShortString
    x: int = Field(ge=-10000, le=10000)
    y: int = Field(ge=-10000, le=10000)
    size: int = Field(ge=1, le=1000)
    tags: list[ShortString] = Field(default_factory=list, max_length=20)
    costumes: list[CostumeResource] = Field(min_length=1, max_length=12)


class DeleteSpriteOp(StrictModel):
    op: Literal['delete_sprite']
    op_id: ShortString
    name: ShortString
    target: TargetRef

    @model_validator(mode='after')
    def validate_target(self) -> DeleteSpriteOp:
        if self.target.kind != 'existing_sprite':
            raise ValueError('delete_sprite requires an existing_sprite target')
        return self


class AddCostumeOp(StrictModel):
    op: Literal['add_costume']
    op_id: ShortString
    source_need_id: ShortString
    target: TargetRef
    name: ShortString
    source: CostumeSource
    tags: list[ShortString] = Field(default_factory=list, max_length=20)
    select_after: bool = False

    @model_validator(mode='after')
    def validate_target(self) -> AddCostumeOp:
        if self.target.kind == 'stage':
            raise ValueError('add_costume cannot target the stage')
        return self


class AddBackdropOp(StrictModel):
    op: Literal['add_backdrop']
    op_id: ShortString
    source_need_id: ShortString
    name: ShortString
    source: BackdropSource
    tags: list[ShortString] = Field(default_factory=list, max_length=20)
    select_after: bool = False


class AddSoundOp(StrictModel):
    op: Literal['add_sound']
    op_id: ShortString
    source_need_id: ShortString
    target: TargetRef
    name: ShortString
    source: SoundSource
    tags: list[ShortString] = Field(default_factory=list, max_length=20)


class SelectBackdropOp(StrictModel):
    op: Literal['select_backdrop']
    op_id: ShortString
    name: ShortString


class ExistingSoundRef(StrictModel):
    kind: Literal['existing_sound']
    name: ShortString


class DeleteSoundOp(StrictModel):
    op: Literal['delete_sound']
    op_id: ShortString
    target: TargetRef
    sound: ExistingSoundRef


def _normalize_resource_op_for_validation(value: Any) -> Any:
    normalized, _ = normalize_resource_op(value)
    return normalized


ResourceOp = Annotated[
    AddBuiltinSpriteOp | AddSpriteOp | DeleteSpriteOp | AddCostumeOp | AddBackdropOp | AddSoundOp | DeleteSoundOp | SelectBackdropOp,
    Field(discriminator='op'),
    BeforeValidator(_normalize_resource_op_for_validation),
]


BlockInput.model_rebuild()
Block.model_rebuild()
TargetRef.model_rebuild()
ProjectSharedState.model_rebuild()
ProjectProgramTarget.model_rebuild()
ProjectManifest.model_rebuild()
ProjectProgramResponse.model_rebuild()
UnifiedGenerationRequest.model_rebuild()
ProjectManifestGenerationRequest.model_rebuild()
ProjectProgramGenerationRequest.model_rebuild()

