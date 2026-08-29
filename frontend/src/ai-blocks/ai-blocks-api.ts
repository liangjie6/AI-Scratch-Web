import type {ProgramIR} from './program-ir';
import type {ProjectManifest, ProjectProgramTarget, AiBlocksScope, AssetPolicy} from './project-plan';
import {createSseParser} from '../ai/text-expansion-api';

export type AiBlocksMode = 'modify' | 'append' | 'rewrite';
export type AiBlocksModelTier = 'normal' | 'advanced' | 'super';
export type AiBlocksThinkingStrength = 'low' | 'medium' | 'high' | 'xhigh' | 'max';

export interface AiBlocksThinkingSettings {
  enabled: boolean;
  strength: AiBlocksThinkingStrength;
}

export interface AiBlocksMessage {
  role: 'user' | 'assistant';
  content: string;
  reasoning?: string;
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

export interface BaseRequest {
  model_tier?: AiBlocksModelTier;
  session_id: string;
  request_id: string;
  messages: AiBlocksMessage[];
  target: AiBlocksTargetContext;
  thinking: AiBlocksThinkingSettings;
}

export interface SummarizeAiBlocksRequest extends BaseRequest {
  proceed_with_defaults?: boolean;
}

export interface AiBlocksPlan {
  steps: string[];
}

export interface GenerateAiBlocksRequest extends BaseRequest {
  requirements: AiBlocksRequirements;
  plan?: AiBlocksPlan;
  protocol_version?: 1 | 2;
  output_contract?: 'program_ir' | 'project_manifest' | 'project_program';
  project_manifest?: ProjectManifest;
  program_target?: ProjectProgramTarget;
  scope?: AiBlocksScope;
  asset_policy?: AssetPolicy;
  asset_match_evidence?: Array<{
    requirement_id: string;
    asset_type: 'sprite' | 'costume' | 'backdrop';
    manifest_version: 'scratch-gui-15.0.1';
    match_count: number;
    candidates: Array<{name: string; tags: string[]}>;
  }>;
}

export type AiGenerationOutput =
  | {kind: 'program'; program: ProgramIR; target?: ProjectProgramTarget}
  | {kind: 'manifest'; manifest: ProjectManifest};

export interface GenerationPhase {phase: string; message: string}

export class AiBlocksApiError extends Error {
  constructor(public readonly code: string, message: string) {
    super(message);
    this.name = 'AiBlocksApiError';
  }
}

function apiError(payload: unknown, fallback: string): AiBlocksApiError {
  const value = payload as {detail?: {code?: string; message?: string}; code?: string; message?: string} | null;
  const detail = value?.detail ?? value;
  return new AiBlocksApiError(detail?.code ?? 'AI_BLOCKS_FAILED', detail?.message ?? fallback);
}

async function fetchSse(path: string, body: unknown, signal?: AbortSignal): Promise<ReadableStream<Uint8Array>> {
  const response = await fetch(path, {
    method: 'POST',
    headers: {Accept: 'text/event-stream', 'Content-Type': 'application/json'},
    body: JSON.stringify(body),
    signal
  });
  if (!response.ok) throw apiError(await response.json().catch(() => null), 'AI 请求失败（HTTP ' + response.status + '）');
  if (!(response.headers.get('content-type') ?? '').toLowerCase().includes('text/event-stream')) {
    throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '服务端未返回 AI 消息流');
  }
  if (!response.body) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '浏览器未获得 AI 消息流');
  return response.body;
}

async function consumeSse(body: ReadableStream<Uint8Array>, onEvent: (event: string, payload: unknown) => void) {
  const parser = createSseParser(({event, data}) => onEvent(event, data));
  const reader = body.getReader();
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
}

export async function streamAiBlocksChat(request: BaseRequest, onDelta: (text: string) => void, signal?: AbortSignal, onReasoningDelta?: (text: string) => void) {
  const body = await fetchSse('/api/ai-blocks/chat', request, signal);
  let completed = false;
  await consumeSse(body, (event, data) => {
    const payload = data as {text?: unknown; code?: string; message?: string};
    if (event === 'assistant_delta' && typeof payload?.text === 'string') onDelta(payload.text);
    else if (event === 'reasoning_delta' && typeof payload?.text === 'string') onReasoningDelta?.(payload.text);
    else if (event === 'message_complete') completed = true;
    else if (event === 'message_error') throw new AiBlocksApiError(payload?.code ?? 'AI_CHAT_ERROR', payload?.message ?? 'AI 对话暂时不可用');
  });
  if (!completed) throw new AiBlocksApiError('STREAM_INCOMPLETE', 'AI 对话连接意外中断');
}

