<script setup lang="ts">
import {nextTick, onBeforeUnmount, onMounted, ref, shallowRef} from 'vue';
import {Blocks as BlocksIcon, Bot, ImagePlus, Images, PanelRightClose, Palette} from 'lucide-vue-next';
import ScratchHost from '../scratch/ScratchHost.vue';
import {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import {importSelectedImage} from '../scratch/asset-adapter';
import type {ImageTarget} from '../ai/image-generation-types';
import ImageGenerationPanel from '../ai/ImageGenerationPanel.vue';
import AssetLibraryDialog from '../ai/AssetLibraryDialog.vue';
import {
  clearGeneratedAssets,
  countGeneratedAssets,
  deleteGeneratedAsset,
  listGeneratedAssets,
  type GeneratedAsset
} from '../ai/asset-library-store';
import CanvasWorkspace, {type CanvasPreparedAsset} from '../canvas/CanvasWorkspace.vue';
import type {CanvasInput} from '../canvas/model/canvas-types';
import type {ImageGenerationResponse} from '../ai/image-generation-types';
import AiBlocksPanel from '../ai-blocks/AiBlocksPanel.vue';
import type {CheckpointRecoveryResult} from '../ai-blocks/checkpoint-recovery';

const bridge = shallowRef<ScratchGuiBridge | null>(null);
const editorError = ref('');
const canvasInput = shallowRef<CanvasInput | null>(null);
const canvasOpen = ref(false);
const pendingCanvasHandoff = shallowRef<CanvasPreparedAsset | null>(null);
const pendingCanvasOpId = ref<string | null>(null);
const canvasHandoffMode = ref<'direct-import' | 'return-prepared-asset'>('direct-import');
const aiBlocksPanel = ref<{acceptCanvasPrepared: (opId: string, payload: CanvasPreparedAsset) => void} | null>(null);
const assetLibraryOpen = ref(false);
const assetLibraryTrigger = ref<HTMLButtonElement | null>(null);
const assets = shallowRef<GeneratedAsset[]>([]);
const assetCount = ref(0);
const libraryLoading = ref(false);
const libraryError = ref('');
const libraryImportingId = ref<string | null>(null);
let canvasTrigger: HTMLElement | null = null;

type ActivePanel = 'image' | 'blocks' | null;

type HeaderTool = 'assets' | 'image' | 'blocks' | 'canvas';

const activePanel = ref<ActivePanel>(null);
const hoveredHeaderTool = ref<HeaderTool | null>(null);
const scratchLocked = ref(false);
const aiBlocksBusy = ref(false);

function debounce<T>(callback: (value: T) => void, delay: number) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const debounced = (value: T) => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => callback(value), delay);
  };
  debounced.cancel = () => {
    if (timer) clearTimeout(timer);
  };
  return debounced;
}

const updateHeaderHover = debounce<HeaderTool | null>(value => {
  hoveredHeaderTool.value = value;
}, 70);

onBeforeUnmount(() => updateHeaderHover.cancel());

function onReady(value: ScratchGuiBridge) {
  bridge.value = value;
}

function onRecovery(result: CheckpointRecoveryResult) {
  if (result.restoredRunIds.length) {
    editorError.value = '已恢复一次中断的 AI 项目提交。';
  }
}

async function togglePanel(panel: Exclude<ActivePanel, null>) {
  if (scratchLocked.value) return;
  activePanel.value = activePanel.value === panel ? null : panel;
  await nextTick();
  window.requestAnimationFrame(() => window.dispatchEvent(new Event('resize')));
}

async function closePanel() {
  if (aiBlocksBusy.value) return;
  activePanel.value = null;
  await nextTick();
  window.requestAnimationFrame(() => window.dispatchEvent(new Event('resize')));
}

function onScratchLockChange(locked: boolean) {
  scratchLocked.value = locked;
}

function onAiBlocksBusyChange(busy: boolean) {
  aiBlocksBusy.value = busy;
}

function openBlank(event?: Event) {
  if (scratchLocked.value) return;
  canvasTrigger = event?.currentTarget as HTMLElement | null;
  canvasInput.value = null;
  canvasOpen.value = true;
}

function openGenerated(result: ImageGenerationResponse) {
  canvasTrigger = assetLibraryOpen.value ? assetLibraryTrigger.value : document.activeElement as HTMLElement | null;
  assetLibraryOpen.value = false;
  canvasInput.value = {
    kind: 'base64',
    base64: result.image_base64,
    mimeType: result.mime_type,
    target: result.target,
    requestId: result.request_id,
    processingHints: result.processing_hints ? {
      backgroundMode: result.processing_hints.background_mode,
      suggestedKeyColor: result.processing_hints.suggested_key_color,
      sheetLayout: result.processing_hints.sheet_layout
    } : undefined
  };
  canvasOpen.value = true;
}

