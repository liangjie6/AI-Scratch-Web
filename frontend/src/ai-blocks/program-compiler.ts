import {extensionForOpcode, getBlockSpec, type BuiltinAiExtension, type FieldResolverKind, type ResourceMenuAssetType} from './block-catalog';
import {
  PROGRAM_IR_LIMITS,
  ProgramIRValidationError,
  type BlockIR,
  type ExistingAssetRefIR,
  type FieldValue,
  type InputIR,
  type InputKind,
  type ProcedureParameterIR,
  type ProgramIR,
  type ResourceRefIR,
  type TargetKind,
  type ValidationIssue,
  type ValidationResult
} from './program-ir';

const hasOwn = (value: object, key: string): boolean => Object.prototype.hasOwnProperty.call(value, key);
const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);
const ownKeysOnly = (value: Record<string, unknown>, allowed: readonly string[], path: string, issues: ValidationIssue[]): void => {
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) issues.push({path: `${path}.${key}`, message: 'unknown property'});
  }
};
const checkString = (value: unknown, path: string, issues: ValidationIssue[], max: number = PROGRAM_IR_LIMITS.shortString): value is string => {
  if (typeof value !== 'string' || value.length === 0 || value.length > max) {
    issues.push({path, message: `must be a non-empty string of at most ${max} characters`});
    return false;
  }
  return true;
};
const checkFinite = (value: unknown, path: string, issues: ValidationIssue[]): value is number => {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    issues.push({path, message: 'must be a finite number'});
    return false;
  }
  return true;
};
const isStateRef = (value: unknown): value is {kind: 'state_ref'; state_kind: 'variable' | 'list' | 'broadcast'; ref_id: string} =>
  isRecord(value) && value.kind === 'state_ref' && ['variable', 'list', 'broadcast'].includes(String(value.state_kind)) && typeof value.ref_id === 'string' && value.ref_id.length > 0;
const resourceOpId = (value: Record<string, unknown>): string | undefined =>
  isRecord(value.ref) && value.ref.value === 'installed_name' && typeof value.ref.op_id === 'string'
    ? value.ref.op_id
    : typeof value.op_id === 'string' ? value.op_id : undefined;
const isResourceRef = (value: unknown): value is ResourceRefIR => isRecord(value) && value.kind === 'resource_ref';
const isExistingAssetRef = (value: unknown): value is ExistingAssetRefIR => isRecord(value) && value.kind === 'existing_asset_ref';

const validateResourceField = (value: unknown, resourceType: ResourceMenuAssetType, path: string, context: ValidationContext): void => {
  if (!isRecord(value)) {
    context.issues.push({path, message: 'resource field must be a reference object'});
    return;
  }
  ownKeysOnly(value, ['kind', 'resource_type', 'op_id', 'ref', 'asset_type', 'name'], path, context.issues);
  if (value.kind === 'resource_ref') {
    const opId = resourceOpId(value);
    const modern = isRecord(value.ref) && value.ref.value === 'installed_name';
    if (!opId || !checkString(opId, path + '.ref.op_id', context.issues) || (!modern && !['sprite', 'costume', 'backdrop', 'sound'].includes(String(value.resource_type)))) {
      context.issues.push({path, message: 'resource_ref requires ref={op_id,value:installed_name}'});
    } else if (typeof value.resource_type === 'string' && value.resource_type !== resourceType) {
      context.issues.push({path: `${path}.resource_type`, message: `must be ${resourceType} for this Scratch field menu`});
    }
  } else if (value.kind === 'existing_asset_ref') {
    if (!['sprite', 'costume', 'backdrop', 'sound'].includes(String(value.asset_type)) || !checkString(value.name, path + '.name', context.issues)) {
      context.issues.push({path, message: 'existing_asset_ref requires asset_type and name only'});
    } else if (value.asset_type !== resourceType) {
      context.issues.push({path: `${path}.asset_type`, message: `must be ${resourceType} for this Scratch field menu`});
    }
  } else context.issues.push({path, message: 'resource field reference kind is invalid'});
};

interface ValidationContext {
  target: TargetKind;
  issues: ValidationIssue[];
  seen: WeakSet<object>;
  visibleBlocks: number;
  procedures: Map<string, ProcedureParameterIR[]>;
  procedureArguments?: Map<string, ProcedureParameterIR>;
  enabledExtensions: ReadonlySet<BuiltinAiExtension>;
}

/** Match the backend sequence contract before compiling parent/next links. */
const validateSequenceShape = (blocks: unknown[], path: string, script: boolean, issues: ValidationIssue[]): void => {
  blocks.forEach((block, index) => {
    if (!isRecord(block) || typeof block.opcode !== 'string') return;
    const shape = getBlockSpec(block.opcode)?.shape;
    if (script && index === 0 && shape !== 'hat') {
      issues.push({path: `${path}[${index}]`, message: 'script must start with an event hat block'});
    }
    if (shape === 'hat' && (!script || index > 0)) {
      issues.push({path: `${path}[${index}]`, message: 'event hat block cannot appear here'});
    }
    // Keep the current catalog policy for field-dependent stop variants. Such
    // variants require a coordinated catalog/backend change before support.
    if (shape === 'cap' && index < blocks.length - 1) {
      issues.push({path: `${path}[${index}]`, message: 'cap block must be the last block in its stack'});
    }
  });
};