export async function summarizeAiBlocksRequirements(request: SummarizeAiBlocksRequest, signal?: AbortSignal): Promise<AiBlocksRequirements> {
  const response = await fetch('/api/ai-blocks/requirements', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(request),
    signal
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) throw apiError(payload, 'AI 无法整理当前需求');
  return payload as AiBlocksRequirements;
}

interface GenerationHandlers {
  onPhase?: (phase: GenerationPhase) => void;
  onPlan?: (steps: string[]) => void;
  onProgram?: (program: ProgramIR) => void;
  onManifest?: (manifest: ProjectManifest) => void;
  onProjectProgram?: (program: ProgramIR, target: ProjectProgramTarget) => void;
  onReasoning?: (text: string, phase: string) => void;
}

export async function streamAiBlocksGeneration(request: GenerateAiBlocksRequest, handlers: GenerationHandlers = {}, signal?: AbortSignal): Promise<AiGenerationOutput> {
  const body = await fetchSse('/api/ai-blocks/generate', request, signal);
  let completed = false;
  let readyEvent: 'program_ready' | 'manifest_ready' | 'project_program_ready' | null = null;
  let program: ProgramIR | null = null;
  let manifest: ProjectManifest | null = null;
  let programTarget: ProjectProgramTarget | undefined;
  await consumeSse(body, (event, data) => {
    const payload = data as {phase?: unknown; message?: unknown; steps?: unknown; plan?: {steps?: unknown}; program?: unknown; manifest?: unknown; target?: unknown; text?: unknown; code?: string; error?: string};
    if (event === 'phase' && typeof payload?.phase === 'string') {
      handlers.onPhase?.({phase: payload.phase, message: typeof payload.message === 'string' ? payload.message : ''});
    } else if (event === 'plan_ready') {
      const steps = Array.isArray(payload.steps) ? payload.steps : payload.plan?.steps;
      if (Array.isArray(steps)) handlers.onPlan?.(steps.filter((item): item is string => typeof item === 'string'));
    } else if (event === 'reasoning_delta' && typeof payload?.text === 'string') {
      handlers.onReasoning?.(payload.text, typeof payload.phase === 'string' ? payload.phase : '');
    } else if (event === 'program_ready' && payload.program && typeof payload.program === 'object') {
      if (readyEvent) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', 'AI 返回了多个完成事件');
      readyEvent = 'program_ready';
      program = payload.program as ProgramIR;
      handlers.onProgram?.(program);
    } else if (event === 'manifest_ready' && payload.manifest && typeof payload.manifest === 'object') {
      if (readyEvent) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', 'AI 返回了多个完成事件');
      readyEvent = 'manifest_ready';
      manifest = payload.manifest as ProjectManifest;
      handlers.onManifest?.(manifest);
    } else if (event === 'project_program_ready' && payload.program && typeof payload.program === 'object' && payload.target && typeof payload.target === 'object') {
      if (readyEvent) throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', 'AI 返回了多个完成事件');
      readyEvent = 'project_program_ready';
      program = payload.program as ProgramIR;
      programTarget = payload.target as ProjectProgramTarget;
      handlers.onProjectProgram?.(program, programTarget);
    } else if (event === 'project_ready') {
      throw new AiBlocksApiError('STREAM_PROTOCOL_ERROR', '旧 project_plan 协议已移除，请重新发起项目生成');
    } else if (event === 'message_complete') {
      completed = true;
    } else if (event === 'message_error') {
      throw new AiBlocksApiError(payload?.code ?? 'AI_BLOCKS_GENERATION_ERROR', typeof payload?.message === 'string' ? payload.message : 'AI 积木生成失败');
    }
  });
  if (!completed) throw new AiBlocksApiError('STREAM_INCOMPLETE', 'AI 积木生成连接意外中断');
  if (request.output_contract === 'project_manifest') {
    if (!manifest) throw new AiBlocksApiError('PROJECT_MANIFEST_MISSING', 'AI 未返回可用的项目清单');
    return {kind: 'manifest', manifest};
  }
  if (!program) throw new AiBlocksApiError('PROGRAM_MISSING', 'AI 未返回可用的积木程序');
  return {kind: 'program', program, ...(programTarget ? {target: programTarget} : {})};
}
