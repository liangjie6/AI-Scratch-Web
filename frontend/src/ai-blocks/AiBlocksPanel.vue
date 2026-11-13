<script setup lang="ts">
import {computed, nextTick, onBeforeUnmount, onMounted, ref, watch} from 'vue';
import {AlertTriangle, Blocks, Bot, BrainCircuit, CircleStop, Coins, History, ListOrdered, LoaderCircle, MessageSquare, Play, Plus, Send, Sparkles, Trash2, Undo2} from 'lucide-vue-next';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import {probeScratchEditorPort} from '../scratch/scratch-editor-port';
import {
  AI_BLOCKS_MODEL_OPTIONS,
  AI_BLOCKS_THINKING_OPTIONS,
  streamProjectProgram,
  streamSourceProgram,
  type AiBlocksMessage,
  type AiBlocksMode,
  type AiBlocksModelTier,
  type AiBlocksThinkingSettings,
  type AiBlocksThinkingStrength,
  type AiBlocksTargetContext,
  type ProjectProgramGenerationRequest,
  type SourceProgramGenerationRequest,
  type UnifiedGenerationEvent,
  type UnifiedGenerationRequest,
  type UnifiedProgramDraft
} from './ai-blocks-api';
import {
  AiRunApiError,
  cancelAiRun,
  createAiRun,
  getAiRuntimeCapabilities,
  getAiRun,
  supportsDurableRuns,
  streamAiRunEvents,
  submitAiRunCommitMetric,
  submitAiRunResourceReviewMetric,
  submitAiRunProgramContext,
  submitAiRunToolResult,
  type AiRunEvent,
  type AiRunRecord,
  type AiRunResourceReviewMetricBucket,
  type AiRuntimeCapabilities,
} from './ai-runs-api';
import {assertProjectSnapshotIsCurrent, assertSnapshotIsCurrent, readAiProjectSnapshot, readAiTargetSnapshot, type AiProjectSnapshot, type AiTargetSnapshot} from './target-context';
import {createScratchProgramPort} from '../scratch/scratch-program-port';
import {LOCAL_PROJECT_STORAGE_KEY} from './run-checkpoint-store';
import {UnifiedGenerationRunner, undoUnifiedGeneration, type AiGenerationProgress, type CommitTelemetry, type ProjectManifestEventSource, type UnifiedCommitResult} from './generation-runner';
import ResourcePlanReview from './ResourcePlanReview.vue';
import {frontendEnabledBuiltinExtensions, type AiBlocksScope, type AssetPolicy, type ImageGenerationSource, type ProjectManifest, type ResourceOp, type TargetRef} from './project-plan';
import {prepareProjectResources} from './project-resource-preparer';
import {createProjectResourceExecutor} from './project-resource-executor';
import {projectProgramGenerationLayers} from './project-contract';
import {SOURCE_PIPELINE_VERSION} from './source-contract';
import type {PreparedResource} from './project-resource-runner';
import type {CanvasInput} from '../canvas/model/canvas-types';
import type {CanvasPreparedAsset} from '../canvas/CanvasWorkspace.vue';
import type {ScratchLibraryItem, ScratchSoundItem, ScratchSpriteItem} from '../ai/scratch-library';
import type {GeneratedAsset} from '../ai/asset-library-store';
import {createId} from '../shared/create-id';
import type {ProgramIR, ScriptIR} from './program-ir';
import type {BuiltinAiExtension} from './block-catalog';

const props = defineProps<{bridge: ScratchGuiBridge | null; commitBlocked?: boolean}>();
const emit = defineEmits<{
  lockChange: [locked: boolean, message: string];
  busyChange: [busy: boolean];
  openCanvas: [opId: string, input: CanvasInput];
}>();

type ViewState = 'chat' | 'streaming' | 'review' | 'preparing' | 'committing';
interface StoredConversation {id: string; title: string; targetId: string; targetName: string; isStage: boolean; mode: AiBlocksMode; scope: AiBlocksScope; messages: AiBlocksMessage[]; updatedAt: number}
interface GenerationMetrics {totalTokens: number}

const THINKING_STORAGE_KEY = 'ai-blocks-thinking-settings';
const CONVERSATIONS_STORAGE_KEY = 'ai-blocks-conversations';
const MAX_CONVERSATIONS = 30;
const projectAssetsFeatureEnabled = import.meta.env.VITE_AI_PROJECT_ASSETS_ENABLED !== 'false';
const scratchEditor15_1Enabled = import.meta.env.VITE_SCRATCH_EDITOR_15_1_ENABLED !== 'false';
const soundAssetsFeatureEnabled = import.meta.env.VITE_AI_SOUND_ASSETS_ENABLED !== 'false';
const audioGenerationFeatureEnabled = import.meta.env.VITE_AI_AUDIO_GENERATION_ENABLED === 'true';
const logicalResourceRefsFeatureEnabled = import.meta.env.VITE_AI_LOGICAL_RESOURCE_REFS_ENABLED !== 'false';
const shortCommitFeatureEnabled = import.meta.env.VITE_AI_SHORT_COMMIT_ENABLED !== 'false';
const durableRunsEnabled = import.meta.env.VITE_AI_DURABLE_RUNS_ENABLED === 'true';
const frontendCommitEnabled = import.meta.env.VITE_AI_BLOCKS_COMMIT_ENABLED !== 'false';
const frontendCrashCheckpointEnabled = import.meta.env.VITE_AI_CRASH_CHECKPOINT_ENABLED !== 'false';
const DURABLE_RUN_SESSION_STORAGE_KEY = 'ai-scratch-durable-project-run-v1';

interface DurableRunSession {
  version: 1;
  runId: string;
  projectId: string;
  request: UnifiedGenerationRequest;
  snapshot: Omit<AiTargetSnapshot, 'targetRef'>;
  projectSnapshot: Omit<AiProjectSnapshot, 'targetRefs'>;
}

interface DurableRunState {
  record: AiRunRecord;
  lastEventId?: string;
  lastSequence: number;
  awaitingResources: boolean;
}

function serializableTargetSnapshot(snapshot: AiTargetSnapshot): Omit<AiTargetSnapshot, 'targetRef'> {
  const {targetRef: _targetRef, ...serializable} = snapshot;
  return serializable;
}

function serializableProjectSnapshot(snapshot: AiProjectSnapshot): Omit<AiProjectSnapshot, 'targetRefs'> {
  const {targetRefs: _targetRefs, ...serializable} = snapshot;
  return serializable;
}

function saveDurableRunSession(session: DurableRunSession) {
  try { sessionStorage.setItem(DURABLE_RUN_SESSION_STORAGE_KEY, JSON.stringify(session)); } catch { /* browser storage is optional */ }
}

function clearDurableRunSession() {
  try { sessionStorage.removeItem(DURABLE_RUN_SESSION_STORAGE_KEY); } catch { /* browser storage is optional */ }
}

function loadDurableRunSession(projectId: string): DurableRunSession | undefined {
  try {
    const value = JSON.parse(sessionStorage.getItem(DURABLE_RUN_SESSION_STORAGE_KEY) ?? 'null') as Partial<DurableRunSession> | null;
    if (value?.version !== 1 || value.projectId !== projectId || typeof value.runId !== 'string' || !value.runId || !value.request || !value.snapshot || !value.projectSnapshot) return undefined;
    return value as DurableRunSession;
  } catch {
    return undefined;
  }
}

