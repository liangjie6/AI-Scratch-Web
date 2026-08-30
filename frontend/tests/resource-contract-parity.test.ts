import {describe, expect, it} from 'vitest';
import {
  RESOURCE_CONTRACT_VERSION,
  RESOURCE_OPERATION_CONTRACT,
  RESOURCE_OPERATION_NAMES,
  RESOURCE_SOURCE_CONTRACT,
} from '../src/ai-blocks/generated/resource-contract';

describe('generated resource contract', () => {
  it('exposes the v2 operation matrix without legacy sprite source fields', () => {
    expect(RESOURCE_CONTRACT_VERSION).toBe(2);
    expect([...RESOURCE_OPERATION_NAMES]).toEqual([
      'add_builtin_sprite',
      'add_sprite',
      'delete_sprite',
      'add_costume',
      'add_backdrop',
      'select_backdrop',
    ]);
    const addSprite = RESOURCE_OPERATION_CONTRACT.find(item => item.op === 'add_sprite');
    expect(addSprite?.required_fields).toContain('costumes');
    expect(addSprite?.forbidden_fields).toContain('source');
    expect(addSprite?.nested_costumes_min).toBe(1);
    expect(addSprite?.nested_costumes_max).toBe(12);
  });

  it('exposes only the two supported source kinds', () => {
    expect(RESOURCE_SOURCE_CONTRACT.map(item => item.kind)).toEqual(['scratch_library', 'image_generation']);
    expect(RESOURCE_SOURCE_CONTRACT[0].required_fields).toEqual(['kind', 'asset_type', 'search_terms']);
  });
});
