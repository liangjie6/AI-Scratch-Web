<script setup lang="ts">
import {onBeforeUnmount, onMounted, ref} from 'vue';
import {ScratchGuiBridge} from './scratch-gui-bridge';
import {probeScratchEditorPort} from './scratch-editor-port';
import {recoverInterruptedAiCommits, type CheckpointRecoveryResult} from '../ai-blocks/checkpoint-recovery';
import {LOCAL_PROJECT_STORAGE_KEY} from '../ai-blocks/run-checkpoint-store';
import {sha256Hex} from '../shared/sha256';

const props = withDefaults(defineProps<{
  locked?: boolean;
}>(), {
  locked: false
});

const emit = defineEmits<{
  ready: [bridge: ScratchGuiBridge];
  error: [error: Error];
  recovery: [result: CheckpointRecoveryResult];
}>();

const container = ref<HTMLElement | null>(null);
const bridge = new ScratchGuiBridge();
let root: {render(props: Record<string, unknown>): void; unmount(): void} | null = null;
let resizeObserver: ResizeObserver | null = null;
let resizeFrame = 0;
const recoveryPending = ref(true);
let startupComplete = false;

interface ScratchEditorArtifactMetadata {
  schema_version: 1;
  packages: {
    scratch_gui: string;
    scratch_vm: string;
    scratch_storage: string;
  };
  standalone: {file: string; sha256: string};
  library_manifest_sha256?: Record<string, string>;
  library_asset_count: number;
}

function isArtifactMetadata(value: unknown): value is ScratchEditorArtifactMetadata {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const metadata = value as Record<string, unknown>;
  if (metadata.schema_version !== 1 || !metadata.packages || !metadata.standalone) return false;
  const packages = metadata.packages as Record<string, unknown>;
  const standalone = metadata.standalone as Record<string, unknown>;
  const versions = [packages.scratch_gui, packages.scratch_vm, packages.scratch_storage];
  return versions.every(version => typeof version === 'string' && /^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]+)?$/.test(version))
    && new Set(versions).size === 1
    && standalone.file === 'scratch-gui-standalone.js'
    && typeof standalone.sha256 === 'string'
    && /^[a-f0-9]{64}$/.test(standalone.sha256)
    && (!metadata.library_manifest_sha256 || Object.entries(metadata.library_manifest_sha256).every(([name, hash]) => /^(?:sprites|costumes|backdrops|sounds)\.json$/.test(name) && typeof hash === 'string' && /^[a-f0-9]{64}$/.test(hash)))
    && Number.isInteger(metadata.library_asset_count)
    && Number(metadata.library_asset_count) > 0;
}

async function assertScratchEditorArtifactMetadata() {
  const response = await fetch('/assets/scratch/editor-capabilities.json', {cache: 'no-store'});
  if (!response.ok) throw new Error('Scratch 编辑器版本描述不可用');
  const metadata: unknown = await response.json();
  if (!isArtifactMetadata(metadata)) throw new Error('Scratch 编辑器包族版本不一致或能力描述无效');
  if (metadata.library_manifest_sha256) {
    for (const [name, expected] of Object.entries(metadata.library_manifest_sha256)) {
      const manifest = await fetch('/assets/scratch/libraries/' + name, {cache: 'no-store'});
      if (!manifest.ok) throw new Error('Scratch 素材清单不可用');
      const actual = await sha256Hex(new Uint8Array(await manifest.arrayBuffer()));
      if (actual !== expected) throw new Error('Scratch 素材清单校验失败');
    }
  }
  const bundle = await fetch('/assets/scratch/' + metadata.standalone.file, {cache: 'no-store'});
  if (!bundle.ok) throw new Error('Scratch 编辑器 standalone 资源不可用');
  const actual = await sha256Hex(new Uint8Array(await bundle.arrayBuffer()));
  if (actual !== metadata.standalone.sha256) throw new Error('Scratch 编辑器 standalone 资源校验失败');
}

