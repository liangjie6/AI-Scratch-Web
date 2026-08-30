import pytest
from pydantic import ValidationError

from app.schemas.ai_blocks import ProjectManifest


def valid_manifest():
    return {
        'schema_version': 1,
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
