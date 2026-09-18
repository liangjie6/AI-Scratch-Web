import type {ProgramIR, TargetKind, ValidationIssue, ValidationResult} from './program-ir';
import {getBlockSpec, type BuiltinAiExtension} from './block-catalog';
import {CANVAS_SIZES} from '../ai/image-generation-types';
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

const FRONTEND_ENABLED_EXTENSIONS = new Set([
  ...(import.meta.env.VITE_AI_PEN_EXTENSION_ENABLED === 'true' ? ['pen'] : []),
  ...(import.meta.env.VITE_AI_MUSIC_EXTENSION_ENABLED === 'true' ? ['music'] : []),
  ...(import.meta.env.VITE_AI_TEXT_TO_SPEECH_EXTENSION_ENABLED === 'true' ? ['text2speech'] : []),
]);

// The browser has no approved, compensable destructive-resource flow yet.
// Reject old durable responses as well as new model output until that flow is
// deliberately released with its paired frontend capability.
const DISABLED_DESTRUCTIVE_RESOURCE_OPERATIONS = new Set(['delete_sprite', 'delete_sound']);

/** Build-time flags are retained as the compatibility fallback for legacy backends. */
export function frontendEnabledBuiltinExtensions(): ReadonlySet<BuiltinAiExtension> {
  return new Set(
    [...FRONTEND_ENABLED_EXTENSIONS].filter((extension): extension is BuiltinAiExtension =>
      extension === 'pen' || extension === 'music' || extension === 'text2speech'
    )
  );
}

export type AiBlocksScope = 'single_sprite' | 'project';
export type AssetPolicy = 'builtin_first' | 'existing_only' | 'allow_generation';
export type TargetRef =
  | {kind: 'current_target'}
  | {kind: 'stage'}
  | {kind: 'existing_sprite'; name: string}
  | {kind: 'created_sprite'; op_id: string};

