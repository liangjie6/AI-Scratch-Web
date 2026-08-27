import {describe, expect, it} from 'vitest';
import {validateProjectPlan} from '../src/ai-blocks/project-plan';

const costumeSource = {kind: 'scratch_library', asset_type: 'costume', search_terms: ['猫']};
const base = {schema_version: 1, scope: 'single_sprite', summary: '添加造型', resource_ops: [], block_programs: []};

describe('ProjectPlan contract', () => {
  it('accepts a single-sprite costume operation', () => {
    const result = validateProjectPlan({...base, resource_ops: [{op: 'add_costume', op_id: 'costume_1', target: {kind: 'current_target'}, name: '新造型', source: costumeSource, tags: [], select_after: true}]});
    expect(result.valid).toBe(true);
  });

  it('rejects single-sprite project operations', () => {
    const result = validateProjectPlan({...base, resource_ops: [{op: 'add_backdrop', op_id: 'backdrop_1', name: '背景', source: {...costumeSource, asset_type: 'backdrop'}, tags: [], select_after: true}]});
    expect(result.valid).toBe(false);
    expect(result.issues.some(issue => issue.path === 'resource_ops[0].op')).toBe(true);
  });

  it('rejects forward created target references', () => {
    const result = validateProjectPlan({schema_version: 1, scope: 'project', summary: '创建角色', resource_ops: [{op: 'add_costume', op_id: 'costume_1', target: {kind: 'created_sprite', op_id: 'sprite_1'}, name: '造型', source: costumeSource, tags: [], select_after: true}, {op: 'add_sprite', op_id: 'sprite_1', name: '角色', x: 0, y: 0, size: 100, tags: [], costumes: []}], block_programs: []});
    expect(result.valid).toBe(false);
    expect(result.issues.some(issue => issue.path === 'resource_ops[0].target.op_id')).toBe(true);
  });

  it('rejects library assets from model plans', () => {
    const result = validateProjectPlan({...base, resource_ops: [{op: 'add_costume', op_id: 'asset_1', target: {kind: 'current_target'}, name: '本地', source: {kind: 'library_asset', asset_id: 'private'}, tags: [], select_after: true}]});
    expect(result.valid).toBe(false);
    expect(result.issues.some(issue => issue.path === 'resource_ops[0].source.kind')).toBe(true);
  });
});