const validateInput = (
  value: unknown,
  expected: InputKind | 'boolean' | 'any',
  path: string,
  depth: number,
  context: ValidationContext,
  resourceType?: ResourceMenuAssetType,
): void => {
  if (!isRecord(value)) {
    context.issues.push({path, message: 'must be an input object'});
    return;
  }
  if (context.seen.has(value)) {
    context.issues.push({path, message: 'cyclic or reused input object'});
    return;
  }
  context.seen.add(value);
  ownKeysOnly(value, ['kind', 'value', 'block', 'resource_type', 'op_id', 'ref', 'asset_type', 'name'], path, context.issues);
  const kind = value.kind;
  if (!['number', 'text', 'angle', 'color', 'resource_ref', 'existing_asset_ref', 'block'].includes(String(kind))) {
    context.issues.push({path: `${path}.kind`, message: 'invalid input kind'});
    return;
  }
  if (kind === 'block') {
    if (!hasOwn(value, 'block') || hasOwn(value, 'value')) {
      context.issues.push({path, message: 'block input requires block and forbids value'});
      return;
    }
    validateBlock(value.block, path + '.block', depth + 1, context, true, expected);
    return;
  }
  if (kind === 'resource_ref') {
    const opId = resourceOpId(value);
    const modern = isRecord(value.ref) && value.ref.value === 'installed_name';
    if (hasOwn(value, 'value') || hasOwn(value, 'block') || !opId || !checkString(opId, path + '.ref.op_id', context.issues) || (!modern && !['sprite', 'costume', 'backdrop', 'sound'].includes(String(value.resource_type)))) {
      context.issues.push({path, message: 'resource_ref requires ref={op_id,value:installed_name}'});
      return;
    }
    if (expected !== 'text' || !resourceType) context.issues.push({path, message: 'resource_ref is only valid for a typed Scratch resource menu'});
    else if (typeof value.resource_type === 'string' && value.resource_type !== resourceType) context.issues.push({path: `${path}.resource_type`, message: `must be ${resourceType} for this Scratch menu`});
    return;
  }
  if (kind === 'existing_asset_ref') {
    if (hasOwn(value, 'value') || hasOwn(value, 'block') || !['sprite', 'costume', 'backdrop', 'sound'].includes(String(value.asset_type)) || !checkString(value.name, path + '.name', context.issues)) {
      context.issues.push({path, message: 'existing_asset_ref requires asset_type and name only'});
    }
    if (expected !== 'text' || !resourceType) context.issues.push({path, message: 'existing_asset_ref is only valid for a typed Scratch resource menu'});
    else if (value.asset_type !== resourceType) context.issues.push({path: `${path}.asset_type`, message: `must be ${resourceType} for this Scratch menu`});
    return;
  }
  if (!hasOwn(value, 'value') || hasOwn(value, 'block')) {
    context.issues.push({path, message: 'scalar input requires value and forbids block'});
    return;
  }
  if (expected === 'boolean') {
    context.issues.push({path, message: 'boolean input requires a nested boolean block'});
  } else if (expected !== 'any' && kind !== expected) {
    context.issues.push({path: `${path}.kind`, message: `expected ${expected}`});
  }
  if ((kind === 'number' || kind === 'angle') && typeof value.value !== 'number') {
    context.issues.push({path: `${path}.value`, message: 'must be a number'});
  } else if ((kind === 'text' || kind === 'color') && typeof value.value !== 'string') {
    context.issues.push({path: `${path}.value`, message: 'must be a string'});
  } else if (typeof value.value === 'string' && value.value.length > PROGRAM_IR_LIMITS.shortString) {
    context.issues.push({path: `${path}.value`, message: 'string is too long'});
  } else if (typeof value.value === 'number' && !Number.isFinite(value.value)) {
    context.issues.push({path: `${path}.value`, message: 'must be finite'});
  }
};

