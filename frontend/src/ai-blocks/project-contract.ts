import type {ProgramIR, ValidationIssue} from './program-ir';
import type {ProjectLocalState, ProjectManifest, ProjectProgramTarget, TargetRef} from './project-plan';

export const MAX_ACCEPTANCE_TIMER_SECONDS = 86_400;

export type TaskTargetRef = Exclude<TargetRef, {kind: 'created_sprite'}>;
export type Scalar = string | number | boolean;
export type TaskStateKind = 'variable' | 'list' | 'broadcast';

export interface ScratchTaskSpec {
  schema_version: 2;
  title: string;
  goal: string;
  assumptions: Array<{id: string; text: string; confidence: number; requires_confirmation: boolean}>;
  entities: Array<{entity_id: string; role: 'stage' | 'sprite'; description: string; existing_target_ref?: TaskTargetRef}>;
  state: Array<{state_id: string; kind: TaskStateKind; scope: 'global' | 'local'; owner_entity_id?: string; initial_value?: Scalar | Scalar[]}>;
  behaviors: Array<{behavior_id: string; owner_entity_id: string; trigger: TaskTrigger; conditions: TaskCondition[]; actions: TaskAction[]; reads: string[]; writes: string[]}>;
  success_criteria: Array<{criterion_id: string; description: string; related_behavior_ids: string[]; priority: 'must' | 'should'; verification: 'executable' | 'structural' | 'manual' | 'unsupported'}>;
  unsupported: Array<{requirement: string; reason_code: string}>;
}

export type TaskOperand =
  | {kind: 'literal'; value: Scalar}
  | {kind: 'state'; state_id: string}
  | {kind: 'attribute'; entity_id: string; attribute: 'x' | 'y' | 'direction' | 'size' | 'visible'}
  | {kind: 'timer'}
  | {kind: 'random'; min: number; max: number};
export type TaskTrigger =
  | {kind: 'green_flag'} | {kind: 'key_pressed'; key: string} | {kind: 'sprite_clicked'}
  | {kind: 'broadcast_received'; state_id: string} | {kind: 'backdrop_switched'; asset_purpose: string}
  | {kind: 'clone_started'} | {kind: 'timer_reached'; seconds: number};
export type TaskCondition =
  | {kind: 'compare'; left: TaskOperand; operator: 'eq' | 'gt' | 'gte' | 'lt' | 'lte'; right: TaskOperand}
  | {kind: 'touching'; entity_id: string} | {kind: 'touching_edge'} | {kind: 'key_down'; key: string}
  | {kind: 'logical'; operator: 'and' | 'or'; operands: TaskCondition[]} | {kind: 'not'; operand: TaskCondition};
export type TaskAction =
  | {kind: 'move_steps'; steps: TaskOperand} | {kind: 'set_axis'; axis: 'x' | 'y'; value: TaskOperand}
  | {kind: 'change_axis'; axis: 'x' | 'y'; delta: TaskOperand} | {kind: 'go_to_entity'; entity_id: string}
  | {kind: 'turn'; degrees: TaskOperand} | {kind: 'point_towards'; entity_id: string}
  | {kind: 'set_state'; state_id: string; value: TaskOperand} | {kind: 'change_state'; state_id: string; delta: TaskOperand}
  | {kind: 'broadcast'; state_id: string} | {kind: 'use_asset'; asset_kind: 'costume' | 'backdrop' | 'sound'; purpose: string; wait?: boolean}
  | {kind: 'set_visibility'; visible: boolean} | {kind: 'say'; text: string; seconds?: TaskOperand}
  | {kind: 'ask'; question: string} | {kind: 'create_clone'; entity_id: string} | {kind: 'delete_this_clone'}
  | {kind: 'wait'; seconds: TaskOperand} | {kind: 'loop'; mode: 'forever' | 'repeat' | 'repeat_until'; count?: TaskOperand; until?: TaskCondition; actions: TaskAction[]}
  | {kind: 'stop'; scope: 'this_script' | 'all'};

