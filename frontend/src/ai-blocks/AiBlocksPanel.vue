<script setup lang="ts">
import {computed, nextTick, onBeforeUnmount, onMounted, ref, watch} from 'vue';
import {AlertTriangle, Blocks, Bot, BrainCircuit, Check, ChevronDown, CircleStop, History, LayoutGrid, LoaderCircle, MessageSquare, Pencil, Play, Plus, RefreshCw, Send, Sparkles, Trash2, Undo2} from 'lucide-vue-next';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import {
  streamAiBlocksChat,
  type AiBlocksChatRequest,
  type AiBlocksMessage,
  type AiBlocksMode,
  type AiBlocksModelTier,
  type AiBlocksThinkingSettings,
  type AiBlocksThinkingStrength,
  type AiBlocksTargetContext,
  type UnifiedGenerationEvent,
  type UnifiedGenerationRequest
} from './ai-blocks-api';
import {readAiTargetSnapshot, type AiTargetSnapshot} from './target-context';
import {UnifiedGenerationRunner, undoUnifiedGeneration, type AiGenerationProgress, type UnifiedCommitResult} from './generation-runner';
import ResourcePlanReview from './ResourcePlanReview.vue';
import {assertProjectManifest, type AiBlocksScope, type AssetPolicy, type ProjectManifest} from './project-plan';
import {prepareProjectResources} from './project-resource-preparer';
import {createProjectResourceExecutor} from './project-resource-executor';
import type {PreparedResource} from './project-resource-runner';
import type {CanvasInput} from '../canvas/model/canvas-types';
import type {CanvasPreparedAsset} from '../canvas/CanvasWorkspace.vue';
import type {ScratchLibraryItem, ScratchSpriteItem} from '../ai/scratch-library';
import type {GeneratedAsset} from '../ai/asset-library-store';
import {createId} from '../shared/create-id';

const props = defineProps<{bridge: ScratchGuiBridge | null}>();
const emit = defineEmits<{
  lockChange: [locked: boolean, message: string];
  busyChange: [busy: boolean];
  openCanvas: [opId: string, input: CanvasInput];
}>();

type ViewState = 'chat' | 'streaming' | 'review' | 'preparing' | 'committing';
interface StoredConversation {id: string; title: string; targetId: string; targetName: string; isStage: boolean; mode: AiBlocksMode; scope: AiBlocksScope; messages: AiBlocksMessage[]; updatedAt: number}

const THINKING_STORAGE_KEY = 'ai-blocks-thinking-settings';
const CONVERSATIONS_STORAGE_KEY = 'ai-blocks-conversations';
const MAX_CONVERSATIONS = 30;
const projectAssetsEnabled = import.meta.env.VITE_AI_PROJECT_ASSETS_ENABLED !== 'false';

