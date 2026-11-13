import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {fileURLToPath, pathToFileURL} from 'node:url';

// Design evidence only: no state/resource/procedure transport or model calls.
const evidenceDir = path.dirname(fileURLToPath(import.meta.url));
const projectRoot = path.resolve(evidenceDir, '../../..');
const require = createRequire(path.join(projectRoot, 'frontend/package.json'));
const {build} = require('esbuild');
const temporaryDir = await fs.mkdtemp(path.join(os.tmpdir(), 'scratch-draft-design-'));

try {
  const bundle = path.join(temporaryDir, 'compiler.mjs');
  await build({
    stdin: {
      contents: [
        'export {getBlockSpec} from "./frontend/src/ai-blocks/block-catalog.ts";',
        'export {compileProgramIR, validateProgramIR, validateCompiledGraph} from "./frontend/src/ai-blocks/program-compiler.ts";'
      ].join('\n'),
      resolveDir: projectRoot,
      sourcefile: 'source-design-evidence.ts',
      loader: 'ts'
    },
    bundle: true,
    platform: 'node',
    format: 'esm',
    outfile: bundle,
    logLevel: 'silent'
  });
  const {getBlockSpec, compileProgramIR, validateProgramIR, validateCompiledGraph} = await import(pathToFileURL(bundle).href);
  const draft = JSON.parse(await fs.readFile(path.join(evidenceDir, 'draft-example.json'), 'utf8'));
  const slot = {id: 'move_and_check', purpose: '初始化位置、移动并检查结果', trigger: {opcode: 'event_whenflagclicked'}};

  const normalizeSequence = body => {
    assert(Array.isArray(body), 'body must be an array');
    return body.map((block, index) => {
      const shape = getBlockSpec(block?.opcode)?.shape;
      assert(shape !== 'hat', 'body cannot contain an event hat');
      assert(shape !== 'cap' || index === body.length - 1, 'cap must end a sequence');
      return normalizeBlock(block);
    });
  };
  const normalizeBlock = source => {
    assert(source && typeof source === 'object' && !Array.isArray(source), 'block must be an object');
    for (const key of Object.keys(source)) assert(['opcode', 'fields', 'inputs', 'substacks'].includes(key), 'unknown block property: ' + key);
    const spec = getBlockSpec(source.opcode);
    assert(spec, 'unknown opcode: ' + source.opcode);
    const result = {opcode: source.opcode};
    if (source.fields !== undefined) {
      assert(source.fields && typeof source.fields === 'object' && !Array.isArray(source.fields));
      for (const [name, value] of Object.entries(source.fields)) {
        assert(spec.fields?.[name], 'unknown field: ' + name);
        assert(['string', 'number'].includes(typeof value), 'probe supports scalar fields only');
      }
      result.fields = structuredClone(source.fields);
    }
    if (source.inputs !== undefined) {
      assert(source.inputs && typeof source.inputs === 'object' && !Array.isArray(source.inputs));
      result.inputs = {};
      for (const [name, value] of Object.entries(source.inputs)) {
        const port = spec.inputs?.[name];
        assert(port, 'unknown input: ' + name);
        if (value && typeof value === 'object' && !Array.isArray(value)) {
          result.inputs[name] = {kind: 'block', block: normalizeBlock(value)};
          continue;
        }
        assert(port.kind !== 'boolean', 'boolean port requires a reporter');
        const kind = port.kind === 'any' ? (typeof value === 'number' ? 'number' : 'text') : port.kind;
        assert(['number', 'angle', 'text', 'color'].includes(kind), 'probe does not support this input kind');
        assert(kind === 'number' || kind === 'angle' ? typeof value === 'number' && Number.isFinite(value) : typeof value === 'string', 'literal kind mismatch: ' + name);
        result.inputs[name] = {kind, value};
      }
    }
    if (source.substacks !== undefined) {
      assert(source.substacks && typeof source.substacks === 'object' && !Array.isArray(source.substacks));
      result.substacks = {};
      for (const [name, body] of Object.entries(source.substacks)) {
        assert(spec.substacks?.includes(name), 'unknown substack: ' + name);
        assert(Array.isArray(body), 'substack must be an array');
        result.substacks[name] = normalizeSequence(body);
      }
    }
    return result;
  };
  const normalize = value => {
    assert.deepEqual(Object.keys(value).sort(), ['body', 'slot_id']);
    assert.equal(value.slot_id, slot.id);
    assert(Array.isArray(value.body) && value.body.length > 0);
    const program = {
      schema_version: 1,
      summary: slot.purpose,
      remove_script_ids: [],
      variables: [], lists: [], broadcasts: [], procedures: [],
      scripts: [{id: 'script_' + slot.id, purpose: slot.purpose, x: 0, y: 0, blocks: [structuredClone(slot.trigger), ...normalizeSequence(value.body)]}]
    };
    const validation = validateProgramIR(program, 'sprite');
    assert(validation.valid, JSON.stringify(validation.issues));
    return program;
  };
  const normalized = normalize(draft);
  const graph = compileProgramIR(normalized, {target: 'sprite'});
  validateCompiledGraph(graph);
  const shadows = Object.values(graph.blocks).filter(block => block.shadow);
  assert.equal(graph.topLevelIds.length, 1);
  assert.equal(Object.values(graph.blocks).filter(block => !block.shadow).length, 8);
  assert(shadows.some(block => block.opcode === 'math_number'));
  assert(shadows.some(block => block.opcode === 'text'));

  // Record a current frontend/backend contract gap instead of claiming the
  // production compiler already rejects this connection sequence.
  const capFollowed = structuredClone(normalized);
  capFollowed.scripts[0].blocks.splice(1, 0, {opcode: 'control_stop', fields: {STOP_OPTION: 'all'}});
  const existingFrontendAcceptsCapFollowed = validateProgramIR(capFollowed, 'sprite').valid;

  const rejected = [];
  for (const [name, mutate] of [
    ['wrong_slot', value => {value.slot_id = 'other';}],
    ['unknown_port', value => {value.body[0].inputs.Z = 1;}],
    ['text_literal_as_number', value => {value.body[2].substacks.SUBSTACK[0].inputs.MESSAGE = 1;}],
    ['boolean_literal_in_condition', value => {value.body[2].inputs.CONDITION = true;}],
    ['reporter_in_stack', value => {value.body.push({opcode: 'motion_xposition'});}],
    ['hat_in_body', value => {value.body.push({opcode: 'event_whenflagclicked'});}],
    ['cap_with_following_block', value => {value.body.unshift({opcode: 'control_stop', fields: {STOP_OPTION: 'all'}});}],
    ['missing_required_input', value => {delete value.body[0].inputs.X;}]
  ]) {
    const candidate = structuredClone(draft);
    mutate(candidate);
    assert.throws(() => normalize(candidate), undefined, name);
    rejected.push(name);
  }
  const sourceFiles = [
    'frontend/package-lock.json',
    'frontend/src/ai-blocks/block-catalog.ts',
    'frontend/src/ai-blocks/program-compiler.ts',
    'frontend/node_modules/scratch-blocks/src/blocks/control.ts',
    'frontend/node_modules/@scratch/scratch-gui/src/lib/blocks.js',
    'frontend/node_modules/@scratch/scratch-vm/src/engine/runtime.js'
  ];
  const hashes = {};
  for (const sourceFile of sourceFiles) hashes[sourceFile] = createHash('sha256').update(await fs.readFile(path.join(projectRoot, sourceFile))).digest('hex');
  const byteLength = value => Buffer.byteLength(JSON.stringify(value));
  process.stdout.write(JSON.stringify({
    status: 'PASS',
    scope: 'sample normalization and existing TypeScript compiler only',
    normalizedProgram: normalized,
    compiled: {visibleBlocks: Object.values(graph.blocks).filter(block => !block.shadow).length, shadowBlocks: shadows.length, topLevelScripts: graph.topLevelIds.length},
    negativeCasesRejected: rejected,
    existingContractObservations: {frontendAcceptsCapFollowed: existingFrontendAcceptsCapFollowed, note: 'The demo normalizer rejects this; the current Go validator also has an explicit cap-last rule. Production files were not changed.'},
    utf8Bytes: {draft: byteLength(draft), normalizedProgram: byteLength(normalized)},
    byteComparisonIsNotTokenOrCostMeasurement: true,
    sourceHashes: hashes
  }, null, 2) + '\n');
} finally {
  await fs.rm(temporaryDir, {recursive: true, force: true});
}
