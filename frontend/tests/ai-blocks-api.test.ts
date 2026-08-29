import {afterEach, describe, expect, it, vi} from 'vitest';
import {
  streamAiBlocksChat,
  streamAiBlocksGeneration,
  summarizeAiBlocksRequirements,
  type AiBlocksTargetContext
} from '../src/ai-blocks/ai-blocks-api';

const target: AiBlocksTargetContext = {
  name: '角色1',
  is_stage: false,
  has_scripts: false,
  mode: 'append',
  costumes: ['造型1'],
  sounds: [],
  sprite_names: ['角色1'],
  current_program: '{}'
};
const base = {
  session_id: '11111111-1111-4111-8111-111111111111',
  request_id: '22222222-2222-4222-8222-222222222222',
  messages: [{role: 'user' as const, content: '点击绿旗后移动'}],
  thinking: {enabled: true, strength: 'medium' as const},
  target
};

function sseResponse(frames: string[]) {
  const encoder = new TextEncoder();
  return new Response(new ReadableStream({
    start(controller) {
      frames.forEach(frame => controller.enqueue(encoder.encode(frame)));
      controller.close();
    }
  }), {headers: {'Content-Type': 'text/event-stream; charset=utf-8'}});
}

afterEach(() => vi.unstubAllGlobals());

describe('AI blocks API', () => {
  it('streams assistant deltas across split UTF-8 frames', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => sseResponse([
      'event: assistant_delta\ndata: {"text":"请说明操作方式"}\n\n',
      'event: message_complete\ndata: {}\n\n'
    ]));
    vi.stubGlobal('fetch', fetchMock);
    let answer = '';
    await streamAiBlocksChat(base, delta => { answer += delta; });
    expect(answer).toBe('请说明操作方式');
    expect(fetchMock).toHaveBeenCalledWith('/api/ai-blocks/chat', expect.objectContaining({method: 'POST'}));
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toMatchObject({thinking: {enabled: true, strength: 'medium'}});
  });

  it('returns structured requirements', async () => {
    const requirements = {
      ready: true,
      title: '移动角色',
      goal: '绿旗后移动',
      behavior_steps: ['移动 10 步'],
      triggers: ['绿旗'],
      controls: [],
      success_criteria: ['角色移动'],
      missing_questions: []
    };
    let requestBody = '';
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      requestBody = String(init?.body ?? '');
      return new Response(JSON.stringify(requirements), {status: 200, headers: {'Content-Type': 'application/json'}});
    });
    vi.stubGlobal('fetch', fetchMock);
    await expect(summarizeAiBlocksRequirements({...base, proceed_with_defaults: true})).resolves.toEqual(requirements);
    expect(JSON.parse(requestBody)).toMatchObject({proceed_with_defaults: true});
  });

  it('streams plan and validated program payload', async () => {
    const program = {
      schema_version: 1,
      summary: '移动角色',
      remove_script_ids: [],
      variables: [],
      lists: [],
      broadcasts: [],
      scripts: [{id: 'script_move', purpose: '移动', x: 40, y: 40, blocks: [{opcode: 'event_whenflagclicked'}]}]
    };
    vi.stubGlobal('fetch', vi.fn(async () => sseResponse([
      'event: phase\ndata: {"phase":"planning","message":"正在规划"}\n\n',
      'event: reasoning_delta\ndata: {"text":"先选择绿旗事件","phase":"planning"}\n\n',
      'event: plan_ready\ndata: {"steps":["添加绿旗事件"]}\n\n',
      'event: program_ready\ndata: ' + JSON.stringify({program}) + '\n\n',
      'event: message_complete\ndata: {}\n\n'
    ])));
    const phases: string[] = [];
    const plans: string[][] = [];
    const reasoning: string[] = [];
    const result = await streamAiBlocksGeneration({
      ...base,
      requirements: {ready: true, title: '移动', goal: '移动', behavior_steps: ['移动'], triggers: ['绿旗'], controls: [], success_criteria: ['移动'], missing_questions: []},
      output_contract: 'program_ir',
      scope: 'single_sprite',
      asset_policy: 'builtin_first'
    }, {
      onPhase: phase => phases.push(phase.message),
      onPlan: steps => plans.push(steps),
      onReasoning: (text, phase) => reasoning.push(phase + ':' + text)
    });
    expect(result).toEqual({kind: 'program', program});
    expect(phases).toEqual(['正在规划']);
    expect(plans).toEqual([['添加绿旗事件']]);
    expect(reasoning).toEqual(['planning:先选择绿旗事件']);
  });

  it('returns a single project program target from the new protocol', async () => {
    const program = {
      schema_version: 1,
      summary: '移动新角色',
      remove_script_ids: [],
      variables: [],
      lists: [],
      broadcasts: [],
      scripts: [{id: 'script_created', purpose: '移动', x: 0, y: 0, blocks: [{opcode: 'event_whenflagclicked'}]}]
    };
    const targetRef = {target: {kind: 'created_sprite' as const, op_id: 'sprite_1'}, mode: 'append' as const, intent: '移动新角色'};
    vi.stubGlobal('fetch', vi.fn(async () => sseResponse([
      'event: plan_ready\ndata: {"plan":{"steps":["移动"]}}\n\n',
      'event: project_program_ready\ndata: ' + JSON.stringify({program, target: targetRef}) + '\n\n',
      'event: message_complete\ndata: {}\n\n'
    ])));
    const result = await streamAiBlocksGeneration({
      ...base,
      requirements: {ready: true, title: '项目', goal: '项目', behavior_steps: ['移动'], triggers: ['绿旗'], controls: [], success_criteria: ['移动'], missing_questions: []},
      output_contract: 'project_program',
      scope: 'project',
      asset_policy: 'builtin_first',
      project_manifest: {schema_version: 1, scope: 'project', summary: '项目', resource_ops: [], program_targets: [targetRef], shared_state: {variables: [], lists: [], broadcasts: []}},
      program_target: targetRef
    }, {onPlan: steps => expect(steps).toEqual(['移动'])});
    expect(result).toEqual({kind: 'program', program, target: targetRef});
  });

  it('rejects a stream that ends without completion', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => sseResponse(['event: assistant_delta\ndata: {"text":"未完成"}\n\n'])));
    await expect(streamAiBlocksChat(base, () => undefined)).rejects.toMatchObject({code: 'STREAM_INCOMPLETE'});
  });
});
