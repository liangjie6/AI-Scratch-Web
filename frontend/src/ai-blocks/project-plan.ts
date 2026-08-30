import type {ValidationIssue, ValidationResult} from './program-ir';

export const PROJECT_PLAN_LIMITS = {
  resourceOps: 24,
  programTargets: 12,
  blockPrograms: 0,
  tags: 20,
  searchTerms: 12,
  costumes: 12,
  text: 2000
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
  tags: string[];
}
export type ResourceOp =
  | {op: 'add_builtin_sprite'; op_id: string; name: string; x: number; y: number; size: number; tags: string[]; source: ScratchLibrarySource & {asset_type: 'sprite'}}
  | {op: 'add_sprite'; op_id: string; name: string; x: number; y: number; size: number; tags: string[]; source: ProjectManifestResourceSource; costumes: CostumeResource[]}
  | {op: 'delete_sprite'; op_id: string; target: Extract<TargetRef, {kind: 'existing_sprite'}>; name: string}
  | {op: 'add_costume'; op_id: string; target: TargetRef; name: string; source: ProjectManifestResourceSource; tags: string[]; select_after: boolean}
  | {op: 'add_backdrop'; op_id: string; name: string; source: ProjectManifestResourceSource; tags: string[]; select_after: boolean}
  | {op: 'select_backdrop'; op_id: string; name: string};
export interface ProjectProgramTarget {target: TargetRef; mode: 'append' | 'modify' | 'rewrite'; intent: string}
export interface ProjectSharedState {
  variables: Array<{name: string; value?: string | number | boolean; scope?: 'local' | 'global'}>;
  lists: Array<{name: string; value?: Array<string | number | boolean>; scope?: 'local' | 'global'}>;
  broadcasts: string[];
}
export interface ProjectManifest {
  schema_version: 1;
  scope: 'project';
  summary: string;
  resource_ops: ResourceOp[];
  program_targets: ProjectProgramTarget[];
  shared_state?: ProjectSharedState;
}

const issue = (path: string, message: string): ValidationIssue => ({path, message});
const isRecord = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === 'object' && !Array.isArray(value);
const isString = (value: unknown, max: number = PROJECT_PLAN_LIMITS.text): value is string => typeof value === 'string' && value.trim().length > 0 && value.length <= max;
const isFiniteNumber = (value: unknown) => typeof value === 'number' && Number.isFinite(value);
const targetKey = (target: TargetRef) => target.kind + ':' + (target.kind === 'existing_sprite' ? target.name : target.kind === 'created_sprite' ? target.op_id : '');

function validateSource(value: unknown, path: string, issues: ValidationIssue[], expected?: ScratchLibrarySource['asset_type']) {
  if (!isRecord(value) || (value.kind !== 'scratch_library' && value.kind !== 'image_generation')) {
    issues.push(issue(path + '.kind', 'must be a supported resource source'));
    return;
  }
  if (value.kind === 'scratch_library') {
    if (!['sprite', 'costume', 'backdrop'].includes(String(value.asset_type))) issues.push(issue(path + '.asset_type', 'must be sprite, costume, or backdrop'));
    if (expected && value.asset_type !== expected) issues.push(issue(path + '.asset_type', 'does not match the resource operation'));
    if (!Array.isArray(value.search_terms) || value.search_terms.length < 1 || value.search_terms.length > PROJECT_PLAN_LIMITS.searchTerms || value.search_terms.some(item => !isString(item, 120))) {
      issues.push(issue(path + '.search_terms', 'must contain 1..12 non-empty search terms'));
    }
    return;
  }
  for (const field of ['reason', 'prompt', 'style']) if (!isString(value[field])) issues.push(issue(path + '.' + field, 'must be a non-empty string'));
  if (value.custom_style !== undefined && !isString(value.custom_style, 120)) issues.push(issue(path + '.custom_style', 'must be a non-empty string when present'));
  if (!isFiniteNumber(value.width) || ![512, 768, 1024].includes(value.width as number)) issues.push(issue(path + '.width', 'must be 512, 768, or 1024'));
  if (!isFiniteNumber(value.height) || ![512, 768, 1024].includes(value.height as number)) issues.push(issue(path + '.height', 'must be 512, 768, or 1024'));
  if (value.sheet_layout !== '1x1') issues.push(issue(path + '.sheet_layout', 'must be 1x1'));
}

