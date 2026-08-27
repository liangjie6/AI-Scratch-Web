import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {applyAiProgram, undoAiGeneration} from '../src/ai-blocks/generation-runner';
import type {ProgramIR} from '../src/ai-blocks/program-ir';
import type {AiTargetSnapshot} from '../src/ai-blocks/target-context';
import type {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';

class MockBlocks implements ScratchBlocksLike {
  _blocks: Record<string, ScratchBlockLike> = {};
  _scripts: string[] = [];
  constructor(private readonly changed: () => void) {}
  getScripts() { return [...this._scripts]; }
  getBlock(id: string) { return this._blocks[id]; }
  createBlock(block: ScratchBlockLike) {
    if (this._blocks[block.id]) return;
    this._blocks[block.id] = structuredClone(block);
    if (block.topLevel && !block.shadow) this._scripts.push(block.id);
    this.changed();
  }
  moveBlock(event: {id: string; newParent?: string; newInput?: string}) {
    const block = this._blocks[event.id];
    if (!block || !event.newParent) return;
    this._scripts = this._scripts.filter(id => id !== event.id);
    const parent = this._blocks[event.newParent];
    if (event.newInput) {
      parent.inputs[event.newInput] = {name: event.newInput, block: event.id, shadow: null};
    } else parent.next = event.id;
    block.parent = event.newParent;
    block.topLevel = false;
    this.changed();
  }
  deleteBlock(id: string) {
    const block = this._blocks[id];
    if (!block) return;
    if (block.next) this.deleteBlock(block.next);
    Object.values(block.inputs).forEach(input => {
      if (input.block) this.deleteBlock(input.block);
      if (input.shadow && input.shadow !== input.block) this.deleteBlock(input.shadow);
    });
    delete this._blocks[id];
    this._scripts = this._scripts.filter(value => value !== id);
    this.changed();
  }
  deleteAllBlocks() { this.getScripts().forEach(id => this.deleteBlock(id)); }
  toXML() { return '<xml />'; }
}

function oldScript(id = 'old_top'): ScratchBlockLike {
  return {
    id,
    opcode: 'event_whenflagclicked',
    inputs: {},
    fields: {},
    next: null,
    parent: null,
    shadow: false,
    topLevel: true,
    x: 20,
    y: 30
  };
}

function program(remove: string[] = []): ProgramIR {
  return {
    schema_version: 1,
    summary: '移动角色',
    remove_script_ids: remove,
    variables: [],
    lists: [],
    broadcasts: [],
    scripts: [{
      id: 'script_move',
      purpose: '绿旗点击后移动',
      x: 40,
      y: 40,
      blocks: [
        {opcode: 'event_whenflagclicked'},
        {opcode: 'motion_movesteps', inputs: {STEPS: {kind: 'number', value: 10}}}
      ]
    }]
  };
}

function harness(withOldScript = true) {
  let revision = 1;
  const blocks = new MockBlocks(() => { revision += 1; });
  if (withOldScript) blocks.createBlock(oldScript());
  const target: ScratchTargetLike = {
    id: 'sprite',
    isStage: false,
    isOriginal: true,
    getName: () => '角色1',
    blocks,
    comments: {},
    variables: {},
    getCostumes: () => [],
    getSounds: () => [],
    createVariable(id, name, type) {
      this.variables![id] = {id, name, type, value: type === 'list' ? [] : type === 'broadcast_msg' ? name : 0};
    },
    deleteVariable(id) { delete this.variables![id]; },
    createComment(id, blockId, text, x, y, width, height, minimized) {
      this.comments![id] = {id, blockId, text, x, y, width, height, minimized};
      if (blockId && this.blocks?.getBlock(blockId)) this.blocks.getBlock(blockId)!.comment = id;
    }
  };
  const stage: ScratchTargetLike = {
    id: 'stage', isStage: true, isOriginal: true, getName: () => '舞台',
    blocks: new MockBlocks(() => { revision += 1; }), comments: {}, variables: {},
    createVariable(id, name, type) { this.variables![id] = {id, name, type, value: type === 'list' ? [] : name}; },
    deleteVariable(id) { delete this.variables![id]; }
  };
  const vm = {
    editingTarget: target,
    runtime: {
      targets: [stage, target],
      getTargetById: (id: string) => [stage, target].find(item => item.id === id) ?? null,
      getTargetForStage: () => stage
    },
    setEditingTarget: vi.fn(),
    stopAll: vi.fn()
  } as unknown as ScratchVmLike;
  let active = false;
  const bridge = {
    currentVm: vm,
    get projectRevision() { return revision; },
    saveProject: vi.fn(async () => new Uint8Array([1, 2, 3])),
    loadProject: vi.fn(async () => undefined),
    refreshWorkspace: vi.fn(),
    beginBlockGeneration: vi.fn(() => {
      if (active) throw new Error('busy');
      active = true;
      return () => { active = false; };
    }),
    stop: vi.fn(),
    activateCodeTab: vi.fn()
  } as unknown as ScratchGuiBridge;
  const snapshot: AiTargetSnapshot = {
    targetId: target.id,
    targetName: '角色1',
    isStage: false,
    revision,
    blockCount: withOldScript ? 1 : 0,
    hasScripts: withOldScript,
    scriptRefs: withOldScript ? {script_1: 'old_top'} : {},
    context: {
      name: '角色1', is_stage: false, has_scripts: withOldScript, mode: 'append',
      costumes: [], sounds: [], sprite_names: ['角色1'], current_program: '{}'
    }
  };
  return {bridge, blocks, target, snapshot, getRevision: () => revision};
}

async function settle<T>(promise: Promise<T>) {
  await vi.runAllTimersAsync();
  return promise;
}

describe('AI block generation transaction', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('window', {setTimeout, clearTimeout});
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('appends a real script beside existing code and adds a Scratch comment', async () => {
    const h = harness(true);
    const result = await settle(applyAiProgram({bridge: h.bridge, snapshot: h.snapshot, mode: 'append', program: program()}));
    expect(h.blocks.getBlock('old_top')).toBeTruthy();
    expect(h.blocks.getScripts()).toHaveLength(2);
    expect(Object.values(h.target.comments ?? {})).toEqual([
      expect.objectContaining({text: 'AI 生成：绿旗点击后移动', minimized: true})
    ]);
    expect(result).toMatchObject({addedBlocks: 2, addedScripts: 1, removedScripts: 0});
  });

  it('restores the saved SB3 snapshot for explicit undo', async () => {
    const h = harness(true);
    const result = await settle(applyAiProgram({bridge: h.bridge, snapshot: h.snapshot, mode: 'append', program: program()}));
    await undoAiGeneration(h.bridge, result);
    expect(h.bridge.loadProject).toHaveBeenCalledWith(result.backup);
    expect(h.bridge.currentVm?.setEditingTarget).toHaveBeenCalledWith('sprite');
    expect(h.bridge.activateCodeTab).toHaveBeenCalled();
  });

  it('replaces only referenced scripts in modify mode', async () => {
    const h = harness(true);
    const result = await settle(applyAiProgram({bridge: h.bridge, snapshot: h.snapshot, mode: 'modify', program: program(['script_1'])}));
    expect(h.blocks.getBlock('old_top')).toBeUndefined();
    expect(h.blocks.getScripts()).toHaveLength(1);
    expect(result.removedScripts).toBe(1);
    expect(Object.keys(h.target.comments ?? {})).toHaveLength(0);
  });

  it('clears scripts but preserves data in rewrite mode', async () => {
    const h = harness(true);
    h.target.variables!.score = {id: 'score', name: '得分', type: '', value: 8};
    await settle(applyAiProgram({bridge: h.bridge, snapshot: h.snapshot, mode: 'rewrite', program: program()}));
    expect(h.blocks.getBlock('old_top')).toBeUndefined();
    expect(h.target.variables!.score.value).toBe(8);
  });

  it('preserves custom and extension scripts during rewrite', async () => {
    const h = harness(true);
    h.blocks.createBlock({...oldScript('custom_top'), opcode: 'procedures_definition', x: 400});
    h.blocks.createBlock({...oldScript('extension_top'), opcode: 'pen_clear', x: 800});
    h.snapshot.revision = h.bridge.projectRevision;
    await settle(applyAiProgram({bridge: h.bridge, snapshot: h.snapshot, mode: 'rewrite', program: program()}));
    expect(h.blocks.getBlock('old_top')).toBeUndefined();
    expect(h.blocks.getBlock('custom_top')).toBeTruthy();
    expect(h.blocks.getBlock('extension_top')).toBeTruthy();
    expect(h.blocks.getScripts()).toHaveLength(3);
    const generatedId = h.blocks.getScripts().find(id => !['custom_top', 'extension_top'].includes(id));
    expect(h.blocks.getBlock(generatedId!)).toMatchObject({x: 1160});
  });

  it('removes partially installed variables when setup fails', async () => {
    const h = harness(false);
    let created = 0;
    const createVariable = h.target.createVariable!.bind(h.target);
    h.target.createVariable = (id, name, type) => {
      created += 1;
      if (created === 2) return;
      createVariable(id, name, type);
    };
    const withVariables = program();
    withVariables.variables = [{name: 'first'}, {name: 'second'}];
    const pending = applyAiProgram({
      bridge: h.bridge, snapshot: h.snapshot, mode: 'append', program: withVariables
    });
    await expect(pending).rejects.toThrow('Scratch 无法创建变量：second');
    expect(h.target.variables).toEqual({});
    expect(h.blocks.getScripts()).toEqual([]);
  });

  it('locally restores deleted code when generation is cancelled', async () => {
    const h = harness(true);
    const controller = new AbortController();
    const pending = applyAiProgram({
      bridge: h.bridge,
      snapshot: h.snapshot,
      mode: 'modify',
      program: program(['script_1']),
      signal: controller.signal,
      onProgress: () => controller.abort()
    });
    const rejection = expect(pending).rejects.toMatchObject({name: 'AbortError'});
    await vi.runAllTimersAsync();
    await rejection;
    expect(h.blocks.getBlock('old_top')).toEqual(expect.objectContaining({opcode: 'event_whenflagclicked'}));
    expect(h.blocks.getScripts()).toEqual(['old_top']);
  });
});
