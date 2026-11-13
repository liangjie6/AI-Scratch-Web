import {afterEach, describe, expect, it, vi} from 'vitest';
import {streamAiBlocksChat, streamSourceProgram, streamUnifiedGeneration, type AiBlocksTargetContext, type UnifiedGenerationRequest} from '../src/ai-blocks/ai-blocks-api';
import fixture from './fixtures/source-project.json';
import type {FrozenSourceContract} from '../src/ai-blocks/source-contract';

const target: AiBlocksTargetContext = {
  name: '角色1', is_stage: false, has_scripts: false, mode: 'append', costumes: ['造型1'], sounds: [], sprite_names: ['角色1'], current_program: '{}'
};
const base: UnifiedGenerationRequest = {
  model_tier: 'normal', session_id: 'session-1', request_id: 'request-1', messages: [{role: 'user', content: '点击绿旗后移动'}],
  thinking: {enabled: true, strength: 'medium'}, target, scope: 'single_sprite', asset_policy: 'builtin_first', proceed_with_defaults: true
};
const NL = String.fromCharCode(10);

function sseResponse(frames: string[], splitBytes = 0) {
  const encoder = new TextEncoder();
  const bytes = encoder.encode(frames.join(''));
  return new Response(new ReadableStream({
    start(controller) {
      if (!splitBytes) controller.enqueue(bytes);
      else for (let index = 0; index < bytes.length; index += splitBytes) controller.enqueue(bytes.slice(index, index + splitBytes));
      controller.close();
    }
  }), {headers: {'Content-Type': 'text/event-stream; charset=utf-8'}});
}
function frame(event: string, data: Record<string, unknown>, phase?: string) {
  return 'event: ' + event + NL + 'data: ' + JSON.stringify({request_id: base.request_id, ...(phase ? {phase} : {}), data}) + NL + NL;
}
function generatedFrames() {
  const requirements = {ready: true, title: '移动角色', goal: '绿旗后移动', behavior_steps: ['移动 10 步'], triggers: ['绿旗'], controls: [], success_criteria: ['角色移动'], missing_questions: []};
  const program = {id: 'script_move', purpose: '移动', x: 40, y: 40, blocks: [{opcode: 'event_whenflagclicked'}]};
  return [
    frame('phase_start', {message: '整理需求'}, 'requirements'),
    frame('phase_complete', requirements, 'requirements'),
    frame('phase_start', {message: '规划'}, 'plan'),
    frame('phase_complete', {steps: ['添加绿旗事件']}, 'plan'),
    frame('phase_start', {target_index: 0}, 'program'),
    frame('usage', {input_tokens: 120, output_tokens: 40, total_tokens: 160}, 'program'),
    frame('metadata', {target_index: 0, schema_version: 1, summary: '移动角色', remove_script_ids: []}, 'program'),
    frame('shared_state', {target_index: 0, variables: [], lists: [], broadcasts: []}, 'program'),
    frame('script_ready', {target_index: 0, index: 0, total: 1, script: program}, 'program'),
    frame('phase_complete', {target_index: 0, script_count: 1}, 'program'),
    frame('complete', {program_count: 1})
  ];
}

afterEach(() => vi.unstubAllGlobals());

