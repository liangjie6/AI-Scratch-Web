import type {ProgramIR, ValidationIssue, ValidationResult} from './program-ir';
import {
  RESOURCE_CONTRACT_VERSION,
  RESOURCE_OPERATION_CONTRACT,
  RESOURCE_OPERATION_NAMES,
  RESOURCE_SOURCE_CONTRACT,
} from './generated/resource-contract';

export {RESOURCE_CONTRACT_VERSION, RESOURCE_OPERATION_CONTRACT, RESOURCE_OPERATION_NAMES, RESOURCE_SOURCE_CONTRACT};

export const PROJECT_PLAN_LIMITS = {
  resourceOps: 24,
  programTargets: 12,
  blockPrograms: 0,
  tags: 20,
  searchTerms: 12,
  costumes: 12,
  text: 2000,
} as const;

export type AiBlocksScope = 'single_sprite' | 'project';
export type AssetPolicy = 'builtin_first' | 'existing_only' | 'allow_generation';
export type TargetRef =
  | {kind: 'current_target'}
  | {kind: 'stage'}
  | {kind: 'existing_sprite'; name: string}
  | {kind: 'created_sprite'; op_id: string};
export type ScratchLibrarySource = {
  kind: 'scratch_library';
  asset_type: 'sprite' | 'costume' | 'backdrop';
  search_terms: string[];
};
export type ImageGenerationSource = {
  kind: 'image_generation';
  reason: string;
  prompt: string;
  style: string;
  custom_style?: string;
  width: number;
  height: number;
  sheet_layout: '1x1';
};
export type ProjectManifestResourceSource = ScratchLibrarySource | ImageGenerationSource;
export interface CostumeResource {
  name: string;
  source: ProjectManifestResourceSource;
  tags?: string[];
}
export type ResourceOp =
  | {op: 'add_builtin_sprite'; op_id: string; name: string; x: number; y: number; size: number; tags?: string[]; source: ScratchLibrarySource & {asset_type: 'sprite'}}
  | {op: 'add_sprite'; op_id: string; name: string; x: number; y: number; size: number; tags?: string[]; costumes: CostumeResource[]}
  | {op: 'delete_sprite'; op_id: string; target: Extract<TargetRef, {kind: 'existing_sprite'}>; name: string}
  | {op: 'add_costume'; op_id: string; target: Exclude<TargetRef, {kind: 'stage'}>; name: string; source: ProjectManifestResourceSource; tags?: string[]; select_after?: boolean}
  | {op: 'add_backdrop'; op_id: string; name: string; source: ProjectManifestResourceSource; tags?: string[]; select_after?: boolean}
  | {op: 'select_backdrop'; op_id: string; name: string};
export interface ProjectProgramTarget {target: TargetRef; mode: 'append' | 'modify' | 'rewrite'; intent: string}
export interface ProjectSharedState {
  variables: Array<{name: string; value?: string | number | boolean; scope?: 'local' | 'global'}>;
  lists: Array<{name: string; value?: Array<string | number | boolean>; scope?: 'local' | 'global'}>;
  broadcasts: string[];
}
export interface ProjectManifest {
  schema_version: typeof RESOURCE_CONTRACT_VERSION;
  scope: 'project';
  summary: string;
  resource_ops: ResourceOp[];
  program_targets: ProjectProgramTarget[];
  shared_state: ProjectSharedState;
}

type IssueDetails = Partial<Pick<ValidationIssue, 'code' | 'actual' | 'expected' | 'operation' | 'repair_scope' | 'repair_id'>>;
const issue = (path: string, message: string, details: IssueDetails = {}): ValidationIssue => ({path, message, ...details});
const contractIssue = (path: string, message: string, operation: string, code: string, actual?: unknown): ValidationIssue => issue(path, message, {
  code,
  actual,
  expected: message,
  operation,
  repair_scope: operation === 'manifest' ? 'manifest' : 'resource_op',
  repair_id: null,
});
const isRecord = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === 'object' && !Array.isArray(value);
const isString = (value: unknown, max: number = PROJECT_PLAN_LIMITS.text): value is string => typeof value === 'string' && value.trim().length > 0 && value.length <= max;
const isFiniteNumber = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);
const targetKey = (target: TargetRef) => target.kind + ':' + (target.kind === 'existing_sprite' ? target.name : target.kind === 'created_sprite' ? target.op_id : '');
const operationSpec = (op: unknown) => RESOURCE_OPERATION_CONTRACT.find(item => item.op === op);