const validateBlock = (
  value: unknown,
  path: string,
  depth: number,
  context: ValidationContext,
  nestedReporter = false,
  expectedReporter?: InputKind | 'boolean' | 'any'
): void => {
  if (depth > PROGRAM_IR_LIMITS.depth) {
    context.issues.push({path, message: `maximum block depth is ${PROGRAM_IR_LIMITS.depth}`});
    return;
  }
  if (!isRecord(value)) {
    context.issues.push({path, message: 'must be a block object'});
    return;
  }
  if (context.seen.has(value)) {
    context.issues.push({path, message: 'cyclic or reused block object'});
    return;
  }
  context.seen.add(value);
  context.visibleBlocks += 1;
  if (value.opcode === 'procedures_call') {
    ownKeysOnly(value, ['opcode', 'procedure_id', 'argument_id', 'inputs'], path, context.issues);
    if (value.argument_id !== undefined && value.argument_id !== null) context.issues.push({path: path + '.argument_id', message: 'is not valid for a procedure call'});
    if (nestedReporter) context.issues.push({path, message: 'procedure calls cannot be nested reporters'});
    const procedureId = value.procedure_id;
    const parameters = typeof procedureId === 'string' ? context.procedures.get(procedureId) : undefined;
    if (!parameters) {
      context.issues.push({path: path + '.procedure_id', message: 'must reference a declared procedure'});
      return;
    }
    if (!isRecord(value.inputs)) {
      context.issues.push({path: path + '.inputs', message: 'must be an object'});
      return;
    }
    const expected = new Map(parameters.map(parameter => [parameter.id, parameter]));
    for (const [name, input] of Object.entries(value.inputs)) {
      const parameter = expected.get(name);
      if (!parameter) context.issues.push({path: `${path}.inputs.${name}`, message: 'does not match a procedure parameter'});
      else validateInput(input, parameter.kind === 'boolean' ? 'boolean' : 'text', `${path}.inputs.${name}`, depth, context);
    }
    for (const parameter of parameters) if (!hasOwn(value.inputs, parameter.id)) context.issues.push({path: `${path}.inputs.${parameter.id}`, message: 'required procedure argument is missing'});
    return;
  }
  if (value.opcode === 'argument_reporter_string_number' || value.opcode === 'argument_reporter_boolean') {
    ownKeysOnly(value, ['opcode', 'procedure_id', 'argument_id'], path, context.issues);
    if (value.procedure_id !== undefined && value.procedure_id !== null) context.issues.push({path: path + '.procedure_id', message: 'is not valid for a procedure argument reporter'});
    const argumentId = value.argument_id;
    const parameter = typeof argumentId === 'string' ? context.procedureArguments?.get(argumentId) : undefined;
    if (!parameter) context.issues.push({path: path + '.argument_id', message: 'must reference a parameter of the current procedure'});
    if (!nestedReporter) context.issues.push({path, message: 'procedure argument reporters must be nested in an input'});
    const expectedKind = value.opcode === 'argument_reporter_boolean' ? 'boolean' : 'string_number';
    if (parameter && parameter.kind !== expectedKind) context.issues.push({path: path + '.opcode', message: 'reporter kind does not match the procedure parameter'});
    if (expectedReporter === 'boolean' && expectedKind !== 'boolean') context.issues.push({path, message: 'input requires a boolean reporter'});
    return;
  }
  ownKeysOnly(value, ['opcode', 'procedure_id', 'argument_id', 'fields', 'inputs', 'substacks'], path, context.issues);
  if (value.procedure_id !== undefined && value.procedure_id !== null) context.issues.push({path: path + '.procedure_id', message: 'is only valid for procedures_call'});
  if (value.argument_id !== undefined && value.argument_id !== null) context.issues.push({path: path + '.argument_id', message: 'is only valid for a procedure argument reporter'});
  if (!checkString(value.opcode, path + '.opcode', context.issues)) return;
  const spec = getBlockSpec(value.opcode);
  if (!spec) {
    const reason = value.opcode.startsWith('procedures_') || value.opcode.startsWith('argument_reporter_') ? 'procedure opcode is not allowed here' : 'unknown or extension opcode';
    context.issues.push({path: path + '.opcode', message: reason});
    return;
  }
  const extension = extensionForOpcode(value.opcode);
  if (extension && !context.enabledExtensions.has(extension)) {
    context.issues.push({path: path + '.opcode', message: `extension ${extension} is not declared for this program`});
  }
  if (!spec.targets.includes(context.target)) {
    context.issues.push({path: path + '.opcode', message: `not valid for ${context.target}`});
  }
  if (nestedReporter) {
    if (spec.shape !== 'reporter' && spec.shape !== 'boolean') {
      context.issues.push({path, message: 'nested input block must be a reporter or boolean'});
    } else if (expectedReporter === 'boolean' && spec.shape !== 'boolean') {
      context.issues.push({path, message: 'input requires a boolean block'});
    }
  } else if (spec.shape === 'reporter' || spec.shape === 'boolean') {
    context.issues.push({path, message: 'reporter blocks must be nested in an input'});
  }

  const fieldsValue = value.fields;
  if (fieldsValue !== undefined && !isRecord(fieldsValue)) {
    context.issues.push({path: path + '.fields', message: 'must be an object'});
  } else {
    const actual = fieldsValue ?? {};
    const expected = spec.fields ?? {};
    for (const name of Object.keys(actual)) {
      if (!hasOwn(expected, name)) context.issues.push({path: `${path}.fields.${name}`, message: 'unknown field'});
      const fieldValue = actual[name];
      if (!['string', 'number', 'boolean'].includes(typeof fieldValue) && !isStateRef(fieldValue) && !isResourceRef(fieldValue) && !isExistingAssetRef(fieldValue)) {
        context.issues.push({path: `${path}.fields.${name}`, message: 'field must be a scalar, state_ref, or resource reference'});
      } else if (typeof fieldValue === 'string' && fieldValue.length > PROGRAM_IR_LIMITS.shortString) {
        context.issues.push({path: `${path}.fields.${name}`, message: 'string is too long'});
      }
    }
    for (const [name, fieldSpec] of Object.entries(expected)) {
      if (fieldSpec.required && !hasOwn(actual, name)) context.issues.push({path: `${path}.fields.${name}`, message: 'required field is missing'});
      if (fieldSpec.resolver && hasOwn(actual, name) && typeof actual[name] !== 'string' && !isStateRef(actual[name])) {
        context.issues.push({path: `${path}.fields.${name}`, message: 'resolved field must use a name string or state_ref'});
      }
      if (fieldSpec.resolver && isStateRef(actual[name])) {
        const expectedState = fieldSpec.resolver === 'variable' ? 'variable' : fieldSpec.resolver === 'list' ? 'list' : 'broadcast';
        if (actual[name].state_kind !== expectedState) context.issues.push({path: `${path}.fields.${name}.state_kind`, message: `must be ${expectedState} for this field`});
      }
      if (fieldSpec.resourceType && hasOwn(actual, name) && typeof actual[name] !== 'string' && !isResourceRef(actual[name]) && !isExistingAssetRef(actual[name])) {
        context.issues.push({path: `${path}.fields.${name}`, message: 'resource field must use a name string or resource reference'});
      }
      if (fieldSpec.resourceType && (isResourceRef(actual[name]) || isExistingAssetRef(actual[name]))) {
        validateResourceField(actual[name], fieldSpec.resourceType, `${path}.fields.${name}`, context);
      }
    }
  }

  const inputsValue = value.inputs;
  if (inputsValue !== undefined && !isRecord(inputsValue)) {
    context.issues.push({path: path + '.inputs', message: 'must be an object'});
  } else {
    const actual = inputsValue ?? {};
    const expected = spec.inputs ?? {};
    for (const name of Object.keys(actual)) {
      const inputSpec = expected[name];
      if (!inputSpec) context.issues.push({path: `${path}.inputs.${name}`, message: 'unknown input'});
      else validateInput(actual[name], inputSpec.kind, `${path}.inputs.${name}`, depth, context, inputSpec.resourceType);
    }
    for (const [name, inputSpec] of Object.entries(expected)) {
      if (inputSpec.required && !hasOwn(actual, name)) context.issues.push({path: `${path}.inputs.${name}`, message: 'required input is missing'});
    }
  }

  const substacksValue = value.substacks;
  if (substacksValue !== undefined && !isRecord(substacksValue)) {
    context.issues.push({path: path + '.substacks', message: 'must be an object'});
  } else {
    const actual = substacksValue ?? {};
    const allowed = spec.substacks ?? [];
    for (const name of Object.keys(actual)) {
      if (name !== 'SUBSTACK' && name !== 'SUBSTACK2') {
        context.issues.push({path: `${path}.substacks.${name}`, message: 'unknown substack'});
        continue;
      }
      if (!allowed.includes(name)) context.issues.push({path: `${path}.substacks.${name}`, message: 'substack not allowed for opcode'});
      const stack = actual[name];
      if (!Array.isArray(stack)) context.issues.push({path: `${path}.substacks.${name}`, message: 'must be an array'});
      else {
        validateSequenceShape(stack, `${path}.substacks.${name}`, false, context.issues);
        stack.forEach((block, index) => validateBlock(block, `${path}.substacks.${name}[${index}]`, depth + 1, context));
      }
    }
    for (const name of allowed) {
      if (!hasOwn(actual, name)) context.issues.push({path: `${path}.substacks.${name}`, message: 'required substack is missing'});
    }
  }
};

