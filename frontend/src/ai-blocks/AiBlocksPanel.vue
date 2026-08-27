<script setup lang="ts">
import {computed, nextTick, onBeforeUnmount, onMounted, ref, watch} from 'vue';
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
  Undo2
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
import type {ProjectPlan, AiBlocksScope, AssetPolicy} from './project-plan';
import {BLOCK_ONLY_MESSAGE} from './block-only-policy';
import {createId} from '../shared/create-id';

const props = defineProps<{bridge: ScratchGuiBridge | null}>();
const emit = defineEmits<{
  lockChange: [locked: boolean, message: string];
  busyChange: [busy: boolean];
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
    if (typeof value?.enabled === 'boolean' && ['low', 'medium', 'high'].includes(strength)) {
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
const draft = ref('');
const requirements = ref<AiBlocksRequirements | null>(null);
const planSteps = ref<string[]>([]);
const phaseMessage = ref('');
const progress = ref({current: 0, total: 0, label: ''});
const result = ref<AiGenerationResult | null>(null);
const projectPlan = ref<ProjectPlan | null>(null);
const errorMessage = ref('');
const noticeMessage = ref('');
const sessionId = ref(createId());
const chatLog = ref<HTMLElement | null>(null);
const reasoningLog = ref<HTMLElement | null>(null);
const requestBusy = ref(false);
let controller: AbortController | null = null;
let unsubscribeTargets: (() => void) | null = null;

const isBusy = computed(() => requestBusy.value || ['summarizing', 'planning', 'injecting'].includes(state.value));
const canSend = computed(() => draft.value.trim().length > 0 && !isBusy.value && Boolean(snapshot.value));
const canSummarize = computed(() => messages.value.some(message => message.role === 'user') && !isBusy.value);
const targetLabel = computed(() => snapshot.value
  ? (snapshot.value.isStage ? '舞台 · ' : '角色 · ') + snapshot.value.targetName
  : '等待 Scratch');
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
      proceed_with_defaults: Boolean(options.proceedWithDefaults)
    }, controller.signal);
    requirements.value = summary;
    if (!summary.ready) {
      const missing = summary.missing_questions.find(Boolean);
      messages.value.push({
        role: 'assistant',
        content: missing || BLOCK_ONLY_MESSAGE
      });
      state.value = 'chat';
      await scrollToLatest();
    } else {
      state.value = 'review';
      shouldGenerate = Boolean(options.generateWhenReady);
    }
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
      output_contract: scope.value === 'project' ? 'project_plan' : 'program_ir',
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
    if (output.kind === 'project') {
      projectPlan.value = output.project;
      state.value = 'chat';
      noticeMessage.value = '项目资源计划已生成，请逐项确认后再准备资源。';
      return;
    }
    state.value = 'injecting';
    setLocked(true, 'AI 生成中，请稍等');
    if (output.kind !== 'program') throw new Error('当前积木执行器尚未接入项目计划');
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
  if (!props.bridge || !result.value || isBusy.value) return;
  setLocked(true, '正在撤销 AI 生成');
  errorMessage.value = '';
  let succeeded = false;
  try {
    await undoAiGeneration(props.bridge, result.value);
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

function runProject() {
  props.bridge?.run();
}

watch(() => props.bridge, attachBridge);
watch([state, reasoning, phaseMessage, planSteps, progress], followGenerationProcess);
watch(mode, () => {
  if (!props.bridge || !snapshot.value || isBusy.value) return;
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
        <span class="ai-blocks-target">{{ targetLabel }}</span>
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

    <fieldset v-if="['chat', 'review'].includes(state)" class="ai-blocks-modes">
      <legend>生成范围与模式</legend>
      <label :class="{'is-active': scope === 'single_sprite'}"><input v-model="scope" type="radio" value="single_sprite" /><span>当前角色</span></label>
      <label :class="{'is-active': scope === 'project'}"><input v-model="scope" type="radio" value="project" /><span>整个项目</span></label>
      <label v-if="scope === 'project'" class="ai-blocks-policy"><span>素材策略</span><select v-model="assetPolicy" aria-label="素材策略"><option value="builtin_first">内置素材优先</option><option value="existing_only">仅已有素材</option><option value="allow_generation">允许生图</option></select></label>
      <label :class="{'is-active': mode === 'modify'}">
        <input v-model="mode" type="radio" value="modify" />
        <span>修改当前代码</span>
      </label>
      <label :class="{'is-active': mode === 'append'}">
        <input v-model="mode" type="radio" value="append" />
        <span>旁路新增</span>
      </label>
      <label :class="{'is-active': mode === 'rewrite'}">
        <input v-model="mode" type="radio" value="rewrite" />
        <span>清空重写</span>
      </label>
    </fieldset>

    <div v-if="errorMessage" class="ai-blocks-alert ai-blocks-alert--error" role="alert">{{ errorMessage }}</div>
    <div v-if="noticeMessage" class="ai-blocks-alert" role="status">{{ noticeMessage }}</div>

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
        <div v-if="result" class="ai-blocks-generation-actions" role="group" aria-label="本次生成操作">
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
                </select>
              </label>
              <label class="ai-blocks-model-select">
                <select v-model="modelTier" aria-label="模型" title="模型" :disabled="isBusy">
                  <option value="normal">普通</option>
                  <option value="advanced">高级</option>
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
            </select>
          </label>
          <label class="ai-blocks-model-select">
            <select v-model="modelTier" aria-label="模型" title="模型">
              <option value="normal">普通</option>
              <option value="advanced">高级</option>
            </select>
          </label>
        </div>
        <button class="button button--quiet" type="button" @click="state = 'chat'">继续补充</button>
        <button class="button button--primary" type="button" @click="generate"><Blocks :size="16" />确认并生成</button>
      </div>
    </section>

    <ResourcePlanReview :open="Boolean(projectPlan)" :plan="projectPlan" @cancel="projectPlan = null" @confirm="noticeMessage = '资源准备执行器尚未接入，当前未修改 Scratch 项目。'" />
  </section>
</template>