function loadThinkingSettings(): AiBlocksThinkingSettings {
  try {
    const value = JSON.parse(localStorage.getItem(THINKING_STORAGE_KEY) ?? 'null');
    if (typeof value?.enabled === 'boolean' && AI_BLOCKS_THINKING_OPTIONS.some(item => item.value === value.strength)) return value;
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
const scope = ref<AiBlocksScope>('project');
const assetPolicy = ref<AssetPolicy>('builtin_first');
const textToSpeechConsent = ref(false);
const runtimeCapabilities = ref<AiRuntimeCapabilities | undefined | null>(null);
const capabilityLoading = ref(false);
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
const isUndoing = ref(false);
const generationDraft = ref<Awaited<ReturnType<UnifiedGenerationRunner['prepare']>> | null>(null);
const progress = ref<AiGenerationProgress>({current: 0, total: 0, label: ''});
const errorMessage = ref('');
const noticeMessage = ref('');
const historyOpen = ref(false);
const conversations = ref<StoredConversation[]>(loadConversations());
const chatLog = ref<HTMLElement | null>(null);
/** 当前轮次的 AI 对话卡片（响应式代理），其推理过程与任务清单在结束后保留在卡片里 */
const turnCard = ref<AiBlocksMessage | null>(null);
/** 当前轮次卡片在 messages 中的索引，用于资源确认对话框跨状态回填 */
const turnCardIndex = ref(-1);
const durableRun = ref<DurableRunState | null>(null);
const generationMetrics = ref<GenerationMetrics>({totalTokens: 0});
let controller: AbortController | null = null;
let unsubscribeTargets: (() => void) | null = null;
let saveTimer: ReturnType<typeof setTimeout> | undefined;
let durableRunResumeAttempted = false;
let isRestoringConversation = false;

const isBusy = computed(() => isUndoing.value || ['streaming', 'preparing', 'committing'].includes(state.value));
const enabledBuiltinExtensions = computed<ReadonlySet<BuiltinAiExtension>>(() => {
  const frontendExtensions = frontendEnabledBuiltinExtensions();
  if (runtimeCapabilities.value === null || !props.bridge?.builtinExtensionLoaderAvailable) return new Set();
  if (!runtimeCapabilities.value) return frontendExtensions;
  return new Set([...frontendExtensions].filter(extension => runtimeCapabilities.value!.builtinExtensions.has(extension)));
});
const projectAssetsAvailable = computed(() => {
  if (!projectAssetsFeatureEnabled || !soundAssetsFeatureEnabled || !props.bridge) return false;
  const local = probeScratchEditorPort(props.bridge);
  if (!local.spriteImport || !local.costumeAssets || !local.backdropAssets || !local.soundAssets) return false;
  // An older backend has no capabilities endpoint, so retain the existing v2
  // fallback only after the local editor probe proves the full asset surface.
  return !runtimeCapabilities.value || (
    runtimeCapabilities.value.projectAssets && runtimeCapabilities.value.soundAssets
  );
});
const textToSpeechExtensionAvailable = computed(() => enabledBuiltinExtensions.value.has('text2speech'));
const aiCommitAvailable = computed(() => scratchEditor15_1Enabled && frontendCommitEnabled && frontendCrashCheckpointEnabled && logicalResourceRefsFeatureEnabled && shortCommitFeatureEnabled && runtimeCapabilities.value !== null && (runtimeCapabilities.value?.commitEnabled ?? true) && (runtimeCapabilities.value?.crashCheckpoint ?? true) && (runtimeCapabilities.value?.logicalResourceRefs ?? true) && (runtimeCapabilities.value?.shortCommit ?? true));
const canSend = computed(() => Boolean(props.bridge && snapshot.value && draft.value.trim() && !isBusy.value && !capabilityLoading.value && aiCommitAvailable.value && !props.commitBlocked));
const scopeSummary = computed(() => scope.value === 'project' ? '整个项目' : '当前角色');
const modeSummary = computed(() => ({modify: '修改', append: '新增', rewrite: '重写'}[mode.value]));
const thinkingLevel = computed({
  get: () => thinkingEnabled.value ? AI_BLOCKS_THINKING_OPTIONS.findIndex(item => item.value === thinkingStrength.value) + 1 : 0,
  set: (value: number) => {
    const index = Number(value);
    thinkingEnabled.value = index > 0;
    if (index > 0) thinkingStrength.value = AI_BLOCKS_THINKING_OPTIONS[index - 1]?.value ?? 'medium';
  },
});
const thinkingLabel = computed(() => thinkingEnabled.value
  ? AI_BLOCKS_THINKING_OPTIONS.find(item => item.value === thinkingStrength.value)?.label ?? '中'
  : '关');
const thinkingHint = computed(() => ({
  low: '快速理解简单需求',
  medium: '平衡速度与规划质量',
  high: '处理多事件与复杂逻辑',
  xhigh: '深入检查项目关联',
  max: '执行最完整的推理与校验',
}[thinkingStrength.value]));
const tokenTotalLabel = computed(() => generationMetrics.value.totalTokens ? generationMetrics.value.totalTokens.toLocaleString('zh-CN') : '--');
const phaseTitle = computed(() => {
  if (phaseName.value === 'requirements') return '正在整理需求';
  if (phaseName.value === 'task_spec') return '正在理解玩法规则';
  if (phaseName.value === 'blueprint') return '正在编排项目职责';
  if (phaseName.value === 'plan') return '正在规划项目';
  if (phaseName.value === 'program') return '正在编写积木';
  if (phaseName.value === 'review') return '等待资源确认';
  if (phaseName.value === 'preparing') return '正在准备项目资源';
  if (phaseName.value === 'committing') return '正在写入积木';
  if (phaseName.value === 'repair') return '正在自动修复';
  return 'AI 正在处理';
});
const progressPercent = computed(() => progress.value.total ? Math.min(100, Math.round(progress.value.current / progress.value.total * 100)) : 0);

function setBusy(locked: boolean, message = 'AI 正在生成，请稍等') {
  emit('lockChange', locked, message);
  emit('busyChange', locked);
}
async function refreshRuntimeCapabilities() {
  runtimeCapabilities.value = await getAiRuntimeCapabilities();
  if (!textToSpeechExtensionAvailable.value) textToSpeechConsent.value = false;
}
function createGenerationRunner(bridge: ScratchGuiBridge) {
  return new UnifiedGenerationRunner(bridge, enabledBuiltinExtensions.value);
}
watch(projectAssetsAvailable, available => {
  if (!available && props.bridge && scope.value === 'project') scope.value = 'single_sprite';
});
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
  if (isRestoringConversation) {
    snapshot.value = next;
    return;
  }
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
    // VM.clear publishes a null selection before the new project is installed.
    if (!isBusy.value && props.bridge?.currentVm?.editingTarget) refreshTarget();
  });
  void resumeDurableRunIfPresent();
}

async function resumeDurableRunIfPresent() {
  if (!durableRunsEnabled || durableRunResumeAttempted || !props.bridge || !snapshot.value || isBusy.value) return;
  durableRunResumeAttempted = true;
  const session = loadDurableRunSession(LOCAL_PROJECT_STORAGE_KEY);
  if (!session) return;
  try {
    assertSnapshotIsCurrent(props.bridge, session.snapshot as AiTargetSnapshot);
    assertProjectSnapshotIsCurrent(props.bridge, session.projectSnapshot as AiProjectSnapshot);
    const record = await getAiRun(session.runId);
    if (record.status === 'CANCELLED' || record.status === 'FAILED') {
      clearDurableRunSession();
      return;
    }
    const lastUserMessage = [...session.request.messages].reverse().find(message => message.role === 'user');
    if (lastUserMessage) messages.value.push({role: 'user', content: lastUserMessage.content});
    pushTurnCard();
    requestId.value = session.request.request_id;
    errorMessage.value = '';
    noticeMessage.value = '已恢复此前中断的 AI 项目任务。';
    phaseMessage.value = '正在恢复任务状态…';
    state.value = 'streaming';
    setBusy(true, '正在恢复 AI 项目任务');
    controller = new AbortController();
    await refreshRuntimeCapabilities();
    const runner = createGenerationRunner(props.bridge);
    const nextDraft = await runner.prepare(session.request, handleEvent, controller.signal, createResumedDurableManifestEventSource(record));
    const activeDurableRun = durableRun.value as DurableRunState | null;
    if (nextDraft.manifest && activeDurableRun?.awaitingResources && nextDraft.manifest.resource_ops.length === 0) {
      phaseName.value = 'program';
      progress.value = {current: 0, total: nextDraft.manifest.program_targets.length, label: '正在生成全部目标程序…'};
      await submitDurableProgramContexts(nextDraft, controller.signal);
      activeDurableRun.awaitingResources = false;
      await prepareDurableProjectPrograms(nextDraft, controller.signal, handleEvent);
    }
    generationDraft.value = nextDraft;
    requirements.value = nextDraft.requirements;
    projectManifest.value = nextDraft.manifest?.resource_ops.length ? nextDraft.manifest : null;
    const card = turnCard.value;
    if (card) {
      card.plan = nextDraft.plan.steps;
      card.content = nextDraft.requirements.title + '：已恢复程序草稿。';
    }
    if (nextDraft.manifest && nextDraft.manifest.resource_ops.length) {
      state.value = 'review';
      phaseName.value = 'review';
      progress.value = {current: progress.value.current, total: progress.value.total, label: '确认资源后才会写入项目。'};
      setBusy(false);
      noticeMessage.value = '已恢复项目草稿，请重新确认资源后提交。';
    } else {
      state.value = 'committing';
      phaseName.value = 'committing';
      progress.value = {current: 0, total: 0, label: '正在写入积木…'};
      const result = await commitDraft(nextDraft, runner, controller.signal);
      state.value = 'chat';
      phaseName.value = '';
      settleTurn('success', '已恢复并写入 ' + result.programs.length + ' 个目标。');
      setBusy(false);
      saveCurrentConversation();
    }
  } catch (error: unknown) {
    if (error instanceof AiRunApiError && error.code === 'RUN_NOT_FOUND') {
      // The server may have expired an old durable run while this tab was
      // closed. Do not retry a permanently missing run on every refresh.
      clearDurableRunSession();
      durableRun.value = null;
    }
    state.value = 'chat';
    setBusy(false);
    if (error instanceof DOMException && error.name === 'AbortError') {
      settleTurn('cancelled', '已取消恢复 AI 项目任务。');
    } else {
      errorMessage.value = error instanceof Error ? error.message : '无法恢复 AI 项目任务';
      settleTurn('error', undefined, errorMessage.value);
    }
  } finally {
    controller = null;
    await scrollToLatest();
  }
}
async function scrollToLatest() {
  await nextTick();
  if (chatLog.value) chatLog.value.scrollTop = chatLog.value.scrollHeight;
}