export const validateProgramIR = (
  value: unknown,
  target: TargetKind = 'sprite',
  enabledExtensions: ReadonlySet<BuiltinAiExtension> = new Set(),
): ValidationResult => {
  const issues: ValidationIssue[] = [];
  if (!isRecord(value)) return {valid: false, issues: [{path: '$', message: 'program must be an object'}]};
  ownKeysOnly(value, ['schema_version', 'summary', 'remove_script_ids', 'variables', 'lists', 'broadcasts', 'procedures', 'scripts'], '$', issues);
  if (value.schema_version !== 1 && value.schema_version !== 2) issues.push({path: '$.schema_version', message: 'must equal 1 or 2'});
  checkString(value.summary, '$.summary', issues, PROGRAM_IR_LIMITS.summary);
  const validateStringArray = (candidate: unknown, path: string): void => {
    if (!Array.isArray(candidate)) issues.push({path, message: 'must be an array'});
    else candidate.forEach((item, index) => checkString(item, `${path}[${index}]`, issues));
  };
  validateStringArray(value.remove_script_ids, '$.remove_script_ids');
  if (Array.isArray(value.remove_script_ids)) {
    const ids = new Set<string>();
    value.remove_script_ids.forEach((id, index) => {
      if (typeof id !== 'string') return;
      if (!/^script_[A-Za-z0-9_-]+$/.test(id)) issues.push({path: `$.remove_script_ids[${index}]`, message: 'must use a script_N id'});
      if (ids.has(id)) issues.push({path: `$.remove_script_ids[${index}]`, message: 'must be unique'});
      ids.add(id);
    });
  }
  validateStringArray(value.broadcasts, '$.broadcasts');
  const validateNamedArray = (candidate: unknown, path: string, list: boolean): void => {
    if (!Array.isArray(candidate)) {
      issues.push({path, message: 'must be an array'});
      return;
    }
    const names = new Set<string>();
    candidate.forEach((item, index) => {
      const itemPath = `${path}[${index}]`;
      if (!isRecord(item)) return void issues.push({path: itemPath, message: 'must be an object'});
      ownKeysOnly(item, ['name', 'value', 'scope'], itemPath, issues);
      if (checkString(item.name, itemPath + '.name', issues)) {
        if (names.has(item.name)) issues.push({path: itemPath + '.name', message: 'must be unique'});
        names.add(item.name);
      }
      if (hasOwn(item, 'scope') && item.scope !== 'local' && item.scope !== 'global') {
        issues.push({path: itemPath + '.scope', message: 'must be local or global'});
      }
      if (hasOwn(item, 'value')) {
        if (list) {
          if (!Array.isArray(item.value)) issues.push({path: itemPath + '.value', message: 'must be an array'});
          else if (item.value.some(entry => !['string', 'number', 'boolean'].includes(typeof entry))) issues.push({path: itemPath + '.value', message: 'contains an invalid value'});
        } else if (!['string', 'number', 'boolean'].includes(typeof item.value)) issues.push({path: itemPath + '.value', message: 'invalid variable value'});
      }
    });
  };
  validateNamedArray(value.variables, '$.variables', false);
  validateNamedArray(value.lists, '$.lists', true);

  const procedures = new Map<string, ProcedureParameterIR[]>();
  const procedureCodes = new Set<string>();
  const rawProcedures = value.procedures ?? [];
  if (!Array.isArray(rawProcedures)) issues.push({path: '$.procedures', message: 'must be an array'});
  else if (rawProcedures.length > PROGRAM_IR_LIMITS.procedures) issues.push({path: '$.procedures', message: `maximum is ${PROGRAM_IR_LIMITS.procedures} procedures`});
  else rawProcedures.forEach((procedure, procedureIndex) => {
    const path = `$.procedures[${procedureIndex}]`;
    if (!isRecord(procedure)) return void issues.push({path, message: 'must be an object'});
    ownKeysOnly(procedure, ['id', 'label', 'parameters', 'warp', 'body'], path, issues);
    const procedureId = typeof procedure.id === 'string' ? procedure.id : '';
    const id = checkString(procedureId, path + '.id', issues);
    if (id && !/^procedure_[A-Za-z0-9_-]+$/.test(procedureId)) issues.push({path: path + '.id', message: 'must use a procedure_N id'});
    if (id && procedures.has(procedureId)) issues.push({path: path + '.id', message: 'procedure id must be unique'});
    const procedureLabel = typeof procedure.label === 'string' ? procedure.label : '';
    if (checkString(procedureLabel, path + '.label', issues) && procedureLabel.includes('%')) {
      issues.push({path: path + '.label', message: 'must not contain Scratch placeholder characters'});
    }
    if (typeof procedure.warp !== 'boolean') issues.push({path: path + '.warp', message: 'must be boolean'});
    const parameters: ProcedureParameterIR[] = [];
    if (!Array.isArray(procedure.parameters)) issues.push({path: path + '.parameters', message: 'must be an array'});
    else if (procedure.parameters.length > PROGRAM_IR_LIMITS.procedureParameters) issues.push({path: path + '.parameters', message: `maximum is ${PROGRAM_IR_LIMITS.procedureParameters} parameters`});
    else procedure.parameters.forEach((parameter, parameterIndex) => {
      const parameterPath = `${path}.parameters[${parameterIndex}]`;
      if (!isRecord(parameter)) return void issues.push({path: parameterPath, message: 'must be an object'});
      ownKeysOnly(parameter, ['id', 'name', 'kind', 'default'], parameterPath, issues);
      if (!checkString(parameter.id, parameterPath + '.id', issues) || !/^arg_[A-Za-z0-9_-]+$/.test(parameter.id)) issues.push({path: parameterPath + '.id', message: 'must use an arg_N id'});
      checkString(parameter.name, parameterPath + '.name', issues);
      if (parameter.kind !== 'string_number' && parameter.kind !== 'boolean') issues.push({path: parameterPath + '.kind', message: 'must be string_number or boolean'});
      if (hasOwn(parameter, 'default') && parameter.default !== null && !['string', 'number', 'boolean'].includes(typeof parameter.default)) issues.push({path: parameterPath + '.default', message: 'must be a scalar'});
      parameters.push(parameter as unknown as ProcedureParameterIR);
    });
    const parameterIds = parameters.map(parameter => parameter.id);
    if (new Set(parameterIds).size !== parameterIds.length) issues.push({path: path + '.parameters', message: 'parameter ids must be unique'});
    const procedureCode = `${procedureLabel} ${parameters.map(parameter => parameter.kind === 'boolean' ? '%b' : '%s').join(' ')}`.trim();
    if (procedureLabel && procedureCodes.has(procedureCode)) issues.push({path: path + '.label', message: 'procedure proccode must be unique'});
    if (procedureLabel) procedureCodes.add(procedureCode);
    if (id && !procedures.has(procedureId)) procedures.set(procedureId, parameters);
  });

  if (!Array.isArray(value.scripts)) issues.push({path: '$.scripts', message: 'must be an array'});
  else {
    if (value.scripts.length === 0) issues.push({path: '$.scripts', message: 'must contain at least one script'});
    if (value.scripts.length > PROGRAM_IR_LIMITS.scripts) issues.push({path: '$.scripts', message: `maximum is ${PROGRAM_IR_LIMITS.scripts} scripts`});
    const ids = new Set<string>();
    const context: ValidationContext = {target, issues, seen: new WeakSet(), visibleBlocks: 0, procedures, enabledExtensions};
    if (Array.isArray(rawProcedures)) rawProcedures.forEach((procedure, procedureIndex) => {
      if (!isRecord(procedure) || !Array.isArray(procedure.body) || typeof procedure.id !== 'string') {
        if (isRecord(procedure) && !Array.isArray(procedure.body)) issues.push({path: `$.procedures[${procedureIndex}].body`, message: 'must be a non-empty array'});
        return;
      }
      if (procedure.body.length === 0) {
        issues.push({path: `$.procedures[${procedureIndex}].body`, message: 'must be a non-empty array'});
        return;
      }
      context.procedureArguments = new Map((procedures.get(procedure.id) ?? []).map(parameter => [parameter.id, parameter]));
      validateSequenceShape(procedure.body, `$.procedures[${procedureIndex}].body`, false, issues);
      procedure.body.forEach((block, blockIndex) => {
        const blockPath = `$.procedures[${procedureIndex}].body[${blockIndex}]`;
        if (isRecord(block) && (String(block.opcode).startsWith('event_') || block.opcode === 'procedures_call')) {
          issues.push({path: blockPath + '.opcode', message: 'procedure bodies cannot contain event hats or procedure calls'});
        }
        validateBlock(block, blockPath, 1, context);
      });
      context.procedureArguments = undefined;
    });
    value.scripts.forEach((script, scriptIndex) => {
      const path = `$.scripts[${scriptIndex}]`;
      if (!isRecord(script)) return void issues.push({path, message: 'must be an object'});
      if (context.seen.has(script)) return void issues.push({path, message: 'cyclic or reused script object'});
      context.seen.add(script);
      ownKeysOnly(script, ['id', 'purpose', 'x', 'y', 'blocks'], path, issues);
      if (checkString(script.id, path + '.id', issues)) {
        if (!/^script_[A-Za-z0-9_-]+$/.test(script.id)) issues.push({path: path + '.id', message: 'must use a script_N id'});
        if (ids.has(script.id)) issues.push({path: path + '.id', message: 'script id must be unique'});
        ids.add(script.id);
      }
      checkString(script.purpose, path + '.purpose', issues);
      checkFinite(script.x, path + '.x', issues);
      checkFinite(script.y, path + '.y', issues);
      if (!Array.isArray(script.blocks) || script.blocks.length === 0) issues.push({path: path + '.blocks', message: 'must be a non-empty array'});
      else {
        validateSequenceShape(script.blocks, `${path}.blocks`, true, issues);
        script.blocks.forEach((block, index) => validateBlock(block, `${path}.blocks[${index}]`, 1, context));
      }
    });
    if (context.visibleBlocks > PROGRAM_IR_LIMITS.visibleBlocks) issues.push({path: '$.scripts', message: `maximum is ${PROGRAM_IR_LIMITS.visibleBlocks} visible blocks`});
  }
  return {valid: issues.length === 0, issues};
};

