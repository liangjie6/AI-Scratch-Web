import type {CostumeExport, ImportedSpriteResult, SoundExport, SpriteImportExpectation, SpriteSoundExpectation} from './asset-adapter';
import type {ScratchEditorPort} from './scratch-editor-port';

type Listener = () => void;

export interface ProjectTransactionHandle {
  assertActive(): void;
  release(): void;
}

export class ScratchGuiBridge implements ScratchEditorPort {
  private vm: ScratchVmLike | null = null;
  private vmListenerCleanups: Array<() => void> = [];
  private storage: ScratchStorageLike | null = null;
  private editorState: ScratchEditorStateLike | null = null;
  private projectLoaded = false;
  private readyResolve: ((vm: ScratchVmLike) => void) | null = null;
  private readyReject: ((reason?: unknown) => void) | null = null;
  private readyPromise!: Promise<ScratchVmLike>;
  private importing = false;
  private fatalImport = false;
  private generatingBlocks = false;
  private lifecycleToken = 0;
  private generationToken = 0;
  private projectTransactionActive = false;
  private projectTransactionToken = 0;
  private projectTransactionMutationDepth = 0;
  private projectTransactionRevision: number | null = null;
  private revision = 0;
  private targetListeners = new Set<Listener>();
  private projectListeners = new Set<Listener>();

  constructor() {
    this.resetReadyPromise();
  }

  get ready() {
    return this.readyPromise;
  }

  private resetReadyPromise() {
    this.readyPromise = new Promise<ScratchVmLike>((resolve, reject) => {
      this.readyResolve = resolve;
      this.readyReject = reject;
    });
  }

  setStorage(storage: ScratchStorageLike) {
    this.storage = storage;
  }

  setEditorState(editorState: ScratchEditorStateLike) {
    this.editorState = editorState;
  }

  setVm(vm: ScratchVmLike) {
    if (this.vm) {
      this.rejectReady(new Error('Scratch VM 已重新绑定'));
      this.resetReadyPromise();
    }
    this.detachVmListeners();
    this.lifecycleToken += 1;
    this.generationToken += 1;
    this.projectTransactionToken += 1;
    this.projectTransactionActive = false;
    this.projectTransactionMutationDepth = 0;
    this.projectTransactionRevision = null;
    this.vm = vm;
    this.projectLoaded = false;
    this.importing = false;
    this.fatalImport = false;
    this.generatingBlocks = false;
    this.revision += 1;
    const onProjectLoaded = () => {
      if (this.vm === vm) this.markProjectLoaded();
    };
    const onProjectChanged = () => {
      // A project transaction owns all writes until release. Scratch may emit
      // PROJECT_CHANGED asynchronously after addSprite/setEditingTarget
      // resolves; treating that event as an external edit invalidates the
      // transaction immediately after a valid resource import.
      if (this.vm !== vm || this.projectTransactionMutationDepth > 0 || this.projectTransactionActive) return;
      this.revision += 1;
      this.projectListeners.forEach(listener => listener());
    };
    const onTargetsUpdate = () => {
      if (this.vm === vm) this.targetListeners.forEach(listener => listener());
    };

    this.addVmListener(vm, 'PROJECT_LOADED', onProjectLoaded);
    this.addVmListener(vm, 'PROJECT_CHANGED', onProjectChanged);
    this.addVmListener(vm, 'targetsUpdate', onTargetsUpdate);
    this.addVmListener(vm.runtime, 'PROJECT_LOADED', onProjectLoaded);

    const waitForTargets = () => {
      if (this.projectLoaded || this.vm !== vm) return;
      if (vm.runtime.targets.some(target => target.isStage)) {
        this.markProjectLoaded();
        return;
      }
      window.setTimeout(waitForTargets, 50);
    };
    waitForTargets();

  }

  private addVmListener(source: {on?: (event: string, listener: (...args: unknown[]) => void) => void; addListener?: (event: string, listener: (...args: unknown[]) => void) => void; off?: (event: string, listener: (...args: unknown[]) => void) => void; removeListener?: (event: string, listener: (...args: unknown[]) => void) => void}, event: string, listener: (...args: unknown[]) => void) {
    const add = source.on ?? source.addListener;
    const remove = source.off ?? source.removeListener;
    if (!add) return;
    add.call(source, event, listener);
    if (remove) this.vmListenerCleanups.push(() => remove.call(source, event, listener));
  }

