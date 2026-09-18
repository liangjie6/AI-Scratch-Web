export const PROGRAM_IR_LIMITS = {
  scripts: 12,
  procedures: 12,
  procedureParameters: 8,
  visibleBlocks: 120,
  depth: 12,
  shortString: 120,
  summary: 1000
} as const;

export type TargetKind = 'stage' | 'sprite';
export type InputKind = 'number' | 'text' | 'angle' | 'color' | 'resource_ref' | 'existing_asset_ref' | 'block';
export interface StateRefFieldIR {
  kind: 'state_ref';
  state_kind: 'variable' | 'list' | 'broadcast';
  ref_id: string;
}

/** A logical resource operation ID, resolved to the VM-installed name at commit. */
export interface ResourceRefIR {
  kind: 'resource_ref';
  /** resource_type/op_id are accepted only while reading v2 migration drafts. */
  resource_type?: 'sprite' | 'costume' | 'backdrop' | 'sound';
  op_id?: string;
  ref?: {op_id: string; value: 'installed_name'};
}

export interface ExistingAssetRefIR {
  kind: 'existing_asset_ref';
  asset_type: 'sprite' | 'costume' | 'backdrop' | 'sound';
  name: string;
}

export type FieldValue = string | number | boolean | StateRefFieldIR | ResourceRefIR | ExistingAssetRefIR;

export interface ScalarInputIR {
  kind: 'number' | 'text' | 'angle' | 'color';
  value: string | number;
}

export interface BlockInputIR {
  kind: 'block';
  block: BlockIR;
}

/** A logical resource operation ID, resolved to the VM-installed name at commit. */
export interface ResourceRefInputIR extends ResourceRefIR {}

export interface ExistingAssetRefInputIR extends ExistingAssetRefIR {}

export type InputIR = ScalarInputIR | ResourceRefInputIR | ExistingAssetRefInputIR | BlockInputIR;

export interface BlockIR {
  opcode: string;
  /** Only valid when opcode is procedures_call; resolved by the compiler. */
  procedure_id?: string;
  /** Only valid for a procedure argument reporter inside a ProcedureIR body. */
  argument_id?: string;
  fields?: Record<string, FieldValue>;
  inputs?: Record<string, InputIR>;
  substacks?: {
    SUBSTACK?: BlockIR[];
    SUBSTACK2?: BlockIR[];
  };
}

export interface ScriptIR {
  id: string;
  purpose: string;
  x: number;
  y: number;
  blocks: BlockIR[];
}

/**
 * A constrained custom-block declaration. The compiler owns Scratch's
 * procedure mutation/proccode serialization; AI output never carries it.
 */
export interface ProcedureParameterIR {
  id: string;
  name: string;
  kind: 'string_number' | 'boolean';
  default?: string | number | boolean;
}

export interface ProcedureIR {
  id: string;
  label: string;
  parameters: ProcedureParameterIR[];
  warp: boolean;
  body: BlockIR[];
}

export interface NamedValueIR {
  name: string;
  value?: string | number | boolean;
  scope?: 'local' | 'global';
}

export interface NamedListIR {
  name: string;
  value?: Array<string | number | boolean>;
  scope?: 'local' | 'global';
}

export interface ProgramIR {
  schema_version: 1 | 2;
  summary: string;
  remove_script_ids: string[];
  variables: NamedValueIR[];
  lists: NamedListIR[];
  broadcasts: string[];
  procedures?: ProcedureIR[];
  scripts: ScriptIR[];
}

export interface ValidationIssue {
  path: string;
  message: string;
  code?: string;
  actual?: unknown;
  expected?: string;
  operation?: string;
  repair_scope?: string;
  repair_id?: string | null;
}

export interface ValidationResult {
  valid: boolean;
  issues: ValidationIssue[];
}

export class ProgramIRValidationError extends Error {
  readonly issues: ValidationIssue[];

  constructor(issues: ValidationIssue[]) {
    super(issues.map(issue => `${issue.path}: ${issue.message}`).join('; '));
    this.name = 'ProgramIRValidationError';
    this.issues = issues;
  }
}
