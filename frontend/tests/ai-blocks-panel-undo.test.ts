// @vitest-environment jsdom
import {EventEmitter} from 'node:events';
import {afterEach, describe, expect, it, vi} from 'vitest';
import {flushPromises, mount} from '@vue/test-utils';
import AiBlocksPanel from '../src/ai-blocks/AiBlocksPanel.vue';
import {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';
import type {UnifiedCommitResult} from '../src/ai-blocks/generation-runner';

vi.mock('../src/ai-blocks/ai-runs-api', async importOriginal => ({...await importOriginal<typeof import('../src/ai-blocks/ai-runs-api')>(), getAiRuntimeCapabilities: vi.fn(async () => undefined)}));
afterEach(() => {localStorage.clear(); sessionStorage.clear();});

function fixture() {
  const target = (id: string, isStage: boolean): ScratchTargetLike => ({id, isStage, isOriginal: true, getName: () => isStage ? 'Stage' : 'Probe', blocks: {_blocks: {}, getScripts: () => [], getBlock: () => undefined} as unknown as ScratchBlocksLike, variables: {}, comments: {}, getCostumes: () => [], getSounds: () => []});
  const events = new EventEmitter();
  let resume: (() => void) | undefined;
  const vm = Object.assign(events, {
    editingTarget: target('original_actor', false) as ScratchTargetLike | undefined,
    runtime: {targets: [target('original_stage', true)] as ScratchTargetLike[], getTargetForStage: () => vm.runtime.targets.find(value => value.isStage)},
    stopAll: vi.fn(), emitWorkspaceUpdate: vi.fn(),
    setEditingTarget: vi.fn((id: string) => {vm.editingTarget = vm.runtime.targets.find(value => value.id === id); events.emit('targetsUpdate');}),
    loadProject: vi.fn(async () => {
      // Match VM.clear(): null selection is synchronously observable before the
      // asynchronous deserializer installs targets with fresh IDs.
      vm.runtime.targets = [];
      vm.editingTarget = undefined;
      events.emit('targetsUpdate');
      await new Promise<void>(resolve => {resume = resolve;});
      vm.runtime.targets = [target('restored_stage', true), target('restored_actor', false)];
      vm.editingTarget = vm.runtime.targets[1];
      events.emit('targetsUpdate');
      events.emit('PROJECT_LOADED');
    }),
  });
  vm.runtime.targets.push(vm.editingTarget!);
  const bridge = new ScratchGuiBridge();
  bridge.setVm(vm as unknown as ScratchVmLike);
  const wrapper = mount(AiBlocksPanel, {props: {bridge}, global: {stubs: {ResourcePlanReview: true}}});
  return {vm, bridge, wrapper, resume: () => resume?.()};
}

describe('AI panel project reload lifecycle', () => {
  it('keeps undo locally busy while VM.clear emits an empty target list', async () => {
    const f = fixture();
    try {
      await flushPromises();
      const result: UnifiedCommitResult = {backup: new Uint8Array([1]), originalTargetId: 'original_actor', originalTargetLocator: {kind: 'sprite', name: 'Probe'}, revisionAtComplete: f.bridge.projectRevision, programs: []};
      // Seed the completed commit without issuing a model request. Vue keeps
      // script-setup bindings here but omits that field from its public type.
      const state = (f.wrapper.vm.$ as unknown as {setupState: {generationResult: UnifiedCommitResult | null}}).setupState;
      state.generationResult = result;
      await f.wrapper.vm.$nextTick();
      const undo = f.wrapper.findAll('button').find(button => button.text() === '撤销生成')!;
      await undo.trigger('click');
      await flushPromises();
      expect(f.vm.editingTarget).toBeUndefined();
      expect(f.wrapper.get('textarea').attributes('disabled')).toBeDefined();
      expect(f.wrapper.text()).not.toContain('请等待 Scratch 编辑器加载完成');
      f.resume();
      await flushPromises();
      expect(f.vm.editingTarget?.id).toBe('restored_actor');
      expect(f.wrapper.text()).toContain('已撤销本次 AI 生成。');
      expect(f.wrapper.findAll('button').some(button => button.text() === '撤销生成')).toBe(false);
    } finally {f.wrapper.unmount(); f.bridge.dispose();}
  });

  it('allows a normal project reload to publish its transient empty target list', async () => {
    const f = fixture();
    try {
      await flushPromises();
      const pending = f.bridge.loadProject(new Uint8Array([1]));
      f.resume();
      await expect(pending).resolves.toBeUndefined();
      await flushPromises();
      expect(f.vm.editingTarget?.id).toBe('restored_actor');
      expect(f.wrapper.text()).toContain('Probe');
    } finally {f.wrapper.unmount(); f.bridge.dispose();}
  });
});