  private detachVmListeners() {
    for (const cleanup of this.vmListenerCleanups.splice(0)) cleanup();
  }

  markProjectLoaded() {
    if (!this.vm) return;
    const firstLoad = !this.projectLoaded;
    this.projectLoaded = true;
    if (this.vm) {
      this.readyResolve?.(this.vm);
      this.readyResolve = null;
      this.readyReject = null;
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

  getLibraryAssetUrl(assetId: string | number, dataFormat: string) {
    const id = String(assetId).toLowerCase();
    const format = String(dataFormat).toLowerCase();
    if (!/^[a-f0-9]{32}$/.test(id) || !/^(?:svg|png|wav|mp3)$/.test(format)) {
      throw new Error('Scratch 素材标识或格式无效');
    }
    return `/assets/scratch/library-assets/${id}.${format}`;
  }

  get builtinExtensionLoaderAvailable() {
    const manager = this.vm?.runtime.extensionManager;
    return Boolean(manager?.isExtensionLoaded && manager.loadExtensionIdSync);
  }

  ensureBuiltinExtensions(extensionIds: readonly ('pen' | 'music' | 'text2speech')[]): void {
    const manager = this.vm?.runtime.extensionManager;
    if (!manager?.isExtensionLoaded || !this.builtinExtensionLoaderAvailable) {
      throw new Error('当前 Scratch VM 不支持受控内置扩展加载');
    }
    for (const extensionId of extensionIds) {
      if (manager.isExtensionLoaded(extensionId)) continue;
      // Commit must not enter the generic URL/worker extension path. These IDs
      // are the fixed, bundled Scratch extensions accepted by the contract.
      manager.loadExtensionIdSync?.(extensionId);
      if (!manager.isExtensionLoaded(extensionId)) throw new Error('Scratch 未能加载内置扩展：' + extensionId);
    }
    this.refreshWorkspace();
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
    if (this.generatingBlocks || this.importing) throw new Error('已有 AI 积木生成任务正在运行');
    this.generatingBlocks = true;
    const generationToken = ++this.generationToken;
    let released = false;
    return () => {
      if (released) return;
      released = true;
      if (this.generationToken === generationToken) this.generatingBlocks = false;
    };
  }

  beginProjectTransaction(): ProjectTransactionHandle {
    if (this.generatingBlocks || this.projectTransactionActive || this.importing) throw new Error('已有 AI 项目事务正在运行');
    this.generatingBlocks = true;
    this.projectTransactionActive = true;
    const token = ++this.projectTransactionToken;
    const transactionRevision = this.revision;
    this.projectTransactionRevision = transactionRevision;
    let released = false;
    return {
      assertActive: () => {
        if (!this.projectTransactionActive || this.projectTransactionToken !== token || this.revision !== transactionRevision) throw new Error('AI 项目事务已失效');
      },
      release: () => {
        if (released) return;
        released = true;
        if (this.projectTransactionToken === token) {
          this.projectTransactionActive = false;
          this.projectTransactionMutationDepth = 0;
          this.projectTransactionRevision = null;
          this.generatingBlocks = false;
        }
      }
    };
  }

  runProjectTransactionMutation<T>(operation: () => T): T {
    return this.runProjectTransactionMutationInternal(operation, true);
  }

  runProjectTransactionCompensation<T>(operation: () => T): T {
    return this.runProjectTransactionMutationInternal(operation, false);
  }

  private runProjectTransactionMutationInternal<T>(operation: () => T, checkRevision: boolean): T {
    if (!this.projectTransactionActive || (checkRevision && this.projectTransactionRevision !== this.revision)) throw new Error('AI 项目事务已失效');
    this.projectTransactionMutationDepth += 1;
    try {
      return operation();
    } finally {
      this.projectTransactionMutationDepth -= 1;
    }
  }

  async runProjectTransactionMutationAsync<T>(operation: () => Promise<T> | T): Promise<T> {
    return this.runProjectTransactionMutationAsyncInternal(operation, true);
  }

  async runProjectTransactionCompensationAsync<T>(operation: () => Promise<T> | T): Promise<T> {
    return this.runProjectTransactionMutationAsyncInternal(operation, false);
  }

  private async runProjectTransactionMutationAsyncInternal<T>(operation: () => Promise<T> | T, checkRevision: boolean): Promise<T> {
    if (!this.projectTransactionActive || (checkRevision && this.projectTransactionRevision !== this.revision)) throw new Error('AI 项目事务已失效');
    this.projectTransactionMutationDepth += 1;
    try {
      return await operation();
    } finally {
      this.projectTransactionMutationDepth -= 1;
    }
  }

  refreshWorkspace() {
    this.requireVm().emitWorkspaceUpdate?.();
  }

  async commitSpriteImport(
    spriteJson: Record<string, unknown>,
    expectations: SpriteImportExpectation[],
    soundExpectations: SpriteSoundExpectation[] = []
  ): Promise<ImportedSpriteResult> {
    const vm = this.requireVm();
    const lifecycleToken = this.lifecycleToken;
    if (!this.projectLoaded) throw new Error('Scratch 项目尚未就绪');
    if (this.fatalImport) throw new Error('上次导入补偿失败，请重新加载最近保存的项目');
    if (this.importing) throw new Error('角色正在导入，请勿重复提交');
    if (this.generatingBlocks && !this.projectTransactionActive) throw new Error('AI 正在生成积木，请稍后再导入角色');
    this.importing = true;
    const before = new Set(vm.runtime.targets.map(target => target.id));

    try {
      await this.runMaybeProjectTransactionMutationAsync(() => vm.addSprite(JSON.stringify(spriteJson)));
      this.assertVmStillBound(vm, lifecycleToken);
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
      const installedSounds = target.getSounds?.() ?? [];
      if (installedSounds.length !== soundExpectations.length) throw new Error('Scratch 声音数量校验失败');
      soundExpectations.forEach((expected, index) => {
        const actual = installedSounds[index];
        if (!actual || actual.name !== expected.name || String(actual.assetId ?? '') !== expected.assetId || String(actual.dataFormat ?? actual.format ?? '') !== expected.dataFormat) {
          throw new Error('Scratch 声音元数据校验失败：' + expected.name);
        }
      });

      // Scratch GUI can emit PROJECT_CHANGED when the selected target changes.
      // Keep that editor-side update inside the transaction mutation window so
      // it does not advance the revision and invalidate the active commit.
      this.runMaybeProjectTransactionMutation(() => vm.setEditingTarget(target.id));
      return {
        targetId: target.id,
        costumeAssetIds: installed.map(costume => String(costume.assetId ?? ''))
      };
    } catch (error) {
      const additions = vm.runtime.targets.filter(target => !before.has(target.id) && !target.isStage);
      let rollbackFailed = false;
      for (const target of additions) {
        try {
          this.runMaybeProjectTransactionMutation(() => vm.deleteSprite(target.id));
        } catch {
          rollbackFailed = true;
        }
      }
      const stale = this.vm !== vm || this.lifecycleToken !== lifecycleToken;
      if (!stale && (rollbackFailed || vm.runtime.targets.some(target => !before.has(target.id) && !target.isStage))) {
        this.fatalImport = true;
      }
      if (stale) throw new Error('Scratch VM 已重新绑定，导入结果已丢弃');
      if (this.fatalImport) {
        throw new Error('导入失败且无法清除半成品角色，请重新加载最近保存的项目');
      }
      throw error;
    } finally {
      this.importing = false;
    }
  }


  async addCostumeToTarget(targetId: string, costume: CostumeExport, selectAfter = true) {
    const vm = this.requireVm();
    if (this.generatingBlocks && !this.projectTransactionActive) throw new Error('AI 正在生成积木，请稍后再导入造型');
    const target = vm.runtime.getTargetById?.(targetId) ?? vm.runtime.targets.find(item => item.id === targetId);
    if (!target || target.isStage) throw new Error('只能向已有角色添加造型');
    const oldIndex = target.currentCostume ?? 0;
    const lifecycleToken = this.lifecycleToken;
    const asset = await this.createVisualAsset(costume);
    this.assertVmStillBound(vm, lifecycleToken);
    await this.runMaybeProjectTransactionMutationAsync(() => vm.addCostume(asset.md5ext, asset.costume, targetId));
    this.assertVmStillBound(vm, lifecycleToken);
    const installed = target.getCostumes?.() ?? [];
    const installedIndex = installed.findIndex(item => String(item.assetId ?? '') === asset.assetId);
    if (installedIndex < 0) throw new Error('Scratch 未能安装造型');
    if (!selectAfter) {
      this.assertVmStillBound(vm, lifecycleToken);
      this.runMaybeProjectTransactionMutation(() => {
        target.setCostume?.(oldIndex);
        if (target.currentCostume !== oldIndex) target.currentCostume = oldIndex;
        vm.runtime.emitProjectChanged?.();
      });
      this.assertVmStillBound(vm, lifecycleToken);
    }
    return {assetId: asset.assetId, name: String(installed[installedIndex]?.name ?? costume.name), costumeIndex: installedIndex};
  }

  async addBackdropToStage(costume: CostumeExport, selectAfter = true) {
    const vm = this.requireVm();
    if (this.generatingBlocks && !this.projectTransactionActive) throw new Error('AI 正在生成积木，请稍后再导入背景');
    const stage = vm.runtime.getTargetForStage?.() ?? vm.runtime.targets.find(item => item.isStage);
    if (!stage) throw new Error('Scratch 舞台尚未就绪');
    const oldIndex = stage.currentCostume ?? 0;
    const lifecycleToken = this.lifecycleToken;
    const asset = await this.createVisualAsset(costume);
    this.assertVmStillBound(vm, lifecycleToken);
    await this.runMaybeProjectTransactionMutationAsync(() => vm.addBackdrop(asset.md5ext, asset.costume));
    this.assertVmStillBound(vm, lifecycleToken);
    const installed = stage.getCostumes?.() ?? [];
    const installedIndex = installed.findIndex(item => String(item.assetId ?? '') === asset.assetId);
    if (installedIndex < 0) throw new Error('Scratch 未能安装背景');
    if (!selectAfter) {
      this.assertVmStillBound(vm, lifecycleToken);
      this.runMaybeProjectTransactionMutation(() => {
        stage.setCostume?.(oldIndex);
        if (stage.currentCostume !== oldIndex) stage.currentCostume = oldIndex;
        vm.runtime.emitProjectChanged?.();
      });
      this.assertVmStillBound(vm, lifecycleToken);
    }
    return {assetId: asset.assetId, name: String(installed[installedIndex]?.name ?? costume.name), costumeIndex: installedIndex};
  }

  async addSoundToTarget(targetId: string, sound: SoundExport) {
    const vm = this.requireVm();
    const storage = this.currentStorage;
    if (!storage?.createAsset || !vm.addSound) throw new Error('当前 Scratch 编辑器不支持添加声音');
    const target = vm.runtime.getTargetById?.(targetId) ?? vm.runtime.targets.find(item => item.id === targetId);
    if (!target) throw new Error('找不到声音目标');
    const bytes = new Uint8Array(await sound.blob.arrayBuffer());
    if (!bytes.length) throw new Error('声音文件为空');
    const dataFormat = sound.dataFormat === 'wav' ? storage.DataFormat.WAV : storage.DataFormat.MP3;
    const asset = storage.createAsset(storage.AssetType.Sound, dataFormat, bytes, null, true);
    const assetId = String(asset?.assetId ?? '');
    if (!assetId) throw new Error('无法创建 Scratch 声音资源');
    const before = target.getSounds?.().length ?? 0;
    await this.runMaybeProjectTransactionMutationAsync(() => vm.addSound!({
      name: sound.name || '声音', asset, assetId, md5ext: assetId + '.' + sound.dataFormat,
      dataFormat: sound.dataFormat, format: sound.dataFormat, rate: sound.rate ?? 44100, sampleCount: sound.sampleCount ?? 0
    }, targetId));
    const installed = target.getSounds?.() ?? [];
    const result = installed[installed.length - 1];
    if (installed.length !== before + 1 || !result) throw new Error('Scratch 未能安装声音');
    return {assetId, name: String(result.name ?? sound.name), soundIndex: installed.length - 1};
  }

  async removeSoundFromTarget(targetId: string, soundIndex: number, assetId: string, compensation = false): Promise<void> {
    const vm = this.requireVm();
    const target = vm.runtime.getTargetById?.(targetId) ?? vm.runtime.targets.find(item => item.id === targetId);
    const sound = target?.getSounds?.()[soundIndex];
    if (!target || !sound || String(sound.assetId ?? '') !== assetId || !vm.deleteSound) throw new Error('待补偿的声音已经变化');
    const previousEditingTargetId = vm.editingTarget?.id;
    const mutate = compensation
      ? (operation: () => Promise<unknown> | unknown) => this.projectTransactionActive ? this.runProjectTransactionCompensationAsync(operation) : Promise.resolve(operation())
      : (operation: () => Promise<unknown> | unknown) => this.runMaybeProjectTransactionMutationAsync(operation);
    if (previousEditingTargetId !== targetId) vm.setEditingTarget(targetId);
    await mutate(() => vm.deleteSound!(soundIndex));
    if (previousEditingTargetId && previousEditingTargetId !== targetId && this.vm === vm) vm.setEditingTarget(previousEditingTargetId);
  }

  selectBackdropByName(name: string) {
    const vm = this.requireVm();
    const lifecycleToken = this.lifecycleToken;
    if (this.generatingBlocks && !this.projectTransactionActive) throw new Error('AI 正在生成积木，请稍后再选择背景');
    const stage = vm.runtime.getTargetForStage?.() ?? vm.runtime.targets.find(item => item.isStage);
    const wanted = name.trim().toLocaleLowerCase();
    const index = stage?.getCostumes?.().findIndex(costume => String(costume.name ?? '').trim().toLocaleLowerCase() === wanted) ?? -1;
    if (!stage || index < 0) throw new Error('找不到已有背景：' + name);
    this.assertVmStillBound(vm, lifecycleToken);
    this.runMaybeProjectTransactionMutation(() => {
      stage.setCostume?.(index);
      if (stage.currentCostume !== index) stage.currentCostume = index;
      vm.runtime.emitProjectChanged?.();
    });
    this.assertVmStillBound(vm, lifecycleToken);
    return {assetId: String(stage.getCostumes?.()[index]?.assetId ?? ''), name: String(stage.getCostumes?.()[index]?.name ?? name)};
  }

  restoreCostumeSelection(targetId: string, costumeIndex: number, compensation = false): void {
    const vm = this.requireVm();
    const target = vm.runtime.getTargetById?.(targetId) ?? vm.runtime.targets.find(item => item.id === targetId);
    if (!target) throw new Error('找不到待恢复选择的目标');
    const lifecycleToken = this.lifecycleToken;
    const previousEditingTargetId = vm.editingTarget?.id;
    const mutate = compensation
      ? (operation: () => void) => this.projectTransactionActive ? this.runProjectTransactionCompensation(operation) : operation()
      : (operation: () => void) => this.runMaybeProjectTransactionMutation(operation);
    mutate(() => {
      if (vm.editingTarget?.id !== targetId) vm.setEditingTarget(targetId);
      target.setCostume?.(costumeIndex);
      if (target.currentCostume !== costumeIndex) target.currentCostume = costumeIndex;
      vm.runtime.emitProjectChanged?.();
    });
    this.assertVmStillBound(vm, lifecycleToken);
    if (previousEditingTargetId && previousEditingTargetId !== targetId && this.vm === vm) vm.setEditingTarget(previousEditingTargetId);
  }

  async removeCostumeFromTarget(targetId: string, costumeIndex: number, assetId: string, compensation = false): Promise<void> {
    const vm = this.requireVm();
    const target = vm.runtime.getTargetById?.(targetId) ?? vm.runtime.targets.find(item => item.id === targetId);
    if (!target) throw new Error('找不到待补偿的目标');
    const installed = target.getCostumes?.() ?? [];
    if (String(installed[costumeIndex]?.assetId ?? '') !== assetId) throw new Error('待补偿的素材位置已经变化');
    const previousEditingTargetId = vm.editingTarget?.id;
    const lifecycleToken = this.lifecycleToken;
    if (vm.editingTarget?.id !== targetId) vm.setEditingTarget(targetId);
    try {
      const mutate = compensation
        ? (operation: () => Promise<unknown> | unknown) => this.projectTransactionActive ? this.runProjectTransactionCompensationAsync(operation) : operation()
        : (operation: () => Promise<unknown> | unknown) => this.runMaybeProjectTransactionMutationAsync(operation);
      const deleted = await mutate(() => vm.deleteCostume?.(costumeIndex));
      this.assertVmStillBound(vm, lifecycleToken);
      if (!deleted) throw new Error('Scratch 未能删除已安装素材');
      const remaining = target.getCostumes?.() ?? [];
      if (remaining.some(costume => String(costume.assetId ?? '') === assetId)) throw new Error('Scratch 补偿后素材仍然存在');
    } finally {
      if (previousEditingTargetId && previousEditingTargetId !== targetId && this.vm === vm) vm.setEditingTarget(previousEditingTargetId);
    }
  }

  private async createVisualAsset(costume: CostumeExport) {
    const storage = this.currentStorage;
    if (!storage?.createAsset) throw new Error('Scratch 素材存储尚未就绪');
    const bytes = new Uint8Array(await costume.blob.arrayBuffer());
    const vector = costume.dataFormat === 'svg';
    if (vector && costume.bitmapResolution !== 1) throw new Error('SVG 造型必须使用 1 倍分辨率');
    if (!vector && costume.bitmapResolution !== 2) throw new Error('PNG 造型必须使用 2 倍分辨率');
    const type = vector ? storage.AssetType.ImageVector : storage.AssetType.ImageBitmap;
    const format = vector ? storage.DataFormat.SVG : storage.DataFormat.PNG;
    const asset = storage.createAsset(type, format, bytes, null, true);
    const assetId = String(asset?.assetId ?? '');
    if (!assetId) throw new Error('无法创建 Scratch 素材');
    return {assetId, md5ext: assetId + '.' + costume.dataFormat, costume: {asset, assetId, md5ext: assetId + '.' + costume.dataFormat, dataFormat: costume.dataFormat, name: costume.name, rotationCenterX: costume.rotationCenterX, rotationCenterY: costume.rotationCenterY, bitmapResolution: costume.bitmapResolution}};
  }

  async saveProject(): Promise<Uint8Array> {
    const vm = this.requireVm();
    const lifecycleToken = this.lifecycleToken;
    const output = await vm.saveProjectSb3();
    this.assertVmStillBound(vm, lifecycleToken);
    if (output instanceof Uint8Array) return new Uint8Array(output);
    const bytes = new Uint8Array(await (output as Blob).arrayBuffer());
    this.assertVmStillBound(vm, lifecycleToken);
    return bytes;
  }

  async loadProject(input: ArrayBuffer | Uint8Array | string) {
    const vm = this.requireVm();
    const lifecycleToken = this.lifecycleToken;
    await vm.loadProject(input);
    this.assertVmStillBound(vm, lifecycleToken);
  }

  run() {
    this.requireVm().greenFlag();
  }

  stop() {
    this.requireVm().stopAll();
  }

  dispose() {
    this.detachVmListeners();
    this.rejectReady(new Error('Scratch GUI bridge 已释放'));
    this.resetReadyPromise();
    this.lifecycleToken += 1;
    this.generationToken += 1;
    this.projectTransactionToken += 1;
    this.projectTransactionActive = false;
    this.projectTransactionMutationDepth = 0;
    this.projectTransactionRevision = null;
    this.vm = null;
    this.storage = null;
    this.editorState = null;
    this.projectLoaded = false;
    this.importing = false;
    this.fatalImport = false;
    this.generatingBlocks = false;
    this.targetListeners.clear();
    this.projectListeners.clear();
    this.revision += 1;
  }

  private rejectReady(reason: Error) {
    this.readyPromise.catch(() => undefined);
    this.readyReject?.(reason);
    this.readyResolve = null;
    this.readyReject = null;
  }

  private runMaybeProjectTransactionMutation<T>(operation: () => T): T {
    return this.projectTransactionActive ? this.runProjectTransactionMutation(operation) : operation();
  }

  private async runMaybeProjectTransactionMutationAsync<T>(operation: () => Promise<T> | T): Promise<T> {
    return this.projectTransactionActive ? this.runProjectTransactionMutationAsync(operation) : operation();
  }

  private assertVmStillBound(vm: ScratchVmLike, lifecycleToken: number) {
    if (this.vm !== vm || this.lifecycleToken !== lifecycleToken) throw new Error('Scratch VM 已重新绑定');
  }

  private requireVm() {
    if (!this.vm) throw new Error('Scratch 编辑器尚未就绪');
    return this.vm;
  }
}
