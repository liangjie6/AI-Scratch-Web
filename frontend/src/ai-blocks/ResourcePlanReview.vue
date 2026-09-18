<script setup lang="ts">
import {computed, ref, watch} from 'vue';
import ScratchLibraryPickerDialog from '../ai/ScratchLibraryPickerDialog.vue';
import {rankScratchLibrary, scratchAssetUrl, selectHighConfidenceScratchMatch, SCRATCH_AUTO_MATCH_MIN_MARGIN, SCRATCH_AUTO_MATCH_MIN_SCORE, type ScratchLibraryItem, type ScratchSoundItem, type ScratchSpriteItem} from '../ai/scratch-library';
import {AlertTriangle, Check, Image, LoaderCircle, Sparkles, X} from 'lucide-vue-next';
import type {AssetPolicy, ImageGenerationSource, ProjectManifest, ProjectManifestResourceSource, ResourceOp} from './project-plan';
import type {AiRunResourceReviewMetricBucket} from './ai-runs-api';

type ScratchSelection = ScratchSpriteItem | ScratchLibraryItem | ScratchSoundItem;

const props = defineProps<{open: boolean; plan: ProjectManifest | null; assetPolicy?: AssetPolicy; runtimeAutoMatchEnabled?: boolean; runtimeAutoMatchMinScore?: number; runtimeAutoMatchMinMargin?: number}>();
const emit = defineEmits<{
  cancel: [];
  confirm: [confirmedGenerationIds: string[], selections: Record<string, ScratchSelection>, generationFallbacks: Record<string, ImageGenerationSource>, reviewMetrics: AiRunResourceReviewMetricBucket[]];
}>();

const pickerEntry = ref<ResourceEntry | null>(null);
const generationEntry = ref<ResourceEntry | null>(null);
const selections = ref(new Map<string, ScratchSelection>());
const selectedGenerationKeys = ref(new Set<string>());
const confirmedMissingGenerationKeys = ref(new Set<string>());
const automaticSelectionKeys = ref(new Set<string>());
const automaticReselectionKeys = ref(new Set<string>());
const reviewRequiredKeys = ref(new Set<string>());
const unmatchedKeys = ref(new Set<string>());
const matching = ref(false);
const matchError = ref('');
const autoCompleting = ref(false);
let matchRun = 0;
const assetAutoMatchEnabled = computed(() => import.meta.env.VITE_AI_ASSET_AUTO_MATCH_ENABLED === 'true' && props.runtimeAutoMatchEnabled === true);

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
      for (const costume of op.costumes) result.push({key: op.op_id + ':' + costume.name, op, name: op.name + ' · ' + costume.name, source: costume.source, tags: costume.tags ?? [], selectionKey: op.op_id + ':' + costume.name});
    } else {
      result.push({key: op.op_id, op, name: op.op === 'delete_sound' ? '删除声音 · ' + op.sound.name : op.name, source: 'source' in op ? op.source : undefined, tags: 'tags' in op ? op.tags ?? [] : [], selectionKey: op.op_id});
    }
  }
  return result;
});

const generated = computed(() => resources.value.filter(entry => entry.source?.kind === 'image_generation'));
const libraryEntries = computed(() => resources.value.filter(entry => entry.source?.kind === 'scratch_library'));
const unmatchedEntries = computed(() => libraryEntries.value.filter(entry => unmatchedKeys.value.has(entry.key)));
const unresolvedLibraryEntries = computed(() => unmatchedEntries.value.filter(entry => !selections.value.has(entry.selectionKey) && !confirmedMissingGenerationKeys.value.has(entry.key)));
const generationAllowed = computed(() => props.assetPolicy !== 'existing_only');
const allGeneratedSelected = computed(() => generated.value.every(entry => selectedGenerationKeys.value.has(entry.key)));
const generationPolicySatisfied = computed(() => generationAllowed.value || !generated.value.length);
const canConfirm = computed(() => !matching.value && !matchError.value && !unresolvedLibraryEntries.value.length && generationPolicySatisfied.value && allGeneratedSelected.value);

function sourceFor(entry: ResourceEntry) {
  return entry.source;
}

async function searchLibrarySource(source: Extract<ProjectManifestResourceSource, {kind: 'scratch_library'}>): Promise<ScratchSelection | undefined> {
  if (!assetAutoMatchEnabled.value) return undefined;
  const matches = await rankScratchLibrary(source.asset_type, source.search_terms);
  return selectHighConfidenceScratchMatch(matches, Math.max(SCRATCH_AUTO_MATCH_MIN_SCORE, props.runtimeAutoMatchMinScore ?? SCRATCH_AUTO_MATCH_MIN_SCORE), Math.max(SCRATCH_AUTO_MATCH_MIN_MARGIN, props.runtimeAutoMatchMinMargin ?? SCRATCH_AUTO_MATCH_MIN_MARGIN));
}

