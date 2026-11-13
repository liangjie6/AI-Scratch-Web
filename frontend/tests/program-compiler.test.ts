import {describe, expect, it} from 'vitest';
import {
  compileProgramIR,
  validateCompiledGraph,
  validateProgramIR,
  type CompiledProgram
} from '../src/ai-blocks/program-compiler';
import type {BlockIR, ProgramIR} from '../src/ai-blocks/program-ir';

const program = (blocks: BlockIR[]): ProgramIR => ({
  schema_version: 1,
  summary: 'test program',
  remove_script_ids: [],
  variables: [],
  lists: [],
  broadcasts: [],
  scripts: [{id: 'script_main', purpose: 'test behavior', x: 20, y: 30, blocks}]
});
const num = (value: number) => ({kind: 'number' as const, value});
const text = (value: string) => ({kind: 'text' as const, value});
const nested = (block: BlockIR) => ({kind: 'block' as const, block});

const opcodes = (output: CompiledProgram): string[] => output.blockOrder.map(id => output.blocks[id].opcode);

const expectInvalid = (value: unknown, target: 'stage' | 'sprite' = 'sprite', message?: string): void => {
  const result = validateProgramIR(value, target);
  expect(result.valid).toBe(false);
  if (message) expect(result.issues.map(issue => issue.message).join(' ')).toContain(message);
};

