import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import {describe, expect, it, vi} from 'vitest';
import {undoUnifiedGeneration, type UnifiedCommitResult} from '../src/ai-blocks/generation-runner';
import {readAiTargetSnapshot} from '../src/ai-blocks/target-context';
import type {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';

const require = createRequire(import.meta.url);
interface RealVm extends ScratchVmLike {
  attachStorage(storage: unknown): void;
  loadProject(bytes: ArrayBuffer): Promise<void>;
  saveProjectSb3(): Promise<Blob>;
  quit(): void;
}

describe('unified undo after real SB3 reload', () => {
  it.each(['sprite', 'stage'] as const)('restores the original %s selection after the VM allocates new target IDs', async kind => {
    const VirtualMachine = require(fileURLToPath(new URL('../node_modules/@scratch/scratch-vm/src/virtual-machine.js', import.meta.url))) as new () => RealVm;
    const Blocks = require(fileURLToPath(new URL('../node_modules/@scratch/scratch-vm/src/engine/blocks.js', import.meta.url)));
    const Sprite = require(fileURLToPath(new URL('../node_modules/@scratch/scratch-vm/src/sprites/sprite.js', import.meta.url)));
    const {ScratchStorage} = require('@scratch/scratch-storage');
    const vm = new VirtualMachine();
    const storage = new ScratchStorage();
    vm.attachStorage(storage);
    try {
      const runtime = vm.runtime;
      const stage = new Sprite(new Blocks(runtime), runtime).createClone('background');
      stage.isStage = true;
      stage.sprite.name = 'Stage';
      const actor = new Sprite(new Blocks(runtime), runtime).createClone('sprite');
      actor.sprite.name = 'Probe';
      runtime.targets.push(stage, actor);
      const {sanitizeSvg} = require('@scratch/scratch-svg-renderer');
      const bytes = await sanitizeSvg.sanitizeByteStream(new TextEncoder().encode('<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><path d="M0 0h2v2H0z"/></svg>'));
      const asset = storage.createAsset(storage.AssetType.ImageVector, storage.DataFormat.SVG, bytes, null, true);
      for (const target of [stage, actor]) {
        target.sprite.costumes.push({name: 'fixture', asset, assetId: asset.assetId, dataFormat: 'svg', md5: asset.assetId + '.svg', bitmapResolution: 1, rotationCenterX: 1, rotationCenterY: 1});
        target.currentCostume = 0;
      }
      const original = kind === 'stage' ? stage : actor;
      vm.editingTarget = original;
      const originalId = original.id;
      const backup = new Uint8Array(await (await vm.saveProjectSb3()).arrayBuffer());
      const setEditingTarget = vi.spyOn(vm, 'setEditingTarget');
      const release = vi.fn();
      const bridge = {
        currentVm: vm, projectRevision: 1,
        beginBlockGeneration: () => release,
        stop: () => vm.stopAll?.(),
        loadProject: (value: Uint8Array) => vm.loadProject(new Uint8Array(value).buffer),
        refreshWorkspace: vi.fn(), activateCodeTab: vi.fn(),
      } as unknown as ScratchGuiBridge;
      const result: UnifiedCommitResult = {backup, originalTargetId: originalId, originalTargetLocator: {kind, name: original.getName()}, revisionAtComplete: 1, programs: []};

      await undoUnifiedGeneration(bridge, result);

      expect(vm.runtime.targets.some(target => target.id === originalId)).toBe(false);
      expect(vm.editingTarget).toBeDefined();
      expect(vm.editingTarget?.id).not.toBe(originalId);
      expect(vm.editingTarget?.getName?.()).toBe(original.getName());
      expect(Boolean(vm.editingTarget?.isStage)).toBe(kind === 'stage');
      expect(setEditingTarget).not.toHaveBeenCalledWith(originalId);
      expect(readAiTargetSnapshot(bridge, 'append')).toMatchObject({targetName: original.getName(), isStage: kind === 'stage'});
      expect(release).toHaveBeenCalledOnce();
    } finally {vm.quit();}
  });
});