export type AcceptanceTrigger =
  | {kind: 'green_flag'}
  | {kind: 'key'; key: string}
  | {kind: 'sprite_click'; entity_id: string}
  | {kind: 'broadcast'; state_id: string};

export type AcceptanceArrange =
  | {kind: 'set_position'; entity_id: string; x: number; y: number}
  | {kind: 'set_variable'; state_id: string; value: Scalar}
  | {kind: 'set_list'; state_id: string; value: Scalar[]}
  | {kind: 'set_timer'; seconds: number};

export type AcceptanceExpectation =
  | {kind: 'variable'; state_id: string; comparator: 'eq' | 'gt' | 'gte' | 'lt' | 'lte'; value: Scalar}
  | {kind: 'position_range'; entity_id: string; axis: 'x' | 'y'; min: number; max: number}
  | {kind: 'costume'; entity_id: string; asset_need_id: string}
  | {kind: 'backdrop'; asset_need_id: string}
  | {kind: 'visibility'; entity_id: string; visible: boolean}
  | {kind: 'list'; state_id: string; comparator: 'equals' | 'contains' | 'length'; value: Scalar | Scalar[]}
  | {kind: 'clone_count'; entity_id: string; comparator: 'eq' | 'gte' | 'lte'; value: number}
  | {kind: 'say_text'; entity_id: string; text: string}
  | {kind: 'question_asked'; text: string}
  | {kind: 'broadcast_emitted'; state_id: string; min_count: number}
  | {kind: 'sound_invoked'; entity_id: string; asset_need_id: string; min_count: number}
  | {kind: 'scripts_stopped'; entity_id?: string};

export interface BlueprintAcceptanceTest {
  test_id: string;
  source_criterion_id: string;
  related_behavior_ids: string[];
  trigger: AcceptanceTrigger;
  arrange?: AcceptanceArrange[];
  expect: AcceptanceExpectation[];
  timeout_ms: number;
}

export interface ProjectBlueprint {
  schema_version: 2;
  task_spec_hash: string;
  asset_needs: Array<{need_id: string; kind: 'sprite' | 'costume' | 'backdrop' | 'sound'; owner_entity_id?: string; purpose: string; search_terms: string[]; tags: string[]; fallback_order: Array<'project_asset' | 'scratch_library' | 'user_asset' | 'image_generation' | 'audio_generation'>; constraints: Record<string, boolean | number>}>;
  entity_bindings: Array<{entity_id: string; resolution: {kind: 'existing_target'; target_ref: TaskTargetRef} | {kind: 'new_sprite'; need_id: string}}>;
  state_plan: Array<{ref_id: string; kind: TaskStateKind; scope: 'global' | 'local'; owner_entity_id?: string; name: string; initial_value?: Scalar | Scalar[]}>;
  behavior_graph: Array<{behavior_id: string; owner_entity_id: string; depends_on: string[]; produces_broadcasts: string[]; consumes_broadcasts: string[]}>;
  program_jobs: Array<{job_id: string; owner_entity_id: string; mode: 'append' | 'modify' | 'rewrite'; behavior_ids: string[]}>;
  acceptance_tests: BlueprintAcceptanceTest[];
}

export interface ResolvedExecutionPlan {
  schema_version: 1;
  base: {task_spec_hash: string; blueprint_hash: string; manifest_hash: string; project_epoch: number; snapshot_hash: string; editor_session_id: string};
  ordered_operations: ProjectManifest['resource_ops'];
  shared_state: ProjectManifest['shared_state'];
  local_state: ProjectLocalState[];
  programs: Array<{job_id: string; target_ref: TargetRef; program: ProgramIR}>;
  lineage: {need_bindings: Array<{need_id: string; op_id: string}>; entity_to_target: Array<{entity_id: string; target_ref: TargetRef}>; job_to_target: Array<{job_id: string; target_ref: TargetRef}>};
}

