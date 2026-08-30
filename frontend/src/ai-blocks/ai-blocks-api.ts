import type {ProgramIR, ScriptIR} from './program-ir';
import type {ProjectManifest, AiBlocksScope, AssetPolicy} from './project-plan';
import {createSseParser} from '../ai/text-expansion-api';

export type AiBlocksMode = 'modify' | 'append' | 'rewrite';
export type AiBlocksModelTier = 'normal' | 'advanced' | 'super';
export type AiBlocksThinkingStrength = 'low' | 'medium' | 'high' | 'xhigh' | 'max';
export type UnifiedGenerationPhase = 'requirements' | 'plan' | 'program';
export type UnifiedGenerationEventType =
  | 'cache_hit'
  | 'phase_start'
  | 'phase_complete'
  | 'content_delta'
  | 'reasoning_delta'
  | 'metadata'
  | 'shared_state'
  | 'script_ready'
  | 'repair'
  | 'validation_error'
  | 'error'
  | 'complete';

export interface AiBlocksThinkingSettings {
  enabled: boolean;
  strength: AiBlocksThinkingStrength;
}

export interface AiBlocksMessage {
  role: 'user' | 'assistant';
  content: string;
  /** 思考过程文本，随生成过程累积并在对话卡片中保留 */
  reasoning?: string;
  /** 任务清单：该次生成的计划步骤，结束后仍保留在卡片中 */
  plan?: string[];
  /** 对话卡片状态：streaming 为进行中；成功/失败/取消均为终态并保留在对话里 */
  status?: 'streaming' | 'success' | 'error' | 'cancelled';
  /** 失败或取消的说明，同卡片内一并保留 */
  error?: string;
}

export interface AiBlocksTargetContext {
  name: string;
  is_stage: boolean;
  has_scripts: boolean;
  mode: AiBlocksMode;
  costumes: string[];
  sounds: string[];
  sprite_names: string[];
  current_program: string;
}

export interface AiBlocksProjectTargetContext extends AiBlocksTargetContext {
  target_id: string;
  revision: number;
}

export interface AiBlocksProjectContext {
  project_id: string;
  version: number;
  targets: AiBlocksProjectTargetContext[];
  shared_state: Record<string, unknown>;
}

export interface AiBlocksRequirements {
  ready: boolean;
  title: string;
  goal: string;
  behavior_steps: string[];
  triggers: string[];
  controls: string[];
  success_criteria: string[];
  missing_questions: string[];
}

export interface AiBlocksPlan {
  steps: string[];
}

export interface ProgramMetadata {
  schema_version: 1;
  summary: string;
  remove_script_ids: string[];
}

export interface ProgramState {
  variables: ProgramIR['variables'];
  lists: ProgramIR['lists'];
  broadcasts: string[];
}

export interface AiBlocksChatRequest {
  model_tier?: AiBlocksModelTier;
  session_id: string;
  request_id: string;
  messages: AiBlocksMessage[];
  target: AiBlocksTargetContext;
  thinking: AiBlocksThinkingSettings;
}

export interface UnifiedGenerationRequest {
  model_tier?: AiBlocksModelTier;
  session_id: string;
  request_id: string;
  messages: AiBlocksMessage[];
  target: AiBlocksTargetContext;
  thinking: AiBlocksThinkingSettings;
  scope: AiBlocksScope;
  asset_policy: AssetPolicy;
  project_context?: AiBlocksProjectContext;
  proceed_with_defaults: boolean;
}

export interface UnifiedGenerationEvent {
  type: UnifiedGenerationEventType;
  requestId: string;
  phase?: UnifiedGenerationPhase;
  data: Record<string, unknown>;
}

export interface UnifiedProgramDraft {
  targetIndex: number;
  metadata: ProgramMetadata;
  state: ProgramState;
  scripts: ScriptIR[];
  program?: ProgramIR;
}

export interface UnifiedGenerationDraft {
  requirements: AiBlocksRequirements;
  plan: AiBlocksPlan;
  manifest?: ProjectManifest;
  programs: UnifiedProgramDraft[];
}

export class AiBlocksApiError extends Error {
  constructor(public readonly code: string, message: string) {
    super(message);
    this.name = 'AiBlocksApiError';
  }
}

function errorFromPayload(payload: unknown, fallback: string): AiBlocksApiError {
  const value = payload as {detail?: unknown; data?: unknown; code?: unknown; message?: unknown} | null;
  const detail = (value?.detail ?? value?.data ?? value) as {code?: unknown; message?: unknown} | null;
  const code = typeof detail?.code === 'string' && /^[A-Z0-9_]+$/.test(detail.code) ? detail.code : 'AI_BLOCKS_REQUEST_FAILED';
  const message = typeof detail?.message === 'string' && detail.message.length <= 240 && !/[\r\n]/.test(detail.message) ? detail.message : fallback;
  return new AiBlocksApiError(code, message);
}

function asRecord(value: unknown, message: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', message);
  return value as Record<string, unknown>;
}