async function resolveLibraryMatches() {
  const run = ++matchRun;
  matching.value = true;
  matchError.value = '';
  autoCompleting.value = false;
  generationEntry.value = null;
  selections.value = new Map();
  automaticSelectionKeys.value = new Set();
  automaticReselectionKeys.value = new Set();
  reviewRequiredKeys.value = new Set();
  unmatchedKeys.value = new Set();
  confirmedMissingGenerationKeys.value = new Set();
  selectedGenerationKeys.value = new Set();
  try {
    const results = await Promise.all(libraryEntries.value.map(async entry => {
      const source = sourceFor(entry);
      if (!source || source.kind !== 'scratch_library') return {entry, selection: undefined};
      return {entry, selection: await searchLibrarySource(source)};
    }));
    if (run !== matchRun || !props.open) return;
    const nextSelections = new Map<string, ScratchSelection>();
    const nextAutomatic = new Set<string>();
    const missing: ResourceEntry[] = [];
    for (const result of results) {
      if (result.selection) {
        nextSelections.set(result.entry.selectionKey, result.selection);
        nextAutomatic.add(result.entry.key);
      } else {
        missing.push(result.entry);
      }
    }
    selections.value = nextSelections;
    automaticSelectionKeys.value = nextAutomatic;
    unmatchedKeys.value = new Set(missing.map(entry => entry.key));
    reviewRequiredKeys.value = new Set(missing.map(entry => entry.key));
    // Missing and ambiguous candidates remain in review. Image generation is
    // always an explicit decision, even when the policy allows it.
    matching.value = false;
    if (!missing.length) maybeAutoConfirm();
  } catch (cause) {
    if (run === matchRun) matchError.value = cause instanceof Error ? cause.message : 'Scratch 素材清单暂时无法读取';
  } finally {
    if (run === matchRun) matching.value = false;
  }
}

watch(() => [props.open, props.plan] as const, ([open]) => {
  if (open) void resolveLibraryMatches();
  else {
    matchRun += 1;
    generationEntry.value = null;
    pickerEntry.value = null;
    autoCompleting.value = false;
    matching.value = false;
    matchError.value = '';
  }
});

function toggleGeneration(key: string) {
  const next = new Set(selectedGenerationKeys.value);
  if (next.has(key)) next.delete(key); else next.add(key);
  selectedGenerationKeys.value = next;
}
function openPicker(entry: ResourceEntry) { pickerEntry.value = entry; }
function selectAsset(item: ScratchSelection) {
  const entry = pickerEntry.value;
  if (!entry) return;
  if (automaticSelectionKeys.value.has(entry.key)) automaticReselectionKeys.value = new Set([...automaticReselectionKeys.value, entry.key]);
  selections.value = new Map(selections.value).set(entry.selectionKey, item);
  automaticSelectionKeys.value = new Set([...automaticSelectionKeys.value].filter(key => key !== entry.key));
  unmatchedKeys.value = new Set([...unmatchedKeys.value].filter(key => key !== entry.key));
  confirmedMissingGenerationKeys.value = new Set([...confirmedMissingGenerationKeys.value].filter(key => key !== entry.key));
  pickerEntry.value = null;
  maybeAutoConfirm();
}
function resourceType(entry: ResourceEntry): AiRunResourceReviewMetricBucket['resource_type'] | undefined {
  const source = sourceFor(entry);
  if (source?.kind !== 'scratch_library') return undefined;
  return source.asset_type;
}
function reviewMetrics(): AiRunResourceReviewMetricBucket[] {
  const buckets = new Map<AiRunResourceReviewMetricBucket['resource_type'], AiRunResourceReviewMetricBucket>();
  const bucketFor = (type: AiRunResourceReviewMetricBucket['resource_type']) => {
    let bucket = buckets.get(type);
    if (!bucket) {
      bucket = {resource_type: type, auto_selected_count: 0, auto_reselected_count: 0, manual_selected_count: 0, review_required_count: 0, generation_fallback_count: 0};
      buckets.set(type, bucket);
    }
    return bucket;
  };
  for (const entry of libraryEntries.value) {
    const type = resourceType(entry);
    if (!type) continue;
    const bucket = bucketFor(type);
    if (reviewRequiredKeys.value.has(entry.key)) bucket.review_required_count += 1;
    if (automaticReselectionKeys.value.has(entry.key)) bucket.auto_reselected_count += 1;
    else if (automaticSelectionKeys.value.has(entry.key)) bucket.auto_selected_count += 1;
    else if (selections.value.has(entry.selectionKey)) bucket.manual_selected_count += 1;
    else if (confirmedMissingGenerationKeys.value.has(entry.key) && generationFallback(entry)) {
      bucket.generation_fallback_count += 1;
    }
  }
  return [...buckets.values()];
}
function requestGeneration(entry: ResourceEntry) {
  if (!generationAllowed.value) return;
  generationEntry.value = entry;
}
function confirmMissingGeneration() {
  const entry = generationEntry.value;
  if (!entry) return;
  confirmedMissingGenerationKeys.value = new Set([...confirmedMissingGenerationKeys.value, entry.key]);
  generationEntry.value = null;
  const next = unresolvedLibraryEntries.value[0];
  if (next && generationAllowed.value) generationEntry.value = next;
  else if (!generated.value.length) maybeAutoConfirm();
}
function cancelGenerationPrompt() { generationEntry.value = null; }