describe('compileProgramIR', () => {
  it('compiles a green flag movement stack deterministically with shadows and comment metadata', () => {
    const ir = program([
      {opcode: 'event_whenflagclicked'},
      {opcode: 'motion_movesteps', inputs: {STEPS: num(10)}}
    ]);
    const first = compileProgramIR(ir);
    const second = compileProgramIR(ir);
    expect(first).toEqual(second);
    expect(opcodes(first)).toEqual(['event_whenflagclicked', 'motion_movesteps', 'math_number']);
    const hat = first.blocks[first.topLevelIds[0]];
    const move = first.blocks[hat.next!];
    const shadow = first.blocks[move.inputs.STEPS.block];
    expect(hat).toMatchObject({topLevel: true, parent: null, x: 20, y: 30});
    expect(move.parent).toBe(hat.id);
    expect(move.inputs.STEPS).toEqual({name: 'STEPS', block: shadow.id, shadow: shadow.id});
    expect(shadow).toMatchObject({opcode: 'math_number', parent: move.id, shadow: true});
    expect(shadow.fields.NUM.value).toBe(10);
    expect(first.scripts[0]).toMatchObject({scriptId: 'script_main', topLevelId: hat.id, comment: 'AI 生成：test behavior'});
  });

  it('compiles touching mouse-pointer with Scratch menu shadow', () => {
    const output = compileProgramIR(program([{opcode: 'event_whenflagclicked'}, {
      opcode: 'control_if',
      inputs: {CONDITION: nested({opcode: 'sensing_touchingobject', inputs: {TOUCHINGOBJECTMENU: text('_mouse_')}})},
      substacks: {SUBSTACK: [{opcode: 'motion_movesteps', inputs: {STEPS: num(3)}}]}
    }]));
    const touching = Object.values(output.blocks).find(block => block.opcode === 'sensing_touchingobject')!;
    const menu = output.blocks[touching.inputs.TOUCHINGOBJECTMENU.shadow!];
    expect(menu).toMatchObject({opcode: 'sensing_touchingobjectmenu', shadow: true, parent: touching.id});
    expect(menu.fields.TOUCHINGOBJECTMENU.value).toBe('_mouse_');
    expect(touching.inputs.TOUCHINGOBJECTMENU).toEqual({name: 'TOUCHINGOBJECTMENU', block: menu.id, shadow: menu.id});
  });

  it('compiles key pressed with Scratch menu shadow', () => {
    const output = compileProgramIR(program([{opcode: 'event_whenflagclicked'}, {
      opcode: 'control_if',
      inputs: {CONDITION: nested({opcode: 'sensing_keypressed', inputs: {KEY_OPTION: text('up arrow')}})},
      substacks: {SUBSTACK: [{opcode: 'motion_movesteps', inputs: {STEPS: num(1)}}]}
    }]));
    const keyPressed = Object.values(output.blocks).find(block => block.opcode === 'sensing_keypressed')!;
    const menu = output.blocks[keyPressed.inputs.KEY_OPTION.block];
    expect(menu).toMatchObject({opcode: 'sensing_keyoptions', shadow: true, parent: keyPressed.id});
    expect(menu.fields.KEY_OPTION.value).toBe('up arrow');
    expect(keyPressed.inputs.KEY_OPTION).toEqual({name: 'KEY_OPTION', block: menu.id, shadow: menu.id});
  });

  it('compiles repeat/if substacks and nested boolean reporters', () => {
    const ir = program([{opcode: 'event_whenflagclicked'}, {
      opcode: 'control_repeat',
      inputs: {TIMES: num(3)},
      substacks: {SUBSTACK: [{
        opcode: 'control_if',
        inputs: {CONDITION: nested({opcode: 'operator_gt', inputs: {OPERAND1: num(4), OPERAND2: num(2)}})},
        substacks: {SUBSTACK: [{opcode: 'motion_turnright', inputs: {DEGREES: num(15)}}]}
      }]}
    }]);
    const output = compileProgramIR(ir);
    const repeat = Object.values(output.blocks).find(block => block.opcode === 'control_repeat')!;
    const conditional = output.blocks[repeat.inputs.SUBSTACK.block];
    const comparison = output.blocks[conditional.inputs.CONDITION.block];
    const turn = output.blocks[conditional.inputs.SUBSTACK.block];
    expect(conditional.parent).toBe(repeat.id);
    expect(comparison.parent).toBe(conditional.id);
    expect(conditional.inputs.CONDITION.shadow).toBeNull();
    expect(turn.parent).toBe(conditional.id);
  });

  it('compiles arithmetic reporters with an obscured scalar shadow', () => {
    const ir = program([{opcode: 'event_whenflagclicked'}, {
      opcode: 'motion_movesteps',
      inputs: {STEPS: nested({opcode: 'operator_add', inputs: {NUM1: num(5), NUM2: num(7)}})}
    }]);
    const output = compileProgramIR(ir);
    const move = Object.values(output.blocks).find(block => block.opcode === 'motion_movesteps')!;
    const add = output.blocks[move.inputs.STEPS.block];
    const slotShadow = output.blocks[move.inputs.STEPS.shadow!];
    expect(add.opcode).toBe('operator_add');
    expect(slotShadow).toMatchObject({opcode: 'math_number', parent: move.id, shadow: true});
    expect(add.inputs.NUM1.shadow).toBe(add.inputs.NUM1.block);
  });

  it('resolves variable, list, and broadcast field names to Scratch IDs', () => {
    const ir: ProgramIR = {
      ...program([
        {opcode: 'event_whenbroadcastreceived', fields: {BROADCAST_OPTION: 'go'}},
        {opcode: 'data_setvariableto', fields: {VARIABLE: 'score'}, inputs: {VALUE: text('1')}},
        {opcode: 'data_addtolist', fields: {LIST: 'items'}, inputs: {ITEM: text('apple')}}
      ]),
      variables: [{name: 'score', value: 0}],
      lists: [{name: 'items', value: []}],
      broadcasts: ['go']
    };
    const output = compileProgramIR(ir, {resolver: {
      variable: name => ({name: name.toUpperCase(), id: 'var-id'}),
      list: name => ({name, id: 'list-id'}),
      broadcast: name => ({name, id: 'broadcast-id'})
    }});
    const byOpcode = (opcode: string) => Object.values(output.blocks).find(block => block.opcode === opcode)!;
     expect(byOpcode('event_whenbroadcastreceived').fields.BROADCAST_OPTION).toEqual({name: 'BROADCAST_OPTION', value: 'go', id: 'broadcast-id', variableType: 'broadcast_msg'});
    expect(byOpcode('data_setvariableto').fields.VARIABLE).toEqual({name: 'VARIABLE', value: 'SCORE', id: 'var-id', variableType: ''});
    expect(byOpcode('data_addtolist').fields.LIST.id).toBe('list-id');
  });

  it('validates and preserves variable and list scope', () => {
    const ir: ProgramIR = {
      ...program([{opcode: 'event_whenflagclicked'}]),
      variables: [{name: 'score', value: 0, scope: 'local'}],
      lists: [{name: 'shared', value: ['a'], scope: 'global'}]
    };
    const output = compileProgramIR(ir);
    expect(output.variables).toEqual([{name: 'score', value: 0, scope: 'local'}]);
    expect(output.lists).toEqual([{name: 'shared', value: ['a'], scope: 'global'}]);

    const invalid = structuredClone(ir) as unknown as {variables: Array<Record<string, unknown>>};
    invalid.variables[0].scope = 'sprite';
    expectInvalid(invalid, 'sprite', 'must be local or global');
  });

  it('supports an injected ID sequence and exposes block order', () => {
    const output = compileProgramIR(program([{opcode: 'event_whenflagclicked'}]), {
      idGenerator: (_kind, ordinal) => `id-${ordinal}`
    });
    expect(output.blockOrder).toEqual(['id-0']);
    expect(output.topLevelIds).toEqual(['id-0']);
  });
});

