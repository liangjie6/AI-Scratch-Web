import type {ImportedSpriteResult, SpriteImportExpectation} from './asset-adapter';

type Listener = () => void;

export class ScratchGuiBridge {
  private vm: ScratchVmLike | null = null;
  private storage: ScratchStorageLike | null = null;
  private editorState: ScratchEditorStateLike | null = null;
  private projectLoaded = false;
  private readyResolve: ((vm: ScratchVmLike) => void) | null = null;
  private importing = false;
  private fatalImport = false;
  private generatingBlocks = false;
  private revision = 0;
  private targetListeners = new Set<Listener>();
  private projectListeners = new Set<Listener>();

  readonly ready = new Promise<ScratchVmLike>(resolve => {
    this.readyResolve = resolve;
  });

  setStorage(storage: ScratchStorageLike) {
    this.storage = storage;
  }

  setEditorState(editorState: ScratchEditorStateLike) {
    this.editorState = editorState;
  }

  setVm(vm: ScratchVmLike) {
    this.vm = vm;
    const onProjectLoaded = () => this.markProjectLoaded();
    const onProjectChanged = () => {
      this.revision += 1;
      this.projectListeners.forEach(listener => listener());
    };
    const onTargetsUpdate = () => {
      this.targetListeners.forEach(listener => listener());
    };

    vm.on?.('PROJECT_LOADED', onProjectLoaded);
    vm.on?.('PROJECT_CHANGED', onProjectChanged);
    vm.on?.('targetsUpdate', onTargetsUpdate);
    vm.runtime.on?.('PROJECT_LOADED', onProjectLoaded);

    const waitForTargets = () => {
      if (this.projectLoaded || this.vm !== vm) return;
      if (vm.runtime.targets.some(target => target.isStage)) {
        this.markProjectLoaded();
        return;
      }
      window.setTimeout(waitForTargets, 50);
    };
    waitForTargets();

    if (this.projectLoaded) {
      this.readyResolve?.(vm);
      this.readyResolve = null;
    }
  }

  markProjectLoaded() {
    const firstLoad = !this.projectLoaded;
    this.projectLoaded = true;
    if (this.vm) {
      this.readyResolve?.(this.vm);
      this.readyResolve = null;
    }
    if (!firstLoad) {
      this.revision += 1;
      this.projectListeners.forEach(listener => listener());
      this.targetListeners.forEach(listener => listener());
    }
  }

  get isReady() {
    return Boolean(this.vm && this.projectLoaded);
  }

  get currentVm() {
    return this.vm;
  }

  get currentStorage() {
    return this.storage;
  }

  get importIsFatal() {
    return this.fatalImport;
  }

  get projectRevision() {
    return this.revision;
  }

  subscribeTargets(listener: Listener) {
    this.targetListeners.add(listener);
    return () => this.targetListeners.delete(listener);
  }

  subscribeProject(listener: Listener) {
    this.projectListeners.add(listener);
    return () => this.projectListeners.delete(listener);
  }

  activateCodeTab() {
    this.editorState?.store.dispatch({
      type: 'scratch-gui/navigation/ACTIVATE_TAB',
      activeTabIndex: 0
    });
  }

  beginBlockGeneration() {
    if (this.generatingBlocks) throw new Error('已有 AI 积木生成任务正在运行');
    this.generatingBlocks = true;
    let released = false;
    return () => {
      if (released) return;
      released = true;
      this.generatingBlocks = false;
    };
  }

  refreshWorkspace() {
    this.requireVm().emitWorkspaceUpdate?.();
  }

  async commitSpriteImport(
    spriteJson: Record<string, unknown>,
    expectations: SpriteImportExpectation[]
  ): Promise<ImportedSpriteResult> {
    const vm = this.requireVm();
    if (!this.projectLoaded) throw new Error('Scratch 项目尚未就绪');
    if (this.fatalImport) throw new Error('上次导入补偿失败，请重新加载最近保存的项目');
    if (this.importing) throw new Error('角色正在导入，请勿重复提交');
    if (this.generatingBlocks) throw new Error('AI 正在生成积木，请稍后再导入角色');
    this.importing = true;
    const before = new Set(vm.runtime.targets.map(target => target.id));

    try {
      await vm.addSprite(JSON.stringify(spriteJson));
      const additions = vm.runtime.targets.filter(target => !before.has(target.id) && !target.isStage);
      if (additions.length !== 1) throw new Error('Scratch 未能创建唯一的新角色');
      const target = additions[0];
      const installed = target.getCostumes?.() ?? target.sprite?.costumes ?? [];
      if (installed.length !== expectations.length) throw new Error('Scratch 造型数量校验失败');

      expectations.forEach((expected, index) => {
        const actual = installed[index];
        if (!actual) throw new Error('Scratch 造型元数据校验失败：' + expected.name + '（造型缺失）');
        const mismatches: string[] = [];
        if (actual.name !== expected.name) mismatches.push('名称实际为“' + String(actual.name ?? '') + '”');
        if (actual.bitmapResolution !== expected.bitmapResolution) {
          mismatches.push('资源分辨率实际为 ' + String(actual.bitmapResolution));
        }
        if (String(actual.assetId ?? '') !== expected.assetId) mismatches.push('资源 ID 不一致');
        if (Math.abs(Number(actual.rotationCenterX) - expected.rotationCenterX) > 0.001) {
          mismatches.push('旋转中心 X 实际为 ' + String(actual.rotationCenterX));
        }
        if (Math.abs(Number(actual.rotationCenterY) - expected.rotationCenterY) > 0.001) {
          mismatches.push('旋转中心 Y 实际为 ' + String(actual.rotationCenterY));
        }
        if (mismatches.length) {
          throw new Error('Scratch 造型元数据校验失败：' + expected.name + '（' + mismatches.join('；') + '）');
        }
      });

      vm.setEditingTarget(target.id);
      return {
        targetId: target.id,
        costumeAssetIds: installed.map(costume => String(costume.assetId ?? ''))
      };
    } catch (error) {
      const additions = vm.runtime.targets.filter(target => !before.has(target.id) && !target.isStage);
      for (const target of additions) {
        try {
          vm.deleteSprite(target.id);
        } catch {
          this.fatalImport = true;
        }
      }
      if (vm.runtime.targets.some(target => !before.has(target.id) && !target.isStage)) {
        this.fatalImport = true;
      }
      if (this.fatalImport) {
        throw new Error('导入失败且无法清除半成品角色，请重新加载最近保存的项目');
      }
      throw error;
    } finally {
      this.importing = false;
    }
  }

  async saveProject() {
    return this.requireVm().saveProjectSb3();
  }

  async loadProject(input: ArrayBuffer | Uint8Array | string) {
    await this.requireVm().loadProject(input);
  }

  run() {
    this.requireVm().greenFlag();
  }

  stop() {
    this.requireVm().stopAll();
  }

  private requireVm() {
    if (!this.vm) throw new Error('Scratch 编辑器尚未就绪');
    return this.vm;
  }
}