function validateSource(value: unknown, path: string, issues: ValidationIssue[], expectedAssetType?: string, allowedKinds: readonly string[] = ['scratch_library', 'image_generation']) {
  if (!isRecord(value) || typeof value.kind !== 'string' || !allowedKinds.includes(value.kind)) {
    issues.push(contractIssue(path + '/kind', 'must use a supported source kind', 'resource', 'FIELD_INVALID', value));
    return;
  }
  const sourceSpec = RESOURCE_SOURCE_CONTRACT.find(item => item.kind === value.kind);
  if (!sourceSpec) {
    issues.push(contractIssue(path + '/kind', 'must use a generated source kind', 'resource', 'FIELD_INVALID', value.kind));
    return;
  }
  const allowedFields = new Set<string>([...sourceSpec.required_fields, ...sourceSpec.optional_fields]);
  for (const key of Object.keys(value)) if (!allowedFields.has(key)) issues.push(contractIssue(path + '/' + key, 'is forbidden for this source kind', 'resource', 'FIELD_FORBIDDEN', value[key]));
  for (const field of sourceSpec.required_fields) if (value[field] === undefined) issues.push(contractIssue(path + '/' + field, 'is required', 'resource', 'FIELD_REQUIRED'));
  if (value.kind === 'scratch_library') {
    if (!(sourceSpec.asset_types as readonly string[]).includes(String(value.asset_type))) issues.push(contractIssue(path + '/asset_type', 'must be a supported asset type', 'resource', 'FIELD_INVALID', value.asset_type));
    if (expectedAssetType && value.asset_type !== expectedAssetType) issues.push(contractIssue(path + '/asset_type', 'does not match the resource operation', 'resource', 'SOURCE_TYPE_MISMATCH', value.asset_type));
    if (!Array.isArray(value.search_terms) || value.search_terms.length < 1 || value.search_terms.length > PROJECT_PLAN_LIMITS.searchTerms || value.search_terms.some(item => !isString(item, 120))) {
      issues.push(contractIssue(path + '/search_terms', 'must contain 1..12 non-empty search terms', 'resource', 'FIELD_INVALID', value.search_terms));
    }
    return;
  }
  for (const field of ['reason', 'prompt', 'style']) if (!isString(value[field])) issues.push(contractIssue(path + '/' + field, 'must be a non-empty string', 'resource', 'FIELD_INVALID', value[field]));
  if (value.custom_style !== undefined && !isString(value.custom_style, 120)) issues.push(contractIssue(path + '/custom_style', 'must be a non-empty string when present', 'resource', 'FIELD_INVALID', value.custom_style));
  if (!isFiniteNumber(value.width) || ![512, 768, 1024].includes(value.width)) issues.push(contractIssue(path + '/width', 'must be 512, 768, or 1024', 'resource', 'FIELD_INVALID', value.width));
  if (!isFiniteNumber(value.height) || ![512, 768, 1024].includes(value.height)) issues.push(contractIssue(path + '/height', 'must be 512, 768, or 1024', 'resource', 'FIELD_INVALID', value.height));
  if (value.sheet_layout !== '1x1') issues.push(contractIssue(path + '/sheet_layout', 'must be 1x1', 'resource', 'FIELD_INVALID', value.sheet_layout));
}