const PROJECT_REQUEST_BUDGET_BYTES = 64 * 1024;
const MANIFEST_CONTEXT_BUDGET_BYTES = 48 * 1024;
const PROGRAM_CONTEXT_BUDGET_BYTES = 24 * 1024;
function encodedSize(value: unknown) { return new TextEncoder().encode(JSON.stringify(value)).byteLength; }
function boundedContextScalar(value: unknown): string | number | boolean | undefined {
  if (typeof value === 'string') return value.slice(0, 120);
  return typeof value === 'number' || typeof value === 'boolean' ? value : undefined;
}
function fitRequestWithinBudget<T extends {messages: Array<unknown>}>(request: T): T {
  while (encodedSize(request) > PROJECT_REQUEST_BUDGET_BYTES && request.messages.length > 1) {
    request.messages.shift();
  }
  if (encodedSize(request) > PROJECT_REQUEST_BUDGET_BYTES) {
    throw new Error('AI 请求超过 64 KiB 软预算，请缩短需求或减少现有积木后重试');
  }
  return request;
}
function compactProgramContext(value: string) {
  try {
    const parsed = JSON.parse(value) as {scripts?: Array<Record<string, unknown>>; local_variables?: unknown; global_variables?: unknown};
    const blockSummary = (value: unknown): {trigger: string; blockCount: number} => {
      const visited = new Set<object>();
      const visit = (block: unknown): number => {
        if (!block || typeof block !== 'object' || Array.isArray(block) || visited.has(block)) return 0;
        visited.add(block);
        const entry = block as Record<string, unknown>;
        const inputs = entry.inputs && typeof entry.inputs === 'object' && !Array.isArray(entry.inputs) ? Object.values(entry.inputs) : [];
        return 1 + visit(entry.next) + inputs.reduce((count, input) => count + visit(input), 0);
      };
      const code = value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
      return {trigger: typeof code.opcode === 'string' ? code.opcode : 'unknown', blockCount: visit(code)};
    };
    return JSON.stringify({
      summarized: true,
      scripts: (parsed.scripts ?? []).map(script => {
        const summary = blockSummary(script.code);
        return {id: script.id, protected: script.protected, x: script.x, y: script.y, purpose: script.comment ?? summary.trigger, trigger: summary.trigger, block_count: summary.blockCount};
      }),
      local_variables: parsed.local_variables ?? [],
      global_variables: parsed.global_variables ?? []
    });
  } catch {
    return JSON.stringify({summarized: true, scripts: []});
  }
}
function removeListPreviews(context: {targets: Array<{current_program: string}>; shared_state: {lists: Array<Record<string, unknown>>}}) {
  for (const list of context.shared_state.lists) delete list.preview;
  for (const target of context.targets) {
    try {
      const program = JSON.parse(target.current_program) as {local_variables?: Array<Record<string, unknown>>; global_variables?: Array<Record<string, unknown>>};
      for (const value of [...(program.local_variables ?? []), ...(program.global_variables ?? [])]) delete value.preview;
      target.current_program = JSON.stringify(program);
    } catch {
      target.current_program = JSON.stringify({summarized: true, scripts: []});
    }
  }
}
function buildProjectContext(bridge: ScratchGuiBridge, current: AiTargetSnapshot, budgetBytes = MANIFEST_CONTEXT_BUDGET_BYTES) {
  const targets = bridge.currentVm?.runtime.targets ?? [];
  const stage = targets.find(target => target.isStage);
  const sharedValues = Object.values(stage?.variables ?? {});
  const sharedState = {
    variables: sharedValues.filter(item => item.type === '').map(item => ({name: item.name, value: boundedContextScalar(item.value), scope: 'global'})),
    lists: sharedValues.filter(item => item.type === 'list').map(item => {
      const values = Array.isArray(item.value) ? item.value : [];
      return {name: item.name, length: values.length, preview: values.slice(0, 5), scope: 'global'};
    }),
    broadcasts: sharedValues.filter(item => item.type === 'broadcast_msg').map(item => item.name)
  };
  const context = {
    project_id: 'scratch-project',
    version: bridge.projectRevision,
    targets: targets.map((target, index) => {
      const name = String(target.getName?.() ?? target.sprite?.name ?? (target.isStage ? '舞台' : '未命名角色'));
      const isCurrent = target.id === current.targetId;
      const targetContext = isCurrent ? current.context : readAiTargetSnapshot(bridge, 'append', target).context;
      return {
        // This is a request-local logical key, never the VM target ID.
        target_id: isCurrent ? 'current_target' : target.isStage ? 'stage' : 'existing_sprite_' + (index + 1),
        revision: bridge.projectRevision,
        name,
        is_stage: Boolean(target.isStage),
        has_scripts: Boolean(createScratchProgramPort(bridge).read(target).getScripts().length),
        mode: isCurrent ? current.context.mode : 'append',
        costumes: (target.getCostumes?.() ?? []).map(item => String(item.name ?? '')).filter(Boolean),
        backdrops: target.isStage ? (target.getCostumes?.() ?? []).map(item => String(item.name ?? '')).filter(Boolean) : [],
        sounds: (target.getSounds?.() ?? []).map(item => String(item.name ?? '')).filter(Boolean),
        sprite_names: targets.filter(item => !item.isStage && item.isOriginal !== false).map(item => String(item.getName?.() ?? item.sprite?.name ?? '')).filter(Boolean),
        current_program: compactProgramContext(targetContext.current_program)
      };
    }),
    shared_state: sharedState
  };
  if (encodedSize(context) > budgetBytes) removeListPreviews(context);
  if (encodedSize(context) > budgetBytes) {
    throw new Error('当前 Scratch 项目上下文超过预算，请减少现有积木后重试');
  }
  return context;
}

function buildProjectedProjectContext(bridge: ScratchGuiBridge, current: AiTargetSnapshot, manifest: ProjectManifest) {
  const context = buildProjectContext(bridge, current, PROGRAM_CONTEXT_BUDGET_BYTES);
  const created = manifest.resource_ops.filter((operation): operation is Extract<ResourceOp, {op: 'add_sprite' | 'add_builtin_sprite'}> => operation.op === 'add_sprite' || operation.op === 'add_builtin_sprite');
  const spriteNames = [...context.targets.flatMap(target => target.is_stage ? [] : [target.name]), ...created.map(operation => operation.name)];
  const targetByCreatedOp = new Map(created.map(operation => [operation.op_id, operation]));
  for (const operation of created) {
    context.targets.push({
      target_id: 'logical_' + operation.op_id,
      revision: context.version,
      name: operation.name,
      is_stage: false,
      has_scripts: false,
      mode: 'append',
      costumes: operation.op === 'add_sprite' ? operation.costumes.map(costume => costume.name) : [],
      backdrops: [],
      sounds: manifest.resource_ops.filter((item): item is Extract<ResourceOp, {op: 'add_sound'}> => item.op === 'add_sound' && item.target.kind === 'created_sprite' && item.target.op_id === operation.op_id).map(item => item.name),
      sprite_names: spriteNames,
      current_program: JSON.stringify({scripts: [], local_variables: [], global_variables: []})
    });
  }
  const stage = context.targets.find(target => target.is_stage);
  if (stage) {
    stage.backdrops = [...(stage.backdrops ?? []), ...manifest.resource_ops
      .filter((operation): operation is Extract<ResourceOp, {op: 'add_backdrop'}> => operation.op === 'add_backdrop')
      .map(operation => operation.name)];
  }
  for (const target of context.targets) target.sprite_names = spriteNames;
  return {context, targetByCreatedOp};
}

function fullProgramContextForTarget(
  bridge: ScratchGuiBridge,
  snapshot: AiTargetSnapshot,
  targetRef: TargetRef,
  fallback: string,
) {
  if (targetRef.kind === 'current_target') return snapshot.context.current_program;
  if (targetRef.kind === 'created_sprite') return fallback;
  const target = targetRef.kind === 'stage'
    ? bridge.currentVm?.runtime.targets.find(item => item.isStage)
    : bridge.currentVm?.runtime.targets.find(item => !item.isStage && item.isOriginal !== false && String(item.getName?.() ?? item.sprite?.name ?? '').trim().toLocaleLowerCase() === targetRef.name.trim().toLocaleLowerCase());
  return target ? readAiTargetSnapshot(bridge, 'append', target).context.current_program : fallback;
}