function loadThinkingSettings(): AiBlocksThinkingSettings {
  try {
    const value = JSON.parse(localStorage.getItem(THINKING_STORAGE_KEY) ?? 'null');
    if (typeof value?.enabled === 'boolean' && ['low', 'medium', 'high', 'xhigh', 'max'].includes(value.strength)) return value;
  } catch {
    // Browser storage is optional.
  }
  return {enabled: true, strength: 'medium'};
}
function loadConversations(): StoredConversation[] {
  try {
    const value = JSON.parse(localStorage.getItem(CONVERSATIONS_STORAGE_KEY) ?? '[]');
    return Array.isArray(value) ? value.filter(item => item && typeof item.id === 'string' && Array.isArray(item.messages)) : [];
  } catch {
    return [];
  }
}
function formatConversationTime(timestamp: number) {
  return new Intl.DateTimeFormat('zh-CN', {month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit'}).format(timestamp);
}
function localGreeting(name: string, isStage: boolean): AiBlocksMessage {
  return {role: 'assistant', content: (isStage ? '舞台' : '角色') + '“' + name + '”已就绪。告诉我想实现的效果，我会一次完成需求整理、规划和积木生成。'};
}

const initialThinking = loadThinkingSettings();
const state = ref<ViewState>('chat');
const mode = ref<AiBlocksMode>('append');
const scope = ref<AiBlocksScope>('single_sprite');
const assetPolicy = ref<AssetPolicy>('builtin_first');
const modelTier = ref<AiBlocksModelTier>('normal');
const thinkingEnabled = ref(initialThinking.enabled);
const thinkingStrength = ref<AiBlocksThinkingStrength>(initialThinking.strength);
const messages = ref<AiBlocksMessage[]>([]);
const draft = ref('');
const snapshot = ref<AiTargetSnapshot | null>(null);
const sessionId = ref(createId());
const requestId = ref('');
const phaseMessage = ref('');
const phaseName = ref('');
const requirements = ref<{title: string; goal: string; behavior_steps: string[]; triggers: string[]; controls: string[]; success_criteria: string[]; missing_questions: string[]} | null>(null);
const projectManifest = ref<ProjectManifest | null>(null);
const preparedResources = ref(new Map<string, PreparedResource>());
const generationResult = ref<UnifiedCommitResult | null>(null);
const generationDraft = ref<Awaited<ReturnType<UnifiedGenerationRunner['prepare']>> | null>(null);
const progress = ref<AiGenerationProgress>({current: 0, total: 0, label: ''});
const errorMessage = ref('');
const noticeMessage = ref('');
const historyOpen = ref(false);
const modesExpanded = ref(true);
const conversations = ref<StoredConversation[]>(loadConversations());
const chatLog = ref<HTMLElement | null>(null);
/** 当前轮次的 AI 对话卡片（响应式代理），其推理过程与任务清单在结束后保留在卡片里 */
const turnCard = ref<AiBlocksMessage | null>(null);
/** 当前轮次卡片在 messages 中的索引，用于资源确认对话框跨状态回填 */
const turnCardIndex = ref(-1);
let controller: AbortController | null = null;
let unsubscribeTargets: (() => void) | null = null;
let saveTimer: ReturnType<typeof setTimeout> | undefined;

const isBusy = computed(() => ['streaming', 'preparing', 'committing'].includes(state.value));
const canSend = computed(() => Boolean(props.bridge && snapshot.value && draft.value.trim() && !isBusy.value));
const scopeSummary = computed(() => scope.value === 'project' ? '整个项目' : '当前角色');
const modeSummary = computed(() => ({modify: '修改', append: '新增', rewrite: '重写'}[mode.value]));
const phaseTitle = computed(() => {
  if (phaseName.value === 'requirements') return '正在整理需求';
  if (phaseName.value === 'plan') return '正在规划项目';
  if (phaseName.value === 'program') return '正在编写积木';
  if (phaseName.value === 'review') return '等待资源确认';
  if (phaseName.value === 'preparing') return '正在准备项目资源';
  if (phaseName.value === 'committing') return '正在写入积木';
  return 'AI 正在处理';
});
const progressPercent = computed(() => progress.value.total ? Math.min(100, Math.round(progress.value.current / progress.value.total * 100)) : 0);

function setBusy(locked: boolean, message = 'AI 正在生成，请稍等') {
  emit('lockChange', locked, message);
  emit('busyChange', locked);
}
function saveConversations() {
  try { localStorage.setItem(CONVERSATIONS_STORAGE_KEY, JSON.stringify(conversations.value.slice(0, MAX_CONVERSATIONS))); } catch { /* optional */ }
}
function saveCurrentConversation() {
  if (!snapshot.value || messages.value.length < 2) return;
  const existing = conversations.value.filter(item => item.id !== sessionId.value);
  existing.unshift({id: sessionId.value, title: messages.value.find(item => item.role === 'user')?.content.slice(0, 36) ?? 'AI 写积木', targetId: snapshot.value.targetId, targetName: snapshot.value.targetName, isStage: snapshot.value.isStage, mode: mode.value, scope: scope.value, messages: messages.value, updatedAt: Date.now()});
  conversations.value = existing.slice(0, MAX_CONVERSATIONS);
  saveConversations();
}
function refreshTarget(reset = false) {
  if (!props.bridge) return;
  const next = readAiTargetSnapshot(props.bridge, mode.value);
  if (reset || !snapshot.value || snapshot.value.targetId !== next.targetId) {
    snapshot.value = next;
    sessionId.value = createId();
    turnCard.value = null;
    turnCardIndex.value = -1;
    messages.value = [localGreeting(next.targetName, next.isStage)];
    requirements.value = null;
    generationDraft.value = null;
    projectManifest.value = null;
    generationResult.value = null;
    noticeMessage.value = '';
    errorMessage.value = '';
    phaseMessage.value = '';
    phaseName.value = '';
    progress.value = {current: 0, total: 0, label: ''};
  } else snapshot.value = next;
}
function attachBridge() {
  unsubscribeTargets?.();
  unsubscribeTargets = null;
  if (!props.bridge) return;
  refreshTarget();
  unsubscribeTargets = props.bridge.subscribeTargets(() => {
    if (!isBusy.value) refreshTarget();
  });
}
async function scrollToLatest() {
  await nextTick();
  if (chatLog.value) chatLog.value.scrollTop = chatLog.value.scrollHeight;
}
function buildProjectContext(bridge: ScratchGuiBridge, current: AiTargetSnapshot) {
  const targets = bridge.currentVm?.runtime.targets ?? [];
  return {
    project_id: 'scratch-project',
    version: bridge.projectRevision,
    targets: targets.map(target => {
      const name = String(target.getName?.() ?? target.sprite?.name ?? (target.isStage ? '舞台' : '未命名角色'));
      const isCurrent = target.id === current.targetId;
      const targetContext = isCurrent ? current.context : readAiTargetSnapshot(bridge, 'append', target).context;
      return {
        target_id: target.id,
        revision: bridge.projectRevision,
        name,
        is_stage: Boolean(target.isStage),
        has_scripts: Boolean(target.blocks?.getScripts().length),
        mode: isCurrent ? current.context.mode : 'append',
        costumes: (target.getCostumes?.() ?? []).map(item => String(item.name ?? '')).filter(Boolean),
        sounds: (target.getSounds?.() ?? []).map(item => String(item.name ?? '')).filter(Boolean),
        sprite_names: targets.filter(item => !item.isStage && item.isOriginal !== false).map(item => String(item.getName?.() ?? item.sprite?.name ?? '')).filter(Boolean),
        current_program: targetContext.current_program
      };
    }),
    shared_state: {}
  };
}
function requestMessages(text: string) {
  return [...messages.value, {role: 'user' as const, content: text}]
    .map(message => ({role: message.role, content: message.content}));
}

function buildRequest(text: string): UnifiedGenerationRequest {
  if (!props.bridge || !snapshot.value) throw new Error('Scratch 编辑器尚未就绪');
  const request: UnifiedGenerationRequest = {
    model_tier: modelTier.value,
    session_id: sessionId.value,
    request_id: createId(),
    messages: requestMessages(text),
    target: snapshot.value.context,
    thinking: {enabled: thinkingEnabled.value, strength: thinkingStrength.value},
    scope: scope.value,
    asset_policy: assetPolicy.value,
    proceed_with_defaults: true
  };
  if (scope.value === 'project') request.project_context = buildProjectContext(props.bridge, snapshot.value);
  return request;
}
function buildChatRequest(text: string): AiBlocksChatRequest {
  if (!snapshot.value) throw new Error('Scratch 编辑器尚未就绪');
  return {
    model_tier: modelTier.value,
    session_id: sessionId.value,
    request_id: createId(),
    messages: requestMessages(text),
    target: snapshot.value.context,
    thinking: {enabled: thinkingEnabled.value, strength: thinkingStrength.value}
  };
}
/** 在对话末尾追加一张 AI 处理卡片，并把它记录为当前轮次卡片 */
function pushTurnCard(): void {
  const card: AiBlocksMessage = {role: 'assistant', content: '', reasoning: '', plan: [], status: 'streaming'};
  messages.value.push(card);
  turnCardIndex.value = messages.value.length - 1;
  turnCard.value = messages.value[messages.value.length - 1];
}
function lastAssistantCard(): AiBlocksMessage | null {
  for (let index = messages.value.length - 1; index >= 0; index -= 1) {
    if (messages.value[index]?.role === 'assistant') return messages.value[index];
  }
  return null;
}
function cardForTurn(): AiBlocksMessage | null {
  if (turnCard.value) return turnCard.value;
  const card = messages.value[turnCardIndex.value];
  return card?.role === 'assistant' ? card : lastAssistantCard();
}
/** 结束当前轮次卡片：保留推理过程与任务清单，并写入成功/失败/取消等终态说明 */
function settleTurn(status: 'success' | 'error' | 'cancelled', content?: string, error?: string) {
  const card = cardForTurn();
  turnCard.value = null;
  turnCardIndex.value = -1;
  if (!card) return;
  if (typeof content === 'string') card.content = content;
  card.error = status === 'error' ? (error ?? card.error ?? 'AI 生成失败') : undefined;
  card.status = status;
}
function statusLabel(status: AiBlocksMessage['status']) {
  if (status === 'error') return '生成失败';
  if (status === 'cancelled') return '已取消';
  return '已完成';
}
function handleEvent(event: UnifiedGenerationEvent) {
  if (event.type === 'reasoning_delta') {
    const card = turnCard.value;
    if (card) card.reasoning = (card.reasoning ?? '') + String(event.data.text ?? '');
  }
  if (event.type === 'phase_start') {
    phaseName.value = event.phase ?? '';
    phaseMessage.value = String(event.data.message ?? '');
  }
  if (event.type === 'phase_complete' && event.phase === 'requirements') {
    requirements.value = event.data as typeof requirements.value;
  }
  if (event.type === 'phase_complete' && event.phase === 'plan') {
    const card = turnCard.value;
    const steps = Array.isArray(event.data.steps) ? event.data.steps.filter((item): item is string => typeof item === 'string') : [];
    if (card) card.plan = steps;
    if (event.data.manifest) {
      projectManifest.value = event.data.manifest as ProjectManifest;
      assertProjectManifest(projectManifest.value);
    }
  }
  if (event.type === 'script_ready') {
    progress.value = {current: Number(event.data.index ?? 0) + 1, total: Number(event.data.total ?? 0), label: String(event.data.target_index ?? '') + ' · 脚本已校验'};
  }
  if (event.type === 'phase_complete' && event.phase === 'program') {
    progress.value = {current: progress.value.current, total: progress.value.total, label: '程序校验完成'};
  }
  if (event.type === 'repair') {
    const attempt = Number(event.data.attempt ?? 1);
    const maxRounds = Number(event.data.max_rounds ?? 1);
    const phase = String(event.data.phase ?? '');
    phaseName.value = '自动修复';
    phaseMessage.value = '校验未通过，AI 正在自动修正（第 ' + attempt + '/' + maxRounds + ' 次）';
    noticeMessage.value = 'AI 输出未通过校验，正在自动修正' + (phase ? '（' + phase + ' 阶段' : '') + '，请稍候。' + (attempt >= maxRounds ? '若仍有问题将显示具体原因。' : '');
  }
  void scrollToLatest();
}
async function sendMessage() {
  const text = draft.value.trim();
  if (!text || !props.bridge || !snapshot.value || isBusy.value) return;
  const request = buildRequest(text);
  draft.value = '';
  messages.value.push({role: 'user', content: text});
  pushTurnCard();
  requestId.value = request.request_id;
  errorMessage.value = '';
  noticeMessage.value = '';
  phaseMessage.value = '';
  phaseName.value = '';
  progress.value = {current: 0, total: 0, label: ''};
  requirements.value = null;
  projectManifest.value = null;
  generationDraft.value = null;
  generationResult.value = null;
  state.value = 'streaming';
  setBusy(true);
  controller = new AbortController();
  try {
    const runner = new UnifiedGenerationRunner(props.bridge);
    const nextDraft = await runner.prepare(request, handleEvent, controller.signal);
    generationDraft.value = nextDraft;
    requirements.value = nextDraft.requirements;
    projectManifest.value = nextDraft.manifest ?? null;
    const card = turnCard.value;
    if (card) {
      card.plan = nextDraft.plan.steps;
      card.content = nextDraft.requirements.title + '：程序草稿已完成校验。';
    }
    if (nextDraft.manifest && nextDraft.manifest.resource_ops.length) {
      state.value = 'review';
      phaseName.value = 'review';
      progress.value = {current: progress.value.current, total: progress.value.total, label: '确认资源后才会写入项目。'};
      setBusy(false);
      noticeMessage.value = '程序草稿已校验，确认资源后才会写入项目。';
    } else {
      state.value = 'committing';
      phaseName.value = 'committing';
      progress.value = {current: 0, total: 0, label: '正在写入积木…'};
      const result = await commitDraft(nextDraft, runner, controller.signal);
      state.value = 'chat';
      phaseName.value = '';
      settleTurn('success', '已完成统一生成并写入 ' + result.programs.length + ' 个目标。');
      setBusy(false);
      saveCurrentConversation();
    }
  } catch (error: unknown) {
    state.value = 'chat';
    if (error instanceof DOMException && error.name === 'AbortError') {
      settleTurn('cancelled', '已取消生成，Scratch 项目未被修改。');
    } else {
      const message = error instanceof Error ? error.message : 'AI 生成失败';
      settleTurn('error', undefined, message);
    }
    setBusy(false);
    saveCurrentConversation();
  } finally {
    controller = null;
    if (state.value === 'streaming') {
      state.value = 'chat';
      setBusy(false);
    }
    await scrollToLatest();
  }
}
async function commitDraft(
  draftValue: Awaited<ReturnType<UnifiedGenerationRunner['prepare']>>,
  runner: UnifiedGenerationRunner,
  signal?: AbortSignal,
  resources?: Map<string, PreparedResource>,
  generatedAssets?: GeneratedAsset[]
): Promise<UnifiedCommitResult> {
  if (!props.bridge) throw new Error('Scratch 编辑器尚未就绪');
  const result = await runner.commit(draftValue, {
    preparedResources: resources,
    resourceExecutor: draftValue.manifest ? createProjectResourceExecutor({bridge: props.bridge, currentTargetId: draftValue.snapshot.targetId}) : undefined,
    signal,
    generatedAssets,
    onProgress: value => { progress.value = value; }
  });
  generationResult.value = result;
  generationDraft.value = null;
  return result;
}
function openResourceReview() {
  if (generationDraft.value?.manifest) projectManifest.value = generationDraft.value.manifest;
}
async function confirmProjectManifest(confirmedGenerationIds: string[], selections: Record<string, ScratchSpriteItem | ScratchLibraryItem>) {
  const draftValue = generationDraft.value;
  if (!draftValue?.manifest || !props.bridge || isBusy.value) return;
  state.value = 'preparing';
  phaseName.value = 'preparing';
  progress.value = {current: 0, total: 0, label: '正在准备项目资源…'};
  setBusy(true, '正在准备项目资源');
  errorMessage.value = '';
  controller = new AbortController();
  try {
    const prepared = await prepareProjectResources({
      plan: draftValue.manifest,
      assetPolicy: draftValue.request.asset_policy,
      selections: new Map(Object.entries(selections)) as never,
      confirmGeneration: request => confirmedGenerationIds.includes(request.resource_key),
      signal: controller.signal,
      onProgress: (current, total, label) => { progress.value = {current, total, label}; }
    });
    preparedResources.value = prepared.prepared;
    state.value = 'committing';
    phaseName.value = 'committing';
    progress.value = {current: 0, total: 0, label: '正在写入积木与资源…'};
    const result = await commitDraft(draftValue, new UnifiedGenerationRunner(props.bridge), controller.signal, prepared.prepared, prepared.generatedAssets);
    state.value = 'chat';
    phaseName.value = '';
    settleTurn('success', '已完成统一生成并写入 ' + result.programs.length + ' 个目标。');
    noticeMessage.value = '资源与全部目标程序已在同一事务中提交。';
    setBusy(false);
    saveCurrentConversation();
  } catch (error: unknown) {
    state.value = 'review';
    phaseName.value = 'review';
    progress.value = {current: progress.value.current, total: progress.value.total, label: '确认资源后才会写入项目。'};
    if (error instanceof DOMException && error.name === 'AbortError') {
      settleTurn('cancelled', '已取消资源准备，项目未被修改。');
    } else {
      const message = error instanceof Error ? error.message : '项目资源提交失败';
      settleTurn('error', undefined, message);
    }
    setBusy(false);
  } finally {
    controller = null;
  }
}
/** 关闭资源确认对话框：当前轮次卡片保留草稿状态，而不是移除思考过程与任务清单 */
function cancelResourceReview() {
  projectManifest.value = null;
  const card = cardForTurn();
  if (card && card.status === 'streaming') {
    settleTurn('success', '程序草稿已校验，可在“重新打开资源确认”继续提交。');
  }
}
function cancel() {
  controller?.abort();
}
async function undoGeneration() {
  if (!props.bridge || !generationResult.value || isBusy.value) return;
  setBusy(true, '正在撤销 AI 生成');
  try {
    const undid = generationResult.value;
    await undoUnifiedGeneration(props.bridge, undid);
    generationResult.value = null;
    snapshot.value = readAiTargetSnapshot(props.bridge, mode.value);
    messages.value.push({role: 'assistant', content: '已撤销本次 AI 生成（' + undid.programs.length + ' 个目标），项目恢复原状。'});
    noticeMessage.value = '已撤销本次 AI 生成。';
    saveCurrentConversation();
  } catch (error: unknown) {
    errorMessage.value = error instanceof Error ? error.message : '撤销失败';
  } finally {
    setBusy(false);
  }
}
function runProject() { props.bridge?.run(); }
function startNewConversation() { if (!isBusy.value && snapshot.value) refreshTarget(true); }
function clearConversationHistory() { conversations.value = []; saveConversations(); }
function restoreConversation(conversation: StoredConversation) {
  if (isBusy.value || !snapshot.value || conversation.targetId !== snapshot.value.targetId) return;
  turnCard.value = null;
  turnCardIndex.value = -1;
  sessionId.value = conversation.id;
  messages.value = conversation.messages.map(message => message.status === 'streaming' ? {...message, status: 'success'} : message);
  mode.value = conversation.mode;
  scope.value = conversation.scope;
  historyOpen.value = false;
  phaseName.value = '';
  phaseMessage.value = '';
  progress.value = {current: 0, total: 0, label: ''};
}
function acceptCanvasPrepared(opId: string, payload: CanvasPreparedAsset) {
  const current = preparedResources.value.get(opId);
  if (!current) throw new Error('找不到待回填的资源操作：' + opId);
  const value = current.kind === 'costume' ? (payload.kind === 'sprite' ? payload.costumes?.[0] : payload.backdrop) : payload.kind === 'sprite' ? {name: current.op_id, costumes: payload.costumes} : payload.backdrop;
  if (!value) throw new Error('画布没有可提交的导出结果');
  preparedResources.value = new Map(preparedResources.value).set(opId, {...current, value});
}
function openPreparedCanvas(opId: string) {
  const prepared = preparedResources.value.get(opId);
  if (!prepared || !prepared.value || typeof prepared.value !== 'object' || !('blob' in prepared.value)) throw new Error('该资源当前不可在画布中处理');
  const blob = (prepared.value as {blob: Blob}).blob;
  emit('openCanvas', opId, {kind: 'blob', blob, fileName: opId + '.png', source: 'upload'});
}
async function openChatHelp() {
  if (!props.bridge || isBusy.value || !snapshot.value) return;
  const text = '请解释当前 Scratch 目标适合生成什么积木';
  const request = buildChatRequest(text);
  messages.value.push({role: 'user', content: text});
  pushTurnCard();
  state.value = 'streaming';
  setBusy(true, 'AI 正在回答，请稍等');
  errorMessage.value = '';
  phaseName.value = '';
  phaseMessage.value = '';
  progress.value = {current: 0, total: 0, label: ''};
  controller = new AbortController();
  try {
    await streamAiBlocksChat(
      request,
      value => { const card = turnCard.value; if (card) card.content += value; },
      controller.signal,
      value => { const card = turnCard.value; if (card) card.reasoning = (card.reasoning ?? '') + value; }
    );
    settleTurn('success');
    saveCurrentConversation();
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      settleTurn('cancelled', '已取消对话。');
    } else {
      const message = error instanceof Error ? error.message : 'AI 对话失败';
      settleTurn('error', undefined, message);
    }
    saveCurrentConversation();
  } finally {
    controller = null;
    state.value = 'chat';
    setBusy(false);
    await scrollToLatest();
  }
}
function persistThinking() {
  try { localStorage.setItem(THINKING_STORAGE_KEY, JSON.stringify({enabled: thinkingEnabled.value, strength: thinkingStrength.value})); } catch { /* optional */ }
}

watch(() => props.bridge, attachBridge);
watch([mode, scope], () => { if (props.bridge && snapshot.value && !isBusy.value) refreshTarget(); });
watch([thinkingEnabled, thinkingStrength], persistThinking);
watch([messages, mode], () => {
  if (saveTimer !== undefined) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    saveTimer = undefined;
    saveCurrentConversation();
  }, 600);
}, {deep: true});
onMounted(attachBridge);
onBeforeUnmount(() => {
  if (saveTimer !== undefined) clearTimeout(saveTimer);
  controller?.abort();
  unsubscribeTargets?.();
  setBusy(false);
  emit('busyChange', false);
});
defineExpose({acceptCanvasPrepared});
</script>