async function importLibraryAsset(asset: GeneratedAsset, target: ImageTarget | 'canvas') {
  if (target === 'canvas') {
    openGenerated(asset.result);
    return;
  }
  if (!bridge.value || libraryImportingId.value || scratchLocked.value) return;
  libraryImportingId.value = asset.id;
  libraryError.value = '';
  try {
    await importSelectedImage(bridge.value, asset.result, target);
    assetLibraryOpen.value = false;
  } catch (error: unknown) {
    libraryError.value = error instanceof Error ? error.message : '素材导入 Scratch 失败';
  } finally {
    libraryImportingId.value = null;
  }
}

function openCanvasHandoff(opId: string, input: CanvasInput) {
  pendingCanvasOpId.value = opId; canvasHandoffMode.value = 'return-prepared-asset'; canvasInput.value = input; canvasOpen.value = true;
}

function onCanvasPrepared(payload: CanvasPreparedAsset) {
  if (pendingCanvasOpId.value && aiBlocksPanel.value) {
    aiBlocksPanel.value.acceptCanvasPrepared(pendingCanvasOpId.value, payload);
    pendingCanvasOpId.value = null;
  } else pendingCanvasHandoff.value = payload;
  void closeCanvas();
}

async function closeCanvas() {
  canvasOpen.value = false;
  canvasInput.value = null;
  canvasHandoffMode.value = 'direct-import';
  await nextTick();
  canvasTrigger?.focus();
  window.requestAnimationFrame(() => window.dispatchEvent(new Event('resize')));
}

async function refreshAssetCount() {
  try {
    assetCount.value = await countGeneratedAssets();
  } catch {
    assetCount.value = 0;
  }
}

async function loadAssetLibrary() {
  libraryLoading.value = true;
  libraryError.value = '';
  try {
    assets.value = await listGeneratedAssets();
    assetCount.value = assets.value.length;
  } catch (error: unknown) {
    libraryError.value = error instanceof Error ? error.message : '无法读取当前浏览器的本地数据';
  } finally {
    libraryLoading.value = false;
  }
}

function openAssetLibrary() {
  if (scratchLocked.value) return;
  assetLibraryOpen.value = true;
  void loadAssetLibrary();
}

async function closeAssetLibrary() {
  assetLibraryOpen.value = false;
  await nextTick();
  assetLibraryTrigger.value?.focus();
}

async function onAssetSaved() {
  await refreshAssetCount();
  if (assetLibraryOpen.value) await loadAssetLibrary();
}

async function removeAsset(id: string) {
  libraryLoading.value = true;
  try {
    await deleteGeneratedAsset(id);
    await loadAssetLibrary();
  } catch (error: unknown) {
    libraryError.value = error instanceof Error ? error.message : '删除素材失败';
    libraryLoading.value = false;
  }
}

async function clearAssetLibrary() {
  if (!window.confirm('确定清空当前浏览器中的所有 AI 素材吗？此操作无法撤销。')) return;
  libraryLoading.value = true;
  try {
    await clearGeneratedAssets();
    await loadAssetLibrary();
  } catch (error: unknown) {
    libraryError.value = error instanceof Error ? error.message : '清空素材库失败';
    libraryLoading.value = false;
  }
}

onMounted(() => void refreshAssetCount());
</script>

