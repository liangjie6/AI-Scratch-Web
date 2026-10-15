<script setup lang="ts">
import {computed, onBeforeUnmount, ref} from 'vue';
import {ImagePlus, LoaderCircle, Palette, RefreshCw, Sparkles, X} from 'lucide-vue-next';
import SpriteSheetSelector from './SpriteSheetSelector.vue';
import {generateImage} from './image-generation-api';
import {streamTextExpansion} from './text-expansion-api';
import {
  CANVAS_SIZES,
  DEFAULT_IMAGE_MODEL,
  IMAGE_MODELS,
  IMAGE_STYLES,
  type ImageGenerationResponse,
  type ImageModel,
  type ImageStyle,
  type ImageTarget
} from './image-generation-types';
import {importSelectedImage} from '../scratch/asset-adapter';
import {saveGeneratedAsset, type GeneratedAsset} from './asset-library-store';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';

const props = defineProps<{bridge: ScratchGuiBridge | null}>();
const emit = defineEmits<{
  openCanvas: [result: ImageGenerationResponse];
  assetSaved: [asset: GeneratedAsset];
}>();
const model = ref<ImageModel>(DEFAULT_IMAGE_MODEL);
const prompt = ref('');
const size = ref(CANVAS_SIZES[0]);
const style = ref<ImageStyle>('cartoon');
const customStyle = ref('');
const target = ref<ImageTarget>('backdrop');
const result = ref<ImageGenerationResponse | null>(null);
const state = ref<'idle' | 'generating' | 'importing' | 'error'>('idle');
const errorMessage = ref('');
const librarySaveError = ref('');
type ExpansionTarget = 'prompt' | 'customStyle';
const expansionTarget = ref<ExpansionTarget | null>(null);
const expansionError = ref<{target: ExpansionTarget; message: string} | null>(null);
let controller: AbortController | null = null;
let expansionController: AbortController | null = null;

const isImageBusy = computed(() => state.value === 'generating' || state.value === 'importing');
const isExpanding = computed(() => expansionTarget.value !== null);
const isBusy = computed(() => isImageBusy.value || isExpanding.value);
const canGenerate = computed(() => prompt.value.trim().length >= 2 && (style.value !== 'custom' || customStyle.value.trim().length >= 2) && !isBusy.value);
const spriteHint = computed(() =>
  model.value === 'gpt-image-2.5-sunburst'
    ? '角色图将生成透明背景的独立主体，方便直接提取角色。'
    : '角色图将生成纯绿底（#00ff00）的独立主体，便于抠图提取角色。'
);

async function submit() {
  if (!canGenerate.value) return;
  controller?.abort();
  const requestController = new AbortController();
  controller = requestController;
  state.value = 'generating';
  errorMessage.value = '';
  librarySaveError.value = '';
  let timedOut = false;
  const timeoutId = window.setTimeout(() => {
    timedOut = true;
    requestController.abort();
  }, 120_000);
  try {
    const selectedSize = size.value;
    if (!selectedSize || typeof selectedSize.width !== 'number' || typeof selectedSize.height !== 'number') {
      throw new Error('画布规格无效，请重新选择后重试');
    }
    const request = {
      prompt: prompt.value.trim(),
      style: style.value,
      custom_style: style.value === 'custom' ? customStyle.value.trim() : undefined,
      width: selectedSize.width,
      height: selectedSize.height,
      target: target.value,
      sheet_layout: '1x1' as const,
      model: model.value
    };
    const generated = await generateImage(request, requestController.signal);
    if (controller !== requestController) return;
    result.value = generated;
    state.value = 'idle';
    try {
      const asset = await saveGeneratedAsset(generated, request);
      emit('assetSaved', asset);
    } catch (error: unknown) {
      librarySaveError.value = error instanceof Error ? error.message : '当前浏览器无法保存素材';
    }
  } catch (error: unknown) {
    if (controller !== requestController) return;
    if (error instanceof DOMException && error.name === 'AbortError' && !timedOut) {
      state.value = 'idle';
      return;
    }
    state.value = 'error';
    errorMessage.value = timedOut ? '生图服务响应超时，请稍后重试' : error instanceof Error ? error.message : '生成失败，请稍后重试';
  } finally {
    window.clearTimeout(timeoutId);
    if (controller === requestController) controller = null;
  }
}

