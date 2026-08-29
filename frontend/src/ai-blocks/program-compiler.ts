import {getBlockSpec, type FieldResolverKind} from './block-catalog';
import {
  PROGRAM_IR_LIMITS,
  ProgramIRValidationError,
  type BlockIR,
  type FieldValue,
  type InputIR,
  type InputKind,
  type ProgramIR,
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

interface ValidationContext {
  target: TargetKind;
  issues: ValidationIssue[];
  seen: WeakSet<object>;
  visibleBlocks: number;
}

const validateInput = (value: unknown, expected: InputKind | 'boolean' | 'any', path: string, depth: number, context: ValidationContext): void => {
  if (!isRecord(value)) {
    context.issues.push({path, message: 'must be an input object'});
    return;
  }
  if (context.seen.has(value)) {
    context.issues.push({path, message: 'cyclic or reused input object'});
    return;
  }
  context.seen.add(value);
  ownKeysOnly(value, ['kind', 'value', 'block'], path, context.issues);
  const kind = value.kind;
  if (!['number', 'text', 'angle', 'color', 'block'].includes(String(kind))) {
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
  ownKeysOnly(value, ['opcode', 'fields', 'inputs', 'substacks'], path, context.issues);
  if (!checkString(value.opcode, path + '.opcode', context.issues)) return;
  const spec = getBlockSpec(value.opcode);
  if (!spec) {
    const reason = value.opcode.startsWith('procedures_') ? 'procedure opcodes are forbidden' : 'unknown or extension opcode';
    context.issues.push({path: path + '.opcode', message: reason});
    return;
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
      if (!['string', 'number', 'boolean'].includes(typeof fieldValue)) {
        context.issues.push({path: `${path}.fields.${name}`, message: 'field must be a string, number, or boolean'});
      } else if (typeof fieldValue === 'string' && fieldValue.length > PROGRAM_IR_LIMITS.shortString) {
        context.issues.push({path: `${path}.fields.${name}`, message: 'string is too long'});
      }
    }
    for (const [name, fieldSpec] of Object.entries(expected)) {
      if (fieldSpec.required && !hasOwn(actual, name)) context.issues.push({path: `${path}.fields.${name}`, message: 'required field is missing'});
      if (fieldSpec.resolver && hasOwn(actual, name) && typeof actual[name] !== 'string') {
        context.issues.push({path: `${path}.fields.${name}`, message: 'resolved field must use a name string'});
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
      else validateInput(actual[name], inputSpec.kind, `${path}.inputs.${name}`, depth, context);
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
      else stack.forEach((block, index) => validateBlock(block, `${path}.substacks.${name}[${index}]`, depth + 1, context));
    }
    for (const name of allowed) {
      if (!hasOwn(actual, name)) context.issues.push({path: `${path}.substacks.${name}`, message: 'required substack is missing'});
    }
  }
};

export const validateProgramIR = (value: unknown, target: TargetKind = 'sprite'): ValidationResult => {
  const issues: ValidationIssue[] = [];
  if (!isRecord(value)) return {valid: false, issues: [{path: '$', message: 'program must be an object'}]};
  ownKeysOnly(value, ['schema_version', 'summary', 'remove_script_ids', 'variables', 'lists', 'broadcasts', 'scripts'], '$', issues);
  if (value.schema_version !== 1) issues.push({path: '$.schema_version', message: 'must equal 1'});
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

  if (!Array.isArray(value.scripts)) issues.push({path: '$.scripts', message: 'must be an array'});
  else {
    if (value.scripts.length === 0) issues.push({path: '$.scripts', message: 'must contain at least one script'});
    if (value.scripts.length > PROGRAM_IR_LIMITS.scripts) issues.push({path: '$.scripts', message: `maximum is ${PROGRAM_IR_LIMITS.scripts} scripts`});
    const ids = new Set<string>();
    const context: ValidationContext = {target, issues, seen: new WeakSet(), visibleBlocks: 0};
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
      else script.blocks.forEach((block, index) => validateBlock(block, `${path}.blocks[${index}]`, 1, context));
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
}
export type IdGenerator = (kind: 'block' | 'shadow', ordinal: number, scriptId: string) => string;
export interface CompileOptions {
  target?: TargetKind;
  resolver?: Partial<ProgramResolver>;
  idGenerator?: IdGenerator;
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
  const validation = validateProgramIR(program, target);
  if (!validation.valid) throw new ProgramIRValidationError(validation.issues);
  const resolvers: ProgramResolver = {
    variable: options.resolver?.variable ?? defaultResolve,
    list: options.resolver?.list ?? defaultResolve,
    broadcast: options.resolver?.broadcast ?? defaultResolve
  };
  const defaultId: IdGenerator = (kind, ordinal, scriptId) => `ai_${scriptId.replace(/[^A-Za-z0-9_-]/g, '_')}_${kind === 'shadow' ? 's' : 'b'}${ordinal}`;
  const makeId = options.idGenerator ?? defaultId;
  const blocks: Record<string, VMBlock> = {};
  const blockOrder: string[] = [];
  let ordinal = 0;
  let currentScriptId = '';
  const create = (opcode: string, parent: string | null, shadow: boolean): VMBlock => {
    const id = makeId(shadow ? 'shadow' : 'block', ordinal++, currentScriptId);
    if (!id || blocks[id]) throw new Error(`ID generator produced invalid or duplicate ID: ${id}`);
    const block: VMBlock = {id, opcode, inputs: {}, fields: {}, next: null, topLevel: false, parent, shadow};
    blocks[id] = block;
    blockOrder.push(id);
    return block;
  };
  const resolveField = (kind: FieldResolverKind, sourceName: string): ResolvedField => {
    const result = resolvers[kind](sourceName);
    if (!result || typeof result.name !== 'string' || typeof result.id !== 'string' || !result.id) throw new Error(`Unable to resolve ${kind}: ${sourceName}`);
    return result;
  };
  const resolvedVmField = (name: string, kind: FieldResolverKind, sourceName: string): VMField => {
    const resolved = resolveField(kind, sourceName);
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
  const compileScalar = (input: Exclude<InputIR, {kind: 'block'}>, parent: string): VMBlock => {
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
    const spec = getBlockSpec(ir.opcode)!;
    const block = create(ir.opcode, parent, false);
    for (const [name, value] of Object.entries(ir.fields ?? {})) {
      const fieldSpec = spec.fields?.[name];
      if (fieldSpec?.resolver) {
        const resolved = resolveField(fieldSpec.resolver, String(value));
        block.fields[name] = resolvedVmField(name, fieldSpec.resolver, String(value));
      } else block.fields[name] = {name, value: value as FieldValue};
    }
    for (const [name, input] of Object.entries(ir.inputs ?? {})) {
      const inputSpec = spec.inputs?.[name];
      if (input.kind === 'block') {
        const reporter = compileReporter(input.block, block.id);
        const shadowKind = inputSpec?.shadow;
        const shadow = compileMenuShadow(inputSpec, '', block.id) ?? (shadowKind ? compileScalar({kind: shadowKind, value: shadowKind === 'color' ? '#000000' : shadowKind === 'text' ? '' : 0}, block.id) : null);
        block.inputs[name] = {name, block: reporter.id, shadow: shadow?.id ?? null};
      } else {
        const scalar = compileMenuShadow(inputSpec, input.value, block.id) ?? compileScalar(input, block.id);
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