function validateTarget(value: unknown, path: string, issues: ValidationIssue[], existingSpriteNames: string[], created: Set<string>) {
  if (!isRecord(value) || !['current_target', 'stage', 'existing_sprite', 'created_sprite'].includes(String(value.kind))) {
    issues.push(issue(path + '.kind', 'must be a supported target reference'));
    return;
  }
  const kind = value.kind;
  if (kind === 'current_target' || kind === 'stage') {
    if ('name' in value || 'op_id' in value) issues.push(issue(path, kind + ' cannot contain name or op_id'));
  } else if (kind === 'existing_sprite') {
    if (!isString(value.name, 120)) issues.push(issue(path + '.name', 'is required'));
    else if (existingSpriteNames.length > 0) {
      const wanted = value.name.trim().toLocaleLowerCase();
      const matches = existingSpriteNames.filter(item => item.trim().toLocaleLowerCase() === wanted);
      if (matches.length === 0) issues.push(issue(path + '.name', 'must refer to an existing sprite'));
      else if (matches.length > 1) issues.push(issue(path + '.name', 'must refer to a uniquely named sprite'));
    }
  } else {
    if (!isString(value.op_id, 120)) issues.push(issue(path + '.op_id', 'is required'));
    else if (!created.has(value.op_id)) issues.push(issue(path + '.op_id', 'must reference a prior sprite resource operation'));
  }
}

function validateTags(value: unknown, path: string, issues: ValidationIssue[]) {
  if (!Array.isArray(value) || value.length > PROJECT_PLAN_LIMITS.tags || value.some(item => !isString(item, 120))) issues.push(issue(path, 'must contain at most 20 non-empty tags'));
}

function validateOperation(value: unknown, index: number, issues: ValidationIssue[], existingSpriteNames: string[], created: Set<string>) {
  const path = 'resource_ops[' + index + ']';
  if (!isRecord(value)) {
    issues.push(issue(path, 'must be an object'));
    return;
  }
  if (!isString(value.op_id, 120)) issues.push(issue(path + '.op_id', 'must be a non-empty identifier'));
  if (!isString(value.name, 120)) issues.push(issue(path + '.name', 'must be a non-empty name'));
  const op = value.op;
  if (!['add_builtin_sprite', 'add_sprite', 'add_costume', 'add_backdrop', 'select_backdrop', 'delete_sprite'].includes(String(op))) {
    issues.push(issue(path + '.op', 'is not supported'));
    return;
  }
  if (op === 'select_backdrop') return;
  if (op === 'delete_sprite') {
    validateTarget(value.target, path + '.target', issues, existingSpriteNames, created);
    if (isRecord(value.target) && value.target.kind !== 'existing_sprite') issues.push(issue(path + '.target.kind', 'delete_sprite requires an existing sprite'));
    if ('source' in value || 'x' in value || 'y' in value || 'size' in value || 'tags' in value || 'select_after' in value || 'costumes' in value) issues.push(issue(path, 'delete_sprite contains unsupported resource fields'));
    return;
  }
  if (op === 'add_builtin_sprite' || op === 'add_sprite') {
    for (const field of ['x', 'y', 'size']) if (!isFiniteNumber(value[field])) issues.push(issue(path + '.' + field, 'must be a finite number'));
    if (isFiniteNumber(value.size) && (value.size as number) < 1) issues.push(issue(path + '.size', 'must be positive'));
    validateTags(value.tags, path + '.tags', issues);
  } else {
    validateTags(value.tags, path + '.tags', issues);
    if (value.select_after !== true && value.select_after !== false) issues.push(issue(path + '.select_after', 'must be boolean'));
  }
  if (op === 'add_builtin_sprite') {
    validateSource(value.source, path + '.source', issues, 'sprite');
    if (value.target !== undefined) issues.push(issue(path + '.target', 'is not allowed'));
    created.add(String(value.op_id));
    return;
  }
  if (op === 'add_sprite') {
    validateSource(value.source, path + '.source', issues);
    const costumes = value.costumes;
    if (!Array.isArray(costumes) || costumes.length > PROJECT_PLAN_LIMITS.costumes) {
      issues.push(issue(path + '.costumes', 'must be an array of at most 12 costumes'));
    } else {
      costumes.forEach((costume, costumeIndex) => {
        const costumePath = path + '.costumes[' + costumeIndex + ']';
        if (!isRecord(costume)) {
          issues.push(issue(costumePath, 'must be an object'));
          return;
        }
        if (!isString(costume.name, 120)) issues.push(issue(costumePath + '.name', 'must be a non-empty name'));
        validateSource(costume.source, costumePath + '.source', issues);
        validateTags(costume.tags, costumePath + '.tags', issues);
        if (isRecord(costume.source) && costume.source.kind === 'scratch_library' && costume.source.asset_type === 'sprite') issues.push(issue(costumePath + '.source.asset_type', 'must not be sprite for a costume'));
      });
    }
    if (value.source === undefined && (!Array.isArray(costumes) || costumes.length === 0)) issues.push(issue(path + '.source', 'is required when costumes is empty'));
    created.add(String(value.op_id));
    return;
  }
  if (op === 'add_costume') {
    validateTarget(value.target, path + '.target', issues, existingSpriteNames, created);
    if (isRecord(value.target) && value.target.kind === 'stage') issues.push(issue(path + '.target.kind', 'add_costume cannot target the stage'));
    if (isRecord(value.source) && value.source.kind === 'scratch_library' && value.source.asset_type === 'sprite') issues.push(issue(path + '.source.asset_type', 'must not be sprite for a costume'));
  } else if (value.target !== undefined) {
    issues.push(issue(path + '.target', 'is not allowed'));
  }
  validateSource(value.source, path + '.source', issues, op === 'add_backdrop' ? 'backdrop' : undefined);
}

