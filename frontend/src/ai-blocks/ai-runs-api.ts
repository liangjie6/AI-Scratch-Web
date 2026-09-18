import {createSseParser} from '../ai/text-expansion-api';
import {createId} from '../shared/create-id';
import type {AiBlocksTargetContext, UnifiedGenerationRequest} from './ai-blocks-api';
import type {BuiltinAiExtension} from './block-catalog';

export const AI_RUN_PROTOCOL_VERSION = 'ai-scratch-run/1';

export type AiRunStatus =
  | 'SNAPSHOTTING'
  | 'SPECIFYING_TASK'
  | 'BLUEPRINTING'
  | 'PLANNING_MANIFEST'
  | 'AWAITING_TOOL_RESULT'
  | 'GENERATING_PROGRAMS'
  | 'READY_TO_COMMIT'
  | 'CANCELLED'
  | 'FAILED';

export interface AiRunRecord {
  protocol_version: typeof AI_RUN_PROTOCOL_VERSION;
  run_id: string;
  status: AiRunStatus;
  state_version: number;
  project_epoch: number;
  artifact_summary: Record<string, unknown>;
  created_at: number;
  updated_at: number;
  expires_at: number;
  terminal: boolean;
}

export interface AiRunEvent {
  protocol_version: typeof AI_RUN_PROTOCOL_VERSION;
  run_id: string;
  event_id: string;
  sequence: number;
  phase: string;
  type: string;
  attempt: number;
  payload: Record<string, unknown>;
}

export interface AiRunToolResult {
  tool_call_id: string;
  idempotency_key?: string;
  expected_project_epoch: number;
  need_id: string;
  op_id: string;
  selected_source_kind: 'scratch_library' | 'image_generation' | 'audio_generation' | 'skipped';
  normalized_name?: string;
  normalized_tags?: string[];
  width?: number;
  height?: number;
  duration_ms?: number;
}

/** Sent only to the live run manager; program text is never stored as an event. */
export interface AiRunProgramContext {
  target_index: number;
  target: AiBlocksTargetContext;
}

/** Payload-free browser outcome, reported only after the project transaction releases. */
export interface AiRunCommitMetric {
  expected_project_epoch: number;
  outcome: 'COMPLETED' | 'ROLLED_BACK' | 'FAILED_NEEDS_RECOVERY' | 'REJECTED_BEFORE_WRITE';
  commit_duration_ms: number;
  verify_duration_ms: number;
  behavior_test_count: number;
  behavior_passed_count: number;
  behavior_must_count: number;
  behavior_must_passed_count: number;
  error_code?: 'COMMIT_FAILED' | 'COMMIT_CONFLICT' | 'COMMIT_RECOVERY_FAILED' | 'SB3_ARCHIVE_INVALID' | 'SB3_RESOURCE_MISSING' | 'SB3_BEHAVIOR_FAILED' | 'SB3_VM_ROUND_TRIP_FAILED';
  idempotency_key?: string;
}

/** Aggregate-only asset-review outcome. Asset names, search terms and IDs are excluded. */
export interface AiRunResourceReviewMetricBucket {
  resource_type: 'sprite' | 'costume' | 'backdrop' | 'sound';
  auto_selected_count: number;
  auto_reselected_count: number;
  manual_selected_count: number;
  review_required_count: number;
  generation_fallback_count: number;
}

export interface AiRunResourceReviewMetric {
  expected_project_epoch: number;
  buckets: AiRunResourceReviewMetricBucket[];
  idempotency_key?: string;
}

export class AiRunApiError extends Error {
  constructor(public readonly code: string, message: string, public readonly status?: number) {
    super(message);
    this.name = 'AiRunApiError';
  }
}

const SCOPE_STORAGE_KEY = 'ai-scratch-run-scope-v1';
const RUN_EVENT_TYPES = new Set([
  'phase_started',
  'requirements_ready',
  'task_spec_ready',
  'blueprint_ready',
  'manifest_ready',
  'asset_resolution_required',
  'tool_result_received',
  'decision_received',
  'program_target_started',
  'program_script_ready',
  'program_target_ready',
  'metadata',
  'shared_state',
  'repair_started',
  'validation_failed',
  'generation_complete',
  'generation_error',
  'cache_hit',
  'usage',
  'phase_completed',
]);

/** Deployment capabilities are limited to feature flags and cached per session scope. */
export interface AiRuntimeCapabilities {
  commitEnabled: boolean;
  crashCheckpoint: boolean;
  durableRuns: boolean;
  projectAssets: boolean;
  soundAssets: boolean;
  logicalResourceRefs: boolean;
  shortCommit: boolean;
  audioGeneration: boolean;
  assetAutoMatch: boolean;
  assetAutoMatchMinScore: number;
  assetAutoMatchMinMargin: number;
  builtinExtensions: ReadonlySet<BuiltinAiExtension>;
}

let runtimeCapabilitiesPromise: Promise<AiRuntimeCapabilities | undefined> | undefined;

function runScope() {
  const existing = sessionStorage.getItem(SCOPE_STORAGE_KEY);
  if (existing) return existing;
  const generated = globalThis.crypto?.randomUUID?.() ?? createId();
  sessionStorage.setItem(SCOPE_STORAGE_KEY, generated);
  return generated;
}