function asData(payload: Record<string, unknown>): Record<string, unknown> {
  return payload.data && typeof payload.data === 'object' && !Array.isArray(payload.data)
    ? payload.data as Record<string, unknown>
    : {};
}

export async function streamUnifiedGeneration(
  request: UnifiedGenerationRequest,
  onEvent: (event: UnifiedGenerationEvent) => void,
  signal?: AbortSignal
): Promise<void> {
  const response = await fetch('/api/ai-blocks/generate-unified', {
    method: 'POST',
    headers: {Accept: 'text/event-stream', 'Content-Type': 'application/json'},
    body: JSON.stringify(request),
    signal
  });
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw errorFromPayload(payload, 'AI 生成请求失败（HTTP ' + response.status + '）');
  }
  if (!(response.headers.get('content-type') ?? '').toLowerCase().includes('text/event-stream')) {
    throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '服务端未返回统一生成流');
  }
  if (!response.body) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '浏览器未获得响应流');

  let completed = false;
  const parser = createSseParser(({event, data}) => {
    if (!['cache_hit', 'phase_start', 'phase_complete', 'content_delta', 'reasoning_delta', 'metadata', 'shared_state', 'script_ready', 'repair', 'validation_error', 'complete', 'error'].includes(event)) {
      throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '收到未知的统一生成事件：' + event);
    }
    const payload = asRecord(data, '统一生成事件数据无效');
    if (payload.request_id !== request.request_id) {
      throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '统一生成事件 request_id 不匹配');
    }
    if (completed) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '统一生成流在完成后仍有输出');
    if (payload.phase !== undefined && !['requirements', 'plan', 'program'].includes(String(payload.phase))) {
      throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '统一生成事件 phase 无效');
    }
    if (['phase_start', 'phase_complete', 'metadata', 'shared_state', 'script_ready', 'validation_error'].includes(event) && typeof payload.phase !== 'string') {
      throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '统一生成事件缺少 phase');
    }
    if (event === 'error') {
      throw errorFromPayload(payload, 'AI 输出未完成');
    }
    if (event === 'complete') {
      if (completed) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '统一生成流重复完成');
      completed = true;
    }
    onEvent({
      type: event as UnifiedGenerationEventType,
      requestId: String(payload.request_id),
      phase: typeof payload.phase === 'string' ? payload.phase as UnifiedGenerationPhase : undefined,
      data: asData(payload)
    });
  });
  const reader = response.body.getReader();
  const decoder = new TextDecoder('utf-8');
  try {
    while (true) {
      const {done, value} = await reader.read();
      if (done) break;
      parser.push(decoder.decode(value, {stream: true}));
    }
    parser.push(decoder.decode());
    parser.finish();
  } finally {
    reader.releaseLock();
  }
  if (!completed) throw new AiBlocksApiError('STREAM_INCOMPLETE', '统一生成连接意外中断');
}

export async function streamAiBlocksChat(
  request: AiBlocksChatRequest,
  onDelta: (text: string) => void,
  signal?: AbortSignal,
  onReasoning?: (text: string) => void
): Promise<void> {
  const response = await fetch('/api/ai-blocks/chat', {
    method: 'POST',
    headers: {Accept: 'text/event-stream', 'Content-Type': 'application/json'},
    body: JSON.stringify(request),
    signal
  });
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw errorFromPayload(payload, 'AI 对话请求失败（HTTP ' + response.status + '）');
  }
  if (!(response.headers.get('content-type') ?? '').toLowerCase().includes('text/event-stream')) {
    throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '服务端未返回对话流');
  }
  if (!response.body) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '浏览器未获得响应流');
  let completed = false;
  const parser = createSseParser(({event, data}) => {
    if (!['assistant_delta', 'reasoning_delta', 'message_complete', 'message_error'].includes(event)) {
      throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '收到未知的 AI 对话事件：' + event);
    }
    const payload = asRecord(data, 'AI 对话事件数据无效');
    if (payload.request_id !== request.request_id) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', 'AI 对话事件 request_id 不匹配');
    if (completed) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', 'AI 对话流在完成后仍有输出');
    if (event === 'assistant_delta' || event === 'reasoning_delta') {
      if (typeof payload.text !== 'string') throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', 'AI 对话文本事件无效');
      if (event === 'assistant_delta') onDelta(payload.text);
      else onReasoning?.(payload.text);
    } else if (event === 'message_complete') {
      if (completed) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', 'AI 对话流重复完成');
      completed = true;
    } else if (event === 'message_error') {
      throw errorFromPayload(payload, 'AI 对话暂时不可用');
    }
  });
  const reader = response.body.getReader();
  const decoder = new TextDecoder('utf-8');
  try {
    while (true) {
      const {done, value} = await reader.read();
      if (done) break;
      parser.push(decoder.decode(value, {stream: true}));
    }
    parser.push(decoder.decode());
    parser.finish();
  } finally {
    reader.releaseLock();
  }
  if (!completed) throw new AiBlocksApiError('STREAM_INCOMPLETE', 'AI 对话连接意外中断');
}
