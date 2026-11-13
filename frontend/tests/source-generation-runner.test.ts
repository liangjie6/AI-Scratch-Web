import {afterEach, describe, expect, it, vi} from 'vitest';
import fixture from './fixtures/source-project.json';
import {UnifiedGenerationRunner} from '../src/ai-blocks/generation-runner';
import {contractSha256, type ProjectBlueprint} from '../src/ai-blocks/project-contract';
import {sourceContractSha256, sourceSnapshotHash, type FrozenSourceContract} from '../src/ai-blocks/source-contract';
import type {UnifiedGenerationRequest} from '../src/ai-blocks/ai-blocks-api';
import type {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';

vi.mock('../src/ai-blocks/verification-service', () => ({prewarmCandidateVerificationRuntime: vi.fn(async () => undefined), verifyCandidateSb3: vi.fn()}));

function bridgeFixture(): ScratchGuiBridge {
  const blocks = () => ({_blocks: {}, getScripts: () => [], getBlock: () => undefined});
  const cat = {id: 'cat-vm-id', isStage: false, isOriginal: true, getName: () => '猫', getCostumes: () => [{name: 'cat'}], getSounds: () => [{name: 'pop'}], variables: {}, comments: {}, blocks: blocks()};
  const stage = {id: 'stage-vm-id', isStage: true, isOriginal: true, getName: () => '舞台', getCostumes: () => [{name: '白背景'}], getSounds: () => [], variables: {}, comments: {}, blocks: blocks()};
  return {projectRevision: 3, currentVm: {editingTarget: cat, runtime: {targets: [stage, cat], getTargetForStage: () => stage, getTargetById: (id: string) => [stage, cat].find(target => target.id === id)}}, saveProject: vi.fn(), beginProjectTransaction: vi.fn()} as unknown as ScratchGuiBridge;
}
function request(): UnifiedGenerationRequest {
  return {...structuredClone(fixture.request), messages: [{role: 'user', content: '接苹果'}], model_tier: 'normal', thinking: {enabled: false, strength: 'low'}} as unknown as UnifiedGenerationRequest;
}
function requirements() {
  return {ready: true, title: fixture.source_contract.title, goal: fixture.source_contract.goal, behavior_steps: fixture.source_contract.script_slots.map(slot => slot.purpose), triggers: ['green_flag'], controls: [], success_criteria: fixture.source_contract.success_criteria, missing_questions: []};
}
function response(req: UnifiedGenerationRequest, plan: Record<string, unknown>, program?: typeof fixture.programs[number]) {
  const frames: Array<[string, Record<string, unknown>, string?]> = [
    ['phase_complete', requirements(), 'requirements'],
    ['phase_complete', {blueprint: plan.blueprint, blueprint_hash: plan.blueprint_hash}, 'blueprint'],
    ['phase_complete', plan, 'plan'],
  ];
  if (program) {
    frames.push(['metadata', {target_index: 0, schema_version: program.schema_version, summary: program.summary, remove_script_ids: program.remove_script_ids, procedures: program.procedures}, 'program']);
    frames.push(['shared_state', {target_index: 0, variables: program.variables, lists: program.lists, broadcasts: program.broadcasts}, 'program']);
    program.scripts.forEach((script, index) => frames.push(['script_ready', {target_index: 0, index, total: program.scripts.length, script}, 'program']));
    frames.push(['phase_complete', {target_index: 0, script_count: program.scripts.length}, 'program']);
  }
  frames.push(['complete', {program_count: program ? 1 : fixture.manifest.program_targets.length}]);
  const bytes = new TextEncoder().encode(frames.map(([event, data, phase]) => `event: ${event}\ndata: ${JSON.stringify({request_id: req.request_id, ...(phase ? {phase} : {}), data})}\n\n`).join(''));
  return new Response(new ReadableStream({start(controller) {for (let offset = 0; offset < bytes.length; offset += 3) controller.enqueue(bytes.slice(offset, offset + 3)); controller.close();}}), {headers: {'Content-Type': 'text/event-stream'}});
}
afterEach(() => vi.unstubAllGlobals());

describe('source pipeline response preparation', () => {
  it('parses real source plan events without TaskSpec and validates every projection before returning a draft', async () => {
    const bridge = bridgeFixture();
    const req = request();
    const fetchMock = vi.fn(async () => response(req, fixture));
    vi.stubGlobal('fetch', fetchMock);
    const draft = await new UnifiedGenerationRunner(bridge).prepare(req);
    expect(draft.taskSpec).toBeUndefined();
    expect(draft.sourceContract?.hash).toBe(fixture.source_contract.hash);
    expect(draft.manifest?.program_targets).toHaveLength(2);
    expect(draft.programs).toEqual([]);
    expect(bridge.saveProject).not.toHaveBeenCalled();
    expect(bridge.beginProjectTransaction).not.toHaveBeenCalled();
  });

  it('rejects mismatched state/target projection and a missing source hash before any transaction', async () => {
    const req = request();
    const changed = structuredClone(fixture);
    changed.manifest.shared_state.variables[0].value = 42;
    const bridge = bridgeFixture();
    vi.stubGlobal('fetch', vi.fn(async () => response(req, changed)));
    await expect(new UnifiedGenerationRunner(bridge).prepare(req)).rejects.toThrow('投影不一致');
    vi.stubGlobal('fetch', vi.fn(async () => response(req, {...fixture, source_contract_hash: undefined})));
    await expect(new UnifiedGenerationRunner(bridge).prepare(req)).rejects.toThrow('绑定无效');
    expect(bridge.beginProjectTransaction).not.toHaveBeenCalled();
  });

  it('keeps a legacy request on its negotiated protocol rather than accepting an unsolicited source contract', async () => {
    const req = request();
    delete req.pipeline_version;
    vi.stubGlobal('fetch', vi.fn(async () => response(req, fixture)));
    await expect(new UnifiedGenerationRunner(bridgeFixture()).prepare(req)).rejects.toThrow('未协商');
  });

  it('rechecks the source projection before opening the commit transaction', async () => {
    const bridge = bridgeFixture();
    const req = request();
    vi.stubGlobal('fetch', vi.fn(async () => response(req, fixture)));
    const runner = new UnifiedGenerationRunner(bridge);
    const draft = await runner.prepare(req);
    draft.manifest!.shared_state.variables[0].value = 999;
    await expect(runner.commit(draft)).rejects.toThrow('投影不一致');
    expect(bridge.beginProjectTransaction).not.toHaveBeenCalled();
    expect(bridge.saveProject).not.toHaveBeenCalled();
  });

  it('rejects changing the frozen request ID before opening a commit transaction', async () => {
    const bridge = bridgeFixture();
    const req = request();
    vi.stubGlobal('fetch', vi.fn(async () => response(req, fixture)));
    const runner = new UnifiedGenerationRunner(bridge);
    const draft = await runner.prepare(req);
    draft.request = {...draft.request, request_id: draft.request.request_id + '-new-budget'};
    await expect(runner.commit(draft)).rejects.toThrow('会话或请求 ID');
    expect(bridge.beginProjectTransaction).not.toHaveBeenCalled();
    expect(bridge.saveProject).not.toHaveBeenCalled();
  });

  it('accepts a single-target source plan with Blueprint and ProgramIR but no Manifest or TaskSpec', async () => {
    const req = {...request(), scope: 'single_sprite' as const, project_context: undefined};
    const contract = structuredClone(fixture.source_contract) as unknown as FrozenSourceContract;
    contract.scope = 'single_sprite';
    contract.targets = [contract.targets[0]];
    contract.states = [contract.states[0]];
    contract.assets = [];
    contract.asset_intents = [];
    contract.script_slots = [contract.script_slots[0]];
    contract.base_snapshot_hash = await sourceSnapshotHash(req);
    contract.hash = await sourceContractSha256({...contract, hash: ''});
    const blueprint = structuredClone(fixture.blueprint) as unknown as ProjectBlueprint;
    blueprint.task_spec_hash = contract.hash;
    blueprint.asset_needs = [];
    blueprint.entity_bindings = [blueprint.entity_bindings[0]];
    blueprint.state_plan = [blueprint.state_plan[0]];
    blueprint.behavior_graph = [blueprint.behavior_graph[0]];
    blueprint.program_jobs = [{...blueprint.program_jobs[0], behavior_ids: [blueprint.behavior_graph[0].behavior_id]}];
    const program = {...structuredClone(fixture.programs[0]), schema_version: 1, scripts: [structuredClone(fixture.programs[0].scripts[0])], variables: [{name: contract.states[0].name, value: 0, scope: 'global'}]} as unknown as typeof fixture.programs[number];
    const plan = {steps: [fixture.steps[0]], source_contract: contract, source_contract_hash: await sourceContractSha256(contract), blueprint, blueprint_hash: await contractSha256(blueprint)};
    vi.stubGlobal('fetch', vi.fn(async () => response(req, plan, program)));
    const draft = await new UnifiedGenerationRunner(bridgeFixture()).prepare(req);
    expect(draft.sourceContract?.scope).toBe('single_sprite');
    expect(draft.taskSpec).toBeUndefined();
    expect(draft.manifest).toBeUndefined();
    expect(draft.programs).toHaveLength(1);
  });
});
