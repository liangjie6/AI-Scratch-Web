export const PROGRAM_IR_LIMITS = {
  scripts: 12,
  visibleBlocks: 120,
  depth: 12,
  shortString: 120,
  summary: 1000
} as const;

export type TargetKind = 'stage' | 'sprite';
export type InputKind = 'number' | 'text' | 'angle' | 'color' | 'block';
export type FieldValue = string | number | boolean;

export interface ScalarInputIR {
  kind: Exclude<InputKind, 'block'>;
  value: string | number;
}

export interface BlockInputIR {
  kind: 'block';
  block: BlockIR;
}

export type InputIR = ScalarInputIR | BlockInputIR;

export interface BlockIR {
  opcode: string;
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
  schema_version: 1;
  summary: string;
  remove_script_ids: string[];
  variables: NamedValueIR[];
  lists: NamedListIR[];
  broadcasts: string[];
  scripts: ScriptIR[];
}

export interface ValidationIssue {
  path: string;
  message: string;
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
