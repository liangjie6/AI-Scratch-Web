import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {describe, expect, it} from 'vitest';
import {BLOCK_CATALOG, SCRATCH_CATALOG_HASH, SCRATCH_SOURCE_VERSIONS, getBlockSpec} from '../src/ai-blocks/block-catalog';

const capabilities = JSON.parse(readFileSync(fileURLToPath(new URL('../../contracts/scratch-capabilities.json', import.meta.url)), 'utf8'));
const overlay = JSON.parse(readFileSync(fileURLToPath(new URL('../../contracts/scratch-policy-overlay.json', import.meta.url)), 'utf8'));

describe('AI blocks opcode contract', () => {
  it('uses the same generated authoring contracts and hash as the capability artifact', () => {
    expect(SCRATCH_CATALOG_HASH).toBe(capabilities.catalog_hash);
    expect(SCRATCH_SOURCE_VERSIONS).toEqual(capabilities.sources.packages);
    expect(Object.keys(BLOCK_CATALOG).sort()).toEqual(Object.keys(capabilities.blocks).sort());
    for (const opcode of Object.keys(BLOCK_CATALOG)) expect(getBlockSpec(opcode)).toEqual(capabilities.blocks[opcode].authoring);
    const backend = readFileSync(fileURLToPath(new URL('../../backend-go/internal/blockcatalog/catalog.go', import.meta.url)), 'utf8');
    expect(backend).toContain(`const CatalogHash = "${SCRATCH_CATALOG_HASH}"`);
  });

  it('preserves the reviewed allowlist and literal, target, reference and compiler shadow policy', () => {
    expect(Object.keys(BLOCK_CATALOG).sort()).toEqual(Object.keys(overlay.blocks).sort());
    expect(capabilities.allowed_extensions).toEqual(['pen', 'music', 'text2speech']);
    for (const [opcode, policy] of Object.entries(overlay.blocks) as Array<[string, {targets: string[]; inputs?: Record<string, unknown>; fieldReferences?: Record<string, unknown>; shapeOverride?: string}]>) {
      const spec = getBlockSpec(opcode)!;
      expect(spec.targets).toEqual(policy.targets);
      for (const [name, input] of Object.entries(policy.inputs ?? {})) expect(spec.inputs?.[name]).toMatchObject(input as object);
      for (const [name, field] of Object.entries(policy.fieldReferences ?? {})) expect(spec.fields?.[name]).toMatchObject(field as object);
      if (policy.shapeOverride) expect(spec.shape).toBe(policy.shapeOverride);
    }
    expect(getBlockSpec('control_while')).toBeUndefined();
    expect(getBlockSpec('translate_getTranslate')).toBeUndefined();
  });

  it('keeps native defaults distinct from authoring literal and shadow contracts', () => {
    expect(capabilities.blocks.motion_movesteps.source_facts.inputs[0].connection_check).toBeNull();
    expect(capabilities.blocks.motion_movesteps.source_facts.default_shadows.STEPS).toEqual({opcode: 'math_number', fields: {NUM: '10'}});
    expect(capabilities.blocks.control_repeat.source_facts.default_shadows.TIMES.opcode).toBe('math_whole_number');
    expect(getBlockSpec('control_repeat')?.inputs?.TIMES).toMatchObject({kind: 'number', shadow: 'number'});
    expect(getBlockSpec('looks_say')?.inputs?.MESSAGE.kind).toBe('text');
    expect(capabilities.blocks.data_setvariableto.source_facts.default_shadows.VALUE).toEqual({opcode: 'text', fields: {TEXT: '0'}});
  });

  it.each([
    ['data_setvariableto', 'VALUE'], ['data_addtolist', 'ITEM'],
    ['data_insertatlist', 'ITEM'], ['data_replaceitemoflist', 'ITEM'],
  ])('keeps polymorphic state values separate from their editable text default: %s.%s', (opcode, name) => {
    expect(capabilities.blocks[opcode].source_facts.inputs.find((input: {name: string}) => input.name === name).connection_check).toBeNull();
    expect(capabilities.blocks[opcode].source_facts.default_shadows[name].opcode).toBe('text');
    expect(getBlockSpec(opcode)?.inputs?.[name]).toMatchObject({kind: 'any', shadow: 'text', required: true});
  });

  it('extracts serialized static enums and separates dynamic data from UI menu commands', () => {
    expect(getBlockSpec('event_whenkeypressed')?.fields?.KEY_OPTION.allowedValues).toEqual(expect.arrayContaining(['i', 'space', 'left arrow', '9']));
    expect(getBlockSpec('sensing_keypressed')?.inputs?.KEY_OPTION.allowedValues).toContain('i');
    expect(getBlockSpec('data_setvariableto')?.fields?.VARIABLE).toMatchObject({resolver: 'variable', menuProvider: 'variables_in_scope'});
    expect(getBlockSpec('motion_goto')?.inputs?.TO).toMatchObject({menuProvider: 'sprites_except_self', builtinValues: ['_random_', '_mouse_']});
    expect(getBlockSpec('sound_play')?.inputs?.SOUND_MENU).toMatchObject({menuProvider: 'sounds_of_target'});
    expect(getBlockSpec('sensing_touchingcolor')?.inputs?.COLOR.allowedValues).toBeUndefined();
    for (const [opcode, block] of Object.entries(capabilities.blocks)) expect(/fixture-|RENAME_VARIABLE_ID|DELETE_VARIABLE_ID|SOUND_RECORD/.test(JSON.stringify(block)), opcode).toBe(false);
  });

  it('records dynamic stop shapes without enabling the unsupported continuation variant', () => {
    expect(capabilities.blocks.control_stop.source_facts.variants).toEqual([
      {field: 'STOP_OPTION', value: 'all', has_next: false},
      {field: 'STOP_OPTION', value: 'this script', has_next: false},
      {field: 'STOP_OPTION', value: 'other scripts in sprite', has_next: true},
    ]);
    expect(getBlockSpec('control_stop')?.shape).toBe('cap');
    expect(capabilities.blocks.control_stop.unsupported_variants).toHaveLength(1);
    expect(capabilities.runtime_facts).toMatchObject({core_blocks_json_count: 0});
    expect(capabilities.runtime_facts.native_primitives).toBeGreaterThan(100);
  });
});