const issue = (path: string, message: string): ValidationIssue => ({path, message, code: 'LINEAGE_INVALID', operation: 'preflight', repair_scope: 'manifest'});
const unique = (values: string[], path: string, issues: ValidationIssue[]) => { if (new Set(values).size !== values.length) issues.push(issue(path, 'values must be unique')); };

/** Canonical serialized representation shared with the backend's contract hash function. */
export function canonicalContractJson(value: unknown): string {
  const normalize = (entry: unknown): unknown => {
    if (Array.isArray(entry)) return entry.map(normalize);
    if (entry && typeof entry === 'object') return Object.fromEntries(Object.entries(entry as Record<string, unknown>).filter(([, item]) => item !== undefined && item !== null).sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0).map(([key, item]) => [key, normalize(item)]));
    return entry;
  };
  return JSON.stringify(normalize(value));
}

export async function contractSha256(value: unknown): Promise<string> {
  const bytes = new TextEncoder().encode(canonicalContractJson(value));
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('');
}

/** Validate the deterministic TaskSpec -> Blueprint projection before resource resolution. */
export function projectTaskSpecToBlueprint(task: ScratchTaskSpec, blueprint: ProjectBlueprint): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  const entities = new Set(task.entities.map(item => item.entity_id));
  const states = new Set(task.state.map(item => item.state_id));
  const behaviors = new Set(task.behaviors.map(item => item.behavior_id));
  unique(task.entities.map(item => item.entity_id), '/entities', issues); unique(task.state.map(item => item.state_id), '/state', issues); unique(task.behaviors.map(item => item.behavior_id), '/behaviors', issues);
  if (task.schema_version !== 2) issues.push(issue('/task_spec/schema_version', 'TaskSpec schema_version must be 2'));
  if (blueprint.schema_version !== 2) issues.push(issue('/schema_version', 'Blueprint schema_version must be 2'));
  const bindings = blueprint.entity_bindings.map(item => item.entity_id);
  if (bindings.length !== entities.size || bindings.some(item => !entities.has(item))) issues.push(issue('/entity_bindings', 'Blueprint must bind every TaskSpec entity exactly once'));
  unique(bindings, '/entity_bindings', issues);
  const needs = new Map(blueprint.asset_needs.map(item => [item.need_id, item]));
  unique([...needs.keys()], '/asset_needs', issues);
  for (const binding of blueprint.entity_bindings) if (binding.resolution.kind === 'new_sprite') {
    const need = needs.get(binding.resolution.need_id);
    if (!need || need.kind !== 'sprite' || need.owner_entity_id !== binding.entity_id) issues.push(issue('/entity_bindings', 'new sprite binding must point to its entity sprite need'));
  }
  if (blueprint.state_plan.length !== states.size || blueprint.state_plan.some(item => !states.has(item.ref_id))) issues.push(issue('/state_plan', 'Blueprint must project every TaskSpec state exactly once'));
  unique(blueprint.state_plan.map(item => item.ref_id), '/state_plan', issues);
  const stateById = new Map(task.state.map(item => [item.state_id, item]));
  for (const [index, statePlan] of blueprint.state_plan.entries()) {
    const source = stateById.get(statePlan.ref_id);
    if (!source || source.kind !== statePlan.kind || source.scope !== statePlan.scope || source.owner_entity_id !== statePlan.owner_entity_id) {
      issues.push(issue(`/state_plan/${index}`, 'Blueprint state plan must preserve TaskSpec kind, scope and owner'));
    }
  }
  if (blueprint.behavior_graph.length !== behaviors.size || blueprint.behavior_graph.some(item => !behaviors.has(item.behavior_id))) issues.push(issue('/behavior_graph', 'Blueprint must project every TaskSpec behavior exactly once'));
  unique(blueprint.behavior_graph.map(item => item.behavior_id), '/behavior_graph', issues);
  const behaviorById = new Map(task.behaviors.map(item => [item.behavior_id, item]));
  const graphByBehavior = new Map(blueprint.behavior_graph.map(item => [item.behavior_id, item]));
  const broadcasts = new Set(task.state.filter(item => item.kind === 'broadcast').map(item => item.state_id));
  for (const [index, node] of blueprint.behavior_graph.entries()) {
    const source = behaviorById.get(node.behavior_id);
    if (!source || source.owner_entity_id !== node.owner_entity_id) issues.push(issue(`/behavior_graph/${index}/owner_entity_id`, 'Blueprint behavior owner must preserve TaskSpec ownership'));
    if (node.depends_on.includes(node.behavior_id) || node.depends_on.some(id => !graphByBehavior.has(id))) issues.push(issue(`/behavior_graph/${index}/depends_on`, 'Behavior dependencies must reference other declared behaviors'));
    if (node.produces_broadcasts.some(id => !broadcasts.has(id)) || node.consumes_broadcasts.some(id => !broadcasts.has(id))) issues.push(issue(`/behavior_graph/${index}`, 'Behavior broadcast edges must reference TaskSpec broadcasts'));
    if (source) {
      const produced = collectBehaviorBroadcasts(source.actions);
      const consumed = source.trigger.kind === 'broadcast_received' ? [source.trigger.state_id] : [];
      if (!sameStringSet(node.produces_broadcasts, produced) || !sameStringSet(node.consumes_broadcasts, consumed)) issues.push(issue(`/behavior_graph/${index}`, 'Behavior broadcast edges must match the TaskSpec trigger and actions'));
    }
  }
  if (hasDependencyCycle(graphByBehavior)) issues.push(issue('/behavior_graph', 'Behavior dependency graph cannot contain a cycle'));
  for (const broadcast of broadcasts) {
    const nodes = blueprint.behavior_graph;
    if (!nodes.some(item => item.produces_broadcasts.includes(broadcast)) || !nodes.some(item => item.consumes_broadcasts.includes(broadcast))) issues.push(issue('/behavior_graph', 'Each declared broadcast requires a producer and consumer: ' + broadcast));
  }
  const jobs = blueprint.program_jobs.flatMap(job => job.behavior_ids);
  if (jobs.length !== behaviors.size || jobs.some(item => !behaviors.has(item)) || new Set(jobs).size !== jobs.length) issues.push(issue('/program_jobs', 'Program jobs must cover every behavior exactly once'));
  for (const [index, job] of blueprint.program_jobs.entries()) if (job.behavior_ids.some(id => behaviorById.get(id)?.owner_entity_id !== job.owner_entity_id)) issues.push(issue(`/program_jobs/${index}/owner_entity_id`, 'Program job owner must match every assigned behavior'));
  validateAcceptanceTests(task, blueprint, needs, issues);
  return issues;
}