export interface ResolvedField {name: string; id: string}
export interface ProgramResolver {
  variable(name: string): ResolvedField;
  list(name: string): ResolvedField;
  broadcast(name: string): ResolvedField;
  state(kind: 'variable' | 'list' | 'broadcast', refId: string): ResolvedField;
}
export interface ResourceResolver {
  resource(ref: ResourceRefIR, expectedType: ResourceMenuAssetType): string;
  existing(ref: ExistingAssetRefIR): string;
}
export type IdGenerator = (kind: 'block' | 'shadow', ordinal: number, scriptId: string) => string;
export interface CompileOptions {
  target?: TargetKind;
  resolver?: Partial<ProgramResolver>;
  resourceResolver?: Partial<ResourceResolver>;
  idGenerator?: IdGenerator;
  enabledExtensions?: ReadonlySet<BuiltinAiExtension>;
}
export interface VMField {name: string; value: string | number | boolean; id?: string; variableType?: '' | 'list' | 'broadcast_msg'}
export interface VMInput {name: string; block: string; shadow: string | null}
export interface VMBlock {
  id: string;
  opcode: string;
  inputs: Record<string, VMInput>;
  fields: Record<string, VMField>;
  next: string | null;
  topLevel: boolean;
  parent: string | null;
  shadow: boolean;
  mutation?: Record<string, unknown>;
  x?: number;
  y?: number;
}
export interface CompiledScript {
  scriptId: string;
  topLevelId: string;
  comment: string;
  blockIds: string[];
}
export interface CompiledProgram {
  blocks: Record<string, VMBlock>;
  blockOrder: string[];
  topLevelIds: string[];
  scripts: CompiledScript[];
  removeScriptIds: string[];
  variables: ProgramIR['variables'];
  lists: ProgramIR['lists'];
  broadcasts: ProgramIR['broadcasts'];
  summary: string;
}