async function loadRuntimeCapabilities(): Promise<AiRuntimeCapabilities | undefined> {
  try {
    const response = await fetch('/api/capabilities', {headers: headers()});
    if (!response.ok) return undefined;
    const value = asRecord(await response.json(), 'AI 能力响应无效');
    const protocols = value.ai_blocks_protocol;
    const features = asRecord(value.features, 'AI 能力响应缺少 features');
    if (!Array.isArray(protocols) || !protocols.includes(3)) return undefined;
    const builtinExtensions = new Set<BuiltinAiExtension>();
    if (features.pen_extension === true) builtinExtensions.add('pen');
    if (features.music_extension === true) builtinExtensions.add('music');
    if (features.text_to_speech_extension === true) builtinExtensions.add('text2speech');
    return {
      commitEnabled: features.ai_commit === true,
      // Older deployments predate the flag; preserve the fail-safe default so
      // an unavailable capability endpoint never disables local recovery.
      crashCheckpoint: features.crash_checkpoint !== false,
      durableRuns: value.ai_runs_protocol === AI_RUN_PROTOCOL_VERSION && features.durable_runs === true,
      projectAssets: features.project_assets === true,
      soundAssets: features.sound_assets === true,
      logicalResourceRefs: features.logical_resource_refs === true,
      shortCommit: features.short_commit === true,
      audioGeneration: features.audio_generation === true,
      assetAutoMatch: features.asset_auto_match === true,
      assetAutoMatchMinScore: typeof features.asset_auto_match_min_score === 'number' ? features.asset_auto_match_min_score : 8,
      assetAutoMatchMinMargin: typeof features.asset_auto_match_min_margin === 'number' ? features.asset_auto_match_min_margin : 5,
      builtinExtensions,
    };
  } catch {
    return undefined;
  }
}

/** A missing or incompatible endpoint is an old backend, so callers retain their v2 fallback. */
export function getAiRuntimeCapabilities(): Promise<AiRuntimeCapabilities | undefined> {
  runtimeCapabilitiesPromise ??= loadRuntimeCapabilities();
  return runtimeCapabilitiesPromise;
}

export async function supportsDurableRuns(signal?: AbortSignal): Promise<boolean> {
  if (signal?.aborted) throw new DOMException('AI task cancelled', 'AbortError');
  const capabilities = await getAiRuntimeCapabilities();
  if (signal?.aborted) throw new DOMException('AI task cancelled', 'AbortError');
  return capabilities?.durableRuns === true;
}

function headers(additional: HeadersInit = {}): Headers {
  const value = new Headers(additional);
  value.set('X-AI-Run-Scope', runScope());
  return value;
}

function asRecord(value: unknown, message: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new AiRunApiError('RUN_PROTOCOL_ERROR', message);
  return value as Record<string, unknown>;
}

function errorFromPayload(payload: unknown, fallback: string, status?: number): AiRunApiError {
  const outer = payload && typeof payload === 'object' ? payload as Record<string, unknown> : {};
  const detail = outer.detail && typeof outer.detail === 'object' && !Array.isArray(outer.detail) ? outer.detail as Record<string, unknown> : outer;
  const code = typeof detail.code === 'string' && /^[A-Z0-9_]+$/.test(detail.code) ? detail.code : 'AI_RUN_REQUEST_FAILED';
  const message = typeof detail.message === 'string' && detail.message.length <= 240 && !/[\r\n]/.test(detail.message) ? detail.message : fallback;
  return new AiRunApiError(code, message, status);
}

function parseRecord(value: unknown): AiRunRecord {
  const record = asRecord(value, 'AI 任务状态响应无效');
  if (record.protocol_version !== AI_RUN_PROTOCOL_VERSION || typeof record.run_id !== 'string' || !record.run_id || typeof record.status !== 'string'
    || !Number.isInteger(record.state_version) || !Number.isInteger(record.project_epoch) || typeof record.terminal !== 'boolean') {
    throw new AiRunApiError('RUN_PROTOCOL_ERROR', 'AI 任务状态协议无效');
  }
  return record as unknown as AiRunRecord;
}

function parseEvent(value: unknown, expectedRunId: string): AiRunEvent {
  const event = asRecord(value, 'AI 任务事件数据无效');
  if (event.protocol_version !== AI_RUN_PROTOCOL_VERSION || event.run_id !== expectedRunId || typeof event.event_id !== 'string' || !event.event_id
    || !Number.isInteger(event.sequence) || (event.sequence as number) < 1 || typeof event.phase !== 'string' || typeof event.type !== 'string'
    || !RUN_EVENT_TYPES.has(event.type) || !Number.isInteger(event.attempt)) {
    throw new AiRunApiError('RUN_PROTOCOL_ERROR', 'AI 任务事件协议无效');
  }
  event.payload = asRecord(event.payload, 'AI 任务事件缺少结构化 payload');
  return event as unknown as AiRunEvent;
}

async function responsePayload(response: Response): Promise<unknown> {
  return response.json().catch(() => null);
}