export function normalizeProjectManifestTargets(value: ProjectManifest): ProjectManifest {
  const normalizeTarget = (raw: unknown): unknown => {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return raw;
    const target = raw as Record<string, unknown>;
    if (target.kind === 'stage' || target.kind === 'current_target') return {kind: target.kind};
    if (target.kind === 'existing_sprite') return {kind: 'existing_sprite', name: target.name};
    if (target.kind === 'created_sprite') return {kind: 'created_sprite', op_id: target.op_id};
    return raw;
  };
  const resourceOps = value.resource_ops.map(operation => {
    const raw = operation as unknown as Record<string, unknown>;
    if (!raw || typeof raw !== 'object' || !('target' in raw)) return operation;
    const target = normalizeTarget(raw.target);
    if (raw.op === 'add_backdrop' && target && typeof target === 'object' && !Array.isArray(target) && (target as Record<string, unknown>).kind === 'stage') {
      const withoutTarget = {...raw};
      delete withoutTarget.target;
      return withoutTarget as unknown as ResourceOp;
    }
    return {...raw, target} as unknown as ResourceOp;
  });
  return {
    ...value,
    resource_ops: resourceOps,
    program_targets: value.program_targets.map(item => {
      const target = normalizeTarget(item.target);
      return target && typeof target === 'object' && !Array.isArray(target) ? {...item, target: target as TargetRef} : item;
    })
  };
}
export type ScratchLibrarySource = {
  kind: 'scratch_library';
  asset_type: 'sprite' | 'costume' | 'backdrop' | 'sound';
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
/** Recognized by the protocol, but intentionally not executable in this client yet. */
export type AudioGenerationSource = {
  kind: 'audio_generation';
  reason: string;
  prompt: string;
  audio_kind: 'sound_effect' | 'speech' | 'music';
  max_duration_seconds: number;
};
export type ProjectManifestResourceSource = ScratchLibrarySource | ImageGenerationSource | AudioGenerationSource;
export interface CostumeResource {
  name: string;
  source: Exclude<ProjectManifestResourceSource, AudioGenerationSource>;
  tags?: string[];
}
export type ResourceOp =
  | {op: 'add_builtin_sprite'; op_id: string; source_need_id?: string; owner_entity_id?: string; name: string; x: number; y: number; size: number; tags?: string[]; source: ScratchLibrarySource & {asset_type: 'sprite'}; include_costumes?: 'all'; include_sounds?: 'all' | 'none'}
  | {op: 'add_sprite'; op_id: string; source_need_id?: string; owner_entity_id?: string; name: string; x: number; y: number; size: number; tags?: string[]; costumes: CostumeResource[]}
  | {op: 'delete_sprite'; op_id: string; target: Extract<TargetRef, {kind: 'existing_sprite'}>; name: string}
  | {op: 'add_costume'; op_id: string; source_need_id?: string; target: Exclude<TargetRef, {kind: 'stage'}>; name: string; source: Exclude<ProjectManifestResourceSource, AudioGenerationSource>; tags?: string[]; select_after?: boolean}
  | {op: 'add_backdrop'; op_id: string; source_need_id?: string; name: string; source: Exclude<ProjectManifestResourceSource, AudioGenerationSource>; tags?: string[]; select_after?: boolean}
  | {op: 'add_sound'; op_id: string; source_need_id?: string; target: TargetRef; name: string; source: (ScratchLibrarySource & {asset_type: 'sound'}) | AudioGenerationSource; tags?: string[]}
  | {op: 'delete_sound'; op_id: string; target: TargetRef; sound: {kind: 'existing_sound'; name: string}}
  | {op: 'select_backdrop'; op_id: string; name: string};
export interface ProjectProgramTarget {job_id?: string; behavior_ids?: string[]; owner_entity_id?: string; target: TargetRef; mode: 'append' | 'modify' | 'rewrite'; intent: string}
export interface ProjectSharedState {
  variables: Array<{ref_id?: string; name: string; value?: string | number | boolean; scope: 'global'}>;
  lists: Array<{ref_id?: string; name: string; value?: Array<string | number | boolean>; scope: 'global'}>;
  broadcasts: Array<{ref_id?: string; name: string}> | string[];
}
export interface ProjectLocalState {ref_id: string; kind: 'variable' | 'list'; target: Exclude<TargetRef, {kind: 'stage'}>; name: string; value?: string | number | boolean | Array<string | number | boolean>}
export interface ProjectManifest {
  /** v2 remains a TypeScript migration shape only; runtime validation rejects it. */
  schema_version: 2 | typeof RESOURCE_CONTRACT_VERSION;
  scope: 'project';
  summary: string;
  assumptions?: string[];
  required_extensions?: Array<'pen' | 'music' | 'text2speech'>;
  resource_ops: ResourceOp[];
  local_state?: ProjectLocalState[];
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
const operationSpec = (op: unknown) => (
  typeof op === 'string' && DISABLED_DESTRUCTIVE_RESOURCE_OPERATIONS.has(op)
    ? undefined
    : RESOURCE_OPERATION_CONTRACT.find(item => item.op === op)
);

function validateSource(value: unknown, path: string, issues: ValidationIssue[], expectedAssetType?: string, allowedKinds: readonly string[] = ['scratch_library', 'image_generation', 'audio_generation']) {
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
  if (value.kind === 'audio_generation') {
    if (expectedAssetType !== 'sound') issues.push(contractIssue(path + '/kind', 'audio generation is only valid for sound resources', 'resource', 'SOURCE_TYPE_MISMATCH', value.kind));
    for (const field of ['reason', 'prompt']) if (!isString(value[field])) issues.push(contractIssue(path + '/' + field, 'must be a non-empty string', 'resource', 'FIELD_INVALID', value[field]));
    if (!['sound_effect', 'speech', 'music'].includes(String(value.audio_kind))) issues.push(contractIssue(path + '/audio_kind', 'must be sound_effect, speech, or music', 'resource', 'FIELD_INVALID', value.audio_kind));
    if (!isFiniteNumber(value.max_duration_seconds) || value.max_duration_seconds <= 0 || value.max_duration_seconds > 60) issues.push(contractIssue(path + '/max_duration_seconds', 'must be a number between 0 and 60', 'resource', 'FIELD_INVALID', value.max_duration_seconds));
    return;
  }
  for (const field of ['reason', 'prompt', 'style']) if (!isString(value[field])) issues.push(contractIssue(path + '/' + field, 'must be a non-empty string', 'resource', 'FIELD_INVALID', value[field]));
  if (value.custom_style !== undefined && !isString(value.custom_style, 120)) issues.push(contractIssue(path + '/custom_style', 'must be a non-empty string when present', 'resource', 'FIELD_INVALID', value.custom_style));
  const validImageSize = CANVAS_SIZES.some(size => size.width === value.width && size.height === value.height);
  if (!isFiniteNumber(value.width) || !isFiniteNumber(value.height) || !validImageSize) {
    issues.push(contractIssue(path, 'must use one of the supported image size pairs', 'resource', 'FIELD_INVALID', {width: value.width, height: value.height}));
  }
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
  } else if (normalized.op === 'add_builtin_sprite' && !('include_sounds' in normalized)) {
    normalized.include_sounds = 'all';
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
  if (['add_builtin_sprite', 'add_sprite', 'add_costume', 'add_backdrop', 'add_sound'].includes(String(op)) && !isString(normalized.source_need_id, 120)) issues.push(contractIssue(path + '/source_need_id', 'must be a non-empty Blueprint need ID', String(op), 'FIELD_REQUIRED', normalized.source_need_id));
  if (normalized.name !== undefined && !isString(normalized.name, 120)) issues.push(contractIssue(path + '/name', 'must be a non-empty name', String(op), 'FIELD_INVALID', normalized.name));
  for (const field of ['x', 'y']) if (normalized[field] !== undefined && (!Number.isInteger(normalized[field]) || Number(normalized[field]) < -10000 || Number(normalized[field]) > 10000)) issues.push(contractIssue(path + '/' + field, 'must be an integer between -10000 and 10000', String(op), 'FIELD_INVALID', normalized[field]));
  if (normalized.size !== undefined && (!Number.isInteger(normalized.size) || Number(normalized.size) < 1 || Number(normalized.size) > 1000)) issues.push(contractIssue(path + '/size', 'must be an integer between 1 and 1000', String(op), 'FIELD_INVALID', normalized.size));
  if (normalized.tags !== undefined) validateTags(normalized.tags, path + '/tags', issues, String(op));
  if (normalized.select_after !== undefined && typeof normalized.select_after !== 'boolean') issues.push(contractIssue(path + '/select_after', 'must be boolean', String(op), 'FIELD_INVALID', normalized.select_after));

  if (op === 'add_builtin_sprite') {
    if (!isString(normalized.owner_entity_id, 120)) issues.push(contractIssue(path + '/owner_entity_id', 'must be a non-empty Blueprint entity ID', String(op), 'FIELD_REQUIRED', normalized.owner_entity_id));
    if (normalized.include_costumes !== 'all') issues.push(contractIssue(path + '/include_costumes', 'must be all', String(op), 'FIELD_INVALID', normalized.include_costumes));
    if (!['all', 'none'].includes(String(normalized.include_sounds))) issues.push(contractIssue(path + '/include_sounds', 'must be all or none', String(op), 'FIELD_INVALID', normalized.include_sounds));
    validateSource(normalized.source, path + '/source', issues, 'sprite', spec.source_kinds);
    if (isString(normalized.op_id, 120)) created.add(normalized.op_id);
    return;
  }
  if (op === 'add_sprite') {
    if (!isString(normalized.owner_entity_id, 120)) issues.push(contractIssue(path + '/owner_entity_id', 'must be a non-empty Blueprint entity ID', String(op), 'FIELD_REQUIRED', normalized.owner_entity_id));
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
  if (op === 'add_sound') {
    validateTarget(normalized.target, path + '/target', issues, existingSpriteNames, created, ['current_target', 'stage', 'existing_sprite', 'created_sprite']);
    validateSource(normalized.source, path + '/source', issues, 'sound', spec.source_kinds);
    return;
  }
  if (op === 'delete_sound') {
    validateTarget(normalized.target, path + '/target', issues, existingSpriteNames, created, spec.target_kinds);
    if (!isRecord(normalized.sound) || normalized.sound.kind !== 'existing_sound' || !isString(normalized.sound.name, 120) || Object.keys(normalized.sound).some(key => !['kind', 'name'].includes(key))) {
      issues.push(contractIssue(path + '/sound', 'must be an existing_sound reference with a name', String(op), 'FIELD_INVALID', normalized.sound));
    }
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

export function validateProjectManifest(
  value: unknown,
  existingSpriteNames: string[] = [],
  permittedExtensions: readonly string[] = [],
  enabledExtensions: ReadonlySet<string> = FRONTEND_ENABLED_EXTENSIONS,
): ValidationResult {
  const issues: ValidationIssue[] = [];
  if (!isRecord(value)) return {valid: false, issues: [contractIssue('/', 'project manifest must be an object', 'manifest', 'SCHEMA_VALIDATION_ERROR', value)]};
  if (!('schema_version' in value)) issues.push(contractIssue('/schema_version', 'is required', 'manifest', 'SCHEMA_VALIDATION_ERROR'));
  else if (value.schema_version !== RESOURCE_CONTRACT_VERSION) issues.push(contractIssue('/schema_version', 'must be 3', 'manifest', 'UNSUPPORTED_SCHEMA_VERSION', value.schema_version));
  if (value.scope !== 'project') issues.push(contractIssue('/scope', 'must be project', 'manifest', 'SCHEMA_VALIDATION_ERROR', value.scope));
  if (!isString(value.summary, 1000)) issues.push(contractIssue('/summary', 'must be a non-empty string up to 1000 characters', 'manifest', 'FIELD_INVALID', value.summary));
  const manifestFields = new Set(['schema_version', 'scope', 'summary', 'assumptions', 'required_extensions', 'resource_ops', 'shared_state', 'local_state', 'program_targets']);
  for (const key of Object.keys(value)) if (!manifestFields.has(key)) issues.push(contractIssue('/' + key, 'is forbidden in a project manifest', 'manifest', 'FIELD_FORBIDDEN', value[key]));
  for (const key of ['schema_version', 'scope', 'summary', 'assumptions', 'required_extensions', 'resource_ops', 'shared_state', 'local_state', 'program_targets']) if (!(key in value)) issues.push(contractIssue('/' + key, 'is required', 'manifest', 'FIELD_REQUIRED'));
  if (!Array.isArray(value.assumptions) || value.assumptions.length > 10 || value.assumptions.some(item => !isString(item, 120))) issues.push(contractIssue('/assumptions', 'must contain at most 10 non-empty assumptions of 120 characters', 'manifest', 'FIELD_INVALID', value.assumptions));
  const allowedExtensions = new Set(['pen', 'music', 'text2speech', 'translate', 'videoSensing']);
  if (!Array.isArray(value.required_extensions) || value.required_extensions.length > allowedExtensions.size || value.required_extensions.some(item => !allowedExtensions.has(String(item)))
    || new Set(value.required_extensions.map(String)).size !== value.required_extensions.length
    || value.required_extensions.some(item => !enabledExtensions.has(String(item)))
    || value.required_extensions.includes('text2speech') && !permittedExtensions.includes('text2speech')) {
    issues.push(contractIssue('/required_extensions', 'contains an extension disabled by this frontend or backend deployment', 'manifest', 'EXTENSION_NOT_ENABLED', value.required_extensions));
  }
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
    for (const key of Object.keys(target)) if (!['job_id', 'behavior_ids', 'owner_entity_id', 'target', 'mode', 'intent'].includes(key)) issues.push(contractIssue(path + '/' + key, 'is forbidden in a program target', 'manifest', 'FIELD_FORBIDDEN', target[key]));
    if (!isString(target.job_id, 120)) issues.push(contractIssue(path + '/job_id', 'must be a non-empty Blueprint job ID', 'manifest', 'FIELD_REQUIRED', target.job_id));
    if (!isString(target.owner_entity_id, 120)) issues.push(contractIssue(path + '/owner_entity_id', 'must be a non-empty Blueprint entity ID', 'manifest', 'FIELD_REQUIRED', target.owner_entity_id));
    if (!Array.isArray(target.behavior_ids) || target.behavior_ids.length < 1 || target.behavior_ids.some(item => !isString(item, 120))) issues.push(contractIssue(path + '/behavior_ids', 'must contain one or more behavior IDs', 'manifest', 'FIELD_INVALID', target.behavior_ids));
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
        for (const key of Object.keys(item)) if (!['ref_id', 'name', 'value', 'scope'].includes(key)) issues.push(contractIssue(path + '/' + key, 'is forbidden in a shared definition', 'manifest', 'FIELD_FORBIDDEN', item[key]));
        if (!isString(item.ref_id, 120)) issues.push(contractIssue(path + '/ref_id', 'must be a non-empty TaskSpec state ID', 'manifest', 'FIELD_REQUIRED', item.ref_id));
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
      const path = '/shared_state/broadcasts/' + index;
      if (!isRecord(item) || !isString(item.ref_id, 120) || !isString(item.name, 120) || Object.keys(item).some(key => !['ref_id', 'name'].includes(key))) issues.push(contractIssue(path, 'must be a broadcast ref_id and name object', 'manifest', 'FIELD_INVALID', item));
      else if (seenBroadcasts.has(item.name)) issues.push(contractIssue(path + '/name', 'must be unique', 'manifest', 'DUPLICATE_NAME', item.name));
      else seenBroadcasts.add(item.name);
    });
  }
  const localState = value.local_state;
  if (!Array.isArray(localState) || localState.length > 100) issues.push(contractIssue('/local_state', 'must be an array of at most 100 local state entries', 'manifest', 'FIELD_INVALID', localState));
  else localState.forEach((item, index) => {
    const path = '/local_state/' + index;
    if (!isRecord(item)) { issues.push(contractIssue(path, 'must be an object', 'manifest', 'FIELD_INVALID', item)); return; }
    for (const key of Object.keys(item)) if (!['ref_id', 'kind', 'target', 'name', 'value'].includes(key)) issues.push(contractIssue(path + '/' + key, 'is forbidden in local state', 'manifest', 'FIELD_FORBIDDEN', item[key]));
    if (!isString(item.ref_id, 120) || !isString(item.name, 120)) issues.push(contractIssue(path, 'ref_id and name are required', 'manifest', 'FIELD_REQUIRED', item));
    if (!['variable', 'list'].includes(String(item.kind))) issues.push(contractIssue(path + '/kind', 'must be variable or list', 'manifest', 'FIELD_INVALID', item.kind));
    validateTarget(item.target, path + '/target', issues, existingSpriteNames, created, ['current_target', 'existing_sprite', 'created_sprite']);
    if (item.kind === 'list' && item.value !== undefined && (!Array.isArray(item.value) || item.value.some(value => !(typeof value === 'string' || typeof value === 'boolean' || isFiniteNumber(value))))) issues.push(contractIssue(path + '/value', 'local list value must be an array of scalar values', 'manifest', 'FIELD_INVALID', item.value));
  });
  return {valid: issues.length === 0, issues};
}

export function assertProjectManifest(value: unknown, existingSpriteNames?: string[], permittedExtensions?: readonly string[], enabledExtensions?: ReadonlySet<string>): asserts value is ProjectManifest {
  const result = validateProjectManifest(value, existingSpriteNames, permittedExtensions, enabledExtensions);
  if (!result.valid) throw new Error(result.issues.map(item => item.path + ': ' + item.message).join('; '));
}

function namedStateValue(value: {name: string; value?: unknown; scope?: string}, list: boolean, scope = value.scope) {
  return {name: value.name, value: value.value === undefined ? (list ? [] : null) : value.value, scope};
}

export function validateProjectProgramSharedState(program: ProgramIR, sharedState: ProjectSharedState, targetKind: TargetKind = 'sprite'): ValidationResult {
  const issues: ValidationIssue[] = [];
  if (program.schema_version === 2) {
    if (program.variables.length || program.lists.length || program.broadcasts.length) issues.push(contractIssue('/shared_state', 'ProgramIR v2 project programs must use manifest state_ref fields and declare no state', 'program', 'SHARED_STATE_MISMATCH', {variables: program.variables, lists: program.lists, broadcasts: program.broadcasts}));
    return {valid: issues.length === 0, issues};
  }
  const effectiveScope = (scope: 'local' | 'global' | undefined): 'local' | 'global' => targetKind === 'stage' ? 'global' : (scope ?? 'local');
  const expectedVariables = sharedState.variables.map(item => namedStateValue(item, false, 'global')).sort((left, right) => left.name.localeCompare(right.name));
  const expectedLists = sharedState.lists.map(item => namedStateValue(item, true, 'global')).sort((left, right) => left.name.localeCompare(right.name));
  const actualVariables = program.variables.filter(item => effectiveScope(item.scope) === 'global').map(item => namedStateValue(item, false, 'global')).sort((left, right) => left.name.localeCompare(right.name));
  const actualLists = program.lists.filter(item => effectiveScope(item.scope) === 'global').map(item => namedStateValue(item, true, 'global')).sort((left, right) => left.name.localeCompare(right.name));
  if (JSON.stringify(actualVariables) !== JSON.stringify(expectedVariables)) issues.push(contractIssue('/variables', 'project program global variables must exactly repeat manifest.shared_state.variables', 'program', 'SHARED_STATE_MISMATCH', actualVariables));
  if (JSON.stringify(actualLists) !== JSON.stringify(expectedLists)) issues.push(contractIssue('/lists', 'project program global lists must exactly repeat manifest.shared_state.lists', 'program', 'SHARED_STATE_MISMATCH', actualLists));
  const sharedBroadcastNames = sharedState.broadcasts.map(item => typeof item === 'string' ? item : item.name);
  if (JSON.stringify([...program.broadcasts].sort()) !== JSON.stringify([...sharedBroadcastNames].sort())) issues.push(contractIssue('/broadcasts', 'project program broadcasts must exactly repeat manifest.shared_state.broadcasts', 'program', 'SHARED_STATE_MISMATCH', program.broadcasts));
  const globalNames = new Set([...sharedState.variables, ...sharedState.lists].map(item => item.name));
  if (program.variables.some(item => effectiveScope(item.scope) !== 'global' && globalNames.has(item.name))) issues.push(contractIssue('/variables', 'target-local state cannot reuse a manifest global name', 'program', 'SHARED_STATE_NAME_CONFLICT', program.variables));
  if (program.lists.some(item => effectiveScope(item.scope) !== 'global' && globalNames.has(item.name))) issues.push(contractIssue('/lists', 'target-local state cannot reuse a manifest global name', 'program', 'SHARED_STATE_NAME_CONFLICT', program.lists));
  return {valid: issues.length === 0, issues};
}

/** Verify that ProgramIR v2 only uses manifest-declared logical assets. */
export function validateProjectProgramResourceRefs(program: ProgramIR, manifest: ProjectManifest): ValidationResult {
  const issues: ValidationIssue[] = [];
  const resourceTypes = new Map<string, 'sprite' | 'costume' | 'backdrop' | 'sound'>();
  for (const operation of manifest.resource_ops) {
    const type = operation.op === 'add_builtin_sprite' || operation.op === 'add_sprite'
      ? 'sprite'
      : operation.op === 'add_costume' ? 'costume'
        : operation.op === 'add_backdrop' ? 'backdrop'
          : operation.op === 'add_sound' ? 'sound' : undefined;
    if (type) resourceTypes.set(operation.op_id, type);
  }
  const visit = (blocks: ProgramIR['scripts'][number]['blocks'], path: string) => blocks.forEach((block, index) => {
    const blockPath = `${path}[${index}]`;
    for (const [fieldName, field] of Object.entries(block.fields ?? {})) {
      const expected = getBlockSpec(block.opcode)?.fields?.[fieldName]?.resourceType;
      if (field && typeof field === 'object' && field.kind === 'resource_ref') {
        const opId = field.ref?.op_id ?? field.op_id;
        const actual = opId ? resourceTypes.get(opId) : undefined;
        if (!expected) issues.push(contractIssue(`${blockPath}.fields.${fieldName}`, 'resource_ref is only valid for a typed Scratch resource field menu', 'program', 'RESOURCE_REF_MENU_MISMATCH', fieldName));
        if (!actual) issues.push(contractIssue(`${blockPath}.fields.${fieldName}.ref.op_id`, 'must reference a manifest resource operation', 'program', 'RESOURCE_REF_NOT_FOUND', opId));
        else if (field.resource_type && actual !== field.resource_type) issues.push(contractIssue(`${blockPath}.fields.${fieldName}.resource_type`, 'must match the referenced manifest resource type', 'program', 'RESOURCE_REF_TYPE_MISMATCH', field.resource_type));
        if (expected && actual && actual !== expected) issues.push(contractIssue(`${blockPath}.fields.${fieldName}.ref`, 'is not valid for this Scratch field menu', 'program', 'RESOURCE_REF_MENU_MISMATCH', actual));
      } else if (field && typeof field === 'object' && field.kind === 'existing_asset_ref') {
        if (!expected) issues.push(contractIssue(`${blockPath}.fields.${fieldName}`, 'existing_asset_ref is only valid for a typed Scratch resource field menu', 'program', 'RESOURCE_REF_MENU_MISMATCH', fieldName));
        else if (field.asset_type !== expected) issues.push(contractIssue(`${blockPath}.fields.${fieldName}.asset_type`, 'is not valid for this Scratch field menu', 'program', 'RESOURCE_REF_MENU_MISMATCH', field.asset_type));
      }
    }
    for (const [inputName, input] of Object.entries(block.inputs ?? {})) {
      if (input.kind === 'resource_ref') {
        const expected = getBlockSpec(block.opcode)?.inputs?.[inputName]?.resourceType;
        const opId = input.ref?.op_id ?? input.op_id;
        const actual = opId ? resourceTypes.get(opId) : undefined;
        if (!expected) issues.push(contractIssue(`${blockPath}.inputs.${inputName}`, 'resource_ref is only valid for a typed Scratch resource menu', 'program', 'RESOURCE_REF_MENU_MISMATCH', inputName));
        if (!actual) issues.push(contractIssue(`${blockPath}.inputs.${inputName}.ref.op_id`, 'must reference a manifest resource operation', 'program', 'RESOURCE_REF_NOT_FOUND', opId));
        else if (input.resource_type && actual !== input.resource_type) issues.push(contractIssue(`${blockPath}.inputs.${inputName}.resource_type`, 'must match the referenced manifest resource type', 'program', 'RESOURCE_REF_TYPE_MISMATCH', input.resource_type));
        if (expected && actual && actual !== expected) issues.push(contractIssue(`${blockPath}.inputs.${inputName}.ref`, 'is not valid for this Scratch menu', 'program', 'RESOURCE_REF_MENU_MISMATCH', actual));
      } else if (input.kind === 'existing_asset_ref') {
        const expected = getBlockSpec(block.opcode)?.inputs?.[inputName]?.resourceType;
        if (!expected) issues.push(contractIssue(`${blockPath}.inputs.${inputName}`, 'existing_asset_ref is only valid for a typed Scratch resource menu', 'program', 'RESOURCE_REF_MENU_MISMATCH', inputName));
        else if (input.asset_type !== expected) issues.push(contractIssue(`${blockPath}.inputs.${inputName}.asset_type`, 'is not valid for this Scratch menu', 'program', 'RESOURCE_REF_MENU_MISMATCH', input.asset_type));
      } else if (input.kind === 'block') visit([input.block], `${blockPath}.inputs.${inputName}.block`);
    }
    for (const [stackName, stack] of Object.entries(block.substacks ?? {})) visit(stack, `${blockPath}.substacks.${stackName}`);
  });
  for (const [scriptIndex, script] of program.scripts.entries()) visit(script.blocks, `scripts[${scriptIndex}].blocks`);
  for (const [procedureIndex, procedure] of (program.procedures ?? []).entries()) visit(procedure.body, `procedures[${procedureIndex}].body`);
  return {valid: issues.length === 0, issues};
}

/** Validate that v2 data/broadcast fields use immutable Manifest ref IDs. */
export function validateProjectProgramStateRefs(program: ProgramIR, manifest: ProjectManifest): ValidationResult {
  const issues: ValidationIssue[] = [];
  const states = new Map<string, 'variable' | 'list' | 'broadcast'>();
  for (const item of manifest.shared_state.variables) if (item.ref_id) states.set(item.ref_id, 'variable');
  for (const item of manifest.shared_state.lists) if (item.ref_id) states.set(item.ref_id, 'list');
  for (const item of manifest.shared_state.broadcasts) if (typeof item !== 'string' && item.ref_id) states.set(item.ref_id, 'broadcast');
  for (const item of manifest.local_state ?? []) states.set(item.ref_id, item.kind);
  const visit = (blocks: ProgramIR['scripts'][number]['blocks'], path: string) => blocks.forEach((block, index) => {
    const blockPath = `${path}[${index}]`;
    for (const [field, value] of Object.entries(block.fields ?? {})) {
      if (typeof value === 'object' && value !== null && value.kind === 'state_ref') {
        const actual = states.get(value.ref_id);
        if (!actual) issues.push(contractIssue(`${blockPath}.fields.${field}.ref_id`, 'state_ref must reference manifest shared_state or local_state', 'program', 'STATE_REF_NOT_FOUND', value.ref_id));
        else if (actual !== value.state_kind) issues.push(contractIssue(`${blockPath}.fields.${field}.state_kind`, 'state_ref type must match the manifest state', 'program', 'STATE_REF_TYPE_MISMATCH', value.state_kind));
      }
    }
    for (const [name, input] of Object.entries(block.inputs ?? {})) if (input.kind === 'block') visit([input.block], `${blockPath}.inputs.${name}.block`);
    for (const [name, stack] of Object.entries(block.substacks ?? {})) visit(stack, `${blockPath}.substacks.${name}`);
  });
  for (const [index, script] of program.scripts.entries()) visit(script.blocks, `scripts[${index}].blocks`);
  for (const [procedureIndex, procedure] of (program.procedures ?? []).entries()) visit(procedure.body, `procedures[${procedureIndex}].body`);
  return {valid: issues.length === 0, issues};
}
