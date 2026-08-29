<script setup lang="ts">
import {computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch} from 'vue';
import {
  Blocks,
  Bot,
  BrainCircuit,
  Check,
  CircleStop,
  History,
  ListChecks,
  LoaderCircle,
  MessageSquare,
  Play,
  Plus,
  Send,
  Sparkles,
  Trash2,
  Undo2,
  ChevronDown,
  Pencil,
  LayoutGrid,
  RefreshCw
} from 'lucide-vue-next';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import {
  streamAiBlocksGeneration,
  summarizeAiBlocksRequirements,
  type AiBlocksMessage,
  type AiBlocksMode,
  type AiBlocksModelTier,
  type AiBlocksRequirements,
  type AiBlocksThinkingSettings,
  type AiBlocksThinkingStrength
} from './ai-blocks-api';
import {assertSnapshotIsCurrent, readAiTargetSnapshot, type AiTargetSnapshot} from './target-context';
import {
  applyAiProgram,
  undoAiGeneration,
  type AiGenerationResult
} from './generation-runner';
import {requestsGenerationNow} from './generation-intent';
import ResourcePlanReview from './ResourcePlanReview.vue';
import {assertProjectManifest, type ProjectManifest, type ProjectProgramTarget, type TargetRef, type ProjectSharedState, type AiBlocksScope, type AssetPolicy} from './project-plan';
import {prepareProjectResources} from './project-resource-preparer';
import type {PreparedResource} from './project-resource-runner';
import type {CanvasInput} from '../canvas/model/canvas-types';
import {applyProjectManifest, undoProjectManifest, type ProjectApplyResult} from './project-resource-runner';
import {createProjectResourceExecutor} from './project-resource-executor';
import {BLOCK_ONLY_MESSAGE} from './block-only-policy';
import {createId} from '../shared/create-id';
import {searchScratchLibrary, type ScratchLibraryItem, type ScratchSpriteItem} from '../ai/scratch-library';

const props = defineProps<{bridge: ScratchGuiBridge | null}>();
const emit = defineEmits<{
  lockChange: [locked: boolean, message: string];
  busyChange: [busy: boolean];
  openCanvas: [opId: string, input: CanvasInput];
}>();

type ViewState = 'chat' | 'summarizing' | 'review' | 'planning' | 'injecting';

interface SummarizeOptions {
  generateWhenReady?: boolean;
  proceedWithDefaults?: boolean;
}

interface StoredConversation {
  id: string;
  title: string;
  targetId: string;
  targetName: string;
  isStage: boolean;
  mode: AiBlocksMode;
  scope?: AiBlocksScope;
  assetPolicy?: AssetPolicy;
  messages: AiBlocksMessage[];
  updatedAt: number;
}

const THINKING_STORAGE_KEY = 'ai-blocks-thinking-settings';
const CONVERSATIONS_STORAGE_KEY = 'ai-blocks-conversations';
const MAX_CONVERSATIONS = 30;

function loadConversations(): StoredConversation[] {
  try {
    const value = JSON.parse(localStorage.getItem(CONVERSATIONS_STORAGE_KEY) ?? '[]');
    if (!Array.isArray(value)) return [];
    return value.filter(item => item && typeof item.id === 'string' && Array.isArray(item.messages));
  } catch {
    return [];
  }
}

function loadThinkingSettings(): AiBlocksThinkingSettings {
  try {
    const value = JSON.parse(localStorage.getItem(THINKING_STORAGE_KEY) ?? 'null');
    const strength = value?.strength as AiBlocksThinkingStrength;
    if (typeof value?.enabled === 'boolean' && ['low', 'medium', 'high', 'xhigh', 'max'].includes(strength)) {
      return {enabled: value.enabled, strength};
    }
  } catch {
    // Ignore unavailable or malformed browser storage.
  }
  return {enabled: true, strength: 'medium'};
}

const initialThinking = loadThinkingSettings();
const state = ref<ViewState>('chat');
const mode = ref<AiBlocksMode>('append');
const projectAssetsEnabled = import.meta.env.VITE_AI_PROJECT_ASSETS_ENABLED !== 'false';
const scope = ref<AiBlocksScope>('single_sprite');
const assetPolicy = ref<AssetPolicy>('builtin_first');
const modelTier = ref<AiBlocksModelTier>('normal');
const thinkingEnabled = ref(initialThinking.enabled);
const thinkingStrength = ref<AiBlocksThinkingStrength>(initialThinking.strength);
const reasoning = ref('');
const snapshot = ref<AiTargetSnapshot | null>(null);
const messages = ref<AiBlocksMessage[]>([]);
const conversations = ref<StoredConversation[]>(loadConversations());
const historyOpen = ref(false);
const modesExpanded = ref(true);
const draft = ref('');
const requirements = ref<AiBlocksRequirements | null>(null);
const planSteps = ref<string[]>([]);
const phaseMessage = ref('');
const progress = ref({current: 0, total: 0, label: ''});
const result = ref<AiGenerationResult | null>(null);
const projectPlan = ref<ProjectManifest | null>(null);
const projectApplyResult = ref<ProjectApplyResult | null>(null);
const preparedResources = shallowRef(new Map<string, PreparedResource>());
const preparedPlan = ref<ProjectManifest | null>(null);
const errorMessage = ref('');
const noticeMessage = ref('');
const sessionId = ref(createId());
const chatLog = ref<HTMLElement | null>(null);
const reasoningLog = ref<HTMLElement | null>(null);
const requestBusy = ref(false);
const lockMessage = ref('AI 生成中，请稍等');
let controller: AbortController | null = null;
let unsubscribeTargets: (() => void) | null = null;