describe('unified AI blocks API', () => {
  it('uses the negotiated source endpoint and parses a real normalized ProgramIR response', async () => {
    const program = fixture.programs[0];
    const sourceRequest = {
      request: {...base, pipeline_version: 'scratch-source/1' as const},
      source_contract: fixture.source_contract as unknown as FrozenSourceContract,
      target_index: 0,
      target,
    };
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse([frame('complete', {target_index: 0, program})], 2));
    vi.stubGlobal('fetch', fetchMock);
    const received: unknown[] = [];
    await streamSourceProgram(sourceRequest, event => received.push(event.data.program));
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/ai-blocks/generate-source-program');
    expect(JSON.parse(String((fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1].body))).toEqual(sourceRequest);
    expect(received).toEqual([program]);
  });

  it('uses one request and parses split UTF-8 unified events', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse(generatedFrames(), 2));
    vi.stubGlobal('fetch', fetchMock);
    const events: string[] = [];
    await streamUnifiedGeneration(base, event => events.push(event.type));
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toMatchObject({scope: 'single_sprite', asset_policy: 'builtin_first'});
    expect(events).toEqual(['phase_start', 'phase_complete', 'phase_start', 'phase_complete', 'phase_start', 'usage', 'metadata', 'shared_state', 'script_ready', 'phase_complete', 'complete']);
  });

  it('passes request id and data separately to event consumers', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse([frame('phase_complete', {steps: ['保留原代码']}, 'plan'), frame('complete', {program_count: 0})])));
    const received: unknown[] = [];
    await streamUnifiedGeneration(base, event => received.push(event));
    expect(received[0]).toMatchObject({type: 'phase_complete', requestId: base.request_id, phase: 'plan', data: {steps: ['保留原代码']}});
  });

  it('rejects legacy event names and incomplete unified streams', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse(['event: plan_ready' + NL + 'data: {}' + NL + NL])));
    await expect(streamUnifiedGeneration(base, () => undefined)).rejects.toMatchObject({code: 'STREAM_PROTOCOL_ERROR'});
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse([frame('phase_start', {}, 'requirements')])));
    await expect(streamUnifiedGeneration(base, () => undefined)).rejects.toMatchObject({code: 'STREAM_INCOMPLETE'});
  });

  it('rejects mismatched request ids and sanitized server errors', async () => {
    const mismatch = 'event: complete' + NL + 'data: ' + JSON.stringify({request_id: 'other', data: {}}) + NL + NL;
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse([mismatch])));
    await expect(streamUnifiedGeneration(base, () => undefined)).rejects.toMatchObject({code: 'STREAM_PROTOCOL_ERROR'});
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse([frame('error', {code: 'OUTPUT_PROTOCOL_ERROR', message: 'AI 输出未通过协议校验'})])));
    await expect(streamUnifiedGeneration(base, () => undefined)).rejects.toMatchObject({code: 'OUTPUT_PROTOCOL_ERROR'});
    const topLevel = 'event: error' + NL + 'data: ' + JSON.stringify({request_id: base.request_id, code: 'AI_PROVIDER_TIMEOUT', message: 'AI 生成超时，请稍后重试'}) + NL + NL;
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse([topLevel])));
    await expect(streamUnifiedGeneration(base, () => undefined)).rejects.toMatchObject({code: 'AI_PROVIDER_TIMEOUT', message: 'AI 生成超时，请稍后重试'});
  });

  it('keeps pure chat on its separate endpoint', async () => {
    const response = 'event: assistant_delta' + NL + 'data: ' + JSON.stringify({request_id: base.request_id, text: '请使用绿旗事件'}) + NL + NL + 'event: message_complete' + NL + 'data: ' + JSON.stringify({request_id: base.request_id}) + NL + NL;
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse([response], 1));
    vi.stubGlobal('fetch', fetchMock);
    let answer = '';
    const chatRequest = {
      model_tier: base.model_tier, session_id: base.session_id, request_id: base.request_id,
      messages: base.messages, target: base.target, thinking: base.thinking
    };
    await streamAiBlocksChat(chatRequest, value => { answer += value; });
    expect(answer).toBe('请使用绿旗事件');
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/ai-blocks/chat');
    const body = JSON.parse(String((fetchMock.mock.calls[0]?.[1] as RequestInit).body));
    expect(body).toEqual(chatRequest);
    expect(body.scope).toBeUndefined();
    expect(body.project_context).toBeUndefined();
  });

  it('self-heals when cyclic behavior dependencies occur in projectProgramGenerationLayers', async () => {
    const {projectProgramGenerationLayers} = await import('../src/ai-blocks/project-contract');
    const blueprint = {
      schema_version: 2,
      task_spec_hash: 'dummy',
      entity_bindings: [],
      asset_needs: [],
      state_plan: [],
      behavior_graph: [
        {behavior_id: 'b_cat', owner_entity_id: 'cat', depends_on: ['b_milk'], produces_broadcasts: [], consumes_broadcasts: []},
        {behavior_id: 'b_milk', owner_entity_id: 'milk', depends_on: ['b_cat'], produces_broadcasts: [], consumes_broadcasts: []},
      ],
      program_jobs: [],
      acceptance_tests: [],
    };
    const manifest = {
      schema_version: 3,
      scope: 'project',
      summary: 'test',
      assumptions: [],
      required_extensions: [],
      resource_ops: [],
      shared_state: {variables: [], lists: [], broadcasts: []},
      program_targets: [
        {job_id: 'job_cat', owner_entity_id: 'cat', behavior_ids: ['b_cat'], target: {kind: 'stage'}, mode: 'append', intent: 'test'},
        {job_id: 'job_milk', owner_entity_id: 'milk', behavior_ids: ['b_milk'], target: {kind: 'stage'}, mode: 'append', intent: 'test'},
      ],
    };
    // 验证不抛出“项目程序行为依赖存在循环”，而是自愈返回合法层级
    const layers = projectProgramGenerationLayers(blueprint as any, manifest as any);
    const flattened = layers.flat();
    expect(flattened.sort()).toEqual([0, 1]);
  });
});