function translateDefaultProjectName(
  message: {id: string; defaultMessage: string},
  options?: {index: number}
) {
  const index = options?.index ?? 1;
  const names: Record<string, string> = {
    'gui.sharedMessages.backdrop': `背景${index}`,
    'gui.sharedMessages.costume': `造型${index}`,
    'gui.sharedMessages.sprite': `角色${index}`,
    'gui.sharedMessages.pop': '啵',
    'gui.defaultProject.meow': '喵',
    'gui.defaultProject.variable': '我的变量'
  };
  return names[message.id] ?? message.defaultMessage.replace('{index}', String(index));
}

function cacheDefaultAssets(gui: ScratchGuiGlobal, storage: ScratchStorageLike) {
  const assets = gui.buildDefaultProject?.(translateDefaultProjectName) ?? [];
  for (const asset of assets) {
    const type = storage.AssetType[asset.assetType];
    const format = storage.DataFormat[asset.dataFormat];
    if (!type || !format) continue;
    const data = typeof asset.data === 'string'
      ? new TextEncoder().encode(asset.data)
      : asset.data;
    storage.cache(type, format, data, String(asset.id));
  }
}

async function mountGui() {
  await assertScratchEditorArtifactMetadata();
  const gui = window.GUI;
  if (!gui || !container.value) {
    throw new Error('本地 Scratch GUI 资源未加载');
  }

  const storage = new gui.ScratchStorage();
  cacheDefaultAssets(gui, storage);
  storage.addWebStore?.(
    [storage.AssetType.ImageVector, storage.AssetType.ImageBitmap, storage.AssetType.Sound],
    asset => bridge.getLibraryAssetUrl(asset.assetId, asset.dataFormat)
  );
  bridge.setStorage(storage);

  const config = {
    storage: {
      scratchStorage: storage,
      saveProject: async () => ({id: 'local'})
    },
    setAssetHost: () => undefined,
    setProjectHost: () => undefined
  };
  gui.setAppElement?.(container.value);
  const state = new gui.EditorState({locale: 'zh-cn'}, () => config);
  bridge.setEditorState(state);
  root = gui.createStandaloneRoot(state, container.value);
  root.render({
    canEditTitle: true,
    projectId: '0',
    assetHost: '',
    projectHost: '',
    canSave: false,
    backpackVisible: false,
    showComingSoon: false,
    showNewFeatureCallouts: false,
    manuallySaveThumbnails: false,
    onVmInit: (vm: ScratchVmLike) => {
      bridge.setVm(vm);
    },
    onProjectLoaded: () => {
      bridge.markProjectLoaded();
      if (startupComplete) return;
      startupComplete = true;
      void finishStartup();
    }
  });
}

async function finishStartup() {
  try {
      const capabilities = probeScratchEditorPort(bridge);
      if (!capabilities.projectRoundTrip || !capabilities.spriteImport || !capabilities.programGraphWrite || !capabilities.workspaceRefresh) {
        emit('error', new Error('当前 Scratch 编辑器缺少 AI 项目提交所需能力'));
        return;
      }
      const recovery = await recoverInterruptedAiCommits(bridge, LOCAL_PROJECT_STORAGE_KEY);
      emit('recovery', recovery);
      emit('ready', bridge);
  } catch (error) {
    emit('error', error instanceof Error ? error : new Error(String(error)));
  } finally {
    recoveryPending.value = false;
  }
}

onMounted(() => {
  if (container.value) {
    resizeObserver = new ResizeObserver(() => {
      window.cancelAnimationFrame(resizeFrame);
      resizeFrame = window.requestAnimationFrame(() => {
        window.dispatchEvent(new Event('resize'));
      });
    });
    resizeObserver.observe(container.value);
  }

  mountGui().catch((error: unknown) => {
    emit('error', error instanceof Error ? error : new Error(String(error)));
  });
});

onBeforeUnmount(() => {
  resizeObserver?.disconnect();
  window.cancelAnimationFrame(resizeFrame);
  root?.unmount();
});

defineExpose({bridge});
</script>

<template>
  <section class="scratch-host" aria-label="Scratch 编辑器" :aria-busy="props.locked || recoveryPending">
    <div ref="container" class="scratch-host__container" :inert="props.locked || recoveryPending || undefined" />
  </section>
</template>