const isBusy = computed(() => requestBusy.value || ['summarizing', 'planning', 'injecting'].includes(state.value));
const canSend = computed(() => draft.value.trim().length > 0 && !isBusy.value && Boolean(snapshot.value));
const canSummarize = computed(() => messages.value.some(message => message.role === 'user') && !isBusy.value);
const scopeSummary = computed(() => scope.value === 'project' ? '项目' : '角色');
const modeSummary = computed(() => ({modify: '修改', append: '新增', rewrite: '重写'}[mode.value]));
const progressPercent = computed(() => progress.value.total
  ? Math.round((progress.value.current / progress.value.total) * 100)
  : 0);
const processTitle = computed(() => {
  if (state.value === 'summarizing') return '正在理解你的需求';
  if (state.value === 'injecting') return '正在编写 Scratch 积木';
  return '正在推理并规划代码';
});
const processMessage = computed(() => {
  if (state.value === 'summarizing') return '正在整理目标、触发方式和动作细节';
  if (state.value === 'injecting') return progress.value.label || '正在把程序写入当前角色';
  return phaseMessage.value || '正在规划积木结构';
});
const currentConversationTitle = computed(() => {
  const firstUserMessage = messages.value.find(message => message.role === 'user')?.content.trim();
  return firstUserMessage ? firstUserMessage.slice(0, 28) : (snapshot.value?.targetName || '新的积木对话');
});

function formatConversationTime(timestamp: number) {
  const diff = Date.now() - timestamp;
  if (diff < 60_000) return '刚刚';
  if (diff < 3_600_000) return Math.floor(diff / 60_000) + ' 分钟前';
  if (diff < 86_400_000) return Math.floor(diff / 3_600_000) + ' 小时前';
  return new Date(timestamp).toLocaleDateString('zh-CN', {month: 'numeric', day: 'numeric'});
}

function persistConversations() {
  try {
    localStorage.setItem(CONVERSATIONS_STORAGE_KEY, JSON.stringify(conversations.value));
  } catch {
    // Conversation history remains available for this page when storage is unavailable.
  }
}

function saveCurrentConversation() {
  if (!snapshot.value || !messages.value.some(message => message.role === 'user')) return;
  const record: StoredConversation = {
    id: sessionId.value,
    title: currentConversationTitle.value,
    targetId: snapshot.value.targetId,
    targetName: snapshot.value.targetName,
    isStage: snapshot.value.isStage,
    mode: mode.value,
    scope: scope.value,
    assetPolicy: assetPolicy.value,
    messages: messages.value.map(message => ({...message})),
    updatedAt: Date.now()
  };
  const existing = conversations.value.filter(conversation => conversation.id !== record.id);
  conversations.value = [record, ...existing].slice(0, MAX_CONVERSATIONS);
  persistConversations();
}

function startNewConversation() {
  if (!snapshot.value || isBusy.value) return;
  saveCurrentConversation();
  resetConversation(snapshot.value);
  historyOpen.value = false;
}

function restoreConversation(conversation: StoredConversation) {
  if (!snapshot.value || isBusy.value) return;
  sessionId.value = conversation.id;
  mode.value = conversation.mode;
  scope.value = projectAssetsEnabled && conversation.scope === 'project' ? 'project' : 'single_sprite';
  assetPolicy.value = ['builtin_first', 'existing_only', 'allow_generation'].includes(conversation.assetPolicy ?? '') ? conversation.assetPolicy as AssetPolicy : 'builtin_first';
  messages.value = conversation.messages.map(message => ({...message}));
  requirements.value = null;
  planSteps.value = [];
  result.value = null;
  reasoning.value = '';
  errorMessage.value = '';
  noticeMessage.value = '';
  state.value = 'chat';
  historyOpen.value = false;
  void scrollToLatest();
}

function clearConversationHistory() {
  if (!window.confirm('确定清空全部 AI 对话记录吗？此操作无法撤销。')) return;
  conversations.value = [];
  persistConversations();
  if (snapshot.value) {
    resetConversation(snapshot.value);
    historyOpen.value = false;
  }
}

function thinkingSettings(): AiBlocksThinkingSettings {
  return {enabled: thinkingEnabled.value, strength: thinkingStrength.value};
}

function localGreeting(name: string, isStage: boolean): AiBlocksMessage {
  return {
    role: 'assistant',
    content: isStage
      ? '准备为舞台“' + name + '”编写积木。想让舞台发生什么？'
      : '准备为角色“' + name + '”编写积木。想让角色做什么？'
  };
}

function setLocked(locked: boolean, message = 'AI 生成中，请稍等') {
  requestBusy.value = locked;
  lockMessage.value = message;
  emit('lockChange', locked, message);
  emit('busyChange', locked);
}

