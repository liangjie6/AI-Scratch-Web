import pytest
from pydantic import ValidationError

from app.schemas.ai_blocks import ProjectManifest


def valid_manifest():
    return {
        'schema_version': 2,
        'scope': 'project',
        'summary': 'Create a sprite',
        'resource_ops': [],
        'program_targets': [],
        'shared_state': {'variables': [], 'lists': [], 'broadcasts': []},
    }


def test_project_manifest_accepts_only_new_strict_shape():
    manifest = ProjectManifest.model_validate(valid_manifest())
    assert manifest.scope == 'project'
    assert manifest.resource_ops == []


def test_project_manifest_requires_shared_state():
    value = valid_manifest()
    value.pop('shared_state')
    with pytest.raises(ValidationError):
        ProjectManifest.model_validate(value)


@pytest.mark.parametrize('field', ['resource_ops', 'program_targets'])
def test_project_manifest_requires_top_level_collections(field):
    value = valid_manifest()
    value.pop(field)
    with pytest.raises(ValidationError):
        ProjectManifest.model_validate(value)


def test_project_manifest_requires_shared_state_collections():
    value = valid_manifest()
    value['shared_state'].pop('variables')
    with pytest.raises(ValidationError):
        ProjectManifest.model_validate(value)


def test_project_manifest_rejects_legacy_target_aliases():
    value = valid_manifest()
    value['resource_ops'] = [{
        'op': 'select_backdrop',
        'op_id': 'select-1',
        'name': 'Forest',
        'target': {'kind': 'stage'},
    }]
    with pytest.raises(ValidationError):
        ProjectManifest.model_validate(value)