async function requestJson(path: string, init: RequestInit, fallback: string): Promise<AiRunRecord> {
  const response = await fetch(path, {...init, headers: headers(init.headers)});
  const payload = await responsePayload(response);
  if (!response.ok) throw errorFromPayload(payload, fallback + '（HTTP ' + response.status + '）', response.status);
  return parseRecord(payload);
}

export function createAiRun(request: UnifiedGenerationRequest, signal?: AbortSignal): Promise<AiRunRecord> {
  if (request.scope !== 'project' || !request.project_context) throw new AiRunApiError('RUN_REQUEST_INVALID', '持久化任务仅支持完整项目生成');
  return requestJson('/api/ai-runs', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({generation: request}),
    signal,
  }, '无法创建 AI 任务');
}

export function getAiRun(runId: string, signal?: AbortSignal): Promise<AiRunRecord> {
  return requestJson('/api/ai-runs/' + encodeURIComponent(runId), {signal}, '无法读取 AI 任务');
}

export async function submitAiRunToolResult(runId: string, result: AiRunToolResult, signal?: AbortSignal): Promise<void> {
  const response = await fetch('/api/ai-runs/' + encodeURIComponent(runId) + '/tool-results', {
    method: 'POST',
    headers: headers({'Content-Type': 'application/json'}),
    body: JSON.stringify({...result, idempotency_key: result.idempotency_key ?? createId()}),
    signal,
  });
  if (!response.ok) throw errorFromPayload(await responsePayload(response), '无法提交资源解析结果', response.status);
}

export async function submitAiRunProgramContext(runId: string, expectedProjectEpoch: number, context: AiRunProgramContext, signal?: AbortSignal): Promise<void> {
  const response = await fetch('/api/ai-runs/' + encodeURIComponent(runId) + '/program-contexts', {
    method: 'POST',
    headers: headers({'Content-Type': 'application/json'}),
    body: JSON.stringify({expected_project_epoch: expectedProjectEpoch, ...context}),
    signal,
  });
  if (!response.ok) throw errorFromPayload(await responsePayload(response), '无法提交项目程序上下文', response.status);
}

export async function submitAiRunCommitMetric(runId: string, metric: AiRunCommitMetric): Promise<void> {
  const response = await fetch('/api/ai-runs/' + encodeURIComponent(runId) + '/client-commit-metrics', {
    method: 'POST',
    headers: headers({'Content-Type': 'application/json'}),
    body: JSON.stringify({...metric, idempotency_key: metric.idempotency_key ?? createId()}),
    keepalive: true,
  });
  if (!response.ok) throw errorFromPayload(await responsePayload(response), '无法提交项目写入指标', response.status);
}

export async function submitAiRunResourceReviewMetric(runId: string, metric: AiRunResourceReviewMetric): Promise<void> {
  const response = await fetch('/api/ai-runs/' + encodeURIComponent(runId) + '/resource-review-metrics', {
    method: 'POST',
    headers: headers({'Content-Type': 'application/json'}),
    body: JSON.stringify({...metric, idempotency_key: metric.idempotency_key ?? createId()}),
    keepalive: true,
  });
  if (!response.ok) throw errorFromPayload(await responsePayload(response), '无法提交素材确认指标', response.status);
}

export async function cancelAiRun(runId: string, expectedProjectEpoch: number, signal?: AbortSignal): Promise<AiRunRecord> {
  return requestJson('/api/ai-runs/' + encodeURIComponent(runId) + '/cancel', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({expected_project_epoch: expectedProjectEpoch, idempotency_key: createId()}),
    signal,
  }, '无法取消 AI 任务');
}

export async function streamAiRunEvents(
  runId: string,
  onEvent: (event: AiRunEvent) => void,
  options: {lastEventId?: string; lastSequence?: number; signal?: AbortSignal} = {},
): Promise<void> {
  const response = await fetch('/api/ai-runs/' + encodeURIComponent(runId) + '/events', {
    headers: headers(options.lastEventId ? {'Last-Event-ID': options.lastEventId} : {}),
    signal: options.signal,
  });
  if (!response.ok) throw errorFromPayload(await responsePayload(response), 'AI 任务事件流不可用', response.status);
  if (!(response.headers.get('content-type') ?? '').toLowerCase().includes('text/event-stream') || !response.body) {
    throw new AiRunApiError('RUN_PROTOCOL_ERROR', 'AI 任务未返回事件流');
  }
  let lastSequence = options.lastSequence ?? 0;
  let terminal = false;
  const parser = createSseParser(({event: eventType, data}) => {
    const event = parseEvent(data, runId);
    if (event.type !== eventType || event.sequence !== lastSequence + 1 || terminal) {
      throw new AiRunApiError('RUN_PROTOCOL_ERROR', 'AI 任务事件顺序无效');
    }
    lastSequence = event.sequence;
    terminal = event.type === 'generation_complete' || event.type === 'generation_error';
    onEvent(event);
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
  if (!terminal && !options.signal?.aborted) throw new AiRunApiError('STREAM_INCOMPLETE', 'AI 任务事件连接意外中断');
}