function resetConversation(nextSnapshot: AiTargetSnapshot) {
  snapshot.value = nextSnapshot;
  mode.value = nextSnapshot.hasScripts ? 'modify' : 'append';
  sessionId.value = createId();
  messages.value = [localGreeting(nextSnapshot.targetName, nextSnapshot.isStage)];
  requirements.value = null;
  planSteps.value = [];
  result.value = null;
  reasoning.value = '';
  errorMessage.value = '';
  noticeMessage.value = '';
  state.value = 'chat';
}

function refreshTarget(forceReset = false) {
  if (!props.bridge?.isReady || isBusy.value) return;
  try {
    const next = readAiTargetSnapshot(props.bridge, mode.value);
    if (forceReset || snapshot.value?.targetId !== next.targetId) {
      if (!forceReset) saveCurrentConversation();
      resetConversation(next);
    } else snapshot.value = next;
  } catch (error: unknown) {
    errorMessage.value = error instanceof Error ? error.message : '无法读取当前 Scratch 目标';
  }
}

function attachBridge() {
  unsubscribeTargets?.();
  unsubscribeTargets = null;
  if (!props.bridge) return;
  unsubscribeTargets = props.bridge.subscribeTargets(() => refreshTarget());
  refreshTarget(true);
}

async function scrollToLatest() {
  await nextTick();
  chatLog.value?.scrollTo({top: chatLog.value.scrollHeight, behavior: 'smooth'});
}

async function followGenerationProcess() {
  await nextTick();
  if (!isBusy.value) return;
  if (chatLog.value) chatLog.value.scrollTop = chatLog.value.scrollHeight;
  if (reasoningLog.value) reasoningLog.value.scrollTop = reasoningLog.value.scrollHeight;
}

async function sendMessage() {
  const text = draft.value.trim();
  if (!text || !snapshot.value || !props.bridge || isBusy.value) return;
  draft.value = '';
  errorMessage.value = '';
  noticeMessage.value = '';
  messages.value.push({role: 'user', content: text});
  await scrollToLatest();
  await summarize({
    generateWhenReady: true,
    proceedWithDefaults: requestsGenerationNow(text)
  });
}

async function summarize(options: SummarizeOptions = {}) {
  const hasUserMessage = messages.value.some(message => message.role === 'user');
  if (!props.bridge || !snapshot.value || !hasUserMessage || isBusy.value) return;
  state.value = 'summarizing';
  errorMessage.value = '';
  controller?.abort();
  controller = new AbortController();
  requestBusy.value = true;
  emit('busyChange', true);
  let shouldGenerate = false;
  try {
    const current = readAiTargetSnapshot(props.bridge, mode.value);
    snapshot.value = current;
    const summary = await summarizeAiBlocksRequirements({
      model_tier: modelTier.value,
      session_id: sessionId.value,
      request_id: createId(),
      messages: messages.value,
      target: current.context,
      thinking: thinkingSettings(),
      proceed_with_defaults: true
    }, controller.signal);
    requirements.value = {...summary, ready: true, missing_questions: []};
    state.value = 'review';
    shouldGenerate = Boolean(options.generateWhenReady);
  } catch (error: unknown) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) {
      errorMessage.value = error instanceof Error ? error.message : '需求整理失败';
    }
    state.value = 'chat';
  } finally {
    controller = null;
    requestBusy.value = false;
    emit('busyChange', false);
  }

  if (shouldGenerate && state.value === 'review' && requirements.value?.ready) {
    await generate();
  }
}

async function generate() {
  if (!props.bridge || !snapshot.value || !requirements.value?.ready || isBusy.value) return;
  const bridge = props.bridge;
  const current = readAiTargetSnapshot(bridge, mode.value);
  snapshot.value = current;
  state.value = 'planning';
  planSteps.value = [];
  phaseMessage.value = '正在规划积木结构';
  progress.value = {current: 0, total: 0, label: ''};
  result.value = null;
  reasoning.value = '';
  errorMessage.value = '';
  noticeMessage.value = '';
  controller = new AbortController();
  bridge.activateCodeTab();
  bridge.stop();
  setLocked(true, 'AI 正在规划代码，请稍等');

  try {
    const output = await streamAiBlocksGeneration({
      model_tier: modelTier.value,
      session_id: sessionId.value,
      request_id: createId(),
      messages: messages.value,
      target: current.context,
      thinking: thinkingSettings(),
      requirements: requirements.value,
      output_contract: scope.value === 'project' ? 'project_manifest' : 'program_ir',
      scope: scope.value,
      asset_policy: assetPolicy.value
    }, {
      onPhase: phase => {
        phaseMessage.value = phase.message || '正在生成积木代码';
      },
      onPlan: steps => {
        planSteps.value = steps;
      },
      onReasoning: text => {
        reasoning.value = (reasoning.value + text).slice(-24_000);
      }
    }, controller.signal);

    assertSnapshotIsCurrent(bridge, current);
    if (output.kind === 'manifest') {
      assertProjectManifest(output.manifest, current.context.sprite_names);
      const manifest = output.manifest;
      const needsGeneration = manifest.resource_ops.some(op =>
        ('source' in op && op.source?.kind === 'image_generation') ||
        (op.op === 'add_sprite' && op.costumes.some(costume => costume.source.kind === 'image_generation'))
      );
      if (needsGeneration) {
        projectPlan.value = manifest;
        state.value = 'chat';
        noticeMessage.value = '项目清单已生成，请确认需要生成的素材。';
        return;
      }
      const selections = await autoSelectBuiltInResources(manifest);
      projectPlan.value = manifest;
      await confirmProjectPlan([], selections);
      await commitPreparedPlan();
      return;
    }
    state.value = 'injecting';
    setLocked(true, 'AI 生成中，请稍等');
    if (output.kind !== 'program') throw new Error('项目清单未返回可执行的积木程序');
    const generated = await applyAiProgram({
      bridge,
      snapshot: current,
      mode: mode.value,
      program: output.program,
      signal: controller.signal,
      onProgress: value => {
        progress.value = value;
      }
    });
    result.value = generated;
    snapshot.value = readAiTargetSnapshot(bridge, mode.value);
    const replacementSummary = generated.removedScripts
      ? `，替换 ${generated.removedScripts} 个脚本`
      : '';
    messages.value.push({
      role: 'assistant',
      content: `积木生成完成。已为${generated.targetName}新增 ${generated.addedBlocks} 块积木、${generated.addedScripts} 个脚本${replacementSummary}。`
    });
    state.value = 'chat';
  } catch (error: unknown) {
    result.value = null;
    projectApplyResult.value = null;
    const message = error instanceof DOMException && error.name === 'AbortError'
      ? '已取消生成，项目已恢复到生成前状态。'
      : `生成失败：${error instanceof Error ? error.message : 'AI 积木生成失败'}`;
    messages.value.push({role: 'assistant', content: message});
    state.value = 'chat';
  } finally {
    controller = null;
    setLocked(false);
    if (state.value === 'chat') await scrollToLatest();
  }
}