function validateTarget(value: unknown, path: string, issues: ValidationIssue[], existingSpriteNames: string[], created: Set<string>, allowedKinds?: readonly string[]) {
  if (!isRecord(value) || !['current_target', 'stage', 'existing_sprite', 'created_sprite'].includes(String(value.kind))) {
    issues.push(contractIssue(path + '/kind', 'must be a supported target reference', 'target', 'FIELD_INVALID', value));
    return;
  }
  const kind = String(value.kind);
  if (allowedKinds && !allowedKinds.includes(kind)) issues.push(contractIssue(path + '/kind', 'target kind is not allowed for this operation', 'target', 'TARGET_KIND_INVALID', kind));
  if (kind === 'current_target' || kind === 'stage') {
    if (Object.keys(value).some(key => key !== 'kind')) issues.push(contractIssue(path, kind + ' cannot contain name or op_id', 'target', 'FIELD_FORBIDDEN', value));
  } else if (kind === 'existing_sprite') {
    if (!isString(value.name, 120)) issues.push(contractIssue(path + '/name', 'is required', 'target', 'FIELD_REQUIRED', value.name));
    else if (existingSpriteNames.length > 0) {
      const wanted = value.name.trim().toLocaleLowerCase();
      const matches = existingSpriteNames.filter(item => item.trim().toLocaleLowerCase() === wanted);
      if (matches.length === 0) issues.push(contractIssue(path + '/name', 'must refer to an existing sprite', 'target', 'TARGET_NOT_FOUND', value.name));
      else if (matches.length > 1) issues.push(contractIssue(path + '/name', 'must refer to a uniquely named sprite', 'target', 'TARGET_NOT_UNIQUE', value.name));
    }
    if (Object.keys(value).some(key => key !== 'kind' && key !== 'name')) issues.push(contractIssue(path, 'existing_sprite accepts only kind and name', 'target', 'FIELD_FORBIDDEN', value));
  } else {
    if (!isString(value.op_id, 120)) issues.push(contractIssue(path + '/op_id', 'is required', 'target', 'FIELD_REQUIRED', value.op_id));
    else if (!created.has(value.op_id)) issues.push(contractIssue(path + '/op_id', 'must reference a prior sprite resource operation', 'target', 'TARGET_NOT_CREATED', value.op_id));
    if (Object.keys(value).some(key => key !== 'kind' && key !== 'op_id')) issues.push(contractIssue(path, 'created_sprite accepts only kind and op_id', 'target', 'FIELD_FORBIDDEN', value));
  }
}

function validateTags(value: unknown, path: string, issues: ValidationIssue[], operation: string) {
  if (!Array.isArray(value) || value.length > PROJECT_PLAN_LIMITS.tags || value.some(item => !isString(item, 120))) issues.push(contractIssue(path, 'must contain at most 20 non-empty tags', operation, 'FIELD_INVALID', value));
}

function normalizedOperation(value: Record<string, unknown>): Record<string, unknown> {
  const normalized = {...value};
  if (normalized.op === 'add_backdrop') {
    if (!('select_after' in normalized)) normalized.select_after = false;
    if (isRecord(normalized.target) && normalized.target.kind === 'stage' && Object.keys(normalized.target).length === 1) delete normalized.target;
    for (const field of ['x', 'y', 'size']) if (normalized[field] === null) delete normalized[field];
  } else if (normalized.op === 'add_costume' && !('select_after' in normalized)) {
    normalized.select_after = false;
  }
  return normalized;
}

