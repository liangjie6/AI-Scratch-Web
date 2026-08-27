<script setup lang="ts">
import {computed, ref, watch} from 'vue';
import {Check, LoaderCircle, Search, X} from 'lucide-vue-next';
import {searchScratchLibrary, scratchAssetUrl, type ScratchAssetType, type ScratchLibraryItem, type ScratchSpriteItem} from './scratch-library';

const props = defineProps<{open: boolean; type: ScratchAssetType; initialTerms?: string[]}>();
const emit = defineEmits<{close: []; select: [item: ScratchSpriteItem | ScratchLibraryItem]}>();
const terms = ref('');
const items = ref<Array<ScratchSpriteItem | ScratchLibraryItem>>([]);
const loading = ref(false);
const error = ref('');
const selected = ref<string | null>(null);
const title = computed(() => props.type === 'sprite' ? '选择角色素材' : props.type === 'costume' ? '选择造型素材' : '选择舞台背景');

watch(() => [props.open, props.type, props.initialTerms] as const, async ([open, type, initial]) => {
  if (!open) return;
  terms.value = (initial ?? []).join(' ');
  await search();
});

async function search() {
  loading.value = true; error.value = ''; selected.value = null;
  try {
    const words = terms.value.split(/\s+/).filter(Boolean);
    items.value = props.type === 'sprite' ? await searchScratchLibrary('sprite', words) : await searchScratchLibrary(props.type, words);
  }
  catch (cause) { error.value = cause instanceof Error ? cause.message : '素材清单暂时无法读取'; items.value = []; }
  finally { loading.value = false; }
}
function preview(item: ScratchSpriteItem | ScratchLibraryItem) {
  const costume = 'costumes' in item ? item.costumes[0] : item;
  return costume ? scratchAssetUrl(costume.md5ext) : '';
}
function choose(item: ScratchSpriteItem | ScratchLibraryItem) {selected.value = item.name; emit('select', item);}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="scratch-picker-overlay" @click.self="emit('close')">
      <section class="scratch-picker" role="dialog" aria-modal="true" :aria-label="title">
        <header class="scratch-picker__header"><h2>{{ title }}</h2><button class="icon-button" type="button" title="关闭" aria-label="关闭" @click="emit('close')"><X :size="18" /></button></header>
        <form class="scratch-picker__search" @submit.prevent="search"><Search :size="17" aria-hidden="true" /><input v-model="terms" aria-label="搜索素材" placeholder="搜索名称或标签" /><button class="button button--primary" type="submit">搜索</button></form>
        <div v-if="loading" class="scratch-picker__state"><LoaderCircle class="spin" :size="20" />正在读取本地素材</div>
        <div v-else-if="error" class="scratch-picker__state scratch-picker__state--error" role="alert">{{ error }}</div>
        <div v-else-if="!items.length" class="scratch-picker__state">没有匹配的本地素材</div>
        <div v-else class="scratch-picker__grid">
          <button v-for="item in items" :key="item.name" class="scratch-picker__item" :class="{ 'is-selected': selected === item.name }" type="button" @click="choose(item)">
            <img :src="preview(item)" :alt="item.name" /><span>{{ item.name }}</span><Check v-if="selected === item.name" :size="16" aria-hidden="true" />
          </button>
        </div>
      </section>
    </div>
  </Teleport>
</template>