function collectBehaviorBroadcasts(actions: TaskAction[]): string[] {
  const broadcasts: string[] = [];
  for (const action of actions) {
    if (action.kind === 'broadcast') broadcasts.push(action.state_id);
    if (action.kind === 'loop') broadcasts.push(...collectBehaviorBroadcasts(action.actions));
  }
  return broadcasts;
}

function sameStringSet(left: string[], right: string[]): boolean {
  return left.length === new Set(left).size && right.length === new Set(right).size && left.length === right.length && left.every(item => right.includes(item));
}

function hasDependencyCycle(graph: Map<string, ProjectBlueprint['behavior_graph'][number]>): boolean {
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (id: string): boolean => {
    if (visiting.has(id)) return true;
    if (visited.has(id)) return false;
    visiting.add(id);
    const cyclic = graph.get(id)?.depends_on.some(visit) ?? false;
    visiting.delete(id);
    visited.add(id);
    return cyclic;
  };
  return [...graph.keys()].some(visit);
}

function validateAcceptanceTests(
  task: ScratchTaskSpec,
  blueprint: ProjectBlueprint,
  needs: Map<string, ProjectBlueprint['asset_needs'][number]>,
  issues: ValidationIssue[],
) {
  const criteria = new Map(task.success_criteria.map(item => [item.criterion_id, item]));
  const entityIds = new Set(task.entities.map(item => item.entity_id));
  const states = new Map(task.state.map(item => [item.state_id, item]));
  const behaviorIds = new Set(task.behaviors.map(item => item.behavior_id));
  unique(blueprint.acceptance_tests.map(item => item.test_id), '/acceptance_tests', issues);
  const totalTimeout = blueprint.acceptance_tests.reduce((total, item) => total + item.timeout_ms, 0);
  if (totalTimeout > 4_000) issues.push(issue('/acceptance_tests', 'Acceptance test total timeout must not exceed 4000ms'));
  for (const [index, test] of blueprint.acceptance_tests.entries()) {
    const path = `/acceptance_tests/${index}`;
    const criterion = criteria.get(test.source_criterion_id);
    if (!criterion) {
      issues.push(issue(path + '/source_criterion_id', 'Acceptance test must reference a TaskSpec success criterion'));
    } else if (criterion.verification !== 'executable') {
      issues.push(issue(path + '/source_criterion_id', 'Acceptance test may only reference an executable success criterion'));
    } else if (!test.related_behavior_ids.length || test.related_behavior_ids.some(id => !criterion.related_behavior_ids.includes(id))) {
      issues.push(issue(path + '/related_behavior_ids', 'Acceptance test behaviors must be owned by its source criterion'));
    }
    if (test.related_behavior_ids.some(id => !behaviorIds.has(id))) issues.push(issue(path + '/related_behavior_ids', 'Acceptance test references an unknown behavior'));
    if (!Number.isInteger(test.timeout_ms) || test.timeout_ms < 1 || test.timeout_ms > 4_000) issues.push(issue(path + '/timeout_ms', 'Acceptance test timeout must be an integer from 1 to 4000ms'));
    const entity = (id: string, childPath: string) => { if (!entityIds.has(id)) issues.push(issue(childPath, 'Acceptance test references an unknown entity')); };
    const state = (id: string, kinds: TaskStateKind[], childPath: string) => {
      const candidate = states.get(id);
      if (!candidate || !kinds.includes(candidate.kind)) issues.push(issue(childPath, 'Acceptance test state has an invalid kind or does not exist'));
    };
    const assetNeed = (id: string, kind: 'costume' | 'backdrop' | 'sound', childPath: string) => {
      if (needs.get(id)?.kind !== kind) issues.push(issue(childPath, `Acceptance test must reference a ${kind} asset need`));
    };
    switch (test.trigger.kind) {
      case 'key': if (!test.trigger.key.trim()) issues.push(issue(path + '/trigger/key', 'Key trigger requires a non-empty key')); break;
      case 'sprite_click': entity(test.trigger.entity_id, path + '/trigger/entity_id'); break;
      case 'broadcast': state(test.trigger.state_id, ['broadcast'], path + '/trigger/state_id'); break;
    }
    for (const [arrangeIndex, arrange] of (test.arrange ?? []).entries()) {
      const arrangePath = `${path}/arrange/${arrangeIndex}`;
      if (arrange.kind === 'set_position') entity(arrange.entity_id, arrangePath + '/entity_id');
      if (arrange.kind === 'set_variable') state(arrange.state_id, ['variable'], arrangePath + '/state_id');
      if (arrange.kind === 'set_list') state(arrange.state_id, ['list'], arrangePath + '/state_id');
      if (arrange.kind === 'set_timer' && (!Number.isFinite(arrange.seconds) || arrange.seconds < 0 || arrange.seconds > MAX_ACCEPTANCE_TIMER_SECONDS)) issues.push(issue(arrangePath + '/seconds', 'Timer arrangement requires a finite value from 0 to 86400 seconds'));
    }
    for (const [expectIndex, expectation] of test.expect.entries()) {
      const expectPath = `${path}/expect/${expectIndex}`;
      switch (expectation.kind) {
        case 'variable': state(expectation.state_id, ['variable'], expectPath + '/state_id'); break;
        case 'position_range': entity(expectation.entity_id, expectPath + '/entity_id'); if (expectation.min > expectation.max) issues.push(issue(expectPath, 'Position range min cannot exceed max')); break;
        case 'costume': entity(expectation.entity_id, expectPath + '/entity_id'); assetNeed(expectation.asset_need_id, 'costume', expectPath + '/asset_need_id'); break;
        case 'backdrop': assetNeed(expectation.asset_need_id, 'backdrop', expectPath + '/asset_need_id'); break;
        case 'visibility': entity(expectation.entity_id, expectPath + '/entity_id'); break;
        case 'list': state(expectation.state_id, ['list'], expectPath + '/state_id'); break;
        case 'clone_count': entity(expectation.entity_id, expectPath + '/entity_id'); break;
        case 'say_text': entity(expectation.entity_id, expectPath + '/entity_id'); break;
        case 'broadcast_emitted': state(expectation.state_id, ['broadcast'], expectPath + '/state_id'); break;
        case 'sound_invoked': entity(expectation.entity_id, expectPath + '/entity_id'); assetNeed(expectation.asset_need_id, 'sound', expectPath + '/asset_need_id'); break;
        case 'scripts_stopped': if (expectation.entity_id) entity(expectation.entity_id, expectPath + '/entity_id'); break;
      }
    }
  }
  const testedCriterionIds = new Set(blueprint.acceptance_tests.map(item => item.source_criterion_id));
  for (const criterion of task.success_criteria) {
    if (criterion.verification === 'executable' && !testedCriterionIds.has(criterion.criterion_id)) {
      issues.push(issue('/acceptance_tests', 'Every executable success criterion must have an acceptance test: ' + criterion.criterion_id));
    }
  }
}

