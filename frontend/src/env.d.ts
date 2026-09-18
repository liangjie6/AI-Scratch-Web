/// <reference types="vite/client" />

declare global {
  interface ScratchAssetLike {
    assetId?: string | number;
    assetType: unknown;
    dataFormat: unknown;
    data?: string | Uint8Array;
  }

  interface ScratchStorageLike {
    AssetType: Record<string, unknown>;
    DataFormat: Record<string, unknown>;
    cache(type: unknown, format: unknown, data: Uint8Array, id: string): string;
    addWebStore?(
      types: unknown[],
      getUrl: (asset: {assetId: string | number; dataFormat: string}) => string
    ): void;
    createAsset?(
      type: unknown,
      format: unknown,
      data: Uint8Array,
      id: string | null,
      generateId: boolean
    ): ScratchAssetLike;
    load?(type: unknown, id: string | number, format: unknown): Promise<ScratchAssetLike | null>;
  }

  interface ScratchVariableLike {
    id: string;
    name: string;
    type: '' | 'list' | 'broadcast_msg';
    value: string | number | boolean | Array<string | number | boolean>;
    isCloud?: boolean;
  }

  interface ScratchBlockLike {
    id: string;
    opcode: string;
    inputs: Record<string, {name?: string; block: string | null; shadow: string | null}>;
    fields: Record<string, {name?: string; value: string | number | boolean; id?: string | null; variableType?: '' | 'list' | 'broadcast_msg'}>;
    next: string | null;
    parent: string | null;
    shadow: boolean;
    topLevel: boolean;
    x?: number;
    y?: number;
    mutation?: Record<string, unknown> | null;
    comment?: string;
  }

  interface ScratchBlocksLike {
    _blocks: Record<string, ScratchBlockLike>;
    _scripts: string[];
    getScripts(): string[];
    getBlock(id: string): ScratchBlockLike | undefined;
    createBlock(block: ScratchBlockLike): void;
    moveBlock(event: {
      id: string;
      oldParent?: string;
      oldInput?: string;
      newParent?: string;
      newInput?: string;
      newCoordinate?: {x: number; y: number};
    }): void;
    deleteBlock(id: string): void;
    deleteAllBlocks(): void;
    toXML(comments?: Record<string, ScratchCommentLike>): string;
  }

  interface ScratchCommentLike {
    id: string;
    blockId: string | null;
    text: string;
    x: number;
    y: number;
    width: number;
    height: number;
    minimized: boolean;
  }

  interface ScratchTargetLike {
    id: string;
    isStage?: boolean;
    isOriginal?: boolean;
    getName?: () => string;
    x?: number;
    y?: number;
    size?: number;
    currentCostume?: number;
    setCostume?: (index: number) => void;
    blocks?: ScratchBlocksLike;
    comments?: Record<string, ScratchCommentLike>;
    variables?: Record<string, ScratchVariableLike>;
    getCostumes?: () => Array<Record<string, unknown> & {
      name?: string;
      assetId?: string | number;
      asset?: ScratchAssetLike;
      dataFormat?: unknown;
      bitmapResolution?: number;
      rotationCenterX?: number;
      rotationCenterY?: number;
    }>;
    getSounds?: () => Array<{
      name?: string;
      assetId?: string | number;
      dataFormat?: string;
      format?: string;
      rate?: number;
      sampleCount?: number;
    }>;
    createVariable?(id: string, name: string, type: '' | 'list' | 'broadcast_msg', isCloud?: boolean): void;
    deleteVariable?(id: string): void;
    lookupVariableByNameAndType?(
      name: string,
      type: '' | 'list' | 'broadcast_msg',
      skipStage?: boolean
    ): ScratchVariableLike | null | undefined;
    createComment?(
      id: string,
      blockId: string | null,
      text: string,
      x: number,
      y: number,
      width: number,
      height: number,
      minimized: boolean
    ): void;
    toJSON?(): Record<string, unknown>;
    sprite?: {
      name?: string;
      costumes?: Array<Record<string, unknown> & {
        name?: string;
        assetId?: string | number;
        asset?: ScratchAssetLike;
        dataFormat?: unknown;
        bitmapResolution?: number;
        rotationCenterX?: number;
        rotationCenterY?: number;
      }>;
    };
  }

  interface ScratchVmLike {
    initialized?: boolean;
    editingTarget?: ScratchTargetLike;
    runtime: {
      targets: ScratchTargetLike[];
      on?: (event: string, listener: (...args: unknown[]) => void) => void;
      addListener?: (event: string, listener: (...args: unknown[]) => void) => void;
      off?: (event: string, listener: (...args: unknown[]) => void) => void;
      removeListener?: (event: string, listener: (...args: unknown[]) => void) => void;
      getTargetById?: (targetId: string) => ScratchTargetLike | null;
      getTargetForStage?: () => ScratchTargetLike;
      emitProjectChanged?: () => void;
      extensionManager?: {
        isExtensionLoaded?(extensionId: string): boolean;
        loadExtensionURL?(extensionId: string): Promise<unknown>;
        loadExtensionIdSync?(extensionId: string): void;
      };
    };
    on?: (event: string, listener: (...args: unknown[]) => void) => void;
    addListener?: (event: string, listener: (...args: unknown[]) => void) => void;
    removeListener?: (event: string, listener: (...args: unknown[]) => void) => void;
    addCostume(md5ext: string, costume: Record<string, unknown>, targetId?: string): Promise<void>;
    addBackdrop(md5ext: string, backdrop: Record<string, unknown>): Promise<void>;
    addSound?(sound: Record<string, unknown>, targetId?: string): Promise<void>;
    deleteSound?(soundIndex: number): unknown;
    addSprite(input: string | Record<string, unknown>): Promise<void>;
    deleteCostume?(costumeIndex: number): unknown;
    deleteSprite(targetId: string): unknown;
    setEditingTarget(targetId: string): void;
    saveProjectSb3(): Promise<Uint8Array | Blob>;
    loadProject(input: ArrayBuffer | Uint8Array | string): Promise<void>;
    emitWorkspaceUpdate?(): void;
    refreshWorkspace?(): void;
    greenFlag(): void;
    stopAll(): void;
  }

  interface ScratchEditorStateLike {
    store: {
      dispatch(action: Record<string, unknown>): unknown;
      getState?(): unknown;
      subscribe?(listener: () => void): () => void;
    };
  }

  interface ScratchGuiGlobal {
    ScratchStorage: new () => ScratchStorageLike;
    EditorState: new (
      params: Record<string, unknown>,
      configFactory: () => Record<string, unknown>
    ) => ScratchEditorStateLike;
    createStandaloneRoot(state: ScratchEditorStateLike, container: HTMLElement): {
      render(props: Record<string, unknown>): void;
      unmount(): void;
    };
    setAppElement?: (element: HTMLElement) => void;
    buildDefaultProject?: (
      translator?: (message: {id: string; defaultMessage: string}, options?: {index: number}) => string
    ) => Array<{
      id: string | number;
      assetType: string;
      dataFormat: string;
      data: string | Uint8Array;
    }>;
  }

  interface Window {
    GUI?: ScratchGuiGlobal;
  }
}

export {};