function validateOperation(value: unknown, index: number, issues: ValidationIssue[], existingSpriteNames: string[], created: Set<string>) {
  const path = '/resource_ops/' + index;
  if (!isRecord(value)) {
    issues.push(contractIssue(path, 'must be an object', 'resource', 'FIELD_INVALID', value));
    return;
  }
  const normalized = normalizedOperation(value);
  const op = normalized.op;
  const spec = operationSpec(op);
  if (!spec) {
    issues.push(contractIssue(path + '/op', 'is not supported', 'resource', 'OP_NOT_SUPPORTED', op));
    return;
  }
  const allowed = new Set<string>([...spec.required_fields, ...spec.optional_fields]);
  for (const key of Object.keys(normalized)) {
    if (!allowed.has(key)) issues.push(contractIssue(path + '/' + key, 'is forbidden for ' + op, String(op), 'FIELD_FORBIDDEN', normalized[key]));
  }
  for (const field of spec.required_fields) if (normalized[field] === undefined) issues.push(contractIssue(path + '/' + field, 'is required', String(op), 'FIELD_REQUIRED'));
  if (normalized.op_id !== undefined && !isString(normalized.op_id, 120)) issues.push(contractIssue(path + '/op_id', 'must be a non-empty identifier', String(op), 'FIELD_INVALID', normalized.op_id));
  if (normalized.name !== undefined && !isString(normalized.name, 120)) issues.push(contractIssue(path + '/name', 'must be a non-empty name', String(op), 'FIELD_INVALID', normalized.name));
  for (const field of ['x', 'y']) if (normalized[field] !== undefined && (!Number.isInteger(normalized[field]) || Number(normalized[field]) < -10000 || Number(normalized[field]) > 10000)) issues.push(contractIssue(path + '/' + field, 'must be an integer between -10000 and 10000', String(op), 'FIELD_INVALID', normalized[field]));
  if (normalized.size !== undefined && (!Number.isInteger(normalized.size) || Number(normalized.size) < 1 || Number(normalized.size) > 1000)) issues.push(contractIssue(path + '/size', 'must be an integer between 1 and 1000', String(op), 'FIELD_INVALID', normalized.size));
  if (normalized.tags !== undefined) validateTags(normalized.tags, path + '/tags', issues, String(op));
  if (normalized.select_after !== undefined && typeof normalized.select_after !== 'boolean') issues.push(contractIssue(path + '/select_after', 'must be boolean', String(op), 'FIELD_INVALID', normalized.select_after));

  if (op === 'add_builtin_sprite') {
    validateSource(normalized.source, path + '/source', issues, 'sprite', spec.source_kinds);
    if (isString(normalized.op_id, 120)) created.add(normalized.op_id);
    return;
  }
  if (op === 'add_sprite') {
    const costumes = normalized.costumes;
    if (!Array.isArray(costumes) || costumes.length < (spec.nested_costumes_min ?? 0) || costumes.length > (spec.nested_costumes_max ?? PROJECT_PLAN_LIMITS.costumes)) {
      issues.push(contractIssue(path + '/costumes', 'must contain 1..12 costumes', String(op), 'FIELD_INVALID', costumes));
    } else {
      costumes.forEach((costume, costumeIndex) => {
        const costumePath = path + '/costumes/' + costumeIndex;
        if (!isRecord(costume)) {
          issues.push(contractIssue(costumePath, 'must be an object', String(op), 'FIELD_INVALID', costume));
          return;
        }
        for (const key of Object.keys(costume)) if (!['name', 'source', 'tags'].includes(key)) issues.push(contractIssue(costumePath + '/' + key, 'is forbidden for a costume', String(op), 'FIELD_FORBIDDEN', costume[key]));
        if (!isString(costume.name, 120)) issues.push(contractIssue(costumePath + '/name', 'must be a non-empty name', String(op), 'FIELD_REQUIRED', costume.name));
        validateSource(costume.source, costumePath + '/source', issues, 'costume', spec.source_kinds);
        if (costume.tags !== undefined) validateTags(costume.tags, costumePath + '/tags', issues, String(op));
      });
    }
    if (isString(normalized.op_id, 120)) created.add(normalized.op_id);
    return;
  }
  if (op === 'delete_sprite') {
    validateTarget(normalized.target, path + '/target', issues, existingSpriteNames, created, spec.target_kinds);
    return;
  }
  if (op === 'add_costume') {
    validateTarget(normalized.target, path + '/target', issues, existingSpriteNames, created, spec.target_kinds);
    validateSource(normalized.source, path + '/source', issues, 'costume', spec.source_kinds);
    return;
  }
  if (op === 'add_backdrop') {
    if (normalized.target !== undefined) issues.push(contractIssue(path + '/target', 'must be omitted; backdrop targets the stage', String(op), 'INVALID_BACKDROP_TARGET', normalized.target));
    for (const field of ['x', 'y', 'size']) if (normalized[field] !== undefined) issues.push(contractIssue(path + '/' + field, 'is not allowed for add_backdrop', String(op), 'INVALID_BACKDROP_PLACEMENT', normalized[field]));
    validateSource(normalized.source, path + '/source', issues, 'backdrop', spec.source_kinds);
    return;
  }
}

