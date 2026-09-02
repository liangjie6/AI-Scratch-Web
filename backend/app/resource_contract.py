from __future__ import annotations

from dataclasses import dataclass
import json
from typing import Any


RESOURCE_CONTRACT_VERSION = 2


def bounded_actual(value: Any, limit: int = 2048) -> Any:
    try:
        encoded = json.dumps(value, ensure_ascii=False, separators=(',', ':'))
    except (TypeError, ValueError):
        encoded = str(value)
        return encoded if len(encoded) <= limit else encoded[: max(0, limit - 3)] + '...'
    if len(encoded) <= limit:
        return value
    return encoded[: max(0, limit - 3)] + '...'


@dataclass(frozen=True)
class ResourceOperationSpec:
    name: str
    required_fields: tuple[str, ...]
    optional_fields: tuple[str, ...] = ()
    defaults: tuple[tuple[str, Any], ...] = ()
    forbidden_fields: tuple[str, ...] = ()
    target_kinds: tuple[str, ...] = ()
    source_asset_types: tuple[str, ...] = ()
    source_kinds: tuple[str, ...] = ('scratch_library', 'image_generation')
    nested_costumes_min: int | None = None
    nested_costumes_max: int | None = None
    description: str = ''

    def as_dict(self) -> dict[str, Any]:
        return {
            'op': self.name,
            'required_fields': list(self.required_fields),
            'optional_fields': list(self.optional_fields),
            'defaults': dict(self.defaults),
            'forbidden_fields': list(self.forbidden_fields),
            'target_kinds': list(self.target_kinds),
            'source_asset_types': list(self.source_asset_types),
            'source_kinds': list(self.source_kinds),
            'nested_costumes_min': self.nested_costumes_min,
            'nested_costumes_max': self.nested_costumes_max,
            'description': self.description,
        }


RESOURCE_OPERATION_SPECS: tuple[ResourceOperationSpec, ...] = (
    ResourceOperationSpec(
        name='add_builtin_sprite',
        required_fields=('op', 'op_id', 'name', 'x', 'y', 'size', 'source'),
        optional_fields=('tags',),
        forbidden_fields=('target', 'select_after', 'costumes'),
        source_asset_types=('sprite',),
        source_kinds=('scratch_library',),
        description='Add a sprite from the Scratch library.',
    ),
    ResourceOperationSpec(
        name='add_sprite',
        required_fields=('op', 'op_id', 'name', 'x', 'y', 'size', 'costumes'),
        optional_fields=('tags',),
        forbidden_fields=('target', 'source', 'select_after'),
        source_asset_types=('costume',),
        nested_costumes_min=1,
        nested_costumes_max=12,
        description='Add a custom sprite with one or more costume resources.',
    ),
    ResourceOperationSpec(
        name='delete_sprite',
        required_fields=('op', 'op_id', 'name', 'target'),
        forbidden_fields=('source', 'x', 'y', 'size', 'tags', 'select_after', 'costumes'),
        target_kinds=('existing_sprite',),
        description='Delete an existing sprite.',
    ),
    ResourceOperationSpec(
        name='add_costume',
        required_fields=('op', 'op_id', 'target', 'name', 'source'),
        optional_fields=('tags', 'select_after'),
        defaults=(('select_after', False),),
        forbidden_fields=('x', 'y', 'size', 'costumes'),
        target_kinds=('current_target', 'existing_sprite', 'created_sprite'),
        source_asset_types=('costume',),
        description='Add a costume to a non-stage target.',
    ),
    ResourceOperationSpec(
        name='add_backdrop',
        required_fields=('op', 'op_id', 'name', 'source'),
        optional_fields=('tags', 'select_after'),
        defaults=(('select_after', False),),
        forbidden_fields=('target', 'x', 'y', 'size', 'costumes'),
        source_asset_types=('backdrop',),
        description='Add a backdrop to the stage.',
    ),
    ResourceOperationSpec(
        name='select_backdrop',
        required_fields=('op', 'op_id', 'name'),
        description='Select an existing backdrop by name.',
    ),
)