function cancel() {
  controller?.abort();
}

async function undoGeneration() {
  if (!props.bridge || (!result.value && !projectApplyResult.value) || isBusy.value) return;
  setLocked(true, '正在撤销 AI 生成');
  errorMessage.value = '';
  let succeeded = false;
  try {
    if (projectApplyResult.value) await undoProjectManifest(props.bridge, projectApplyResult.value);
    else if (result.value) await undoAiGeneration(props.bridge, result.value);
    succeeded = true;
  } catch (error: unknown) {
    errorMessage.value = error instanceof Error ? error.message : '撤销失败';
  } finally {
    setLocked(false);
  }
  if (succeeded) {
    refreshTarget(true);
    noticeMessage.value = '已撤销本次 AI 生成。';
  }
}


function targetIdForManifestRef(target: TargetRef, applied: ProjectApplyResult, bridge: ScratchGuiBridge): string {
  if (target.kind === 'current_target') return applied.original_target_id;
  if (target.kind === 'created_sprite') {
    const created = applied.addedTargets.find(item => item.op_id === target.op_id);
    if (!created) throw new Error('找不到清单创建的角色：' + target.op_id);
    return created.target_id;
  }
  const targets = bridge.currentVm?.runtime.targets ?? [];
  if (target.kind === 'stage') {
    const stage = targets.find(item => item.isStage);
    if (!stage) throw new Error('Scratch 舞台尚未就绪');
    return stage.id;
  }
  const wanted = target.name.trim().toLocaleLowerCase();
  const existing = targets.find(item => !item.isStage && String(item.getName?.() ?? item.sprite?.name ?? '').trim().toLocaleLowerCase() === wanted);
  if (!existing) throw new Error('找不到已有角色：' + target.name);
  return existing.id;
}

function applyProjectSharedState(bridge: ScratchGuiBridge, shared: ProjectSharedState | undefined) {
  if (!shared) return;
  const stage = bridge.currentVm?.runtime.getTargetForStage?.() ?? bridge.currentVm?.runtime.targets.find(item => item.isStage);
  const createVariable = stage?.createVariable;
  if (!createVariable) throw new Error('Scratch 舞台变量接口尚未就绪');
  const values = stage.variables ?? {};
  const ensure = (name: string, type: '' | 'list' | 'broadcast_msg', value: unknown) => {
    const existing = Object.values(values).find(item => item.name === name && item.type === type);
    if (existing) {
      if (value !== undefined) existing.value = value as typeof existing.value;
      return;
    }
    const id = createId();
    createVariable.call(stage, id, name, type, false);
    const created = stage.variables?.[id];
    if (created && value !== undefined) created.value = value as typeof created.value;
  };
  shared.variables.forEach(item => ensure(item.name, '', item.value));
  shared.lists.forEach(item => ensure(item.name, 'list', item.value ?? []));
  shared.broadcasts.forEach(name => ensure(name, 'broadcast_msg', ''));
  bridge.currentVm?.runtime.emitProjectChanged?.();
}