<template>
  <section class="ai-blocks-panel" aria-label="AI 写积木">
    <header class="ai-blocks-panel__header">
      <h2><Blocks :size="18" aria-hidden="true" />AI 写积木</h2>
      <div class="ai-blocks-panel__header-actions">
        <button class="icon-button" type="button" :class="{'is-active': historyOpen}" :disabled="isBusy" title="查看历史对话" aria-label="查看历史对话" @click="historyOpen = !historyOpen"><History :size="16" /></button>
        <button class="icon-button" type="button" :disabled="isBusy || !snapshot" title="新建对话" aria-label="新建对话" @click="startNewConversation"><Plus :size="17" /></button>
        <span v-if="snapshot" class="ai-blocks-count">{{ snapshot.blockCount }} 块</span>
      </div>
    </header>
    <section v-if="historyOpen" class="ai-blocks-history" aria-label="历史对话">
      <div class="ai-blocks-history__heading"><div><strong>历史对话</strong><span>{{ conversations.length }} 条记录</span></div><button v-if="conversations.length" class="button button--quiet" type="button" :disabled="isBusy" @click="clearConversationHistory"><Trash2 :size="14" />清空</button></div>
      <div v-if="!conversations.length" class="ai-blocks-history__empty"><MessageSquare :size="18" /><span>发送第一条需求后，对话会自动保存在这里</span></div>
      <div v-else class="ai-blocks-history__list"><button v-for="conversation in conversations" :key="conversation.id" class="ai-blocks-history__item" type="button" :disabled="isBusy" @click="restoreConversation(conversation)"><MessageSquare :size="14" /><span><strong>{{ conversation.title }}</strong><small>{{ conversation.isStage ? '舞台' : '角色' }} · {{ conversation.targetName }} · {{ formatConversationTime(conversation.updatedAt) }}</small></span></button></div>
    </section>
    <fieldset class="ai-blocks-modes" :class="{'is-collapsed': isBusy || !modesExpanded}">
      <legend><button class="ai-blocks-modes__toggle" type="button" :disabled="isBusy" @click="modesExpanded = !modesExpanded"><span>模式</span><span class="ai-blocks-modes__summary"><span class="ai-blocks-modes__tag">{{ scopeSummary }}</span><span class="ai-blocks-modes__tag">{{ modeSummary }}</span></span><ChevronDown :size="18" /></button></legend>
      <div class="ai-blocks-mode-options">
        <label class="ai-blocks-scope-card" :class="{'is-active': scope === 'single_sprite'}"><input v-model="scope" type="radio" value="single_sprite" /><span><LayoutGrid :size="20" /><strong>当前角色</strong><small>只编写当前角色的积木</small></span></label>
        <label v-if="projectAssetsEnabled" class="ai-blocks-scope-card" :class="{'is-active': scope === 'project'}"><input v-model="scope" type="radio" value="project" /><span><Blocks :size="20" /><strong>整个项目</strong><small>同时处理资源和多个目标</small></span></label>
        <label v-if="scope === 'project'" class="ai-blocks-policy"><span>素材策略</span><select v-model="assetPolicy" aria-label="素材策略"><option value="builtin_first">内置素材优先</option><option value="existing_only">仅已有素材</option><option value="allow_generation">允许生图</option></select></label>
        <label v-for="item in [{value: 'modify', label: '修改当前代码', icon: Pencil}, {value: 'append', label: '旁路新增', icon: LayoutGrid}, {value: 'rewrite', label: '清空重写', icon: RefreshCw}]" :key="item.value" class="ai-blocks-mode-card" :class="{'is-active': mode === item.value}"><input v-model="mode" type="radio" :value="item.value" /><span><component :is="item.icon" :size="20" /><strong>{{ item.label }}</strong><small>{{ item.value === 'modify' ? '继续编辑原有积木' : item.value === 'append' ? '保留原代码并添加功能' : '重新生成全部积木' }}</small></span></label>
      </div>
    </fieldset>
    <div v-if="errorMessage" class="ai-blocks-alert ai-blocks-alert--error" role="alert">{{ errorMessage }}</div>
    <div v-if="noticeMessage" class="ai-blocks-alert" role="status">{{ noticeMessage }}</div>
    <div v-if="generationDraft?.manifest && !projectManifest" class="ai-blocks-prepared-actions"><button class="button button--quiet" type="button" :disabled="isBusy" @click="openResourceReview">重新打开资源确认</button></div>
    <div ref="chatLog" class="ai-blocks-chat" role="log" aria-live="polite">
      <article v-for="(message, index) in messages" :key="index" class="ai-blocks-message" :class="['is-' + message.role, message.status ? 'is-' + message.status : '']">
        <span class="ai-blocks-message__avatar"><Bot v-if="message.role === 'assistant'" :size="14" /><span v-else>你</span></span>
        <div class="ai-blocks-message__body">
          <p v-if="message.content" class="ai-blocks-message__text">{{ message.content }}</p>
          <template v-if="message.role === 'assistant'">
            <div v-if="message.status === 'streaming'" class="ai-blocks-message__process">
              <header><LoaderCircle class="ai-blocks-inline-spinner" :size="15" /><strong>{{ phaseTitle }}</strong></header>
              <p v-if="progress.label || phaseMessage" class="ai-blocks-message__phase">{{ progress.label || phaseMessage || '正在生成结构化草稿' }}</p>
              <div class="ai-blocks-message__progress">
                <div class="ai-blocks-progress__track" :class="{'is-indeterminate': !progress.total}"><span :style="{width: progressPercent + '%'}" /></div>
                <span v-if="progress.total">{{ progress.current }} / {{ progress.total }}</span>
                <button v-if="isBusy" class="button button--quiet" type="button" @click="cancel"><CircleStop :size="15" />取消</button>
              </div>
            </div>
            <div v-if="message.reasoning" class="ai-blocks-message__reasoning"><span><BrainCircuit :size="14" />思考过程</span><p>{{ message.reasoning }}</p></div>
            <ol v-if="message.plan && message.plan.length" class="ai-blocks-message__plan"><li v-for="step in message.plan" :key="step"><Check :size="13" />{{ step }}</li></ol>
            <p v-if="message.error" class="ai-blocks-message__error"><AlertTriangle :size="14" /><span>{{ message.error }}</span></p>
            <span v-if="message.status && message.status !== 'streaming'" class="ai-blocks-message__status" :class="'is-' + message.status">{{ statusLabel(message.status) }}</span>
          </template>
        </div>
      </article>
      <div v-if="generationResult" class="ai-blocks-generation-actions"><button class="button button--primary" type="button" @click="runProject"><Play :size="15" />运行查看</button><button class="button button--quiet" type="button" @click="undoGeneration"><Undo2 :size="15" />撤销生成</button></div>
    </div>
    <form class="ai-blocks-composer" @submit.prevent="sendMessage">
      <textarea v-model="draft" rows="3" maxlength="2000" :disabled="isBusy" aria-label="描述要实现的积木功能" placeholder="描述希望实现的效果" @keydown.enter.exact.prevent="sendMessage" />
      <div class="ai-blocks-composer__actions"><button class="button button--quiet" type="button" :disabled="isBusy || !snapshot" @click="openChatHelp"><Bot :size="15" />问 AI</button><div class="ai-blocks-composer__submit"><label class="ai-blocks-thinking-toggle"><input v-model="thinkingEnabled" type="checkbox" role="switch" :disabled="isBusy" /><BrainCircuit :size="15" /><span>思考</span></label><label class="ai-blocks-thinking-strength"><select v-model="thinkingStrength" aria-label="思考强度" :disabled="!thinkingEnabled || isBusy"><option value="low">低</option><option value="medium">中</option><option value="high">高</option><option value="xhigh">超高</option><option value="max">最大</option></select></label><label class="ai-blocks-model-select"><select v-model="modelTier" aria-label="模型" :disabled="isBusy"><option value="normal">普通</option><option value="advanced">高级</option><option value="super">超级</option></select></label><button class="icon-button ai-blocks-send" type="submit" :disabled="!canSend" title="发送" aria-label="发送需求"><Send :size="16" /></button></div></div>
    </form>
    <section v-if="requirements && state === 'review' && !projectManifest" class="ai-blocks-review"><div class="ai-blocks-review__title"><Sparkles :size="17" /><div><span>需求已整理</span><h3>{{ requirements.title }}</h3></div></div><p>{{ requirements.goal }}</p><ol><li v-for="step in requirements.behavior_steps" :key="step">{{ step }}</li></ol></section>
    <ResourcePlanReview :open="Boolean(projectManifest)" :plan="projectManifest" @cancel="cancelResourceReview" @confirm="confirmProjectManifest" />
  </section>
</template>