/** Validate the Blueprint -> Manifest projection; the model may not invent execution IDs. */
export function projectBlueprintToManifest(blueprint: ProjectBlueprint, manifest: ProjectManifest): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  const needs = new Set(blueprint.asset_needs.map(item => item.need_id));
  const jobs = new Map(blueprint.program_jobs.map(item => [item.job_id, item]));
  const state = new Map(blueprint.state_plan.map(item => [item.ref_id, item]));
  for (const [index, op] of manifest.resource_ops.entries()) {
    if (!('source_need_id' in op) || typeof op.source_need_id !== 'string' || !needs.has(op.source_need_id)) issues.push(issue(`/resource_ops/${index}/source_need_id`, 'resource op must reference a Blueprint asset need'));
    if ((op.op === 'add_builtin_sprite' || op.op === 'add_sprite') && (typeof op.owner_entity_id !== 'string' || !blueprint.entity_bindings.some(binding => binding.entity_id === op.owner_entity_id && binding.resolution.kind === 'new_sprite' && binding.resolution.need_id === op.source_need_id))) issues.push(issue(`/resource_ops/${index}/owner_entity_id`, 'created sprite must match its Blueprint binding'));
  }
  for (const target of manifest.program_targets) {
    const job = typeof target.job_id === 'string' ? jobs.get(target.job_id) : undefined;
    if (!job || job.owner_entity_id !== target.owner_entity_id || canonicalContractJson(job.behavior_ids) !== canonicalContractJson(target.behavior_ids)) issues.push(issue('/program_targets', 'program target must preserve its Blueprint job lineage'));
  }
  const manifestState = [...manifest.shared_state.variables, ...manifest.shared_state.lists, ...manifest.shared_state.broadcasts, ...(manifest.local_state ?? [])];
  if (manifestState.length !== state.size || manifestState.some(item => typeof item === 'string' || !state.has(item.ref_id ?? ''))) issues.push(issue('/shared_state', 'Manifest must project every Blueprint state exactly once'));
  return issues;
}