async function applyProjectPrograms(manifest: ProjectManifest, applied: ProjectApplyResult, bridge: ScratchGuiBridge) {
  applyProjectSharedState(bridge, manifest.shared_state);
  let expectedRevision = bridge.projectRevision;
  for (const target of manifest.program_targets) {
    if (bridge.projectRevision !== expectedRevision) throw new Error('项目在生成期间发生了外部修改，已停止本次项目生成');
    const targetId = targetIdForManifestRef(target.target, applied, bridge);
    bridge.currentVm?.setEditingTarget?.(targetId);
    const targetSnapshot = readAiTargetSnapshot(bridge, target.mode);
    const output = await streamAiBlocksGeneration({
      model_tier: modelTier.value,
      session_id: sessionId.value,
      request_id: createId(),
      messages: messages.value,
      target: targetSnapshot.context,
      thinking: thinkingSettings(),
      requirements: requirements.value!,
      plan: planSteps.value.length ? {steps: [...planSteps.value]} : undefined,
      output_contract: 'project_program',
      scope: 'project',
      asset_policy: assetPolicy.value,
      project_manifest: manifest,
      program_target: target
    }, {
      onPhase: phase => { phaseMessage.value = phase.message || '正在生成目标积木'; },
      onPlan: steps => { planSteps.value = steps; },
      onReasoning: text => { reasoning.value = (reasoning.value + text).slice(-24_000); }
    }, controller?.signal);
    if (output.kind !== 'program') throw new Error('项目目标未返回可执行的积木程序');
    const generated = await applyAiProgram({
      bridge,
      snapshot: targetSnapshot,
      mode: target.mode,
      program: output.program,
      signal: controller?.signal,
      onProgress: value => { progress.value = value; }
    });
    applied.appliedPrograms.push({target_id: generated.targetId, added_blocks: generated.addedBlocks, added_scripts: generated.addedScripts, removed_scripts: generated.removedScripts});
    expectedRevision = bridge.projectRevision;
  }
}

async function autoSelectBuiltInResources(plan: ProjectManifest) {
  const selections: Record<string, ScratchSpriteItem | ScratchLibraryItem> = {};
  const skipped = new Set<string>();
  for (const op of plan.resource_ops) {
    if (op.op === 'add_builtin_sprite') {
      const matches = await searchScratchLibrary('sprite', op.source.search_terms, 1);
      if (!matches[0]) throw new Error('找不到内置角色素材：' + op.name);
      selections[op.op_id] = matches[0];
    } else if (op.op === 'add_costume' && op.source.kind === 'scratch_library') {
      const matches = await searchScratchLibrary('costume', op.source.search_terms, 1);
      if (!matches[0]) { skipped.add(op.op_id); continue; }
      selections[op.op_id] = matches[0];
    } else if (op.op === 'add_backdrop' && op.source.kind === 'scratch_library') {
      const matches = await searchScratchLibrary('backdrop', op.source.search_terms, 1);
      if (!matches[0]) throw new Error('找不到内置背景素材：' + op.name);
      selections[op.op_id] = matches[0];
    } else if (op.op === 'add_sprite') {
      if (!op.costumes.length && op.source.kind === 'scratch_library') {
        const matches = await searchScratchLibrary('costume', op.source.search_terms, 1);
        if (!matches[0]) throw new Error('找不到内置造型素材：' + op.name);
        selections[op.op_id] = matches[0];
      }
      for (const costume of op.costumes) {
        if (costume.source.kind !== 'scratch_library') continue;
        const matches = await searchScratchLibrary('costume', costume.source.search_terms, 1);
        if (!matches[0]) continue;
        selections[op.op_id + ':' + costume.name] = matches[0];
      }
    }
  }
  plan.resource_ops = plan.resource_ops.filter(op => !skipped.has(op.op_id));
  return selections;
}

async function confirmProjectPlan(confirmedGenerationKeys: string[], selectionRecord: Record<string, ScratchSpriteItem | ScratchLibraryItem>) {
  if (!projectPlan.value) return;
  state.value = 'injecting';
  setLocked(true, '正在准备并提交项目资源');
  errorMessage.value = '';
  try {
    const plan = projectPlan.value;
    if (!plan || !snapshot.value || !props.bridge) return;
    const prepared = await prepareProjectResources({plan, assetPolicy: assetPolicy.value, selections: new Map(Object.entries(selectionRecord)), confirmGeneration: confirmation => confirmedGenerationKeys.includes(confirmation.resource_key), onProgress: (current, total, label) => {progress.value = {current, total, label};}});
    preparedResources.value = prepared.prepared;
    preparedPlan.value = plan;
    projectPlan.value = null;
    state.value = 'chat';
    noticeMessage.value = '资源已准备 ' + prepared.prepared.size + ' 项，可选择画布处理后再提交。';
  } catch (error: unknown) {
    state.value = 'chat';
    errorMessage.value = error instanceof Error ? error.message : '资源准备失败';
  } finally {
    setLocked(false);
  }
}

