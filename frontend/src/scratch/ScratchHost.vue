<script setup lang="ts">
import {onBeforeUnmount, onMounted, ref} from 'vue';
import {ScratchGuiBridge} from './scratch-gui-bridge';

const props = withDefaults(defineProps<{
  locked?: boolean;
}>(), {
  locked: false
});

const emit = defineEmits<{
  ready: [bridge: ScratchGuiBridge];
  error: [error: Error];
}>();

const container = ref<HTMLElement | null>(null);
const bridge = new ScratchGuiBridge();
let root: {render(props: Record<string, unknown>): void; unmount(): void} | null = null;
let resizeObserver: ResizeObserver | null = null;
let resizeFrame = 0;

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
  const gui = window.GUI;
  if (!gui || !container.value) {
    throw new Error('本地 Scratch GUI 资源未加载');
  }

  const storage = new gui.ScratchStorage();
  cacheDefaultAssets(gui, storage);
  storage.addWebStore?.(
    [storage.AssetType.ImageVector, storage.AssetType.ImageBitmap, storage.AssetType.Sound],
    asset => `/assets/scratch/library-assets/${asset.assetId}.${asset.dataFormat}`
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
      emit('ready', bridge);
    }
  });
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
  <section class="scratch-host" aria-label="Scratch 编辑器" :aria-busy="props.locked">
    <div ref="container" class="scratch-host__container" :inert="props.locked || undefined" />
  </section>
</template>