function durableEventAsUnified(event: AiRunEvent): UnifiedGenerationEvent | undefined {
  const requestId = durableRun.value?.record.run_id ?? 'durable-run';
  const data = event.payload;
  if (event.type === 'requirements_ready') return {type: 'phase_complete', requestId, phase: 'requirements', data};
  if (event.type === 'task_spec_ready') return {type: 'phase_complete', requestId, phase: 'task_spec', data};
  if (event.type === 'blueprint_ready') return {type: 'phase_complete', requestId, phase: 'blueprint', data};
  if (event.type === 'manifest_ready') return {type: 'phase_complete', requestId, phase: 'plan', data};
  if (event.type === 'program_target_started') return {type: 'phase_start', requestId, phase: 'program', data};
  if (event.type === 'metadata' || event.type === 'shared_state') return {type: event.type, requestId, phase: 'program', data};
  if (event.type === 'cache_hit' || event.type === 'usage') return {type: event.type, requestId, data};
  if (event.type === 'program_script_ready') return {type: 'script_ready', requestId, phase: 'program', data};
  if (event.type === 'program_target_ready') return {type: 'phase_complete', requestId, phase: 'program', data};
  if (event.type === 'repair_started') return {type: 'repair', requestId, phase: 'program', data};
  if (event.type === 'validation_failed') return {type: 'validation_error', requestId, phase: 'program', data};
  if (event.type === 'generation_complete') return {type: 'complete', requestId, data};
  return undefined;
}

function recordDurableEvent(event: AiRunEvent) {
  if (!durableRun.value || durableRun.value.record.run_id !== event.run_id) throw new AiRunApiError('RUN_PROTOCOL_ERROR', '收到不属于当前项目的 AI 任务事件');
  durableRun.value.lastEventId = event.event_id;
  durableRun.value.lastSequence = event.sequence;
}

function throwDurableFailure(event: AiRunEvent) {
  if (event.type !== 'generation_error') return;
  const code = typeof event.payload.code === 'string' ? event.payload.code : 'AI_GENERATION_ERROR';
  const message = typeof event.payload.message === 'string' ? event.payload.message : 'AI 输出未完成，请重新生成';
  throw new AiRunApiError(code, message);
}

function createDurableManifestEventSource(onCreated?: (record: AiRunRecord) => void): ProjectManifestEventSource {
  return async (request, onEvent, signal) => {
    const created = await createAiRun(request, signal);
    durableRun.value = {record: created, lastSequence: 0, awaitingResources: false};
    onCreated?.(created);
    const streamController = new AbortController();
    const abort = () => streamController.abort();
    signal?.addEventListener('abort', abort, {once: true});
    try {
      await streamAiRunEvents(created.run_id, event => {
        recordDurableEvent(event);
        throwDurableFailure(event);
        if (event.type === 'asset_resolution_required') {
          durableRun.value!.awaitingResources = true;
          streamController.abort();
          return;
        }
        const unified = durableEventAsUnified(event);
        if (unified) onEvent(unified);
      }, {signal: streamController.signal});
    } catch (error) {
      if (!durableRun.value?.awaitingResources || signal?.aborted) throw error;
    } finally {
      signal?.removeEventListener('abort', abort);
    }
    const latest = await getAiRun(created.run_id, signal);
    if (latest.status === 'FAILED') throw new AiRunApiError('AI_GENERATION_ERROR', 'AI 输出未完成，请重新生成');
    if (latest.status === 'CANCELLED') throw new DOMException('AI generation cancelled', 'AbortError');
    durableRun.value!.record = latest;
    durableRun.value!.awaitingResources = latest.status === 'AWAITING_TOOL_RESULT';
  };
}

function createResumedDurableManifestEventSource(record: AiRunRecord): ProjectManifestEventSource {
  return async (_request, onEvent, signal) => {
    durableRun.value = {record, lastSequence: 0, awaitingResources: false};
    const streamController = new AbortController();
    const abort = () => streamController.abort();
    signal?.addEventListener('abort', abort, {once: true});
    try {
      await streamAiRunEvents(record.run_id, event => {
        recordDurableEvent(event);
        throwDurableFailure(event);
        if (event.type === 'asset_resolution_required') {
          durableRun.value!.awaitingResources = true;
          streamController.abort();
          return;
        }
        const unified = durableEventAsUnified(event);
        if (unified) onEvent(unified);
      }, {signal: streamController.signal});
    } catch (error) {
      if (!durableRun.value?.awaitingResources || signal?.aborted) throw error;
    } finally {
      signal?.removeEventListener('abort', abort);
    }
    const latest = await getAiRun(record.run_id, signal);
    if (latest.status === 'FAILED') throw new AiRunApiError('AI_GENERATION_ERROR', 'AI 输出未完成，请重新生成');
    if (latest.status === 'CANCELLED') throw new DOMException('AI generation cancelled', 'AbortError');
    durableRun.value!.record = latest;
    durableRun.value!.awaitingResources = latest.status === 'AWAITING_TOOL_RESULT';
  };
}

function sourceForOperation(operation: ResourceOp) {
  if ('source' in operation) return operation.source;
  if (operation.op === 'add_sprite') return operation.costumes[0]?.source;
  return undefined;
}

function preparedResourceSummary(operation: ResourceOp, prepared: PreparedResource, fallback?: ImageGenerationSource) {
  const source = fallback ?? sourceForOperation(operation);
  const value = prepared.value as {assetWidth?: unknown; assetHeight?: unknown; rate?: unknown; sampleCount?: unknown; costumes?: Array<{assetWidth?: unknown; assetHeight?: unknown}>} | null;
  const costume = value && Array.isArray(value.costumes) ? value.costumes[0] : value;
  const width = typeof costume?.assetWidth === 'number' ? costume.assetWidth : undefined;
  const height = typeof costume?.assetHeight === 'number' ? costume.assetHeight : undefined;
  const rate = value && typeof value.rate === 'number' ? value.rate : undefined;
  const sampleCount = value && typeof value.sampleCount === 'number' ? value.sampleCount : undefined;
  return {
    selected_source_kind: source?.kind === 'image_generation' ? 'image_generation' as const : source?.kind === 'audio_generation' ? 'audio_generation' as const : source ? 'scratch_library' as const : 'skipped' as const,
    ...(source ? {normalized_name: 'name' in operation ? operation.name : operation.op_id, normalized_tags: 'tags' in operation && Array.isArray(operation.tags) ? operation.tags : []} : {}),
    ...(width !== undefined && height !== undefined ? {width, height} : {}),
    ...(rate !== undefined && sampleCount !== undefined ? {duration_ms: Math.max(1, Math.round(sampleCount / rate * 1000))} : {}),
  };
}

async function submitDurableResourceResults(
  manifest: ProjectManifest,
  prepared: Map<string, PreparedResource>,
  generationFallbacks: Record<string, ImageGenerationSource>,
  signal?: AbortSignal,
) {
  const active = durableRun.value;
  if (!active?.awaitingResources) return;
  const sentNeeds = new Set<string>();
  for (const operation of manifest.resource_ops) {
    if (!('source_need_id' in operation) || !operation.source_need_id || sentNeeds.has(operation.source_need_id)) continue;
    const resource = prepared.get(operation.op_id);
    if (!resource) throw new Error('资源解析结果缺失：' + operation.op_id);
    sentNeeds.add(operation.source_need_id);
    await submitAiRunToolResult(active.record.run_id, {
      tool_call_id: 'resolve_' + operation.source_need_id,
      expected_project_epoch: active.record.project_epoch,
      need_id: operation.source_need_id,
      op_id: operation.op_id,
      ...preparedResourceSummary(operation, resource, generationFallbacks[operation.op_id]),
    }, signal);
  }
  active.awaitingResources = false;
}

async function submitDurableProgramContexts(
  draftValue: Awaited<ReturnType<UnifiedGenerationRunner['prepare']>>,
  signal?: AbortSignal,
) {
  const active = durableRun.value;
  if (!active || !props.bridge || !draftValue.manifest) throw new Error('持久化 AI 任务尚未准备完成');
  const {context, targetByCreatedOp} = buildProjectedProjectContext(props.bridge, draftValue.snapshot, draftValue.manifest);
  const contexts = draftValue.manifest.program_targets.map((programTarget, targetIndex) => {
    const targetRef = programTarget.target;
    let target: typeof context.targets[number] | undefined;
    if (targetRef.kind === 'current_target') {
      target = context.targets.find(item => item.target_id === 'current_target');
    } else if (targetRef.kind === 'stage') {
      target = context.targets.find(item => item.is_stage);
    } else if (targetRef.kind === 'existing_sprite') {
      target = context.targets.find(item => !item.is_stage && item.name.trim().toLocaleLowerCase() === targetRef.name.trim().toLocaleLowerCase());
    } else {
      const created = targetByCreatedOp.get(targetRef.op_id);
      target = created ? context.targets.find(item => item.target_id === 'logical_' + created.op_id) : undefined;
    }
    if (!target) throw new Error('投影项目上下文缺少程序目标：' + targetIndex);
    return {
      target_index: targetIndex,
      target: {
        name: target.name,
        is_stage: target.is_stage,
        has_scripts: target.has_scripts,
        mode: target.mode,
        costumes: target.costumes,
        backdrops: target.backdrops,
        sounds: target.sounds,
        sprite_names: target.sprite_names,
        current_program: fullProgramContextForTarget(props.bridge!, draftValue.snapshot, targetRef, target.current_program),
      } satisfies AiBlocksTargetContext,
    };
  });
  for (const context of contexts) {
    if (encodedSize({expected_project_epoch: active.record.project_epoch, ...context}) > PROJECT_REQUEST_BUDGET_BYTES) {
      throw new Error('单个项目目标程序上下文超过 64 KiB 软预算，请减少现有积木后重试');
    }
    await submitAiRunProgramContext(active.record.run_id, active.record.project_epoch, context, signal);
  }
}