function generationFallback(entry: ResourceEntry): ImageGenerationSource | undefined {
  const source = sourceFor(entry);
  if (!source || source.kind !== 'scratch_library') return undefined;
  if (source.asset_type === 'sound') return undefined;
  const subject = source.search_terms.join('、');
  const target = entry.op.op === 'add_backdrop' ? '舞台背景' : '角色造型';
  return {
    kind: 'image_generation',
    reason: 'Scratch 内置素材库没有匹配的' + target + '，自动生成替代素材',
    prompt: '请生成一个适合 Scratch 项目的' + target + '，主题为：' + subject,
    style: 'scratch',
    width: 480,
    height: 360,
    sheet_layout: '1x1'
  };
}
function maybeAutoConfirm() {
  if (unresolvedLibraryEntries.value.length || matching.value || matchError.value || autoCompleting.value || !generationPolicySatisfied.value) return;
  confirm();
}
function confirm() {
  if (!canConfirm.value || autoCompleting.value) return;
  autoCompleting.value = true;
  const confirmed = [...selectedGenerationKeys.value, ...confirmedMissingGenerationKeys.value];
  const fallbacks: Record<string, ImageGenerationSource> = {};
  for (const key of confirmedMissingGenerationKeys.value) {
    const entry = resources.value.find(item => item.key === key);
    const fallback = entry && generationFallback(entry);
    if (fallback) fallbacks[entry.selectionKey] = fallback;
  }
  emit('confirm', confirmed, Object.fromEntries(selections.value), fallbacks, reviewMetrics());
}

