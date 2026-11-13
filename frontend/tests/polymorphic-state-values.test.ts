import {execFile} from 'node:child_process';
import {mkdtemp, rm, writeFile} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';
import {describe, expect, it} from 'vitest';
import {compileProgramIR, validateProgramIR} from '../src/ai-blocks/program-compiler';
import type {BlockIR, InputIR, ProgramIR} from '../src/ai-blocks/program-ir';

const exec = promisify(execFile);
const cli = fileURLToPath(new URL('../scripts/verify-ai-program.mjs', import.meta.url));
const number = (value: number): InputIR => ({kind: 'number', value});
const text = (value: string): InputIR => ({kind: 'text', value});
const nested = (block: BlockIR): InputIR => ({kind: 'block', block});
const flag: BlockIR = {opcode: 'event_whenflagclicked'};
const program = (blocks: BlockIR[]): ProgramIR => ({schema_version: 1, summary: 'typed state values', remove_script_ids: [], variables: [{name: 'value', value: 99}], lists: [{name: 'items', value: []}], broadcasts: [], scripts: [{id: 'script_values', purpose: 'preserve scalar types', x: 0, y: 0, blocks: [flag, ...blocks]}]});
const cases = [
  ['data_setvariableto', 'VALUE', 'VARIABLE'],
  ['data_addtolist', 'ITEM', 'LIST'],
  ['data_insertatlist', 'ITEM', 'LIST'],
  ['data_replaceitemoflist', 'ITEM', 'LIST'],
] as const;
const operation = (opcode: typeof cases[number][0], port: string, field: string, value: InputIR): BlockIR => ({opcode, fields: {[field]: field === 'VARIABLE' ? 'value' : 'items'}, inputs: {[port]: value, ...(['data_insertatlist', 'data_replaceitemoflist'].includes(opcode) ? {INDEX: text('1')} : {})}});

describe('polymorphic state value ports', () => {
  it.each(cases)('compiles numeric and existing text literals without casts: %s', (opcode, port, field) => {
    for (const [input, shadowOpcode, shadowField, expected] of [[number(0), 'math_number', 'NUM', 0], [text('007'), 'text', 'TEXT', '007']] as const) {
      const graph = compileProgramIR(program([operation(opcode, port, field, input)]));
      const block = Object.values(graph.blocks).find(value => value.opcode === opcode)!;
      const shadow = graph.blocks[block.inputs[port].block];
      expect(shadow.opcode).toBe(shadowOpcode);
      expect(shadow.fields[shadowField].value).toBe(expected);
      expect(typeof shadow.fields[shadowField].value).toBe(typeof expected);
    }
  });

  it.each(cases)('keeps the native text fallback under a connected numeric reporter: %s', (opcode, port, field) => {
    const graph = compileProgramIR(program([operation(opcode, port, field, nested({opcode: 'operator_add', inputs: {NUM1: number(1), NUM2: number(2)}}))]));
    const block = Object.values(graph.blocks).find(value => value.opcode === opcode)!;
    expect(graph.blocks[block.inputs[port].block].opcode).toBe('operator_add');
    expect(graph.blocks[block.inputs[port].shadow!]).toMatchObject({opcode: 'text', fields: {TEXT: {value: ''}}});
  });

  it('continues to reject number literals on the explicit text speech policy', () => {
    expect(validateProgramIR(program([{opcode: 'looks_say', inputs: {MESSAGE: number(0)}}])).valid).toBe(false);
  });

  it('executes and round-trips numeric variable and mixed list values in the real Scratch VM', async () => {
    const append = (value: InputIR): BlockIR => ({opcode: 'data_addtolist', fields: {LIST: 'items'}, inputs: {ITEM: value}});
    const readVariable = nested({opcode: 'data_variable', fields: {VARIABLE: 'value'}});
    const ir = program([
      {opcode: 'data_setvariableto', fields: {VARIABLE: 'value'}, inputs: {VALUE: number(0)}},
      append(readVariable), append(number(7)),
      {opcode: 'data_insertatlist', fields: {LIST: 'items'}, inputs: {INDEX: text('2'), ITEM: number(3)}},
      {opcode: 'data_replaceitemoflist', fields: {LIST: 'items'}, inputs: {INDEX: text('3'), ITEM: number(9)}},
      append(text('seven')),
      {opcode: 'data_setvariableto', fields: {VARIABLE: 'value'}, inputs: {VALUE: text('007')}}, append(readVariable),
      {opcode: 'data_setvariableto', fields: {VARIABLE: 'value'}, inputs: {VALUE: number(2.5)}}, append(readVariable),
      append(nested({opcode: 'operator_equals', inputs: {OPERAND1: number(1), OPERAND2: number(1)}})),
    ]);
    const directory = await mkdtemp(path.join(os.tmpdir(), 'scratch-polymorphic-state-'));
    try {
      const inputPath = path.join(directory, 'case.json');
      await writeFile(inputPath, JSON.stringify({program: ir, roundTrip: true, scenarios: [{maxTicks: 12, expect: {lists: {items: [0, 3, 9, 'seven', '007', 2.5, true]}}}]}));
      const {stdout} = await exec(process.execPath, [cli, '--input', inputPath], {timeout: 25000, maxBuffer: 1024 * 1024});
      const result = JSON.parse(stdout);
      expect(result.status).toBe('PASS');
      expect(result.roundTrip.status).toBe('PASS');
      expect(result.scenarios[0].assertions.find((assertion: {name: string}) => assertion.name === 'list:items').actual).toEqual([0, 3, 9, 'seven', '007', 2.5, true]);
    } finally {await rm(directory, {recursive: true, force: true});}
  }, 30000);
});