async function prepareProjectPrograms(
  draftValue: Awaited<ReturnType<UnifiedGenerationRunner['prepare']>>,
  signal?: AbortSignal,
  onEvent?: (event: UnifiedGenerationEvent) => void
) {
  const bridge = props.bridge;
  const manifest = draftValue.manifest;
  const taskSpec = draftValue.taskSpec;
  const blueprint = draftValue.blueprint;
  const sourceContract = draftValue.sourceContract;
  const sourcePipeline = draftValue.request.pipeline_version === SOURCE_PIPELINE_VERSION;
  if (!bridge || !manifest || !blueprint || (sourcePipeline ? !sourceContract : !taskSpec)) return;
  const {context: projectContext, targetByCreatedOp} = buildProjectedProjectContext(bridge, draftValue.snapshot, manifest);
  const generateTarget = async (targetIndex: number): Promise<UnifiedProgramDraft> => {
    const programTarget = manifest.program_targets[targetIndex];
    if (!programTarget) throw new Error('项目程序目标不存在：' + targetIndex);
    let target: typeof projectContext.targets[number] | undefined;
    if (programTarget.target.kind === 'current_target') {
      target = projectContext.targets.find(item => item.target_id === 'current_target');
    } else if (programTarget.target.kind === 'stage') {
      target = projectContext.targets.find(item => item.is_stage);
    } else if (programTarget.target.kind === 'existing_sprite') {
      const existingName = programTarget.target.name;
      target = projectContext.targets.find(item => !item.is_stage && item.name.trim().toLocaleLowerCase() === existingName.trim().toLocaleLowerCase());
    } else {
      const created = targetByCreatedOp.get(programTarget.target.op_id);
      target = created ? projectContext.targets.find(item => item.target_id === 'logical_' + created.op_id) : undefined;
    }
    if (!target) throw new Error('投影项目上下文缺少程序目标：' + targetIndex);
    const targetContext: AiBlocksTargetContext = {
        name: target.name,
        is_stage: target.is_stage,
        has_scripts: target.has_scripts,
        mode: programTarget.mode,
        costumes: target.costumes,
        backdrops: target.backdrops,
        sounds: target.sounds,
        sprite_names: target.sprite_names,
        current_program: fullProgramContextForTarget(bridge, draftValue.snapshot, programTarget.target, target.current_program)
    };
    let program: ProgramIR | undefined;
    const receive = (event: UnifiedGenerationEvent) => {
      onEvent?.(event);
      if (event.type === 'complete' && event.data.program && typeof event.data.program === 'object' && !Array.isArray(event.data.program)) program = event.data.program as ProgramIR;
    };
    if (sourcePipeline) {
      const request: SourceProgramGenerationRequest = {
        request: draftValue.request,
        source_contract: sourceContract!,
        target_index: targetIndex,
        target: targetContext,
      };
      if (encodedSize(request) > PROJECT_REQUEST_BUDGET_BYTES) throw new Error('源码程序请求超过 64 KiB 软预算，请减少已有积木后重试');
      await streamSourceProgram(request, receive, signal);
    } else {
      const request: ProjectProgramGenerationRequest = {
        ...draftValue.request,
        messages: [...draftValue.request.messages],
        scope: 'project',
        target: targetContext,
        project_context: projectContext,
        output_contract: 'project_program',
        requirements: draftValue.requirements,
        task_spec: taskSpec!,
        blueprint,
        plan: draftValue.plan,
        manifest,
        program_target: programTarget,
        target_index: targetIndex,
        request_id: createId()
      };
      fitRequestWithinBudget(request);
      await streamProjectProgram(request, receive, signal);
    }
    if (!program) throw new Error('项目目标未返回有效 ProgramIR：' + targetIndex);
    return {
      targetIndex,
      metadata: {
        schema_version: program.schema_version,
        summary: program.summary,
        remove_script_ids: program.remove_script_ids,
        procedures: program.procedures ?? [],
      },
      state: {variables: program.variables, lists: program.lists, broadcasts: program.broadcasts},
      scripts: program.scripts,
      program
    };
  };
  const programs = new Map<number, UnifiedProgramDraft>();
  const layers = sourcePipeline ? [manifest.program_targets.map((_, index) => index)] : projectProgramGenerationLayers(blueprint, manifest);
  for (const layer of layers) {
    // A provider request is expensive; run only independent jobs, two at a time.
    for (let offset = 0; offset < layer.length; offset += 2) {
      const batch = await Promise.all(layer.slice(offset, offset + 2).map(generateTarget));
      for (const item of batch) programs.set(item.targetIndex, item);
    }
  }
  draftValue.programs = [...programs.values()].sort((left, right) => left.targetIndex - right.targetIndex);
}

async function prepareDurableProjectPrograms(
  draftValue: Awaited<ReturnType<UnifiedGenerationRunner['prepare']>>,
  signal?: AbortSignal,
  onEvent?: (event: UnifiedGenerationEvent) => void,
) {
  const active = durableRun.value;
  if (!active || !draftValue.manifest) throw new Error('持久化 AI 任务尚未准备完成');
  const metadata = new Map<number, Record<string, unknown>>();
  const sharedState = new Map<number, Record<string, unknown>>();
  const scripts = new Map<number, ScriptIR[]>();
  const totals = new Map<number, number>();
  await streamAiRunEvents(active.record.run_id, event => {
    recordDurableEvent(event);
    throwDurableFailure(event);
    const unified = durableEventAsUnified(event);
    if (unified) onEvent?.(unified);
    const targetIndex = typeof event.payload.target_index === 'number' ? event.payload.target_index : undefined;
    if (event.type === 'metadata' && targetIndex !== undefined) {
      if (metadata.has(targetIndex)) throw new AiRunApiError('RUN_PROTOCOL_ERROR', '项目目标重复输出 metadata');
      metadata.set(targetIndex, event.payload);
    } else if (event.type === 'shared_state' && targetIndex !== undefined) {
      if (sharedState.has(targetIndex)) throw new AiRunApiError('RUN_PROTOCOL_ERROR', '项目目标重复输出 shared_state');
      sharedState.set(targetIndex, event.payload);
    } else if (event.type === 'program_script_ready' && targetIndex !== undefined) {
      const index = event.payload.index;
      const total = event.payload.total;
      const script = event.payload.script;
      if (typeof index !== 'number' || !Number.isInteger(index) || typeof total !== 'number' || !Number.isInteger(total) || total < 1 || !script || typeof script !== 'object' || Array.isArray(script)) {
        throw new AiRunApiError('RUN_PROTOCOL_ERROR', '项目程序脚本事件无效');
      }
      const previousTotal = totals.get(targetIndex);
      if (previousTotal !== undefined && previousTotal !== total) throw new AiRunApiError('RUN_PROTOCOL_ERROR', '项目程序脚本总数不一致');
      const collected = scripts.get(targetIndex) ?? [];
      if (index !== collected.length || index >= total) throw new AiRunApiError('RUN_PROTOCOL_ERROR', '项目程序脚本序号无效');
      totals.set(targetIndex, total);
      collected.push(script as ScriptIR);
      scripts.set(targetIndex, collected);
    } else if (event.type === 'program_target_ready' && targetIndex !== undefined) {
      const expected = event.payload.script_count;
      if (!Number.isInteger(expected) || expected !== (scripts.get(targetIndex)?.length ?? 0) || expected !== totals.get(targetIndex)) {
        throw new AiRunApiError('RUN_PROTOCOL_ERROR', '项目目标程序完成事件无效');
      }
    }
  }, {lastEventId: active.lastEventId, lastSequence: active.lastSequence, signal});
  const latest = await getAiRun(active.record.run_id, signal);
  if (latest.status !== 'READY_TO_COMMIT') throw new AiRunApiError('RUN_STATE_CONFLICT', 'AI 任务尚未完成项目程序生成');
  active.record = latest;
  const programs: UnifiedProgramDraft[] = [];
  for (let index = 0; index < draftValue.manifest.program_targets.length; index += 1) {
    const item = metadata.get(index);
    const stateValue = sharedState.get(index);
    const targetScripts = scripts.get(index);
    if (!item || !stateValue || !targetScripts || totals.get(index) !== targetScripts.length) {
      throw new AiRunApiError('RUN_PROTOCOL_ERROR', '持久化任务缺少完整项目程序：' + index);
    }
    const schemaVersion = item.schema_version;
    if (schemaVersion !== 1 && schemaVersion !== 2 || typeof item.summary !== 'string' || !Array.isArray(item.remove_script_ids)
      || (item.procedures !== undefined && !Array.isArray(item.procedures))
      || !Array.isArray(stateValue.variables) || !Array.isArray(stateValue.lists) || !Array.isArray(stateValue.broadcasts)) {
      throw new AiRunApiError('RUN_PROTOCOL_ERROR', '持久化任务程序合同无效：' + index);
    }
    const program: ProgramIR = {
      schema_version: schemaVersion,
      summary: item.summary,
      remove_script_ids: item.remove_script_ids as string[],
      variables: stateValue.variables as ProgramIR['variables'],
      lists: stateValue.lists as ProgramIR['lists'],
      broadcasts: stateValue.broadcasts as string[],
      procedures: Array.isArray(item.procedures) ? item.procedures as NonNullable<ProgramIR['procedures']> : [],
      scripts: targetScripts,
    };
    programs.push({
      targetIndex: index,
      metadata: {
        schema_version: schemaVersion,
        summary: item.summary,
        remove_script_ids: item.remove_script_ids as string[],
        procedures: Array.isArray(item.procedures) ? item.procedures as NonNullable<ProgramIR['procedures']> : [],
      },
      state: {variables: program.variables, lists: program.lists, broadcasts: program.broadcasts},
      scripts: targetScripts,
      program,
    });
  }
  draftValue.programs = programs;
}
function requestMessages(text: string) {
  return [...messages.value, {role: 'user' as const, content: text}]
    .map(message => ({role: message.role, content: message.content}));
}