RESOURCE_OPERATION_BY_NAME = {item.name: item for item in RESOURCE_OPERATION_SPECS}
RESOURCE_OPERATION_NAMES = tuple(item.name for item in RESOURCE_OPERATION_SPECS)

RESOURCE_SOURCE_SPECS: tuple[dict[str, Any], ...] = (
    {
        'kind': 'scratch_library',
        'required_fields': ('kind', 'asset_type', 'search_terms'),
        'optional_fields': (),
        'asset_types': ('sprite', 'costume', 'backdrop'),
    },
    {
        'kind': 'image_generation',
        'required_fields': ('kind', 'reason', 'prompt', 'style', 'width', 'height', 'sheet_layout'),
        'optional_fields': ('custom_style',),
        'asset_types': (),
    },
)

RESOURCE_DIAGNOSTIC_CODES = (
    'DEFAULT_APPLIED',
    'REDUNDANT_STAGE_TARGET_REMOVED',
    'REDUNDANT_TARGET_FIELD_REMOVED',
    'NULL_FIELD_REMOVED',
)

RESOURCE_ERROR_CODES = (
    'INVALID_BACKDROP_TARGET',
    'INVALID_BACKDROP_PLACEMENT',
    'INVALID_BACKDROP_SOURCE',
    'FIELD_FORBIDDEN',
    'FIELD_REQUIRED',
    'SCHEMA_VALIDATION_ERROR',
    'PROGRAM_CONTRACT_INVALID',
)


def resource_operation_matrix() -> list[dict[str, Any]]:
    return [item.as_dict() for item in RESOURCE_OPERATION_SPECS]


def resource_operation_contract_json() -> str:
    return json.dumps(resource_operation_matrix(), ensure_ascii=False, sort_keys=True, separators=(',', ':'))


def render_resource_operation_contract() -> str:
    rows = [
        'RESOURCE_OPERATION_CONTRACT (schema_version=2; generated from backend/app/resource_contract.py):',
        'Every resource operation must contain only required and optional fields. Never add a forbidden field.',
        '',
        '| op | required fields | optional/default fields | forbidden fields |',
        '| --- | --- | --- | --- |',
    ]
    for spec in RESOURCE_OPERATION_SPECS:
        optional = ', '.join(spec.optional_fields) or 'none'
        if spec.defaults:
            optional += ' (' + ', '.join(f'{key}={value!r}' for key, value in spec.defaults) + ')'
        required = ', '.join(spec.required_fields) or 'none'
        forbidden = ', '.join(spec.forbidden_fields) or 'none'
        rows.append(f'| {spec.name} | {required} | {optional} | {forbidden} |')
    rows.extend([
        '',
        'Source kinds by operation: ' + '; '.join(
            f'{spec.name}={", ".join(spec.source_kinds) or "none"}'
            for spec in RESOURCE_OPERATION_SPECS
        ) + '.',
        'Source asset types by operation: ' + '; '.join(
            f'{spec.name}={", ".join(spec.source_asset_types) or "none"}'
            for spec in RESOURCE_OPERATION_SPECS
        ) + '.',
        'Target kinds by operation: ' + '; '.join(
            f'{spec.name}={", ".join(spec.target_kinds) or "none"}'
            for spec in RESOURCE_OPERATION_SPECS
        ) + '.',
        'The add_sprite costumes array contains 1..12 entries. Each entry has name, source, and optional tags; the source must be a costume or image_generation source.',
        '',
        'RESOURCE_SOURCE_CONTRACT_JSON=' + json.dumps(list(RESOURCE_SOURCE_SPECS), ensure_ascii=False, sort_keys=True, separators=(',', ':')),
        'RESOURCE_OPERATION_CONTRACT_JSON=' + resource_operation_contract_json(),
    ])
    return '\n'.join(rows)