/**
 * Return stable ProgramIR generation layers from the Blueprint behavior DAG.
 * Jobs in one layer have no unresolved cross-job behavior dependency and may
 * therefore be requested concurrently without changing the program contract.
 */
export function projectProgramGenerationLayers(blueprint: ProjectBlueprint, manifest: ProjectManifest): number[][] {
  const behaviorToTarget = new Map<string, number>();
  for (let index = 0; index < manifest.program_targets.length; index += 1) {
    const target = manifest.program_targets[index];
    if (!target?.job_id) throw new Error('项目程序目标缺少 job_id');
    for (const behaviorId of target.behavior_ids ?? []) {
      if (behaviorToTarget.has(behaviorId)) throw new Error('项目行为被多个程序目标声明：' + behaviorId);
      behaviorToTarget.set(behaviorId, index);
    }
  }

  const dependencies = manifest.program_targets.map(() => new Set<number>());
  for (const behavior of blueprint.behavior_graph) {
    const targetIndex = behaviorToTarget.get(behavior.behavior_id);
    if (targetIndex === undefined) throw new Error('蓝图行为没有对应程序目标：' + behavior.behavior_id);
    for (const dependencyId of behavior.depends_on) {
      const dependencyTarget = behaviorToTarget.get(dependencyId);
      if (dependencyTarget === undefined) throw new Error('蓝图行为依赖没有对应程序目标：' + dependencyId);
      if (dependencyTarget !== targetIndex) dependencies[targetIndex].add(dependencyTarget);
    }
  }

  const remaining = dependencies.map(item => new Set(item));
  const completed = new Set<number>();
  const layers: number[][] = [];
  while (completed.size < manifest.program_targets.length) {
    const layer = remaining
      .map((item, index) => item.size === 0 && !completed.has(index) ? index : -1)
      .filter(index => index >= 0);
    if (!layer.length) {
      console.warn('项目程序行为依赖存在循环，已启用自愈降级调度');
      let minDeps = Infinity;
      let fallbackCandidate = -1;
      for (let i = 0; i < manifest.program_targets.length; i += 1) {
        if (!completed.has(i) && remaining[i].size < minDeps) {
          minDeps = remaining[i].size;
          fallbackCandidate = i;
        }
      }
      if (fallbackCandidate >= 0) {
        layer.push(fallbackCandidate);
      } else {
        for (let i = 0; i < manifest.program_targets.length; i += 1) {
          if (!completed.has(i)) layer.push(i);
        }
      }
    }
    layers.push(layer);
    for (const index of layer) completed.add(index);
    for (const item of remaining) for (const index of layer) item.delete(index);
  }
  return layers;
}