<template>
  <div class="app-shell">
    <main class="workspace" :class="{'is-canvas-hidden': canvasOpen}" :inert="canvasOpen || assetLibraryOpen" :aria-hidden="canvasOpen || assetLibraryOpen ? 'true' : undefined">
      <div class="header-tools">
        <button
          ref="assetLibraryTrigger" :class="{'is-hovered': hoveredHeaderTool === 'assets'}" @pointerenter="updateHeaderHover('assets')" @pointerleave="updateHeaderHover(null)"
          class="panel-toggle--header"
          type="button"
          :disabled="scratchLocked"
          title="打开素材库"
          :aria-label="'打开素材库，共 ' + assetCount + ' 项素材'"
          @click="openAssetLibrary"
        >
          <Images :size="18" aria-hidden="true" />
          <span class="header-tool__content" aria-hidden="true">
            <span class="header-tool__label">素材库</span>
            <span v-if="assetCount" class="asset-library-trigger__count">{{ assetCount > 99 ? '99+' : assetCount }}</span>
          </span>
        </button>
        <button class="panel-toggle--header" :class="{'is-active': activePanel === 'image', 'is-hovered': hoveredHeaderTool === 'image'}" @pointerenter="updateHeaderHover('image')" @pointerleave="updateHeaderHover(null)" type="button" :disabled="scratchLocked" :title="activePanel === 'image' ? '收起生成素材' : '展开生成素材'" :aria-label="activePanel === 'image' ? '收起生成素材' : '展开生成素材'" :aria-expanded="activePanel === 'image'" aria-controls="image-generation-panel" @click="togglePanel('image')">
          <Bot :size="18" aria-hidden="true" />
          <span class="header-tool__content" aria-hidden="true"><span class="header-tool__label">生成素材</span></span>
        </button>
        <button class="panel-toggle--header" :class="{'is-active': activePanel === 'blocks', 'is-hovered': hoveredHeaderTool === 'blocks'}" @pointerenter="updateHeaderHover('blocks')" @pointerleave="updateHeaderHover(null)" type="button" :disabled="scratchLocked" :title="scratchLocked ? '打开 AI 写积木' : activePanel === 'blocks' ? '收起 AI 写积木' : '打开 AI 写积木'" :aria-label="scratchLocked ? '打开 AI 写积木' : activePanel === 'blocks' ? '收起 AI 写积木' : '打开 AI 写积木'" :aria-expanded="activePanel === 'blocks'" aria-controls="ai-blocks-panel" @click="togglePanel('blocks')">
          <BlocksIcon :size="18" aria-hidden="true" />
          <span class="header-tool__content" aria-hidden="true"><span class="header-tool__label">AI 写积木</span></span>
        </button>
        <button class="panel-toggle--header" :class="{'is-hovered': hoveredHeaderTool === 'canvas'}" type="button" @pointerenter="updateHeaderHover('canvas')" @pointerleave="updateHeaderHover(null)" :disabled="scratchLocked" title="打开画布" aria-label="打开画布" @click="openBlank">
          <Palette :size="18" aria-hidden="true" />
          <span class="header-tool__content" aria-hidden="true"><span class="header-tool__label">无边画布</span></span>
        </button>
      </div>

      <section class="workspace__editor">
        <div v-if="editorError" class="editor-error" role="alert">{{ editorError }}</div>
        <ScratchHost :locked="scratchLocked" @ready="onReady" @recovery="onRecovery" @error="editorError = $event.message" />
      </section>
      <aside class="workspace__ai" :class="{'is-collapsed': !activePanel, 'is-blocks-panel': activePanel === 'blocks'}" @keydown.stop @keyup.stop>
        <button v-if="activePanel" class="icon-button panel-toggle" type="button" :disabled="aiBlocksBusy" title="关闭侧栏" aria-label="关闭侧栏" :aria-expanded="true" @click="closePanel">
          <PanelRightClose :size="17" aria-hidden="true" />
        </button>
        <div v-if="activePanel === 'image'" id="image-generation-panel">
          <header class="ai-panel-heading">
            <h2><ImagePlus :size="18" aria-hidden="true" /><span>生成素材</span></h2>
            <p>生成背景或角色，也可以把本地图片放入画布处理。</p>
          </header>
          <ImageGenerationPanel :bridge="bridge" @open-canvas="openGenerated" @asset-saved="onAssetSaved" />
        </div>
        <AiBlocksPanel
          v-else-if="activePanel === 'blocks'"
          id="ai-blocks-panel"
          :bridge="bridge"
          ref="aiBlocksPanel"
          @lock-change="onScratchLockChange"
          @busy-change="onAiBlocksBusyChange"
          @open-canvas="openCanvasHandoff"
        />
      </aside>
    </main>

    <CanvasWorkspace v-if="canvasOpen" :initial-input="canvasInput" :bridge="bridge" :handoff-mode="canvasHandoffMode" @close="closeCanvas" @prepared="onCanvasPrepared" />
    <AssetLibraryDialog
      :open="assetLibraryOpen"
      :assets="assets"
      :loading="libraryLoading"
      :error="libraryError"
      :importing-id="libraryImportingId"
      @close="closeAssetLibrary"
      @import="importLibraryAsset"
      @remove="removeAsset"
      @clear="clearAssetLibrary"
      @retry="loadAssetLibrary"
    />
  </div>
</template>