async function commitPreparedPlan() {
  const manifest = preparedPlan.value;
  if (!manifest || !snapshot.value || !props.bridge || isBusy.value) return;
  const bridge = props.bridge;
  setLocked(true, '正在提交项目资源');
  state.value = 'injecting';
  errorMessage.value = '';
  controller = new AbortController();
  let applied: ProjectApplyResult | null = null;
  try {
    applied = await applyProjectManifest({
      bridge,
      manifest,
      snapshot: snapshot.value,
      prepared: preparedResources.value,
      executor: createProjectResourceExecutor({bridge, currentTargetId: snapshot.value.targetId}),
      signal: controller.signal,
      onProgress: (current, total, label) => { progress.value = {current, total, label}; }
    });
    await applyProjectPrograms(manifest, applied, bridge);
    applied.revision_at_complete = bridge.projectRevision;
    bridge.currentVm?.setEditingTarget?.(applied.original_target_id);
    bridge.refreshWorkspace();
    projectApplyResult.value = applied;
    preparedPlan.value = null;
    state.value = 'chat';
    noticeMessage.value = '项目提交完成：新增 ' + applied.addedTargets.length + ' 个角色、' + applied.addedCostumes.length + ' 个造型、' + applied.addedBackdrops.length + ' 个背景，写入 ' + applied.appliedPrograms.length + ' 段程序。';
  } catch (error: unknown) {
    if (applied) {
      try {
        await bridge.loadProject(applied.backup);
        bridge.currentVm?.setEditingTarget?.(applied.original_target_id);
      } catch (restoreError) {
        errorMessage.value = '项目提交失败且无法恢复备份：' + (restoreError instanceof Error ? restoreError.message : '未知错误');
      }
    }
    state.value = 'chat';
    if (!errorMessage.value) errorMessage.value = error instanceof DOMException && error.name === 'AbortError' ? '已取消项目生成，项目已恢复到提交前状态。' : error instanceof Error ? error.message : '项目提交失败';
  } finally {
    controller = null;
    setLocked(false);
  }
}

function openPreparedCanvas(opId: string) {
  const prepared = preparedResources.value.get(opId);
  if (!prepared || !('blob' in (prepared.value as object))) throw new Error('该资源当前不可在画布中处理');
  const blob = (prepared.value as {blob: Blob}).blob;
  emit('openCanvas', opId, {kind: 'blob', blob, fileName: opId + '.png', source: 'upload'});
}

function acceptCanvasPrepared(opId: string, payload: {kind: 'sprite' | 'backdrop'; costumes?: unknown[]; backdrop?: unknown}) {
  const current = preparedResources.value.get(opId);
  if (!current) throw new Error('找不到待回填的资源操作：' + opId);
  const value = current.kind === 'costume' ? (payload.kind === 'sprite' ? payload.costumes?.[0] : payload.backdrop) : payload.kind === 'sprite' ? {name: current.op_id, costumes: payload.costumes} : payload.backdrop;
  if (!value) throw new Error('画布没有可提交的导出结果');
  const next = new Map(preparedResources.value);
  next.set(opId, {...current, value});
  preparedResources.value = next;
}

defineExpose({acceptCanvasPrepared});

function runProject() {
  props.bridge?.run();
}

watch(() => props.bridge, attachBridge);
watch([state, reasoning, phaseMessage, planSteps, progress], followGenerationProcess);
watch([mode, scope, assetPolicy], () => {
  if (!props.bridge || !snapshot.value || isBusy.value) return;
  planSteps.value = [];
  snapshot.value = readAiTargetSnapshot(props.bridge, mode.value);
});
watch([thinkingEnabled, thinkingStrength], () => {
  try {
    localStorage.setItem(THINKING_STORAGE_KEY, JSON.stringify(thinkingSettings()));
  } catch {
    // The current selection still applies to this page when storage is unavailable.
  }
});
watch([messages, mode], saveCurrentConversation, {deep: true});

onMounted(attachBridge);
onBeforeUnmount(() => {
  controller?.abort();
  unsubscribeTargets?.();
  setLocked(false);
  emit('busyChange', false);
});
</script>