function buildRequest(text: string): UnifiedGenerationRequest {
  if (!props.bridge || !snapshot.value) throw new Error('Scratch 编辑器尚未就绪');
  const request: UnifiedGenerationRequest = {
    ...(runtimeCapabilities.value?.sourcePipeline ? {pipeline_version: SOURCE_PIPELINE_VERSION} : {}),
    model_tier: modelTier.value,
    session_id: sessionId.value,
    request_id: createId(),
    messages: requestMessages(text),
    target: scope.value === 'project' ? {...snapshot.value.context, current_program: compactProgramContext(snapshot.value.context.current_program)} : snapshot.value.context,
    thinking: {enabled: thinkingEnabled.value, strength: thinkingStrength.value},
    scope: scope.value,
    asset_policy: assetPolicy.value,
    proceed_with_defaults: true,
    permitted_extensions: textToSpeechConsent.value && textToSpeechExtensionAvailable.value ? ['text2speech'] : [],
  };
  if (scope.value === 'project') request.project_context = buildProjectContext(props.bridge, snapshot.value);
  return fitRequestWithinBudget(request);
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
  if (event.type === 'usage') {
    const totalTokens = Number(event.data.total_tokens ?? Number(event.data.input_tokens ?? 0) + Number(event.data.output_tokens ?? 0));
    if (Number.isFinite(totalTokens)) generationMetrics.value.totalTokens += Math.max(0, totalTokens);
  }
  if (event.type === 'reasoning_delta') {
    const card = turnCard.value;
    if (card) card.reasoning = (card.reasoning ?? '') + String(event.data.text ?? '');
  }
  if (event.type === 'phase_start') {
    phaseName.value = event.phase ?? '';
    phaseMessage.value = String(event.data.message ?? '');
    if (event.phase === 'program' && typeof event.data.target_index === 'number') {
      const targetIndex = event.data.target_index;
      const targetCount = typeof event.data.target_count === 'number' ? event.data.target_count : 0;
      const targetName = String(event.data.target_name ?? '');
      phaseMessage.value = targetName || (targetCount > 1 ? '目标 ' + (targetIndex + 1) + '/' + targetCount : '当前目标');
    }
  }
  if (event.type === 'phase_complete' && event.phase === 'requirements') {
    requirements.value = event.data as typeof requirements.value;
  }
  if (event.type === 'phase_complete' && event.phase === 'plan') {
    const card = turnCard.value;
    const steps = Array.isArray(event.data.steps) ? event.data.steps.filter((item): item is string => typeof item === 'string') : [];
    if (card) card.plan = steps;
  }
  if (event.type === 'script_ready') {
    const targetIndex = Number(event.data.target_index ?? 0);
    const targetCount = Number(event.data.target_count ?? 0);
    const scriptIndex = Number(event.data.index ?? 0);
    const scriptTotal = Number(event.data.total ?? 0);
    const targetLabel = String(event.data.target_name ?? '') || (targetCount > 1 ? '目标 ' + (targetIndex + 1) + '/' + targetCount : '当前目标');
    const isCommittingProject = state.value === 'committing' && scope.value === 'project' && targetCount > 0;
    progress.value = isCommittingProject
      ? {current: Math.min(targetIndex, targetCount), total: targetCount, label: targetLabel + ' · 脚本 ' + (scriptIndex + 1) + '/' + scriptTotal}
      : {current: scriptIndex + 1, total: scriptTotal, label: targetLabel + ' · 脚本 ' + (scriptIndex + 1) + '/' + scriptTotal};
  }
  if (event.type === 'phase_complete' && event.phase === 'program') {
    const targetIndex = Number(event.data.target_index ?? 0);
    const targetCount = Number(event.data.target_count ?? 0);
    const targetLabel = String(event.data.target_name ?? '') || (targetCount > 1 ? '目标 ' + (targetIndex + 1) + '/' + targetCount : '当前目标');
    const isCommittingProject = state.value === 'committing' && scope.value === 'project' && targetCount > 0;
    progress.value = isCommittingProject
      ? {current: Math.min(targetIndex, targetCount), total: targetCount, label: targetLabel + ' · 程序已校验'}
      : {current: progress.value.total, total: progress.value.total, label: targetLabel + ' · 程序已校验'};
  }
  if (event.type === 'repair') {
    const attempt = Number(event.data.attempt ?? 1);
    const maxRounds = Number(event.data.max_rounds ?? 1);
    const phase = String(event.data.phase ?? '');
    phaseName.value = 'repair';
    phaseMessage.value = '校验未通过，AI 正在自动修正（第 ' + attempt + '/' + maxRounds + ' 次）';
    noticeMessage.value = 'AI 输出未通过校验，正在自动修正' + (phase ? '（' + phase + ' 阶段' : '') + '，请稍候。' + (attempt >= maxRounds ? '若仍有问题将显示具体原因。' : '');
  }
  void scrollToLatest();
}
async function sendMessage() {
  if (props.commitBlocked) {
    errorMessage.value = '存在待恢复的 AI 项目提交，新的 AI 写入已暂停。';
    return;
  }
  const text = draft.value.trim();
  const initialBridge = props.bridge;
  const initialSnapshot = snapshot.value;
  if (!text || !initialBridge || !initialSnapshot || isBusy.value || capabilityLoading.value) return;
  capabilityLoading.value = true;
  let request: UnifiedGenerationRequest | undefined;
  let useDurableRun = false;
  try {
    await refreshRuntimeCapabilities();
    if (props.bridge !== initialBridge || snapshot.value !== initialSnapshot || isBusy.value) return;
    if (!aiCommitAvailable.value) {
      errorMessage.value = 'AI 项目写入已被当前部署暂停。';
      return;
    }
    request = buildRequest(text);
    useDurableRun = durableRunsEnabled && request.scope === 'project' && await supportsDurableRuns();
  } catch (error: unknown) {
    errorMessage.value = error instanceof Error ? error.message : '无法读取 AI 运行能力';
    return;
  } finally {
    capabilityLoading.value = false;
  }
  if (!request) return;
  const durableSessionBase = useDurableRun ? {
    snapshot: serializableTargetSnapshot(snapshot.value),
    projectSnapshot: serializableProjectSnapshot(readAiProjectSnapshot(props.bridge)),
  } : undefined;
  durableRun.value = null;
  draft.value = '';
  messages.value.push({role: 'user', content: text});
  pushTurnCard();
  requestId.value = request.request_id;
  generationMetrics.value = {totalTokens: 0};
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
    const runner = createGenerationRunner(props.bridge);
    const nextDraft = await runner.prepare(
      request,
      handleEvent,
      controller.signal,
      useDurableRun ? createDurableManifestEventSource(record => {
        saveDurableRunSession({
          version: 1,
          runId: record.run_id,
          // The embedded editor currently has one local project storage slot;
          // keep this key aligned with checkpoint recovery across refreshes.
          projectId: LOCAL_PROJECT_STORAGE_KEY,
          request,
          snapshot: durableSessionBase!.snapshot,
          projectSnapshot: durableSessionBase!.projectSnapshot,
        });
      }) : undefined,
    );
    if (nextDraft.manifest && !useDurableRun) {
      phaseName.value = 'program';
      progress.value = {current: 0, total: nextDraft.manifest.program_targets.length, label: '正在生成全部目标程序…'};
      await prepareProjectPrograms(nextDraft, controller.signal, handleEvent);
    }
    const activeDurableRun = durableRun.value as DurableRunState | null;
    if (nextDraft.manifest && useDurableRun && activeDurableRun?.awaitingResources && nextDraft.manifest.resource_ops.length === 0) {
      phaseName.value = 'program';
      progress.value = {current: 0, total: nextDraft.manifest.program_targets.length, label: '正在生成全部目标程序…'};
      await submitDurableProgramContexts(nextDraft, controller.signal);
      activeDurableRun.awaitingResources = false;
      await prepareDurableProjectPrograms(nextDraft, controller.signal, handleEvent);
    }
    generationDraft.value = nextDraft;
    requirements.value = nextDraft.requirements;
    projectManifest.value = nextDraft.manifest?.resource_ops.length ? nextDraft.manifest : null;
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
      projectManifest.value = null;
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
  if (!aiCommitAvailable.value) throw new Error('AI 项目写入已被当前部署暂停');
  if (!props.bridge) throw new Error('Scratch 编辑器尚未就绪');
  const durableForCommit = durableRun.value;
  let telemetry: CommitTelemetry | undefined;
  const reportTelemetry = () => {
    if (!durableForCommit || !telemetry) return;
    // runner.commit has already settled, so its project lock is released before
    // this best-effort network report can begin.
    void submitAiRunCommitMetric(durableForCommit.record.run_id, {
      expected_project_epoch: durableForCommit.record.project_epoch,
      outcome: telemetry.outcome,
      commit_duration_ms: telemetry.commitDurationMs,
      verify_duration_ms: telemetry.verifyDurationMs,
      behavior_test_count: telemetry.behaviorTestCount,
      behavior_passed_count: telemetry.behaviorPassedCount,
      behavior_must_count: telemetry.behaviorMustCount,
      behavior_must_passed_count: telemetry.behaviorMustPassedCount,
      ...(telemetry.errorCode ? {error_code: telemetry.errorCode} : {}),
    }).catch(() => undefined);
  };
  try {
    const result = await runner.commit(draftValue, {
      preparedResources: resources,
      resourceExecutor: draftValue.manifest ? createProjectResourceExecutor({bridge: props.bridge, currentTargetId: draftValue.snapshot.targetId}) : undefined,
      signal,
      generatedAssets,
      ...(durableForCommit ? {runId: durableForCommit.record.run_id} : {}),
      checkpointEnabled: runtimeCapabilities.value?.crashCheckpoint !== false,
      onProgress: value => { progress.value = value; },
      onCommitTelemetry: value => { telemetry = value; },
    });
    reportTelemetry();
    generationResult.value = result;
    generationDraft.value = null;
    if (durableForCommit && durableRun.value?.record.run_id === durableForCommit.record.run_id) {
      clearDurableRunSession();
      durableRun.value = null;
    }
    return result;
  } catch (error) {
    reportTelemetry();
    throw error;
  }
}
function openResourceReview() {
  if (generationDraft.value?.manifest) projectManifest.value = generationDraft.value.manifest;
}
async function confirmProjectManifest(confirmedGenerationIds: string[], selections: Record<string, ScratchSpriteItem | ScratchLibraryItem | ScratchSoundItem>, generationFallbacks: Record<string, ImageGenerationSource> = {}, reviewMetrics: AiRunResourceReviewMetricBucket[] = []) {
  const draftValue = generationDraft.value;
  if (props.commitBlocked || !aiCommitAvailable.value) return;
  if (!draftValue?.manifest || !props.bridge || isBusy.value) return;
  // Close the review overlay before awaiting asset preparation or program generation.
  projectManifest.value = null;
  noticeMessage.value = '';
  state.value = 'preparing';
  phaseName.value = 'preparing';
  progress.value = {current: 0, total: 0, label: '正在准备项目资源…'};
  setBusy(true, '正在准备项目资源');
  errorMessage.value = '';
  controller = new AbortController();
  try {
    if ((!audioGenerationFeatureEnabled || runtimeCapabilities.value?.audioGeneration !== true)
      && draftValue.manifest.resource_ops.some(operation => 'source' in operation && operation.source.kind === 'audio_generation')) {
      throw new Error('音频生成能力尚未开放，请为声音选择 Scratch 官方素材');
    }
    const durableForReview = durableRun.value;
    if (durableForReview && reviewMetrics.length) {
      void submitAiRunResourceReviewMetric(durableForReview.record.run_id, {
        expected_project_epoch: durableForReview.record.project_epoch,
        buckets: reviewMetrics,
      }).catch(() => undefined);
    }
    const prepared = await prepareProjectResources({
      plan: draftValue.manifest,
      assetPolicy: draftValue.request.asset_policy,
      selections: new Map(Object.entries(selections)) as never,
      generationFallbacks: new Map(Object.entries(generationFallbacks)),
      confirmGeneration: request => confirmedGenerationIds.includes(request.resource_key),
      signal: controller.signal,
      onProgress: (current, total, label) => { progress.value = {current, total, label}; }
    });
    preparedResources.value = prepared.prepared;
    if (durableRun.value?.awaitingResources) {
      phaseName.value = 'program';
      progress.value = {current: 0, total: draftValue.manifest.program_targets.length, label: '正在生成全部目标程序…'};
      await submitDurableProgramContexts(draftValue, controller.signal);
      await submitDurableResourceResults(draftValue.manifest, prepared.prepared, generationFallbacks, controller.signal);
      await prepareDurableProjectPrograms(draftValue, controller.signal, handleEvent);
    }
    state.value = 'committing';
    phaseName.value = 'committing';
    progress.value = {current: 0, total: 0, label: '正在写入积木与资源…'};
    const result = await commitDraft(draftValue, createGenerationRunner(props.bridge), controller.signal, prepared.prepared, prepared.generatedAssets);
    projectManifest.value = null;
    state.value = 'chat';
    phaseName.value = '';
    settleTurn('success', '已完成统一生成并写入 ' + result.programs.length + ' 个目标。');
    noticeMessage.value = '资源与全部目标程序已在同一事务中提交。';
    setBusy(false);
    saveCurrentConversation();
  } catch (error: unknown) {
    state.value = 'review';
    phaseName.value = 'review';
    noticeMessage.value = '';
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
  const wasBusy = isBusy.value;
  if (wasBusy) controller?.abort();
  if (durableRun.value?.awaitingResources) {
    const run = durableRun.value;
    void cancelAiRun(run.record.run_id, run.record.project_epoch)
      .then(() => {
        if (durableRun.value?.record.run_id !== run.record.run_id) return;
        durableRun.value = null;
        clearDurableRunSession();
      })
      .catch(() => {
        errorMessage.value = '取消请求未确认，任务恢复记录已保留。';
      });
  }
  projectManifest.value = null;
  const card = cardForTurn();
  if (!wasBusy && card && card.status === 'streaming') {
    settleTurn('success', '程序草稿已校验，可在“重新打开资源确认”继续提交。');
  }
}
function cancel() {
  controller?.abort();
  if (durableRun.value && !durableRun.value.record.terminal) {
    const run = durableRun.value;
    void cancelAiRun(run.record.run_id, run.record.project_epoch)
      .then(() => {
        if (durableRun.value?.record.run_id !== run.record.run_id) return;
        durableRun.value = null;
        clearDurableRunSession();
      })
      .catch(() => {
        errorMessage.value = '取消请求未确认，任务恢复记录已保留。';
      });
  }
}
async function undoGeneration() {
  if (!props.bridge || !generationResult.value || isBusy.value) return;
  isUndoing.value = true;
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
    isUndoing.value = false;
    setBusy(false);
  }
}
function runProject() { props.bridge?.run(); }
function startNewConversation() { if (!isBusy.value && snapshot.value) refreshTarget(true); }
function clearConversationHistory() { conversations.value = []; saveConversations(); }
function deleteConversation(id: string) {
  conversations.value = conversations.value.filter(item => item.id !== id);
  saveConversations();
}
async function restoreConversation(conversation: StoredConversation) {
  if (isBusy.value) return;
  isRestoringConversation = true;
  try {
    if (props.bridge?.currentVm) {
      const targets = props.bridge.currentVm.runtime?.targets ?? [];
      let matchedTarget = targets.find(t => t.id === conversation.targetId && t.isOriginal !== false);
      if (!matchedTarget) {
        matchedTarget = targets.find(t => Boolean(t.isStage) === Boolean(conversation.isStage) && (t.isStage || (t.getName?.() ?? t.sprite?.name ?? '') === conversation.targetName) && t.isOriginal !== false);
      }
      if (matchedTarget && props.bridge.currentVm.editingTarget?.id !== matchedTarget.id) {
        props.bridge.currentVm.setEditingTarget(matchedTarget.id);
        props.bridge.refreshWorkspace();
      }
      try {
        snapshot.value = readAiTargetSnapshot(props.bridge, conversation.mode);
      } catch {
        // 保留现有 snapshot
      }
    }
    turnCard.value = null;
    turnCardIndex.value = -1;
    sessionId.value = conversation.id;
    messages.value = conversation.messages.map(message => message.status === 'streaming' ? {...message, status: 'success'} : message);
    mode.value = conversation.mode;
    scope.value = conversation.scope;
    historyOpen.value = false;
    noticeMessage.value = '';
    errorMessage.value = '';
    phaseName.value = '';
    phaseMessage.value = '';
    progress.value = {current: 0, total: 0, label: ''};
    requirements.value = null;
    generationDraft.value = null;
    projectManifest.value = null;
    generationResult.value = null;
    void scrollToLatest();
  } finally {
    await nextTick();
    isRestoringConversation = false;
  }
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
function persistThinking() {
  try { localStorage.setItem(THINKING_STORAGE_KEY, JSON.stringify({enabled: thinkingEnabled.value, strength: thinkingStrength.value})); } catch { /* optional */ }
}

watch(() => props.bridge, attachBridge);
watch([mode, scope], () => { if (props.bridge && snapshot.value && !isBusy.value) refreshTarget(); });
// Advanced (DeepSeek) defaults to low reasoning to keep multi-phase generation responsive.
watch(modelTier, tier => {
  if (tier === 'advanced') thinkingStrength.value = 'low';
});
watch([thinkingEnabled, thinkingStrength], persistThinking);
watch([messages, mode], () => {
  if (saveTimer !== undefined) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    saveTimer = undefined;
    saveCurrentConversation();
  }, 600);
}, {deep: true});
onMounted(() => {
  void refreshRuntimeCapabilities();
  attachBridge();
});
watch(textToSpeechExtensionAvailable, available => {
  if (!available) textToSpeechConsent.value = false;
});
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
      <div class="ai-blocks-panel__identity">
        <span class="ai-blocks-panel__mark"><Blocks :size="16" aria-hidden="true" /></span>
        <h2>AI 写积木</h2>
        <span v-if="snapshot" class="ai-blocks-target">{{ snapshot.isStage ? '舞台' : snapshot.targetName }}</span>
      </div>
      <div class="ai-blocks-panel__header-actions">
        <button class="icon-button" type="button" :class="{'is-active': historyOpen}" :disabled="isBusy" title="查看历史对话" aria-label="查看历史对话" @click="historyOpen = !historyOpen"><History :size="16" /></button>
        <button class="icon-button" type="button" :disabled="isBusy || !snapshot" title="新建对话" aria-label="新建对话" @click="startNewConversation"><Plus :size="17" /></button>
      </div>
    </header>
    <section v-if="historyOpen" class="ai-blocks-history" aria-label="历史对话">
      <div class="ai-blocks-history__heading"><div><strong>历史对话</strong><span>{{ conversations.length }} 条记录</span></div><button v-if="conversations.length" class="button button--quiet" type="button" :disabled="isBusy" @click="clearConversationHistory"><Trash2 :size="14" />清空</button></div>
      <div v-if="!conversations.length" class="ai-blocks-history__empty"><MessageSquare :size="18" /><span>发送第一条需求后，对话会自动保存在这里</span></div>
      <div v-else class="ai-blocks-history__list">
        <div
          v-for="conversation in conversations"
          :key="conversation.id"
          class="ai-blocks-history__item-row"
          :class="{'is-current': sessionId === conversation.id}"
        >
          <button
            class="ai-blocks-history__item"
            type="button"
            :disabled="isBusy"
            :title="'恢复对话：' + conversation.title"
            @click="restoreConversation(conversation)"
          >
            <MessageSquare :size="14" />
            <span>
              <strong>{{ conversation.title }}</strong>
              <small>{{ conversation.isStage ? '舞台' : '角色' }} · {{ conversation.targetName }} · {{ formatConversationTime(conversation.updatedAt) }}</small>
            </span>
          </button>
          <button
            class="icon-button ai-blocks-history__item-remove"
            type="button"
            :disabled="isBusy"
            title="删除此记录"
            aria-label="删除此记录"
            @click.stop="deleteConversation(conversation.id)"
          >
            <Trash2 :size="12" />
          </button>
        </div>
      </div>
    </section>
    <section class="ai-blocks-context" aria-label="生成范围与写入模式">
      <fieldset class="ai-blocks-scope">
        <legend class="visually-hidden">生成范围</legend>
        <label :class="{'is-active': scope === 'single_sprite'}"><input v-model="scope" type="radio" value="single_sprite" :disabled="isBusy" /><span>当前角色</span></label>
        <label v-if="projectAssetsAvailable" :class="{'is-active': scope === 'project'}"><input v-model="scope" type="radio" value="project" :disabled="isBusy" /><span>整个项目</span></label>
      </fieldset>
      <label class="ai-blocks-mode-select"><span class="visually-hidden">写入模式</span><select v-model="mode" aria-label="写入模式" :disabled="isBusy"><option value="modify">修改当前代码</option><option value="append">旁路新增</option><option value="rewrite">清空重写</option></select></label>
      <div v-if="scope === 'project'" class="ai-blocks-project-options">
        <label class="ai-blocks-policy"><span>素材策略</span><select v-model="assetPolicy" aria-label="素材策略" :disabled="isBusy"><option value="builtin_first">内置素材优先</option><option value="existing_only">仅已有素材</option><option value="allow_generation">允许生图</option></select></label>
        <label v-if="textToSpeechExtensionAvailable" class="ai-blocks-consent"><input v-model="textToSpeechConsent" type="checkbox" :disabled="isBusy" /><span>允许将朗读文本发送至 Scratch 语音服务</span></label>
      </div>
    </section>
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
            <details v-if="message.reasoning" class="ai-blocks-message__reasoning"><summary><BrainCircuit :size="14" />思考过程</summary><p>{{ message.reasoning }}</p></details>
            <details v-if="message.plan && message.plan.length" class="ai-blocks-message__plan"><summary><ListOrdered :size="14" />计划大纲</summary><ol><li v-for="step in message.plan" :key="step">{{ step }}</li></ol></details>
            <p v-if="message.error" class="ai-blocks-message__error"><AlertTriangle :size="14" /><span>{{ message.error }}</span></p>
            <span v-if="message.status && message.status !== 'streaming'" class="ai-blocks-message__status" :class="'is-' + message.status">{{ statusLabel(message.status) }}</span>
          </template>
        </div>
      </article>
      <div v-if="generationResult" class="ai-blocks-generation-actions"><button class="button button--primary" type="button" @click="runProject"><Play :size="15" />运行查看</button><button class="button button--quiet" type="button" @click="undoGeneration"><Undo2 :size="15" />撤销生成</button></div>
    </div>
    <form class="ai-blocks-composer" @submit.prevent="sendMessage">
      <div class="ai-blocks-composer__settings">
        <fieldset class="ai-blocks-model-options"><legend class="visually-hidden">模型档位</legend><label v-for="item in AI_BLOCKS_MODEL_OPTIONS" :key="item.value" :class="{'is-active': modelTier === item.value}" :title="item.model"><input v-model="modelTier" type="radio" :value="item.value" :disabled="isBusy" /><span>{{ item.label }}</span></label></fieldset>
        <details class="ai-blocks-thinking-control">
          <summary><BrainCircuit :size="14" /><span>思考</span><strong>{{ thinkingLabel }}</strong></summary>
          <div class="ai-blocks-thinking-popover">
            <div><span>思考强度</span><strong>{{ thinkingLabel }}<template v-if="thinkingEnabled">，{{ thinkingHint }}</template></strong></div>
            <input v-model.number="thinkingLevel" type="range" min="0" :max="AI_BLOCKS_THINKING_OPTIONS.length" step="1" :disabled="isBusy" aria-label="思考强度" />
            <div class="ai-blocks-thinking-scale"><span>关</span><span v-for="item in AI_BLOCKS_THINKING_OPTIONS" :key="item.value">{{ item.label }}</span></div>
          </div>
        </details>
      </div>
      <div class="ai-blocks-composer__input"><textarea v-model="draft" rows="3" maxlength="2000" :disabled="isBusy" aria-label="描述要实现的积木功能" placeholder="描述希望实现的效果..." @keydown.enter.exact.prevent="sendMessage" /><button class="icon-button ai-blocks-send" type="submit" :disabled="!canSend" title="发送" aria-label="发送需求"><Send :size="16" /></button></div>
      <div class="ai-blocks-composer__token-usage" title="本次生成累计 Token"><Coins :size="12" /><strong>{{ tokenTotalLabel }}</strong> tokens</div>
    </form>
    <ResourcePlanReview :open="Boolean(projectManifest)" :plan="projectManifest" :asset-policy="assetPolicy" :runtime-auto-match-enabled="runtimeCapabilities?.assetAutoMatch === true" :runtime-auto-match-min-score="runtimeCapabilities?.assetAutoMatchMinScore" :runtime-auto-match-min-margin="runtimeCapabilities?.assetAutoMatchMinMargin" @cancel="cancelResourceReview" @confirm="confirmProjectManifest" />
  </section>
</template>