function cancel() {
  controller?.abort();
  controller = null;
  state.value = 'idle';
}

async function expandText(target: ExpansionTarget) {
  const original = (target === 'prompt' ? prompt.value : customStyle.value).trim();
  if (!original || isBusy.value) return;

  expansionController?.abort();
  expansionController = new AbortController();
  expansionTarget.value = target;
  expansionError.value = null;
  let received = '';
  const maxLength = target === 'prompt' ? 800 : 240;

  try {
    await streamTextExpansion(
      {kind: target === 'prompt' ? 'scene' : 'style', text: original},
      delta => {
        received = (received + delta).slice(0, maxLength);
        if (target === 'prompt') prompt.value = received;
        else customStyle.value = received;
      },
      expansionController.signal
    );
    if (target === 'prompt') prompt.value = received.trim();
    else customStyle.value = received.trim();
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return;
    if (!received) {
      if (target === 'prompt') prompt.value = original;
      else customStyle.value = original;
    }
    expansionError.value = {
      target,
      message: error instanceof Error ? error.message : 'AI 扩写暂时不可用'
    };
  } finally {
    expansionTarget.value = null;
    expansionController = null;
  }
}

async function importResult() {
  if (!result.value || !props.bridge || isBusy.value) return;
  state.value = 'importing';
  errorMessage.value = '';
  try {
    await importSelectedImage(props.bridge, result.value, target.value);
    state.value = 'idle';
  } catch (error: unknown) {
    state.value = 'error';
    errorMessage.value = error instanceof Error ? error.message : '导入 Scratch 失败';
  }
}

onBeforeUnmount(() => {
  controller?.abort();
  expansionController?.abort();
});
</script>

