<script setup lang="ts">
import {computed, ref, watch} from 'vue';
import ScratchLibraryPickerDialog from '../ai/ScratchLibraryPickerDialog.vue';
import type {ScratchLibraryItem, ScratchSpriteItem} from '../ai/scratch-library';
import {AlertTriangle, Check, Image, Sparkles, X} from 'lucide-vue-next';
import type {ProjectManifest, ProjectManifestResourceSource, ResourceOp} from './project-plan';

const props = defineProps<{open: boolean; plan: ProjectManifest | null}>();
const emit = defineEmits<{cancel: []; confirm: [confirmedGenerationIds: string[], selections: Record<string, ScratchSpriteItem | ScratchLibraryItem> ]}>();
const pickerEntry = ref<ResourceEntry | null>(null);
const selections = ref(new Map<string, ScratchSpriteItem | ScratchLibraryItem>());
const selectedGenerationKeys = ref(new Set<string>());

interface ResourceEntry {
  key: string;
  op: ResourceOp;
  name: string;
  source?: ProjectManifestResourceSource;
  tags: string[];
  selectionKey: string;
}

const resources = computed<ResourceEntry[]>(() => {
  const result: ResourceEntry[] = [];
  for (const op of props.plan?.resource_ops ?? []) {
    if (op.op === 'add_sprite' && op.costumes.length) {
      for (const costume of op.costumes) result.push({key: op.op_id + ':' + costume.name, op, name: op.name + ' · ' + costume.name, source: costume.source, tags: costume.tags, selectionKey: op.op_id + ':' + costume.name});
    } else {
      result.push({key: op.op_id, op, name: op.name, source: 'source' in op ? op.source : undefined, tags: 'tags' in op ? op.tags : [], selectionKey: op.op_id});
    }
  }
  return result;
});

const generated = computed(() => resources.value.filter(entry => entry.source?.kind === 'image_generation'));
const libraryEntries = computed(() => resources.value.filter(entry => entry.source?.kind === 'scratch_library'));
const allGeneratedSelected = computed(() => generated.value.every(entry => selectedGenerationKeys.value.has(entry.key)));
const canConfirm = computed(() => allGeneratedSelected.value && libraryEntries.value.every(entry => selections.value.has(entry.selectionKey)));

watch(() => props.open, open => {
  if (open) {
    selectedGenerationKeys.value = new Set(generated.value.map(entry => entry.key));
    selections.value = new Map();
  }
});

function toggleGeneration(key: string) {
  const next = new Set(selectedGenerationKeys.value);
  if (next.has(key)) next.delete(key); else next.add(key);
  selectedGenerationKeys.value = next;
}
function openPicker(entry: ResourceEntry) { pickerEntry.value = entry; }
function selectAsset(item: ScratchSpriteItem | ScratchLibraryItem) {
  if (!pickerEntry.value) return;
  selections.value = new Map(selections.value).set(pickerEntry.value.selectionKey, item);
  pickerEntry.value = null;
}
const pickerType = computed(() => pickerEntry.value?.source?.kind === 'scratch_library' ? pickerEntry.value.source.asset_type : 'sprite');
const pickerTerms = computed(() => pickerEntry.value?.source?.kind === 'scratch_library' ? pickerEntry.value.source.search_terms : []);
function confirm() {
  if (!canConfirm.value) return;
  const confirmed = generated.value.filter(entry => selectedGenerationKeys.value.has(entry.key)).map(entry => entry.key);
  emit('confirm', [...confirmed], Object.fromEntries(selections.value));
}
function sourceLabel(entry: ResourceEntry) {
  if (entry.op.op === 'select_backdrop') return '切换已有背景';
  if (entry.source?.kind === 'scratch_library') return 'Scratch 内置素材';
  return '待确认生图';
}
function detail(entry: ResourceEntry) {
  if (entry.op.op === 'select_backdrop') return entry.op.name;
  if (entry.source?.kind === 'scratch_library') return entry.source.search_terms.join('、');
  return entry.source?.kind === 'image_generation' ? entry.source.prompt : '资源来源由清单指定';
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open && plan" class="resource-review-overlay">
      <section class="resource-review" role="dialog" aria-modal="true" aria-labelledby="resource-review-title">
        <header class="resource-review__header"><div><h2 id="resource-review-title"><Image :size="19" />确认项目资源</h2><p>{{ plan.summary }}</p></div><button class="icon-button" type="button" title="取消" aria-label="取消" @click="emit('cancel')"><X :size="18" /></button></header>
        <div class="resource-review__notice"><AlertTriangle :size="17" /><span>确认前不会调用生图服务，也不会修改 Scratch 项目。</span></div>
        <div class="resource-review__list">
          <article v-for="entry in resources" :key="entry.key" class="resource-review__item">
            <button v-if="entry.source?.kind === 'scratch_library'" class="button button--quiet resource-review__choose" type="button" @click="openPicker(entry)">{{ selections.has(entry.selectionKey) ? '已选择' : '选择素材' }}</button>
            <label v-if="entry.source?.kind === 'image_generation'" class="resource-review__check"><input type="checkbox" :checked="selectedGenerationKeys.has(entry.key)" :aria-label="'确认生成 ' + entry.name" @click="toggleGeneration(entry.key)" /><span>确认</span></label>
            <div class="resource-review__icon"><Sparkles v-if="sourceLabel(entry) === '待确认生图'" :size="18" /><Check v-else :size="18" /></div>
            <div><strong>{{ entry.name }}</strong><span>{{ sourceLabel(entry) }}</span><p>{{ detail(entry) }}</p><small v-if="entry.tags.length">#{{ entry.tags.join(' #') }}</small></div>
          </article>
          <p v-if="!resources.length" class="resource-review__empty">此项目不需要新增资源。</p>
        </div>
        <footer class="resource-review__footer"><span>{{ resources.length }} 项资源<span v-if="generated.length">，其中 {{ generated.length }} 项需要确认生成</span></span><div><button class="button" type="button" @click="emit('cancel')">取消</button><button class="button button--primary" type="button" :disabled="!canConfirm" @click="confirm">确认并准备</button></div></footer>
      </section>
      <ScratchLibraryPickerDialog v-if="pickerEntry" :open="Boolean(pickerEntry)" :type="pickerType" :initial-terms="pickerTerms" @close="pickerEntry = null" @select="selectAsset" />
    </div>
  </Teleport>
</template>