<template>
  <section class="ai-blocks-panel" aria-label="AI 写积木">
    <header class="ai-blocks-panel__header">
      <div>
        <h2><Blocks :size="18" aria-hidden="true" />AI 写积木</h2>
      </div>
      <div class="ai-blocks-panel__header-actions">
        <button class="icon-button" type="button" :class="{'is-active': historyOpen}" :disabled="isBusy" title="查看历史对话" aria-label="查看历史对话" :aria-expanded="historyOpen" @click="historyOpen = !historyOpen">
          <History :size="16" aria-hidden="true" />
        </button>
        <button class="icon-button" type="button" :disabled="isBusy || !snapshot" title="新建对话" aria-label="新建对话" @click="startNewConversation">
          <Plus :size="17" aria-hidden="true" />
        </button>
        <span v-if="snapshot" class="ai-blocks-count">{{ snapshot.blockCount }} 块</span>
      </div>
    </header>

    <section v-if="historyOpen" class="ai-blocks-history" aria-label="历史对话">
      <div class="ai-blocks-history__heading">
        <div><strong>历史对话</strong><span>{{ conversations.length }} 条记录</span></div>
        <button v-if="conversations.length" class="button button--quiet ai-blocks-history__clear" type="button" :disabled="isBusy" @click="clearConversationHistory"><Trash2 :size="14" />清空</button>
      </div>
      <div v-if="!conversations.length" class="ai-blocks-history__empty">
        <MessageSquare :size="18" aria-hidden="true" />
        <span>发送第一条需求后，对话会自动保存在这里</span>
      </div>
      <div v-else class="ai-blocks-history__list">
        <button v-for="conversation in conversations" :key="conversation.id" class="ai-blocks-history__item" :class="{'is-current': conversation.id === sessionId}" type="button" :disabled="isBusy" @click="restoreConversation(conversation)">
          <span class="ai-blocks-history__item-icon"><MessageSquare :size="14" aria-hidden="true" /></span>
          <span class="ai-blocks-history__item-copy"><strong>{{ conversation.title }}</strong><small>{{ conversation.isStage ? '舞台' : '角色' }} · {{ conversation.targetName }} · {{ formatConversationTime(conversation.updatedAt) }}</small></span>
        </button>
      </div>
    </section>

    <fieldset v-if="['chat', 'review', 'summarizing', 'planning', 'injecting'].includes(state)" class="ai-blocks-modes" :class="{'is-collapsed': isBusy || !modesExpanded}">
      <legend>
        <button class="ai-blocks-modes__toggle" type="button" :aria-expanded="!isBusy && modesExpanded" :disabled="isBusy" aria-controls="ai-blocks-mode-options" @click="modesExpanded = !modesExpanded">
          <span>模式</span>
          <span class="ai-blocks-modes__summary"><span class="ai-blocks-modes__tag">{{ scopeSummary }}</span><span class="ai-blocks-modes__tag">{{ modeSummary }}</span></span>
          <ChevronDown class="ai-blocks-modes__chevron" :size="18" aria-hidden="true" />
        </button>
      </legend>
      <div id="ai-blocks-mode-options" class="ai-blocks-mode-options">
        <label class="ai-blocks-mode-card ai-blocks-scope-card" :class="{'is-active': scope === 'single_sprite'}"><input v-model="scope" type="radio" value="single_sprite" /><span><LayoutGrid :size="20" aria-hidden="true" /><strong>当前角色</strong><small>只编写当前角色的积木</small></span></label>
        <label v-if="projectAssetsEnabled" class="ai-blocks-mode-card ai-blocks-scope-card" :class="{'is-active': scope === 'project'}"><input v-model="scope" type="radio" value="project" /><span><Blocks :size="20" aria-hidden="true" /><strong>整个项目</strong><small>同时处理资源和多个目标</small></span></label>
        <label v-if="scope === 'project'" class="ai-blocks-policy"><span>素材策略</span><select v-model="assetPolicy" aria-label="素材策略"><option value="builtin_first">内置素材优先</option><option value="existing_only">仅已有素材</option><option value="allow_generation">允许生图</option></select></label>
        <label class="ai-blocks-mode-card" :class="{'is-active': mode === 'modify'}"><input v-model="mode" type="radio" value="modify" /><span><Pencil :size="20" aria-hidden="true" /><strong>修改当前代码</strong><small>继续编辑原有积木</small></span></label>
        <label class="ai-blocks-mode-card" :class="{'is-active': mode === 'append'}"><input v-model="mode" type="radio" value="append" /><span><LayoutGrid :size="20" aria-hidden="true" /><strong>旁路新增</strong><small>保留原代码并添加功能</small></span></label>
        <label class="ai-blocks-mode-card" :class="{'is-active': mode === 'rewrite'}"><input v-model="mode" type="radio" value="rewrite" /><span><RefreshCw :size="20" aria-hidden="true" /><strong>清空重写</strong><small>重新生成全部积木</small></span></label>
      </div>
    </fieldset>

    <div v-if="errorMessage" class="ai-blocks-alert ai-blocks-alert--error" role="alert">{{ errorMessage }}</div>
    <div v-if="noticeMessage" class="ai-blocks-alert" role="status">{{ noticeMessage }}</div>
    <div v-if="preparedPlan" class="ai-blocks-prepared-actions" role="group" aria-label="项目资源操作">
      <template v-for="op in preparedPlan.resource_ops" :key="op.op_id">
        <button v-if="'source' in op && op.source?.kind === 'image_generation'" class="button button--quiet" type="button" @click="openPreparedCanvas(op.op_id)">转到画布：{{ op.name }}</button>
      </template>
      <button class="button button--primary" type="button" @click="commitPreparedPlan">提交项目</button>
    </div>

    <template v-if="['chat', 'summarizing', 'planning', 'injecting'].includes(state)">
      <div ref="chatLog" class="ai-blocks-chat" role="log" aria-live="polite">
        <article v-for="(message, index) in messages" :key="index" class="ai-blocks-message" :class="'is-' + message.role">
          <span class="ai-blocks-message__avatar" aria-hidden="true">
            <Bot v-if="message.role === 'assistant'" :size="14" />
            <span v-else>你</span>
          </span>
          <p>{{ message.content || '正在思考…' }}</p>
        </article>
        <article v-if="isBusy" class="ai-blocks-message is-assistant is-working" aria-live="polite">
          <span class="ai-blocks-message__avatar" aria-hidden="true"><Bot :size="14" /></span>
          <div class="ai-blocks-message__process">
            <header>
              <LoaderCircle class="ai-blocks-inline-spinner" :size="15" aria-hidden="true" />
              <strong>{{ processTitle }}</strong>
              <span class="ai-blocks-working-dots" aria-hidden="true"><span /><span /><span /></span>
            </header>
            <p class="ai-blocks-message__phase">{{ processMessage }}</p>
            <div v-if="reasoning" ref="reasoningLog" class="ai-blocks-message__reasoning">
              <span><BrainCircuit :size="14" aria-hidden="true" />推理过程</span>
              <p>{{ reasoning }}</p>
            </div>
            <ol v-if="planSteps.length" class="ai-blocks-message__plan">
              <li v-for="step in planSteps" :key="step"><Check :size="13" aria-hidden="true" />{{ step }}</li>
            </ol>
            <div v-if="state === 'planning' || state === 'injecting'" class="ai-blocks-message__progress">
              <div v-if="state === 'injecting' && progress.total" class="ai-blocks-progress__track" aria-hidden="true">
                <span :style="{width: progressPercent + '%'}" />
              </div>
              <div v-else class="ai-blocks-progress__track is-indeterminate" aria-hidden="true"><span /></div>
              <span v-if="state === 'injecting' && progress.total">{{ progress.current }} / {{ progress.total }} 块</span>
              <button class="button button--quiet" type="button" @click="cancel"><CircleStop :size="15" />取消生成</button>
            </div>
          </div>
        </article>
        <div v-if="result || projectApplyResult" class="ai-blocks-generation-actions" role="group" aria-label="本次生成操作">
          <button class="button button--primary" type="button" @click="runProject"><Play :size="15" />运行查看</button>
          <button class="button button--quiet" type="button" @click="undoGeneration"><Undo2 :size="15" />撤销生成</button>
        </div>
      </div>
      <form class="ai-blocks-composer" @submit.prevent="sendMessage">
        <textarea v-model="draft" rows="3" maxlength="2000" :disabled="isBusy" aria-label="描述要实现的积木功能" placeholder="描述希望实现的效果" @keydown.enter.exact.prevent="sendMessage" />
        <div class="ai-blocks-composer__actions">
          <button class="button button--quiet" type="button" :disabled="!canSummarize" @click="summarize()">
            <LoaderCircle v-if="state === 'summarizing'" class="spin" :size="15" aria-hidden="true" />
            <ListChecks v-else :size="15" aria-hidden="true" />
            {{ state === 'summarizing' ? '整理中' : '整理需求' }}
          </button>
          <div class="ai-blocks-composer__submit">
            <div class="ai-blocks-thinking-settings">
              <label class="ai-blocks-thinking-toggle" title="开启或关闭思考">
                <input v-model="thinkingEnabled" type="checkbox" role="switch" :disabled="isBusy" />
                <BrainCircuit :size="15" aria-hidden="true" />
                <span>思考</span>
              </label>
              <label class="ai-blocks-thinking-strength">
                <select v-model="thinkingStrength" aria-label="思考强度" title="思考强度" :disabled="!thinkingEnabled || isBusy">
                  <option value="low">低</option>
                  <option value="medium">中</option>
                  <option value="high">高</option>
                  <option value="xhigh">超高</option>
                  <option value="max">最大</option>
                </select>
              </label>
              <label class="ai-blocks-model-select">
                <select v-model="modelTier" aria-label="模型" title="模型" :disabled="isBusy">
                  <option value="normal">普通</option>
                  <option value="advanced">高级</option>
                  <option value="super">超级</option>
                </select>
              </label>
            </div>
            <button class="icon-button ai-blocks-send" type="submit" :disabled="!canSend" title="发送" aria-label="发送需求">
              <Send :size="16" aria-hidden="true" />
            </button>
          </div>
        </div>
      </form>
    </template>

    <section v-else-if="state === 'review' && requirements" class="ai-blocks-review">
      <div class="ai-blocks-review__title">
        <Sparkles :size="17" aria-hidden="true" />
        <div><span>需求已整理</span><h3>{{ requirements.title }}</h3></div>
      </div>
      <p>{{ requirements.goal }}</p>
      <div v-if="requirements.triggers.length"><strong>触发</strong><span>{{ requirements.triggers.join('；') }}</span></div>
      <div v-if="requirements.controls.length"><strong>操作</strong><span>{{ requirements.controls.join('；') }}</span></div>
      <ol><li v-for="step in requirements.behavior_steps" :key="step">{{ step }}</li></ol>
      <div class="ai-blocks-review__actions">
        <div class="ai-blocks-thinking-settings">
          <label class="ai-blocks-thinking-toggle" title="开启或关闭思考">
            <input v-model="thinkingEnabled" type="checkbox" role="switch" />
            <BrainCircuit :size="15" aria-hidden="true" />
            <span>思考</span>
          </label>
          <label class="ai-blocks-thinking-strength">
            <select v-model="thinkingStrength" aria-label="思考强度" title="思考强度" :disabled="!thinkingEnabled">
              <option value="low">低</option>
              <option value="medium">中</option>
              <option value="high">高</option>
              <option value="xhigh">超高</option>
              <option value="max">最大</option>
            </select>
          </label>
          <label class="ai-blocks-model-select">
            <select v-model="modelTier" aria-label="模型" title="模型">
              <option value="normal">普通</option>
              <option value="advanced">高级</option>
              <option value="super">超级</option>
            </select>
          </label>
        </div>
        <button class="button button--quiet" type="button" @click="state = 'chat'">继续补充</button>
        <button class="button button--primary" type="button" @click="generate"><Blocks :size="16" />确认并生成</button>
      </div>
    </section>

    <ResourcePlanReview :open="Boolean(projectPlan)" :plan="projectPlan" @cancel="projectPlan = null" @confirm="confirmProjectPlan" />
  </section>
</template>