function sourceLabel(entry: ResourceEntry) {
  if (entry.op.op === 'select_backdrop') return '直接切换已有背景';
  if (entry.source?.kind === 'image_generation') return '待确认生图';
  if (entry.source?.kind === 'scratch_library' && selections.value.has(entry.selectionKey)) return automaticSelectionKeys.value.has(entry.key) ? '已自动匹配 Scratch 素材' : '已选择 Scratch 素材';
  if (entry.source?.kind === 'scratch_library' && confirmedMissingGenerationKeys.value.has(entry.key)) return '已确认生成替代素材';
  return assetAutoMatchEnabled.value ? '没有高置信度的 Scratch 素材' : '等待选择 Scratch 素材';
}
function detail(entry: ResourceEntry) {
  if (entry.op.op === 'select_backdrop') return entry.op.name;
  if (entry.source?.kind === 'scratch_library') {
    const selected = selections.value.get(entry.selectionKey);
    return selected ? '已匹配：' + selected.name : '搜索：' + entry.source.search_terms.join('、');
  }
  return entry.source?.kind === 'image_generation' ? entry.source.prompt : '资源来源由清单指定';
}
function selectedLabel(entry: ResourceEntry) { return selections.value.get(entry.selectionKey)?.name ?? ''; }
function selectedSoundPreview(entry: ResourceEntry) {
  const selected = selections.value.get(entry.selectionKey);
  return selected && 'assetType' in selected && selected.assetType === 'sound' ? scratchAssetUrl(selected.md5ext) : '';
}
function pickerType(entry: ResourceEntry) {
  return entry.source?.kind === 'scratch_library' ? entry.source.asset_type : 'sprite';
}
function pickerTerms(entry: ResourceEntry) {
  return entry.source?.kind === 'scratch_library' ? entry.source.search_terms : [];
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open && plan" class="resource-review-overlay">
      <section v-if="!autoCompleting" class="resource-review" role="dialog" aria-modal="true" aria-labelledby="resource-review-title">
        <header class="resource-review__header"><div><h2 id="resource-review-title"><Image :size="19" />项目资源</h2><p>{{ plan.summary }}</p></div><button class="icon-button" type="button" title="取消" aria-label="取消" @click="emit('cancel')"><X :size="18" /></button></header>
        <div v-if="matching" class="resource-review__notice resource-review__notice--loading"><LoaderCircle class="spin" :size="17" />正在匹配 Scratch 内置素材，匹配成功的资源会直接使用。</div>
        <div v-else-if="matchError" class="resource-review__notice resource-review__notice--error"><AlertTriangle :size="17" /><span>{{ matchError }}<button class="button button--quiet" type="button" @click="resolveLibraryMatches">重试</button></span></div>
        <div v-else-if="unmatchedEntries.length" class="resource-review__notice"><AlertTriangle :size="17" /><span>{{ generationAllowed ? '以下资源没有高置信度的 Scratch 素材，请选择素材或确认生成替代素材。' : '以下资源没有高置信度的 Scratch 素材，请手动选择其他素材。' }}</span></div>
        <div v-else class="resource-review__notice resource-review__notice--success"><Check :size="17" /><span>Scratch 内置素材已完成高置信度匹配；生图仍需单独确认。</span></div>
        <div class="resource-review__list">
          <article v-for="entry in resources" :key="entry.key" class="resource-review__item">
            <div v-if="entry.source?.kind === 'scratch_library'" class="resource-review__resource-actions">
              <button class="button button--quiet resource-review__choose" type="button" @click="openPicker(entry)">{{ selectedLabel(entry) ? '更换' : '选择素材' }}</button>
              <button v-if="!selectedLabel(entry) && generationAllowed && !confirmedMissingGenerationKeys.has(entry.key)" class="button button--quiet resource-review__generate" type="button" @click="requestGeneration(entry)"><Sparkles :size="14" />生成素材</button>
              <span v-if="confirmedMissingGenerationKeys.has(entry.key)" class="resource-review__confirmed">已确认生成</span>
            </div>
            <label v-if="entry.source?.kind === 'image_generation'" class="resource-review__check"><input type="checkbox" :checked="selectedGenerationKeys.has(entry.key)" :aria-label="'确认生成 ' + entry.name" @click="toggleGeneration(entry.key)" /><span>确认</span></label>
            <div class="resource-review__icon"><Sparkles v-if="entry.source?.kind === 'image_generation' || (entry.source?.kind === 'scratch_library' && !selections.has(entry.selectionKey))" :size="18" /><Check v-else :size="18" /></div>
            <div><strong>{{ entry.name }}</strong><span>{{ sourceLabel(entry) }}</span><p>{{ detail(entry) }}</p><audio v-if="selectedSoundPreview(entry)" class="resource-review__audio" controls preload="metadata" :src="selectedSoundPreview(entry)" :aria-label="'试听 ' + selectedLabel(entry)" /><small v-if="entry.tags.length">#{{ entry.tags.join(' #') }}</small></div>
          </article>
          <p v-if="!resources.length" class="resource-review__empty">此项目不需要新增资源。</p>
        </div>
        <footer class="resource-review__footer"><span v-if="matching">正在匹配素材…</span><span v-else-if="unresolvedLibraryEntries.length">还有 {{ unresolvedLibraryEntries.length }} 项资源需要处理</span><span v-else-if="generated.length">{{ generated.length }} 项生图将自动处理</span><span v-else>资源将自动使用</span><div><button class="button" type="button" @click="emit('cancel')">取消</button><button class="button button--primary" type="button" :disabled="!canConfirm" @click="confirm">确认并准备</button></div></footer>
      </section>
      <section v-else class="resource-review resource-review--processing" role="status" aria-live="polite">
        <div class="resource-review__processing">
          <LoaderCircle class="spin" :size="28" />
          <h2>正在准备项目资源</h2>
          <p>已确认素材，正在继续写入项目。</p>
          <button class="button" type="button" @click="emit('cancel')">取消</button>
        </div>
      </section>
      <ScratchLibraryPickerDialog v-if="pickerEntry" :open="Boolean(pickerEntry)" :type="pickerType(pickerEntry)" :initial-terms="pickerTerms(pickerEntry)" @close="pickerEntry = null" @select="selectAsset" />
      <div v-if="generationEntry" class="resource-generation-overlay" @click.self="cancelGenerationPrompt">
        <section class="resource-generation-prompt" role="dialog" aria-modal="true" aria-labelledby="resource-generation-title">
          <header class="resource-generation-prompt__header"><div><h2 id="resource-generation-title"><Sparkles :size="18" />生成替代素材？</h2><p>Scratch 内置素材库没有找到匹配项。</p></div><button class="icon-button" type="button" title="取消" aria-label="取消" @click="cancelGenerationPrompt"><X :size="18" /></button></header>
          <div class="resource-generation-prompt__body"><strong>{{ generationEntry.name }}</strong><p>{{ generationEntry.source?.kind === 'scratch_library' ? generationEntry.source.search_terms.join('、') : '' }}</p><small>确认后才会调用生图服务；取消可以手动选择其他 Scratch 素材。</small></div>
          <footer class="resource-generation-prompt__footer"><button class="button" type="button" @click="cancelGenerationPrompt">取消</button><button class="button button--primary" type="button" @click="confirmMissingGeneration">确认生成</button></footer>
        </section>
      </div>
    </div>
  </Teleport>
</template>
