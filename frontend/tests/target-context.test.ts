import {describe, expect, it} from 'vitest';
import {assertSnapshotIsCurrent, readAiTargetSnapshot} from '../src/ai-blocks/target-context';
import type {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';

function block(id: string, opcode: string, options: Partial<ScratchBlockLike> = {}): ScratchBlockLike {
  return {
    id, opcode, inputs: {}, fields: {}, next: null, parent: null,
    shadow: false, topLevel: false, ...options
  };
}

describe('AI target snapshot', () => {
  it('rejects a silent block mutation even when the project revision is unchanged', () => {
    const blockRecord = block('native', 'event_whenflagclicked', {topLevel: true});
    const blocks = {
      _blocks: {native: blockRecord}, _scripts: ['native'],
      getScripts: () => ['native'],
      getBlock: (id: string) => ({native: blockRecord}[id])
    } as unknown as ScratchBlocksLike;
    const target = {id: 'sprite', isStage: false, isOriginal: true, blocks, variables: {}, comments: {}, getName: () => '角色1', getCostumes: () => [], getSounds: () => []} as ScratchTargetLike;
    const stage = {id: 'stage', isStage: true, variables: {}, getName: () => '舞台'} as ScratchTargetLike;
    const vm = {editingTarget: target, runtime: {targets: [stage, target], getTargetForStage: () => stage}} as unknown as ScratchVmLike;
    const bridge = {currentVm: vm, projectRevision: 7} as unknown as ScratchGuiBridge;
    const snapshot = readAiTargetSnapshot(bridge, 'rewrite');
    blockRecord.fields = {VALUE: {name: 'VALUE', value: 'changed'}};
    expect(() => assertSnapshotIsCurrent(bridge, snapshot)).toThrow('当前目标内容发生了变化');
  });

  it('exposes native script refs but protects custom and extension scripts', () => {
    const records: Record<string, ScratchBlockLike> = {
      native: block('native', 'event_whenflagclicked', {topLevel: true, x: 10, next: 'move'}),
      move: block('move', 'motion_movesteps', {parent: 'native', inputs: {
        STEPS: {name: 'STEPS', block: 'number', shadow: 'number'}
      }}),
      number: block('number', 'math_number', {parent: 'move', shadow: true, fields: {
        NUM: {name: 'NUM', value: 10}
      }}),
      custom: block('custom', 'procedures_definition', {topLevel: true, x: 400}),
      extension: block('extension', 'pen_clear', {topLevel: true, x: 800})
    };
    const blocks = {
      _blocks: records, _scripts: ['native', 'custom', 'extension'],
      getScripts: () => ['native', 'custom', 'extension'],
      getBlock: (id: string) => records[id]
    } as unknown as ScratchBlocksLike;
    const stage = {id: 'stage', isStage: true, variables: {}, getName: () => '舞台'} as ScratchTargetLike;
    const target = {
      id: 'sprite', isStage: false, isOriginal: true, blocks, variables: {}, comments: {},
      getName: () => '角色1', getCostumes: () => [], getSounds: () => []
    } as ScratchTargetLike;
    const vm = {
      editingTarget: target,
      runtime: {targets: [stage, target], getTargetForStage: () => stage}
    } as unknown as ScratchVmLike;
    const bridge = {currentVm: vm, projectRevision: 7} as unknown as ScratchGuiBridge;

    const snapshot = readAiTargetSnapshot(bridge, 'rewrite');
    const current = JSON.parse(snapshot.context.current_program) as {
      scripts: Array<{id: string; protected: boolean}>
    };
    expect(snapshot.scriptRefs).toEqual({script_1: 'native'});
    expect(current.scripts).toEqual([
      expect.objectContaining({id: 'script_1', protected: false}),
      expect.objectContaining({id: 'script_2', protected: true}),
      expect.objectContaining({id: 'script_3', protected: true})
    ]);
  });
});
