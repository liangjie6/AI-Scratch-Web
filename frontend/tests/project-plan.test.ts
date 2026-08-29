import {describe, expect, it} from 'vitest';
import {validateProjectManifest} from '../src/ai-blocks/project-plan';

const costumeSource = {kind: 'scratch_library', asset_type: 'costume', search_terms: ['猫']};
const base = {schema_version: 1, scope: 'project', summary: '添加造型', resource_ops: [], program_targets: [], shared_state: {variables: [], lists: [], broadcasts: []}};

describe('ProjectManifest contract', () => {
  it('accepts a project costume operation', () => {
    const result = validateProjectManifest({...base, resource_ops: [{op: 'add_costume', op_id: 'costume_1', target: {kind: 'current_target'}, name: '新造型', source: costumeSource, tags: [], select_after: true}]});
    expect(result.valid).toBe(true);
  });

  it('accepts a backdrop operation in project scope', () => {
    const result = validateProjectManifest({...base, resource_ops: [{op: 'add_backdrop', op_id: 'backdrop_1', name: '背景', source: {...costumeSource, asset_type: 'backdrop'}, tags: [], select_after: true}]});
    expect(result.valid).toBe(true);
  });

  it('rejects the removed single-sprite manifest scope', () => {
    const result = validateProjectManifest({...base, scope: 'single_sprite'});
    expect(result.valid).toBe(false);
    expect(result.issues.some(issue => issue.path === 'scope')).toBe(true);
  });

  it('rejects forward created target references', () => {
    const result = validateProjectManifest({...base, resource_ops: [
      {op: 'add_costume', op_id: 'costume_1', target: {kind: 'created_sprite', op_id: 'sprite_1'}, name: '造型', source: costumeSource, tags: [], select_after: true},
      {op: 'add_sprite', op_id: 'sprite_1', name: '角色', x: 0, y: 0, size: 100, tags: [], source: {kind: 'image_generation', reason: '需要自定义角色', prompt: '角色', style: 'cartoon', width: 512, height: 512, sheet_layout: '1x1'}, costumes: []}
    ]});
    expect(result.valid).toBe(false);
    expect(result.issues.some(issue => issue.path === 'resource_ops[0].target.op_id')).toBe(true);
  });

  it('rejects model-only library assets', () => {
    const result = validateProjectManifest({...base, resource_ops: [{op: 'add_costume', op_id: 'asset_1', target: {kind: 'current_target'}, name: '本地', source: {kind: 'library_asset', asset_id: 'private'}, tags: [], select_after: true}]});
    expect(result.valid).toBe(false);
    expect(result.issues.some(issue => issue.path === 'resource_ops[0].source.kind')).toBe(true);
  });

  it('requires shared state names to be unique and global', () => {
    const result = validateProjectManifest({...base, shared_state: {variables: [{name: 'score', scope: 'local'}, {name: 'score', scope: 'global'}], lists: [], broadcasts: []}});
    expect(result.valid).toBe(false);
    expect(result.issues.some(issue => issue.path.includes('shared_state.variables'))).toBe(true);
  });
});