const primitive = {
  number: ['math_number', 'NUM'],
  text: ['text', 'TEXT'],
  angle: ['math_angle', 'NUM'],
  color: ['colour_picker', 'COLOUR']
} as const;

const defaultResolve = (name: string): ResolvedField => ({name, id: name});
const resolverVariableType: Record<FieldResolverKind, VMField['variableType']> = {variable: '', list: 'list', broadcast: 'broadcast_msg'};

export const validateCompiledGraph = (graph: Pick<CompiledProgram, 'blocks' | 'blockOrder' | 'topLevelIds'>): void => {
  const errors: string[] = [];
  const ids = Object.keys(graph.blocks);
  if (new Set(ids).size !== ids.length) errors.push('duplicate block IDs');
  if (new Set(graph.blockOrder).size !== graph.blockOrder.length || graph.blockOrder.some(id => !graph.blocks[id]) || graph.blockOrder.length !== ids.length) errors.push('invalid block order');
  for (const [id, block] of Object.entries(graph.blocks)) {
    if (block.id !== id) errors.push(`block key/id mismatch at ${id}`);
    if (block.next && !graph.blocks[block.next]) errors.push(`missing next reference from ${id}`);
    if (block.next && graph.blocks[block.next]?.parent !== id) errors.push(`next parent mismatch from ${id}`);
    if (block.parent && !graph.blocks[block.parent]) errors.push(`missing parent reference from ${id}`);
    if (block.topLevel !== graph.topLevelIds.includes(id)) errors.push(`topLevel mismatch at ${id}`);
    for (const input of Object.values(block.inputs)) {
      if (!graph.blocks[input.block]) errors.push(`missing input block from ${id}`);
      else if (graph.blocks[input.block].parent !== id) errors.push(`input parent mismatch from ${id}`);
      if (input.shadow && !graph.blocks[input.shadow]) errors.push(`missing shadow from ${id}`);
      else if (input.shadow && graph.blocks[input.shadow].parent !== id) errors.push(`shadow parent mismatch from ${id}`);
    }
  }
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (id: string): void => {
    if (visiting.has(id)) return void errors.push(`cycle at ${id}`);
    if (visited.has(id)) return;
    visiting.add(id);
    const block = graph.blocks[id];
    if (!block) {
      errors.push(`missing referenced block ${id}`);
      visiting.delete(id);
      return;
    }
    if (block.next) visit(block.next);
    for (const input of Object.values(block.inputs)) {
      visit(input.block);
      if (input.shadow && input.shadow !== input.block) visit(input.shadow);
    }
    visiting.delete(id);
    visited.add(id);
  };
  graph.topLevelIds.forEach(visit);
  if (visited.size !== ids.length) errors.push('unreachable blocks');
  if (errors.length) throw new Error('Invalid compiled graph: ' + errors.join('; '));
};