describe('validateProgramIR', () => {
  it('rejects sprite motion on the stage', () => {
    expectInvalid(program([{opcode: 'event_whenflagclicked'}, {opcode: 'motion_movesteps', inputs: {STEPS: num(10)}}]), 'stage', 'not valid for stage');
  });

  it('rejects unknown opcodes, undeclared extensions, and undeclared procedures', () => {
    expectInvalid(program([{opcode: 'unknown_block'}]), 'sprite', 'unknown or extension opcode');
    expectInvalid(program([{opcode: 'event_whenflagclicked'}, {opcode: 'pen_penDown'}]), 'sprite', 'extension pen is not declared');
    expectInvalid(program([{opcode: 'event_whenflagclicked'}, {opcode: 'procedures_call'}]), 'sprite', 'must reference a declared procedure');
  });

  it('requires a hat first and rejects hats inside stacks or substacks', () => {
    expectInvalid(program([{opcode: 'looks_show'}]), 'sprite', 'script must start with an event hat block');
    expectInvalid(program([{opcode: 'event_whenflagclicked'}, {opcode: 'event_whenkeypressed', fields: {KEY_OPTION: 'space'}}]), 'sprite', 'event hat block cannot appear here');
    expectInvalid(program([{opcode: 'event_whenflagclicked'}, {
      opcode: 'control_repeat', inputs: {TIMES: num(1)},
      substacks: {SUBSTACK: [{opcode: 'control_start_as_clone'}]}
    }]), 'sprite', 'event hat block cannot appear here');
    expect(validateProgramIR(program([{opcode: 'control_start_as_clone'}, {opcode: 'looks_show'}])).valid).toBe(true);
  });

  it('requires caps to end their main stack and every substack', () => {
    const stop: BlockIR = {opcode: 'control_stop', fields: {STOP_OPTION: 'all'}};
    expectInvalid(program([{opcode: 'event_whenflagclicked'}, stop, {opcode: 'looks_show'}]), 'sprite', 'cap block must be the last block');
    expectInvalid(program([{opcode: 'event_whenflagclicked'}, {
      opcode: 'control_repeat', inputs: {TIMES: num(1)},
      substacks: {SUBSTACK: [stop, {opcode: 'looks_show'}]}
    }]), 'sprite', 'cap block must be the last block');
    expect(validateProgramIR(program([{opcode: 'event_whenflagclicked'}, stop])).valid).toBe(true);
  });

  it('rejects unknown, missing, and incorrectly typed inputs', () => {
    expectInvalid(program([{opcode: 'motion_movesteps', inputs: {WRONG: num(10)}}]), 'sprite', 'unknown input');
    expectInvalid(program([{opcode: 'motion_movesteps', inputs: {STEPS: text('ten')}}]), 'sprite', 'expected number');
    expectInvalid(program([{opcode: 'control_if', inputs: {CONDITION: num(1)}, substacks: {SUBSTACK: []}}]), 'sprite', 'requires a nested boolean block');
  });

  it('rejects cyclic IR object graphs', () => {
    const cycle: Record<string, unknown> = {kind: 'block'};
    const block: Record<string, unknown> = {opcode: 'operator_not', inputs: {OPERAND: cycle}};
    cycle.block = block;
    expectInvalid(program([{opcode: 'control_if', inputs: {CONDITION: cycle as never}, substacks: {SUBSTACK: []}}]), 'sprite', 'cyclic');
  });

  it('rejects block depth beyond 12', () => {
    let condition: BlockIR = {opcode: 'operator_equals', inputs: {OPERAND1: text('a'), OPERAND2: text('a')}};
    for (let index = 0; index < 12; index++) condition = {opcode: 'operator_not', inputs: {OPERAND: nested(condition)}};
    expectInvalid(program([{opcode: 'control_if', inputs: {CONDITION: nested(condition)}, substacks: {SUBSTACK: []}}]), 'sprite', 'maximum block depth');
  });

  it('rejects script and visible-block limits', () => {
    const tooManyScripts: ProgramIR = {...program([{opcode: 'event_whenflagclicked'}]), scripts: Array.from({length: 13}, (_, index) => ({id: `script_s${index}`, purpose: 'p', x: 0, y: 0, blocks: [{opcode: 'event_whenflagclicked'}]}))};
    expectInvalid(tooManyScripts, 'sprite', 'maximum is 12 scripts');
    const tooManyBlocks = program(Array.from({length: 121}, () => ({opcode: 'looks_show'})));
    expectInvalid(tooManyBlocks, 'sprite', 'maximum is 120 visible blocks');
  });
});

describe('compiled graph validation', () => {
  it('accepts complete output and rejects broken references and cycles', () => {
    const output = compileProgramIR(program([{opcode: 'event_whenflagclicked'}, {opcode: 'looks_show'}]));
    expect(() => validateCompiledGraph(output)).not.toThrow();
    const broken = structuredClone(output);
    broken.blocks[broken.topLevelIds[0]].next = 'missing';
    expect(() => validateCompiledGraph(broken)).toThrow(/missing next reference/);
    const cyclic = structuredClone(output);
    const first = cyclic.blocks[cyclic.topLevelIds[0]];
    const second = cyclic.blocks[first.next!];
    second.next = first.id;
    first.parent = second.id;
    expect(() => validateCompiledGraph(cyclic)).toThrow(/cycle/);
  });

  it('produces unique IDs and reciprocal parent/next/input references', () => {
    const output = compileProgramIR(program([{opcode: 'event_whenflagclicked'}, {opcode: 'motion_movesteps', inputs: {STEPS: num(10)}}]));
    expect(new Set(output.blockOrder).size).toBe(output.blockOrder.length);
    for (const block of Object.values(output.blocks)) {
      if (block.next) expect(output.blocks[block.next].parent).toBe(block.id);
      for (const input of Object.values(block.inputs)) {
        expect(output.blocks[input.block].parent).toBe(block.id);
        if (input.shadow) expect(output.blocks[input.shadow].parent).toBe(block.id);
      }
    }
  });
});