/** Build the browser-only execution plan after every lineage relation has been checked. */
export function buildResolvedExecutionPlan(base: ResolvedExecutionPlan['base'], blueprint: ProjectBlueprint, manifest: ProjectManifest, programs: ResolvedExecutionPlan['programs']): {plan?: ResolvedExecutionPlan; issues: ValidationIssue[]} {
  const issues = projectBlueprintToManifest(blueprint, manifest);
  const programTargets = new Map(manifest.program_targets.filter((target): target is ProjectProgramTarget & {job_id: string; owner_entity_id: string; behavior_ids: string[]} => typeof target.job_id === 'string' && typeof target.owner_entity_id === 'string' && Array.isArray(target.behavior_ids)).map(target => [target.job_id, target]));
  if (programs.length !== programTargets.size || programs.some(item => !programTargets.has(item.job_id))) issues.push(issue('/programs', 'Resolved plan must include exactly one program for every manifest job'));
  if (issues.length) return {issues};
  return {issues, plan: {
    schema_version: 1, base, ordered_operations: manifest.resource_ops, shared_state: manifest.shared_state, local_state: manifest.local_state ?? [], programs,
    lineage: {
      need_bindings: manifest.resource_ops.flatMap(op => 'source_need_id' in op && typeof op.source_need_id === 'string' ? [{need_id: op.source_need_id, op_id: op.op_id}] : []),
      entity_to_target: [...programTargets.values()].map(target => ({entity_id: target.owner_entity_id, target_ref: target.target})),
      job_to_target: [...programTargets.values()].map(target => ({job_id: target.job_id, target_ref: target.target})),
    },
  }};
}