export function validateProjectManifest(value: unknown, existingSpriteNames: string[] = []): ValidationResult {
  const issues: ValidationIssue[] = [];
  if (!isRecord(value)) return {valid: false, issues: [issue('$', 'project manifest must be an object')]};
  if (value.schema_version !== 1) issues.push(issue('schema_version', 'must be 1'));
  if (value.scope !== 'project') issues.push(issue('scope', 'must be project'));
  if (!isString(value.summary, 1000)) issues.push(issue('summary', 'must be a non-empty string up to 1000 characters'));
  for (const key of ['block_programs', 'program', 'scripts', 'blocks', 'next', 'parent', 'shadow', 'vm_id', 'javascript', 'project.json']) if (key in value) issues.push(issue(key, 'is not allowed in a project manifest'));
  const resourceOps = value.resource_ops;
  const programTargets = value.program_targets;
  if (!Array.isArray(resourceOps) || resourceOps.length > PROJECT_PLAN_LIMITS.resourceOps) issues.push(issue('resource_ops', 'must be an array of at most 24 operations'));
  if (!Array.isArray(programTargets) || programTargets.length > PROJECT_PLAN_LIMITS.programTargets) issues.push(issue('program_targets', 'must be an array of at most 12 targets'));
  const created = new Set<string>();
  const operationIds = new Set<string>();
  if (Array.isArray(resourceOps)) resourceOps.forEach((operation, index) => {
    if (isRecord(operation) && typeof operation.op_id === 'string') {
      if (operationIds.has(operation.op_id)) issues.push(issue('resource_ops[' + index + '].op_id', 'must be unique'));
      operationIds.add(operation.op_id);
    }
    validateOperation(operation, index, issues, existingSpriteNames, created);
  });
  const targetKeys = new Set<string>();
  if (Array.isArray(programTargets)) programTargets.forEach((target, index) => {
    const path = 'program_targets[' + index + ']';
    if (!isRecord(target)) {
      issues.push(issue(path, 'must be an object'));
      return;
    }
    validateTarget(target.target, path + '.target', issues, existingSpriteNames, created);
    if (!['append', 'modify', 'rewrite'].includes(String(target.mode))) issues.push(issue(path + '.mode', 'must be append, modify, or rewrite'));
    if (!isString(target.intent, 120)) issues.push(issue(path + '.intent', 'must be a non-empty string'));
    if (isRecord(target.target)) {
      const key = targetKey(target.target as TargetRef);
      if (targetKeys.has(key)) issues.push(issue(path + '.target', 'must be unique'));
      targetKeys.add(key);
      if (target.target.kind !== 'current_target' && target.mode !== 'append') issues.push(issue(path + '.mode', 'non-current targets must use append'));
    }
  });
  if (value.shared_state !== undefined) {
    const shared = value.shared_state;
    if (!isRecord(shared)) issues.push(issue('shared_state', 'must be an object'));
    else {
      for (const field of ['variables', 'lists', 'broadcasts']) if (!Array.isArray(shared[field])) issues.push(issue('shared_state.' + field, 'must be an array'));
      const names = (field: string) => Array.isArray(shared[field]) ? shared[field] : [];
      for (const [field, values] of [['variables', names('variables')], ['lists', names('lists')]] as const) {
        const seen = new Set<string>();
        values.forEach((item, index) => {
          if (!isRecord(item) || !isString(item.name, 120)) issues.push(issue('shared_state.' + field + '[' + index + '].name', 'must be a non-empty name'));
          else if (seen.has(item.name)) issues.push(issue('shared_state.' + field + '[' + index + '].name', 'must be unique'));
          else seen.add(item.name);
          if (isRecord(item) && item.scope === 'local') issues.push(issue('shared_state.' + field + '[' + index + '].scope', 'must be global'));
        });
      }
      const broadcasts = names('broadcasts');
      const seenBroadcasts = new Set<string>();
      broadcasts.forEach((item, index) => {
        if (!isString(item, 120)) issues.push(issue('shared_state.broadcasts[' + index + ']', 'must be a non-empty name'));
        else if (seenBroadcasts.has(item)) issues.push(issue('shared_state.broadcasts[' + index + ']', 'must be unique'));
        else seenBroadcasts.add(item);
      });
    }
  }
  return {valid: issues.length === 0, issues};
}

export function assertProjectManifest(value: unknown, existingSpriteNames?: string[]): asserts value is ProjectManifest {
  const result = validateProjectManifest(value, existingSpriteNames);
  if (!result.valid) throw new Error(result.issues.map(item => item.path + ': ' + item.message).join('; '));
}