export function validateProjectManifest(value: unknown, existingSpriteNames: string[] = []): ValidationResult {
  const issues: ValidationIssue[] = [];
  if (!isRecord(value)) return {valid: false, issues: [contractIssue('/', 'project manifest must be an object', 'manifest', 'SCHEMA_VALIDATION_ERROR', value)]};
  if (!('schema_version' in value)) issues.push(contractIssue('/schema_version', 'is required', 'manifest', 'SCHEMA_VALIDATION_ERROR'));
  else if (value.schema_version !== RESOURCE_CONTRACT_VERSION) issues.push(contractIssue('/schema_version', 'must be 2', 'manifest', 'UNSUPPORTED_SCHEMA_VERSION', value.schema_version));
  if (value.scope !== 'project') issues.push(contractIssue('/scope', 'must be project', 'manifest', 'SCHEMA_VALIDATION_ERROR', value.scope));
  if (!isString(value.summary, 1000)) issues.push(contractIssue('/summary', 'must be a non-empty string up to 1000 characters', 'manifest', 'FIELD_INVALID', value.summary));
  const manifestFields = new Set(['schema_version', 'scope', 'summary', 'resource_ops', 'program_targets', 'shared_state']);
  for (const key of Object.keys(value)) if (!manifestFields.has(key)) issues.push(contractIssue('/' + key, 'is forbidden in a project manifest', 'manifest', 'FIELD_FORBIDDEN', value[key]));
  for (const key of ['schema_version', 'scope', 'summary', 'resource_ops', 'program_targets', 'shared_state']) if (!(key in value)) issues.push(contractIssue('/' + key, 'is required', 'manifest', 'FIELD_REQUIRED'));
  const resourceOps = value.resource_ops;
  const programTargets = value.program_targets;
  if (!Array.isArray(resourceOps) || resourceOps.length > PROJECT_PLAN_LIMITS.resourceOps) issues.push(contractIssue('/resource_ops', 'must be an array of at most 24 operations', 'manifest', 'FIELD_INVALID', resourceOps));
  if (!Array.isArray(programTargets) || programTargets.length > PROJECT_PLAN_LIMITS.programTargets) issues.push(contractIssue('/program_targets', 'must be an array of at most 12 targets', 'manifest', 'FIELD_INVALID', programTargets));
  const created = new Set<string>();
  const operationIds = new Set<string>();
  if (Array.isArray(resourceOps)) resourceOps.forEach((operation, index) => {
    if (isRecord(operation) && typeof operation.op_id === 'string') {
      if (operationIds.has(operation.op_id)) issues.push(contractIssue('/resource_ops/' + index + '/op_id', 'must be unique', 'manifest', 'DUPLICATE_OPERATION_ID', operation.op_id));
      operationIds.add(operation.op_id);
    }
    validateOperation(operation, index, issues, existingSpriteNames, created);
  });
  const targetKeys = new Set<string>();
  if (Array.isArray(programTargets)) programTargets.forEach((target, index) => {
    const path = '/program_targets/' + index;
    if (!isRecord(target)) {
      issues.push(contractIssue(path, 'must be an object', 'manifest', 'FIELD_INVALID', target));
      return;
    }
    for (const key of Object.keys(target)) if (!['target', 'mode', 'intent'].includes(key)) issues.push(contractIssue(path + '/' + key, 'is forbidden in a program target', 'manifest', 'FIELD_FORBIDDEN', target[key]));
    validateTarget(target.target, path + '/target', issues, existingSpriteNames, created);
    if (!['append', 'modify', 'rewrite'].includes(String(target.mode))) issues.push(contractIssue(path + '/mode', 'must be append, modify, or rewrite', 'manifest', 'FIELD_INVALID', target.mode));
    if (!isString(target.intent, 120)) issues.push(contractIssue(path + '/intent', 'must be a non-empty string', 'manifest', 'FIELD_REQUIRED', target.intent));
    if (isRecord(target.target) && ['current_target', 'stage', 'existing_sprite', 'created_sprite'].includes(String(target.target.kind))) {
      const key = targetKey(target.target as TargetRef);
      if (targetKeys.has(key)) issues.push(contractIssue(path + '/target', 'must be unique', 'manifest', 'DUPLICATE_TARGET', key));
      targetKeys.add(key);
      if (target.target.kind !== 'current_target' && target.mode !== 'append') issues.push(contractIssue(path + '/mode', 'non-current targets must use append', 'manifest', 'TARGET_MODE_INVALID', target.mode));
    }
  });
  const shared = value.shared_state;
  if (!isRecord(shared)) issues.push(contractIssue('/shared_state', 'must be an object', 'manifest', 'FIELD_INVALID', shared));
  else {
    const sharedFields = new Set(['variables', 'lists', 'broadcasts']);
    for (const key of Object.keys(shared)) if (!sharedFields.has(key)) issues.push(contractIssue('/shared_state/' + key, 'is forbidden in shared state', 'manifest', 'FIELD_FORBIDDEN', shared[key]));
    for (const field of ['variables', 'lists', 'broadcasts']) if (!Array.isArray(shared[field])) issues.push(contractIssue('/shared_state/' + field, 'must be an array', 'manifest', 'FIELD_INVALID', shared[field]));
    for (const [field, values] of [['variables', Array.isArray(shared.variables) ? shared.variables : []], ['lists', Array.isArray(shared.lists) ? shared.lists : []]] as const) {
      if (values.length > (field === 'variables' ? 100 : 50)) issues.push(contractIssue('/shared_state/' + field, 'exceeds the shared state limit', 'manifest', 'FIELD_INVALID', values.length));
      const seen = new Set<string>();
      values.forEach((item, index) => {
        const path = '/shared_state/' + field + '/' + index;
        if (!isRecord(item)) {
          issues.push(contractIssue(path, 'must be an object', 'manifest', 'FIELD_INVALID', item));
          return;
        }
        for (const key of Object.keys(item)) if (!['name', 'value', 'scope'].includes(key)) issues.push(contractIssue(path + '/' + key, 'is forbidden in a shared definition', 'manifest', 'FIELD_FORBIDDEN', item[key]));
        if (!isString(item.name, 120)) issues.push(contractIssue(path + '/name', 'must be a non-empty name', 'manifest', 'FIELD_REQUIRED', item.name));
        else if (seen.has(item.name)) issues.push(contractIssue(path + '/name', 'must be unique', 'manifest', 'DUPLICATE_NAME', item.name));
        else seen.add(item.name);
        if (item.scope !== 'global') issues.push(contractIssue(path + '/scope', 'must be global', 'manifest', 'SCOPE_INVALID', item.scope));
        if (item.value !== undefined) {
          if (field === 'variables' && !(item.value === null || typeof item.value === 'string' || typeof item.value === 'boolean' || isFiniteNumber(item.value))) {
            issues.push(contractIssue(path + '/value', 'must be a string, finite number, boolean, or null', 'manifest', 'FIELD_INVALID', item.value));
          }
          if (field === 'lists' && (!Array.isArray(item.value) || item.value.length > 500 || item.value.some(value => !(typeof value === 'string' || typeof value === 'boolean' || isFiniteNumber(value))))) {
            issues.push(contractIssue(path + '/value', 'must contain at most 500 strings, finite numbers, or booleans', 'manifest', 'FIELD_INVALID', item.value));
          }
        }
      });
    }
    const broadcasts = Array.isArray(shared.broadcasts) ? shared.broadcasts : [];
    if (broadcasts.length > 100) issues.push(contractIssue('/shared_state/broadcasts', 'must contain at most 100 broadcasts', 'manifest', 'FIELD_INVALID', broadcasts.length));
    const seenBroadcasts = new Set<string>();
    broadcasts.forEach((item, index) => {
      if (!isString(item, 120)) issues.push(contractIssue('/shared_state/broadcasts/' + index, 'must be a non-empty name', 'manifest', 'FIELD_REQUIRED', item));
      else if (seenBroadcasts.has(item)) issues.push(contractIssue('/shared_state/broadcasts/' + index, 'must be unique', 'manifest', 'DUPLICATE_NAME', item));
      else seenBroadcasts.add(item);
    });
  }
  return {valid: issues.length === 0, issues};
}

