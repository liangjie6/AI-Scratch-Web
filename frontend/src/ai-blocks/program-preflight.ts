import {validateProgramIR} from './program-compiler';
import {
  validateProjectProgramResourceRefs,
  validateProjectProgramSharedState,
  validateProjectProgramStateRefs,
  type ProjectManifest,
} from './project-plan';
import type {BuiltinAiExtension} from './block-catalog';
import type {ProgramIR, TargetKind, ValidationIssue, ValidationResult} from './program-ir';

/**
 * Run every deterministic ProgramIR contract check before any VM graph write.
 * This intentionally has no bridge or network dependency so callers can
 * preflight all project targets as one batch.
 */
export function preflightProgram(
  program: ProgramIR,
  target: TargetKind,
  enabledExtensions: ReadonlySet<BuiltinAiExtension>,
  manifest?: ProjectManifest,
): ValidationResult {
  const issues: ValidationIssue[] = [];
  const programResult = validateProgramIR(program, target, enabledExtensions);
  issues.push(...programResult.issues);
  if (!manifest && program.schema_version === 2) {
    issues.push({
      path: '$.schema_version',
      message: 'ProgramIR v2 requires a ProjectManifest binding table',
      code: 'RESOURCE_BINDING_REQUIRED',
      operation: 'preflight',
      repair_scope: 'program',
    });
  }
  if (manifest) {
    issues.push(...validateProjectProgramSharedState(program, manifest.shared_state, target).issues);
    issues.push(...validateProjectProgramResourceRefs(program, manifest).issues);
    issues.push(...validateProjectProgramStateRefs(program, manifest).issues);
  }
  return {valid: issues.length === 0, issues};
}
