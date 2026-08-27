import type {ProgramIR, ValidationIssue, ValidationResult} from './program-ir';

export const PROJECT_PLAN_LIMITS = {resourceOps: 24, blockPrograms: 12, tags: 20, searchTerms: 12, costumes: 12, text: 2000} as const;
export type AiBlocksScope = 'single_sprite' | 'project';
export type AssetPolicy = 'builtin_first' | 'existing_only' | 'allow_generation';
export type TargetRef = {kind: 'current_target'} | {kind: 'stage'} | {kind: 'existing_sprite'; name: string} | {kind: 'created_sprite'; op_id: string};
export type ScratchLibrarySource = {kind: 'scratch_library'; asset_type: 'sprite' | 'costume' | 'backdrop'; search_terms: string[]};
export type ImageGenerationSource = {kind: 'image_generation'; reason: string; prompt: string; style: string; custom_style?: string; width: number; height: number; sheet_layout: '1x1'};
export type ProjectPlanResourceSource = ScratchLibrarySource | ImageGenerationSource;
export type ResourceOp = {op: 'add_builtin_sprite'; op_id: string; name: string; x: number; y: number; size: number; tags: string[]; source: ScratchLibrarySource & {asset_type: 'sprite'}} | {op: 'add_sprite'; op_id: string; name: string; x: number; y: number; size: number; tags: string[]; costumes: Array<{name: string; source: ProjectPlanResourceSource; tags: string[]}>} | {op: 'add_costume'; op_id: string; target: TargetRef; name: string; source: ProjectPlanResourceSource; tags: string[]; select_after: boolean} | {op: 'add_backdrop'; op_id: string; name: string; source: ProjectPlanResourceSource; tags: string[]; select_after: boolean} | {op: 'select_backdrop'; op_id: string; name: string};
export interface BlockProgramPlan {target: TargetRef; mode: 'append' | 'modify' | 'rewrite'; program: ProgramIR}
export interface ProjectPlan {schema_version: 1; scope: AiBlocksScope; summary: string; resource_ops: ResourceOp[]; block_programs: BlockProgramPlan[]}

const issue = (path: string, message: string): ValidationIssue => ({path, message});
const isRecord = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === 'object' && !Array.isArray(value);

export function validateProjectPlan(value: unknown): ValidationResult {
  const issues: ValidationIssue[] = [];
  if (!isRecord(value)) return {valid: false, issues: [issue('$', 'project plan must be an object')]};
  if (value.schema_version !== 1) issues.push(issue('schema_version', 'must be 1'));
  if (value.scope !== 'single_sprite' && value.scope !== 'project') issues.push(issue('scope', 'must be single_sprite or project'));
  if (typeof value.summary !== 'string' || !value.summary.trim() || value.summary.length > 1000) issues.push(issue('summary', 'must be a non-empty string up to 1000 characters'));
  const ops = Array.isArray(value.resource_ops) ? value.resource_ops : []; const programs = Array.isArray(value.block_programs) ? value.block_programs : [];
  if (!Array.isArray(value.resource_ops)) issues.push(issue('resource_ops', 'must be an array')); if (!Array.isArray(value.block_programs)) issues.push(issue('block_programs', 'must be an array'));
  if (ops.length > 24) issues.push(issue('resource_ops', 'has too many operations')); if (programs.length > 12) issues.push(issue('block_programs', 'has too many programs'));
  const opIds = new Set<string>(); const created = new Set<string>();
  ops.forEach((op, index) => { const path = 'resource_ops[' + index + ']'; if (!isRecord(op) || typeof op.op_id !== 'string' || !op.op_id.trim()) {issues.push(issue(path + '.op_id', 'must be a non-empty operation id')); return;} if (opIds.has(op.op_id)) issues.push(issue(path + '.op_id', 'must be unique')); opIds.add(op.op_id); if (op.op === 'add_sprite') created.add(op.op_id); const source = op.source;
    if (isRecord(source) && source.kind === 'library_asset') issues.push(issue(path + '.source.kind', 'library_asset is not accepted in model plans'));
    if (op.op === 'add_builtin_sprite' && (!isRecord(source) || source.kind !== 'scratch_library' || source.asset_type !== 'sprite')) issues.push(issue(path + '.source', 'must be a sprite scratch_library source'));
    if (op.op === 'add_costume' && isRecord(op.target) && op.target.kind === 'stage') issues.push(issue(path + '.target', 'add_costume cannot target stage'));
    if ((op.op === 'add_costume' || op.op === 'add_sprite') && isRecord(source) && source.kind === 'scratch_library' && source.asset_type === 'sprite') issues.push(issue(path + '.source.asset_type', 'costume source cannot be sprite'));
    if (op.op === 'add_backdrop' && isRecord(source) && source.kind === 'scratch_library' && source.asset_type !== 'backdrop') issues.push(issue(path + '.source.asset_type', 'backdrop source is required'));
    if (op.op === 'add_costume' && isRecord(op.target) && op.target.kind === 'created_sprite' && (!isRecord(op.target) || typeof op.target.op_id !== 'string' || !opIds.has(op.target.op_id) || !created.has(op.target.op_id))) issues.push(issue(path + '.target.op_id', 'must reference a prior add_sprite operation'));
  });
  if (value.scope === 'single_sprite') { ops.forEach((op, i) => {if (isRecord(op) && op.op !== 'add_costume') issues.push(issue('resource_ops[' + i + '].op', 'single_sprite only permits add_costume')); if (isRecord(op) && isRecord(op.target) && op.target.kind !== 'current_target') issues.push(issue('resource_ops[' + i + '].target', 'single_sprite must target current_target'));}); programs.forEach((p, i) => {if (isRecord(p) && (!isRecord(p.target) || p.target.kind !== 'current_target')) issues.push(issue('block_programs[' + i + '].target', 'single_sprite must target current_target'));}); if (programs.length > 1) issues.push(issue('block_programs', 'single_sprite permits at most one program'));}
  programs.forEach((p, i) => {if (isRecord(p) && isRecord(p.target) && p.target.kind === 'created_sprite' && (!created.has(String(p.target.op_id)))) issues.push(issue('block_programs[' + i + '].target.op_id', 'must reference a created sprite'));});
  return {valid: issues.length === 0, issues};
}

export function assertProjectPlan(value: unknown): asserts value is ProjectPlan {const result = validateProjectPlan(value); if (!result.valid) throw new Error(result.issues.map(item => item.path + ': ' + item.message).join('; '));}