export const compileProgramIR = (program: ProgramIR, options: CompileOptions = {}): CompiledProgram => {
  const target = options.target ?? 'sprite';
  const validation = validateProgramIR(program, target, options.enabledExtensions);
  if (!validation.valid) throw new ProgramIRValidationError(validation.issues);
  const resolvers: ProgramResolver = {
    variable: options.resolver?.variable ?? defaultResolve,
    list: options.resolver?.list ?? defaultResolve,
    broadcast: options.resolver?.broadcast ?? defaultResolve,
    state: options.resolver?.state ?? ((kind, refId) => defaultResolve(refId))
  };
  const defaultId: IdGenerator = (kind, ordinal, scriptId) => `ai_${scriptId.replace(/[^A-Za-z0-9_-]/g, '_')}_${kind === 'shadow' ? 's' : 'b'}${ordinal}`;
  const makeId = options.idGenerator ?? defaultId;
  const blocks: Record<string, VMBlock> = {};
  const blockOrder: string[] = [];
  let ordinal = 0;
  let currentScriptId = '';
  let activeProcedureParameters = new Map<string, ProcedureParameterIR>();
  const procedureDefinitions = new Map((program.procedures ?? []).map(procedure => [procedure.id, procedure]));
  const procedureMutation = (procedure: NonNullable<ProgramIR['procedures']>[number]) => ({
    tagName: 'mutation',
    children: [],
    proccode: [procedure.label, ...procedure.parameters.map(parameter => parameter.kind === 'boolean' ? '%b' : '%s')].join(' '),
    argumentids: JSON.stringify(procedure.parameters.map(parameter => parameter.id)),
    argumentnames: JSON.stringify(procedure.parameters.map(parameter => parameter.name)),
    argumentdefaults: JSON.stringify(procedure.parameters.map(parameter => String(parameter.default ?? (parameter.kind === 'boolean' ? false : '')))),
    warp: String(procedure.warp),
  });
  const create = (opcode: string, parent: string | null, shadow: boolean): VMBlock => {
    const id = makeId(shadow ? 'shadow' : 'block', ordinal++, currentScriptId);
    if (!id || blocks[id]) throw new Error(`ID generator produced invalid or duplicate ID: ${id}`);
    const block: VMBlock = {id, opcode, inputs: {}, fields: {}, next: null, topLevel: false, parent, shadow};
    blocks[id] = block;
    blockOrder.push(id);
    return block;
  };
  const resolveField = (kind: FieldResolverKind, source: FieldValue): ResolvedField => {
    const result = isStateRef(source)
      ? resolvers.state(source.state_kind, source.ref_id)
      : resolvers[kind](String(source));
    if (!result || typeof result.name !== 'string' || typeof result.id !== 'string' || !result.id) throw new Error(`Unable to resolve ${kind}: ${isStateRef(source) ? source.ref_id : String(source)}`);
    return result;
  };
  const resolvedVmField = (name: string, kind: FieldResolverKind, source: FieldValue): VMField => {
    const resolved = resolveField(kind, source);
    return {name, value: resolved.name, id: resolved.id, variableType: resolverVariableType[kind]};
  };
  const compileMenuShadow = (spec: import('./block-catalog').InputSpec | undefined, value: string | number, parent: string): VMBlock | null => {
    if (!spec?.shadowOpcode || !spec.shadowField) return null;
    const shadow = create(spec.shadowOpcode, parent, true);
    if (spec.shadowResolver && typeof value === 'string' && value) {
      shadow.fields[spec.shadowField] = resolvedVmField(spec.shadowField, spec.shadowResolver, value);
    } else {
      shadow.fields[spec.shadowField] = {name: spec.shadowField, value, ...(spec.shadowResolver ? {variableType: resolverVariableType[spec.shadowResolver]} : {})};
    }
    return shadow;
  };
  const compileReporter = (ir: BlockIR, parent: string): VMBlock => compileBlock(ir, parent);
  const resolveResource = (input: ResourceRefIR, expectedType?: ResourceMenuAssetType): string => {
    const opId = input.ref?.op_id ?? input.op_id;
    if (!opId) throw new Error('逻辑资源引用缺少 op_id');
    if (!expectedType) throw new Error(`Resource reference is not attached to a typed Scratch menu: ${opId}`);
    const value = options.resourceResolver?.resource?.(input, expectedType);
    if (!value) throw new Error(`Unable to resolve resource: ${opId}`);
    return value;
  };
  const resolveExistingResource = (input: ExistingAssetRefIR): string => {
    const value = options.resourceResolver?.existing?.(input);
    if (!value) throw new Error(`Unable to resolve existing ${input.asset_type}: ${input.name}`);
    return value;
  };
  const compileScalar = (input: Exclude<InputIR, {kind: 'block'}>, parent: string): VMBlock => {
    if (input.kind === 'resource_ref') {
      const value = resolveResource(input);
      const menu = compileMenuShadow(undefined, value, parent);
      return menu ?? compileScalar({kind: 'text', value}, parent);
    }
    if (input.kind === 'existing_asset_ref') return compileScalar({kind: 'text', value: resolveExistingResource(input)}, parent);
    const [opcode, field] = primitive[input.kind];
    const shadow = create(opcode, parent, true);
    shadow.fields[field] = {name: field, value: input.value};
    return shadow;
  };
  const compileStack = (stack: BlockIR[], owner: string | null): VMBlock | null => {
    let first: VMBlock | null = null;
    let previous: VMBlock | null = null;
    for (const ir of stack) {
      const block = compileBlock(ir, previous ? previous.id : owner);
      if (!first) first = block;
      if (previous) previous.next = block.id;
      previous = block;
    }
    return first;
  };
  const compileBlock = (ir: BlockIR, parent: string | null): VMBlock => {
    if (ir.opcode === 'procedures_call') {
      const procedure = procedureDefinitions.get(ir.procedure_id ?? '');
      if (!procedure) throw new Error('自定义积木调用缺少声明：' + (ir.procedure_id ?? ''));
      const block = create('procedures_call', parent, false);
      block.mutation = procedureMutation(procedure);
      for (const parameter of procedure.parameters) {
        const input = ir.inputs?.[parameter.id];
        if (!input) throw new Error('自定义积木调用缺少参数：' + parameter.id);
        if (input.kind === 'block') {
          const reporter = compileReporter(input.block, block.id);
          block.inputs[parameter.id] = {name: parameter.id, block: reporter.id, shadow: null};
        } else {
          const scalar = compileScalar(input, block.id);
          block.inputs[parameter.id] = {name: parameter.id, block: scalar.id, shadow: scalar.id};
        }
      }
      return block;
    }
    if (ir.opcode === 'argument_reporter_string_number' || ir.opcode === 'argument_reporter_boolean') {
      const parameter = activeProcedureParameters.get(ir.argument_id ?? '');
      if (!parameter) throw new Error('自定义积木参数引用无效：' + (ir.argument_id ?? ''));
      const block = create(ir.opcode, parent, false);
      block.fields.VALUE = {name: 'VALUE', value: parameter.name};
      return block;
    }
    const spec = getBlockSpec(ir.opcode)!;
    const block = create(ir.opcode, parent, false);
    for (const [name, value] of Object.entries(ir.fields ?? {})) {
      const fieldSpec = spec.fields?.[name];
      if (fieldSpec?.resolver) {
        block.fields[name] = resolvedVmField(name, fieldSpec.resolver, value);
      } else if (isResourceRef(value)) {
        block.fields[name] = {name, value: resolveResource(value, fieldSpec?.resourceType)};
      } else if (isExistingAssetRef(value)) {
        block.fields[name] = {name, value: resolveExistingResource(value)};
      } else block.fields[name] = {name, value: value as Exclude<FieldValue, {kind: 'state_ref'} | ResourceRefIR | ExistingAssetRefIR>};
    }
    for (const [name, input] of Object.entries(ir.inputs ?? {})) {
      const inputSpec = spec.inputs?.[name];
      if (input.kind === 'block') {
        const reporter = compileReporter(input.block, block.id);
        const shadowKind = inputSpec?.shadow;
        const shadow = compileMenuShadow(inputSpec, '', block.id) ?? (shadowKind ? compileScalar({kind: shadowKind, value: shadowKind === 'color' ? '#000000' : shadowKind === 'text' ? '' : 0}, block.id) : null);
        block.inputs[name] = {name, block: reporter.id, shadow: shadow?.id ?? null};
      } else {
        const inputValue = input.kind === 'resource_ref'
          ? resolveResource(input, inputSpec?.resourceType)
          : input.kind === 'existing_asset_ref' ? resolveExistingResource(input) : input.value;
        const scalar = compileMenuShadow(inputSpec, inputValue, block.id) ?? compileScalar(input, block.id);
        block.inputs[name] = {name, block: scalar.id, shadow: scalar.id};
      }
    }
    for (const name of spec.substacks ?? []) {
      const first = compileStack(ir.substacks?.[name] ?? [], block.id);
      if (first) block.inputs[name] = {name, block: first.id, shadow: null};
    }
    return block;
  };

  const scripts: CompiledScript[] = [];
  const topLevelIds: string[] = [];
  for (const [procedureIndex, procedure] of (program.procedures ?? []).entries()) {
    currentScriptId = procedure.id;
    const startIndex = blockOrder.length;
    const definition = create('procedures_definition', null, false);
    const prototype = create('procedures_prototype', definition.id, false);
    prototype.mutation = procedureMutation(procedure);
    definition.inputs.custom_block = {name: 'custom_block', block: prototype.id, shadow: null};
    for (const parameter of procedure.parameters) {
      const reporter = create(
        parameter.kind === 'boolean' ? 'argument_reporter_boolean' : 'argument_reporter_string_number',
        prototype.id,
        false,
      );
      reporter.fields.VALUE = {name: 'VALUE', value: parameter.name};
      prototype.inputs[parameter.id] = {name: parameter.id, block: reporter.id, shadow: null};
    }
    activeProcedureParameters = new Map(procedure.parameters.map(parameter => [parameter.id, parameter]));
    const body = compileStack(procedure.body, definition.id);
    activeProcedureParameters = new Map();
    if (!body) throw new Error('自定义积木定义缺少内容：' + procedure.id);
    definition.next = body.id;
    definition.topLevel = true;
    definition.x = 260;
    definition.y = procedureIndex * 150;
    topLevelIds.push(definition.id);
    scripts.push({
      scriptId: procedure.id,
      topLevelId: definition.id,
      comment: `AI 自定义积木：${procedure.label}`,
      blockIds: blockOrder.slice(startIndex),
    });
  }
  for (const script of program.scripts) {
    currentScriptId = script.id;
    const startIndex = blockOrder.length;
    const first = compileStack(script.blocks, null)!;
    first.topLevel = true;
    first.parent = null;
    first.x = script.x;
    first.y = script.y;
    topLevelIds.push(first.id);
    scripts.push({scriptId: script.id, topLevelId: first.id, comment: `AI 生成：${script.purpose}`, blockIds: blockOrder.slice(startIndex)});
  }
  const result: CompiledProgram = {
    blocks,
    blockOrder,
    topLevelIds,
    scripts,
    removeScriptIds: [...program.remove_script_ids],
    variables: program.variables.map(value => ({...value})),
    lists: program.lists.map(value => ({...value, value: value.value ? [...value.value] : undefined})),
    broadcasts: [...program.broadcasts],
    summary: program.summary
  };
  validateCompiledGraph(result);
  return result;
};