<template>
  <div class="ai-panel">
    <form class="ai-form" @submit.prevent="submit">
      <label class="field-label" for="prompt">画面描述</label>
      <div class="prompt-input-wrap">
        <textarea id="prompt" v-model="prompt" class="prompt-input prompt-input--with-action" maxlength="800" rows="3" :disabled="isBusy" placeholder="例如：夜晚的森林小屋，窗户透出暖黄色灯光" />
        <button v-if="prompt.trim()" class="ai-expand-button" type="button" aria-label="使用 AI 扩写场景提示词" :disabled="isBusy" @click="expandText('prompt')">
          <LoaderCircle v-if="expansionTarget === 'prompt'" class="spin" :size="13" aria-hidden="true" />
          <Sparkles v-else :size="13" aria-hidden="true" />
          {{ expansionTarget === 'prompt' ? '扩写中' : 'AI扩写' }}
        </button>
      </div>
      <p v-if="expansionError?.target === 'prompt'" class="expansion-error" role="alert">{{ expansionError.message }}</p>

      <div class="image-model-field">
        <label class="field-label" for="image-model">模型选择</label>
        <select id="image-model" v-model="model" class="field-control" :disabled="isBusy">
          <option v-for="option in IMAGE_MODELS" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
      </div>

      <div class="field-grid">
        <label class="field-group">
          <span class="field-label">画布规格</span>
          <select v-model="size" class="field-control" :disabled="isBusy">
            <option v-for="option in CANVAS_SIZES" :key="option.label" :value="option">{{ option.label }}</option>
          </select>
        </label>
        <label class="field-group">
          <span class="field-label">画面风格</span>
          <select v-model="style" class="field-control" :disabled="isBusy">
            <option v-for="option in IMAGE_STYLES" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
        </label>
      </div>
      <div v-if="style === 'custom'" class="custom-style-field">
        <label class="field-label" for="custom-style">自定义画面风格</label>
        <div class="prompt-input-wrap">
          <textarea id="custom-style" v-model="customStyle" class="prompt-input prompt-input--with-action custom-style-input" maxlength="240" rows="3" :disabled="isBusy" placeholder="例如：复古木刻版画，黑白高对比" />
          <button v-if="customStyle.trim()" class="ai-expand-button" type="button" aria-label="使用 AI 扩写风格提示词" :disabled="isBusy" @click="expandText('customStyle')">
            <LoaderCircle v-if="expansionTarget === 'customStyle'" class="spin" :size="13" aria-hidden="true" />
            <Sparkles v-else :size="13" aria-hidden="true" />
            {{ expansionTarget === 'customStyle' ? '扩写中' : 'AI扩写' }}
          </button>
        </div>
        <p v-if="expansionError?.target === 'customStyle'" class="expansion-error" role="alert">{{ expansionError.message }}</p>
      </div>

      <fieldset class="target-toggle">
        <legend class="field-label">导入到</legend>
        <label :class="{'is-active': target === 'backdrop'}">
          <input v-model="target" type="radio" value="backdrop" :disabled="isBusy" />
          <span>舞台背景</span>
        </label>
        <label :class="{'is-active': target === 'sprite'}">
          <input v-model="target" type="radio" value="sprite" :disabled="isBusy" />
          <span>新角色</span>
        </label>
      </fieldset>
      <p v-if="target === 'sprite'" class="field-note">{{ spriteHint }}</p>

      <div class="ai-form__actions">
        <button v-if="state === 'generating'" class="button button--quiet" type="button" @click="cancel">
          <X :size="16" aria-hidden="true" />取消生成
        </button>
        <button v-else class="button button--primary" type="submit" :disabled="!canGenerate">
          <ImagePlus :size="16" aria-hidden="true" />
          生成 1 张图片
        </button>
      </div>
    </form>

    <div v-if="state === 'error'" class="ai-message ai-message--error" role="alert">
      <span>{{ errorMessage }}</span>
      <button class="text-button" type="button" @click="submit"><RefreshCw :size="14" aria-hidden="true" />重试</button>
    </div>
    <div v-if="librarySaveError" class="ai-message ai-message--warning" role="status">
      <span>图片已生成，但未能保存到素材库：{{ librarySaveError }}</span>
    </div>
    <div class="result-area">
      <div v-if="state === 'generating'" class="generation-progress" role="status" aria-live="polite">
        <div class="generation-progress__visual" aria-hidden="true">
          <span class="generation-progress__scan" />
          <i v-for="dot in 3" :key="dot" class="generation-progress__dot" :style="{animationDelay: `${(dot - 1) * 180}ms`}" />
        </div>
        <strong>正在构思画面</strong>
        <span class="generation-progress__hint">AI 正在组织构图与色彩，请稍候</span>
      </div>
      <SpriteSheetSelector v-else-if="result" :result="result" />
      <div v-else class="result-empty"><ImagePlus :size="23" aria-hidden="true" /><span>生成结果会显示在这里</span></div>
      <div v-if="result" class="result-actions">
        <button class="button button--canvas" :class="{'button--import': target === 'sprite'}" type="button" :disabled="isBusy" @click="emit('openCanvas', result)">
          <Palette :size="16" aria-hidden="true" />在画布中编辑
        </button>
        <button v-if="target === 'backdrop'" class="button button--import" type="button" :disabled="!props.bridge || isBusy" @click="importResult">
          <LoaderCircle v-if="state === 'importing'" class="spin" :size="16" aria-hidden="true" />
          {{ state === 'importing' ? '正在导入' : '直接导入背景' }}
        </button>
      </div>
    </div>
  </div>
</template>
