/**
 * The only application-facing contract for Scratch VM mutation. Keep this
 * deliberately small so an upstream package upgrade has one compatibility
 * boundary rather than VM calls scattered through feature code.
 */
export interface ScratchEditorPort {
  readonly isReady: boolean;
  readonly projectRevision: number;
  readonly currentVm: ScratchVmLike | null;
  readonly currentStorage: ScratchStorageLike | null;
  readonly builtinExtensionLoaderAvailable: boolean;
  getLibraryAssetUrl(assetId: string | number, dataFormat: string): string;
  ensureBuiltinExtensions(extensionIds: readonly ('pen' | 'music' | 'text2speech')[]): void;
  refreshWorkspace(): void;
  saveProject(): Promise<Uint8Array>;
  loadProject(input: ArrayBuffer | Uint8Array | string): Promise<void>;
  beginProjectTransaction(): {assertActive(): void; release(): void};
}

export interface ScratchEditorCapabilities {
  projectRoundTrip: boolean;
  spriteImport: boolean;
  costumeAssets: boolean;
  backdropAssets: boolean;
  soundAssets: boolean;
  programGraphWrite: boolean;
  builtinExtensionLoader: boolean;
  workspaceRefresh: boolean;
}

export function probeScratchEditorPort(port: ScratchEditorPort): ScratchEditorCapabilities {
  const vm = port.currentVm;
  const storage = port.currentStorage;
  const audioEngine = (vm?.runtime as {audioEngine?: {decodeSoundPlayer?: unknown}} | undefined)?.audioEngine;
  const graphTargets = vm?.runtime.targets ?? [];
  const supportsProgramGraphWrite = (target: ScratchTargetLike) => {
    const blocks = target.blocks as unknown as Record<string, unknown> | undefined;
    return typeof blocks?.createBlock === 'function'
      && typeof blocks.moveBlock === 'function'
      && typeof blocks.deleteBlock === 'function'
      && typeof target.createComment === 'function';
  };
  return {
    projectRoundTrip: Boolean(vm?.saveProjectSb3 && vm?.loadProject),
    spriteImport: Boolean(vm?.addSprite),
    costumeAssets: Boolean(vm?.addCostume && storage?.createAsset),
    backdropAssets: Boolean(vm?.addBackdrop && storage?.createAsset),
    soundAssets: Boolean(
      vm?.addSound && storage?.createAsset && storage.AssetType.Sound
      && (storage.DataFormat.WAV || storage.DataFormat.MP3)
      && typeof audioEngine?.decodeSoundPlayer === 'function'
    ),
    programGraphWrite: graphTargets.length > 0 && graphTargets.every(supportsProgramGraphWrite),
    builtinExtensionLoader: port.builtinExtensionLoaderAvailable,
    workspaceRefresh: Boolean(vm?.emitWorkspaceUpdate || vm?.refreshWorkspace)
  };
}
