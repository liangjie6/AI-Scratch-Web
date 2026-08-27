<script setup lang="ts">
import {computed, ref, watch} from 'vue';
import ScratchLibraryPickerDialog from '../ai/ScratchLibraryPickerDialog.vue';
import type {ScratchLibraryItem, ScratchSpriteItem} from '../ai/scratch-library';
import {AlertTriangle, Check, Image, Sparkles, X} from 'lucide-vue-next';
import type {ProjectPlan, ResourceOp} from './project-plan';

const props = defineProps<{open: boolean; plan: ProjectPlan | null}>();
const emit = defineEmits<{cancel: []; confirm: [confirmedGenerationIds: string[], selections: Record<string, ScratchSpriteItem | ScratchLibraryItem> ]}>();
const pickerOp = ref<ResourceOp | null>(null);
const selections = ref(new Map<string, ScratchSpriteItem | ScratchLibraryItem>());
const resources = computed(() => props.plan?.resource_ops ?? []);
const selectedGenerationIds = ref(new Set<string>());
function sourceOf(op: ResourceOp) {return 'source' in op ? op.source : undefined;}
function tagsOf(op: ResourceOp) {return 'tags' in op ? op.tags : [];}
const generated = computed(() => resources.value.filter(op => sourceOf(op)?.kind === 'image_generation'));
const allGeneratedSelected = computed(() => generated.value.every(op => selectedGenerationIds.value.has(op.op_id)));
const canConfirm = computed(() => allGeneratedSelected.value && !resources.value.some(op => sourceOf(op)?.kind === 'scratch_library' && !selections.value.has(op.op_id)));
watch(() => props.open, open => {if (open) selectedGenerationIds.value = new Set(generated.value.map(op => op.op_id));});
function toggleGeneration(id: string) {const next = new Set(selectedGenerationIds.value); if (next.has(id)) next.delete(id); else next.add(id); selectedGenerationIds.value = next;}
function openPicker(op: ResourceOp) {pickerOp.value = op;}
function selectAsset(item: ScratchSpriteItem | ScratchLibraryItem) {if (!pickerOp.value) return; selections.value = new Map(selections.value).set(pickerOp.value.op_id, item); pickerOp.value = null;}
const pickerType = computed(() => {const source = pickerOp.value ? sourceOf(pickerOp.value) : undefined; return source?.kind === 'scratch_library' ? source.asset_type : 'sprite';});
const pickerTerms = computed(() => {const source = pickerOp.value ? sourceOf(pickerOp.value) : undefined; return source?.kind === 'scratch_library' ? source.search_terms : [];});
function confirm() {if (!allGeneratedSelected.value || resources.value.some(op => sourceOf(op)?.kind === 'scratch_library' && !selections.value.has(op.op_id))) return; emit('confirm', [...selectedGenerationIds.value], Object.fromEntries(selections.value));}
function sourceLabel(op: ResourceOp) {
  if (op.op === 'select_backdrop') return '切换已有背景';
  if (sourceOf(op)?.kind === 'scratch_library') return 'Scratch 内置素材';
  return '待确认生图';
}
function detail(op: ResourceOp) {
  if (op.op === 'select_backdrop') return op.name;
  const source = sourceOf(op);
  if (source?.kind === 'scratch_library') return source.search_terms.join('、');
  return source?.kind === 'image_generation' ? source.prompt : '';
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open && plan" class="resource-review-overlay">
      <section class="resource-review" role="dialog" aria-modal="true" aria-labelledby="resource-review-title">
        <header class="resource-review__header"><div><h2 id="resource-review-title"><Image :size="19" />确认项目资源</h2><p>{{ plan.summary }}</p></div><button class="icon-button" type="button" title="取消" aria-label="取消" @click="emit('cancel')"><X :size="18" /></button></header>
        <div class="resource-review__notice"><AlertTriangle :size="17" /><span>确认前不会调用生图服务，也不会修改 Scratch 项目。</span></div>
        <div class="resource-review__list">
          <article v-for="op in resources" :key="op.op_id" class="resource-review__item"><button v-if="sourceOf(op)?.kind === 'scratch_library'" class="button button--quiet resource-review__choose" type="button" @click="openPicker(op)">{{ selections.has(op.op_id) ? '已选择' : '选择素材' }}</button><label v-if="sourceOf(op)?.kind === 'image_generation'" class="resource-review__check"><input type="checkbox" :checked="selectedGenerationIds.has(op.op_id)" :aria-label="'确认生成 ' + op.name" @click="toggleGeneration(op.op_id)" /><span>确认</span></label><div class="resource-review__icon"><Sparkles v-if="sourceLabel(op) === '待确认生图'" :size="18" /><Check v-else :size="18" /></div><div><strong>{{ op.name }}</strong><span>{{ sourceLabel(op) }}</span><p>{{ detail(op) }}</p><small v-if="tagsOf(op).length">#{{ tagsOf(op).join(' #') }}</small></div></article>
        </div>
        <footer class="resource-review__footer"><span>{{ resources.length }} 项资源<span v-if="generated.length">，其中 {{ generated.length }} 项需要确认生成</span></span><div><button class="button" type="button" @click="emit('cancel')">取消</button><button class="button button--primary" type="button" :disabled="!canConfirm" @click="confirm">确认并准备</button></div></footer>
      </section>
      <ScratchLibraryPickerDialog v-if="pickerOp" :open="Boolean(pickerOp)" :type="pickerType" :initial-terms="pickerTerms" @close="pickerOp = null" @select="selectAsset" />
    </div>
  </Teleport>
</template>