export function assertProjectManifest(value: unknown, existingSpriteNames?: string[]): asserts value is ProjectManifest {
  const result = validateProjectManifest(value, existingSpriteNames);
  if (!result.valid) throw new Error(result.issues.map(item => item.path + ': ' + item.message).join('; '));
}

function namedStateValue(value: {name: string; value?: unknown; scope?: string}, list: boolean) {
  return {name: value.name, value: value.value === undefined ? (list ? [] : null) : value.value, scope: value.scope};
}

export function validateProjectProgramSharedState(program: ProgramIR, sharedState: ProjectSharedState): ValidationResult {
  const issues: ValidationIssue[] = [];
  const expectedVariables = sharedState.variables.map(item => namedStateValue(item, false)).sort((left, right) => left.name.localeCompare(right.name));
  const expectedLists = sharedState.lists.map(item => namedStateValue(item, true)).sort((left, right) => left.name.localeCompare(right.name));
  const actualVariables = program.variables.filter(item => item.scope === 'global').map(item => namedStateValue(item, false)).sort((left, right) => left.name.localeCompare(right.name));
  const actualLists = program.lists.filter(item => item.scope === 'global').map(item => namedStateValue(item, true)).sort((left, right) => left.name.localeCompare(right.name));
  if (JSON.stringify(actualVariables) !== JSON.stringify(expectedVariables)) issues.push(contractIssue('/variables', 'project program global variables must exactly repeat manifest.shared_state.variables', 'program', 'SHARED_STATE_MISMATCH', actualVariables));
  if (JSON.stringify(actualLists) !== JSON.stringify(expectedLists)) issues.push(contractIssue('/lists', 'project program global lists must exactly repeat manifest.shared_state.lists', 'program', 'SHARED_STATE_MISMATCH', actualLists));
  if (JSON.stringify([...program.broadcasts].sort()) !== JSON.stringify([...sharedState.broadcasts].sort())) issues.push(contractIssue('/broadcasts', 'project program broadcasts must exactly repeat manifest.shared_state.broadcasts', 'program', 'SHARED_STATE_MISMATCH', program.broadcasts));
  const globalNames = new Set([...sharedState.variables, ...sharedState.lists].map(item => item.name));
  if (program.variables.some(item => item.scope !== 'global' && globalNames.has(item.name))) issues.push(contractIssue('/variables', 'target-local state cannot reuse a manifest global name', 'program', 'SHARED_STATE_NAME_CONFLICT', program.variables));
  if (program.lists.some(item => item.scope !== 'global' && globalNames.has(item.name))) issues.push(contractIssue('/lists', 'target-local state cannot reuse a manifest global name', 'program', 'SHARED_STATE_NAME_CONFLICT', program.lists));
  return {valid: issues.length === 0, issues};
}
