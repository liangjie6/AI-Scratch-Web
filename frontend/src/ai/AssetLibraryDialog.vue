<script setup lang="ts">
import {nextTick, ref, watch} from 'vue';
import {Image, Images, LoaderCircle, Palette, RefreshCw, Trash2, UserRound, X} from 'lucide-vue-next';
import type {ImageTarget} from './image-generation-types';
import {IMAGE_STYLES} from './image-generation-types';
import type {GeneratedAsset} from './asset-library-store';

const props = defineProps<{
  open: boolean;
  assets: GeneratedAsset[];
  loading: boolean;
  error: string;
  importingId: string | null;
}>();

const emit = defineEmits<{
  close: [];
  import: [asset: GeneratedAsset, target: ImageTarget | 'canvas'];
  remove: [id: string];
  clear: [];
  retry: [];
}>();

const closeButton = ref<HTMLButtonElement | null>(null);
const dateFormatter = new Intl.DateTimeFormat('zh-CN', {
  month: 'numeric',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit'
});

watch(() => props.open, async open => {
  if (!open) return;
  await nextTick();
  closeButton.value?.focus();
});

function imageUrl(asset: GeneratedAsset) {
  return `data:${asset.result.mime_type};base64,${asset.result.image_base64}`;
}

function styleLabel(asset: GeneratedAsset) {
  if (asset.style === 'custom') return asset.customStyle || '自定义风格';
  return IMAGE_STYLES.find(option => option.value === asset.style)?.label ?? 'AI 生成';
}

function closeOnEscape(event: KeyboardEvent) {
  if (event.key === 'Escape') emit('close');
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="asset-library-overlay" @click.self="emit('close')" @keydown="closeOnEscape">
      <section class="asset-library" role="dialog" aria-modal="true" aria-labelledby="asset-library-title">
        <header class="asset-library__header">
          <div>
            <h2 id="asset-library-title"><Images :size="19" aria-hidden="true" />素材库</h2>
            <p>{{ assets.length ? `已在本地保存 ${assets.length} 项素材` : 'AI 生成结果会自动保存在这里' }}</p>
          </div>
          <button ref="closeButton" class="icon-button" type="button" title="关闭素材库" aria-label="关闭素材库" @click="emit('close')">
            <X :size="18" aria-hidden="true" />
          </button>
        </header>

        <div class="asset-library__content">
          <div v-if="loading" class="asset-library__loading" role="status">
            <LoaderCircle class="spin" :size="22" aria-hidden="true" />
            <span>正在读取本地素材</span>
          </div>
          <div v-else-if="error" class="asset-library__error" role="alert">
            <strong>素材库暂时无法读取</strong>
            <span>{{ error }}</span>
            <button class="button button--quiet" type="button" @click="emit('retry')"><RefreshCw :size="15" />重试</button>
          </div>
          <div v-else-if="!assets.length" class="asset-library__empty">
            <Images :size="30" aria-hidden="true" />
            <strong>还没有本地素材</strong>
            <span>在右侧生成一张图片后，它会自动出现在这里。</span>
          </div>
          <div v-else class="asset-library__grid">
            <article v-for="asset in assets" :key="asset.id" class="asset-card">
              <div class="asset-card__preview">
                <img :src="imageUrl(asset)" :alt="asset.name" />
                <span>{{ asset.target === 'backdrop' ? '舞台背景' : '角色素材' }}</span>
              </div>
              <div class="asset-card__body">
                <strong class="asset-card__name" :title="asset.name">{{ asset.name }}</strong>
                <p :title="asset.prompt">{{ asset.prompt }}</p>
                <div class="asset-card__meta">
                  <span>{{ styleLabel(asset) }}</span>
                  <span class="asset-card__attachment" :title="asset.attachment === 'attached' ? '该素材已被已验证的 AI 项目写入使用' : '该素材尚未写入已验证的 AI 项目'">
                    {{ asset.attachment === 'attached' ? '已挂载' : '未挂载' }}
                  </span>
                  <time :datetime="new Date(asset.createdAt).toISOString()">{{ dateFormatter.format(asset.createdAt) }}</time>
                </div>
                <div class="asset-card__actions">
                  <button class="button" type="button" :disabled="!!props.importingId" @click="emit('import', asset, 'sprite')">
                    <LoaderCircle v-if="props.importingId === asset.id" class="spin" :size="14" /><UserRound v-else :size="14" aria-hidden="true" />导入为角色
                  </button>
                  <button class="button" type="button" :disabled="!!props.importingId" @click="emit('import', asset, 'backdrop')">
                    <Image :size="14" aria-hidden="true" />导入为背景
                  </button>
                  <button class="button button--primary" type="button" :disabled="!!props.importingId" @click="emit('import', asset, 'canvas')">
                    <Palette :size="14" aria-hidden="true" />导入到画布
                  </button>
                  <button class="icon-button" type="button" title="删除这项素材" aria-label="删除这项素材" :disabled="!!props.importingId" @click="emit('remove', asset.id)">
                    <Trash2 :size="16" aria-hidden="true" />
                  </button>
                </div>
              </div>
            </article>
          </div>
        </div>

        <footer v-if="assets.length && !loading && !error" class="asset-library__footer">
          <span>图片仅保存在当前浏览器中</span>
          <button class="text-button asset-library__clear" type="button" @click="emit('clear')"><Trash2 :size="14" />清空素材库</button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>
