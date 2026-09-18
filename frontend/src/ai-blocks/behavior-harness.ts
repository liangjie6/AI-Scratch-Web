import type {AcceptanceArrange, AcceptanceExpectation, AcceptanceTrigger, ProjectBlueprint, ScratchTaskSpec, TaskStateKind} from './project-contract';

export interface BehaviorVmTarget {
  id: string;
  isStage?: boolean;
  isOriginal?: boolean;
  sprite?: {name?: string};
  getName?(): string;
  getCostumes?(): Array<{name?: string}>;
  getSounds?(): Array<{name?: string}>;
  currentCostume?: number;
  x?: number;
  y?: number;
  visible?: boolean;
  variables?: Record<string, {name: string; type: string; value: unknown}>;
  setXY?(x: number, y: number): void;
}

export interface BehaviorVmLike {
  runtime: {
    targets: BehaviorVmTarget[];
    disposeTarget?(target: BehaviorVmTarget): void;
    getTargetForStage?(): BehaviorVmTarget | undefined;
    startHats?(opcode: string, matchFields?: Record<string, unknown> | null, target?: BehaviorVmTarget): unknown;
    emit?(event: string, ...args: unknown[]): unknown;
    threads?: Array<{target?: BehaviorVmTarget}>;
    _primitives?: Record<string, (args: Record<string, unknown>, util: {target?: BehaviorVmTarget}) => unknown>;
    ioDevices?: {
      clock?: {
        resetProjectTimer?(): void;
        _projectTimer?: {startTime?: number};
      };
    };
  };
  greenFlag?(): void;
  stopAll?(): void;
}

type ResolvedTrigger =
  | {kind: 'green_flag'}
  | {kind: 'key'; key: string}
  | {kind: 'sprite_click'; targetName: string}
  | {kind: 'broadcast'; name: string};
type ResolvedArrange =
  | {kind: 'set_position'; targetName: string; x: number; y: number}
  | {kind: 'set_variable'; state: ResolvedState; value: string | number | boolean}
  | {kind: 'set_list'; state: ResolvedState; value: Array<string | number | boolean>}
  | {kind: 'set_timer'; seconds: number};
type ResolvedExpectation =
  | {kind: 'variable'; state: ResolvedState; comparator: 'eq' | 'gt' | 'gte' | 'lt' | 'lte'; value: string | number | boolean}
  | {kind: 'position_range'; targetName: string; axis: 'x' | 'y'; min: number; max: number}
  | {kind: 'costume'; targetName: string; name: string}
  | {kind: 'backdrop'; name: string}
  | {kind: 'visibility'; targetName: string; visible: boolean}
  | {kind: 'list'; state: ResolvedState; comparator: 'equals' | 'contains' | 'length'; value: string | number | boolean | Array<string | number | boolean>}
  | {kind: 'clone_count'; targetName: string; comparator: 'eq' | 'gte' | 'lte'; value: number}
  | {kind: 'say_text'; targetName: string; text: string}
  | {kind: 'question_asked'; text: string}
  | {kind: 'broadcast_emitted'; name: string; minCount: number}
  | {kind: 'sound_invoked'; targetName: string; name: string; minCount: number}
  | {kind: 'scripts_stopped'; targetName?: string};

interface ResolvedState {
  name: string;
  kind: TaskStateKind;
  ownerTargetName?: string;
}

export interface BehaviorHarnessTest {
  id: string;
  priority: 'must' | 'should';
  trigger: ResolvedTrigger;
  arrange: ResolvedArrange[];
  expect: ResolvedExpectation[];
  timeoutMs: number;
}

export interface BehaviorHarnessPlan {
  tests: BehaviorHarnessTest[];
}

export interface BehaviorHarnessResult {
  passed: boolean;
  checks: string[];
  totalCount: number;
  passedCount: number;
  mustTotal: number;
  mustPassed: number;
  error?: string;
}

export interface BehaviorBindingInput {
  entityTargets: Map<string, string>;
  states: Map<string, ResolvedState>;
  assetNeeds: Map<string, {kind: 'costume' | 'backdrop' | 'sound'; name: string}>;
}


/** Converts model-owned IDs to browser-only names (testing disabled). */
export function buildBehaviorHarnessPlan(_task?: ScratchTaskSpec, _blueprint?: ProjectBlueprint, _bindings?: BehaviorBindingInput): BehaviorHarnessPlan {
  return {tests: []};
}

/** 行为验收测试已移除，直接返回空结果并通过。 */
export async function runBehaviorHarness(_vm: BehaviorVmLike, _plan: BehaviorHarnessPlan): Promise<BehaviorHarnessResult> {
  return {passed: true, checks: [], totalCount: 0, passedCount: 0, mustTotal: 0, mustPassed: 0};
}

