import {execFile} from 'node:child_process';
import {mkdtemp, rm, writeFile} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';
import {describe, expect, it} from 'vitest';
import type {BlockIR, ProgramIR} from '../src/ai-blocks/program-ir';

const exec = promisify(execFile);
const cli = fileURLToPath(new URL('../scripts/verify-ai-program.mjs', import.meta.url));
const n = (value: number) => ({kind: 'number' as const, value});
const t = (value: string) => ({kind: 'text' as const, value});
const state = (ref_id: string, state_kind: 'variable' | 'broadcast' = 'variable') => ({kind: 'state_ref' as const, state_kind, ref_id});
const script = (id: string, blocks: BlockIR[]) => ({id, purpose: id, x: 0, y: 0, blocks});
const program = (scripts: ProgramIR['scripts'], rest: Partial<ProgramIR> = {}): ProgramIR => ({schema_version: 1, summary: 'independent VM regression fixture', remove_script_ids: [], variables: [], lists: [], broadcasts: [], scripts, ...rest});
const flag = (): BlockIR => ({opcode: 'event_whenflagclicked'});
const move = (steps: number): BlockIR => ({opcode: 'motion_movesteps', inputs: {STEPS: n(steps)}});

async function verify(input: unknown) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'ai-real-vm-test-'));
  try {
    const inputPath = path.join(directory, 'case.json');
    await writeFile(inputPath, JSON.stringify(input));
    const {stdout} = await exec(process.execPath, [cli, '--input', inputPath], {timeout: 25000, maxBuffer: 1024 * 1024});
    return JSON.parse(stdout);
  } finally {await rm(directory, {recursive: true, force: true});}
}

describe('generated ProgramIR in the real Scratch VM', () => {
  it('executes numeric variable loops and preserves their graph through actual SB3 export/load', async () => {
    const result = await verify({
      program: program([script('script_loop', [flag(),
        {opcode: 'data_setvariableto', fields: {VARIABLE: 'score'}, inputs: {VALUE: t('0')}},
        {opcode: 'control_repeat', inputs: {TIMES: n(3)}, substacks: {SUBSTACK: [move(10), {opcode: 'data_changevariableby', fields: {VARIABLE: 'score'}, inputs: {VALUE: n(2)}}]}},
      ])], {variables: [{name: 'score', value: 0}]}),
      roundTrip: true,
      scenarios: [{name: 'three moves and score increments', maxTicks: 15, expect: {x: 30, y: 0, variables: {score: 6}}}],
    });
    expect(result.status).toBe('PASS');
    expect(result.roundTrip.status).toBe('PASS');
  }, 30000);

  it('uses real keyboard IO for both event hats and sustained-key reporters', async () => {
    const result = await verify({
      program: program([script('script_key', [
        {opcode: 'event_whenkeypressed', fields: {KEY_OPTION: 'right arrow'}},
        {opcode: 'control_if', inputs: {CONDITION: {kind: 'block', block: {opcode: 'sensing_keypressed', inputs: {KEY_OPTION: t('right arrow')}}}}, substacks: {SUBSTACK: [move(7)]}},
      ])]),
      scenarios: [{name: 'keydown then keyup', maxTicks: 8, events: [{tick: 0, kind: 'key', key: 'ArrowRight'}, {tick: 3, kind: 'key', key: 'ArrowRight', isDown: false}], expect: {x: 7, keys: {'right arrow': false}}}],
    });
    expect(result.status).toBe('PASS');
  }, 30000);

  it('delivers a stage broadcast to another sprite using shared logical state bindings', async () => {
    const result = await verify({
      programs: [
        {name: 'Stage', targetKind: 'stage', program: program([script('script_sender', [flag(), {opcode: 'data_setvariableto', fields: {VARIABLE: state('score')}, inputs: {VALUE: t('0')}}, {opcode: 'event_broadcast', inputs: {BROADCAST_INPUT: t('go')}}])], {schema_version: 2})},
        {name: 'Receiver', program: program([script('script_receiver', [{opcode: 'event_whenbroadcastreceived', fields: {BROADCAST_OPTION: state('msg_go', 'broadcast')}}, move(12), {opcode: 'data_changevariableby', fields: {VARIABLE: state('score')}, inputs: {VALUE: n(1)}}])], {schema_version: 2})},
      ],
      fixture: {states: [{ref_id: 'score', kind: 'variable', name: 'score', scope: 'global', value: 99}, {ref_id: 'msg_go', kind: 'broadcast', name: 'go', scope: 'global'}]},
      roundTrip: true,
      scenarios: [{name: 'stage-to-sprite broadcast', maxTicks: 12, expect: {targets: {Receiver: {x: 12, variables: {score: 1}}}}}],
    });
    expect(result.status).toBe('PASS');
    expect(result.roundTrip.status).toBe('PASS');
  }, 30000);

  for (const mode of ['modify', 'rewrite']) {
    it(`applies ${mode} with the production graph writer and preserves protected/unrelated blocks`, async () => {
      const existing = program([script('script_old', [flag(), move(10)]), script('script_spare', [flag(), {opcode: 'motion_changeyby', inputs: {DY: n(3)}}])]);
      const generated = program([script('script_new', [flag(), move(20)])], {remove_script_ids: mode === 'modify' ? ['script_old'] : []});
      const result = await verify({
        program: generated, mode,
        fixture: {program: existing, protectedScripts: [{id: 'protected', opcode: 'unsupported_fixture_hat'}], variables: [{name: 'untouched', value: 7}],
          states: [{ref_id: 'existing_untouched', name: 'untouched', kind: 'variable', scope: 'local', value: 999}],
          expectedPreservedScriptIds: mode === 'modify' ? ['protected', 'script_spare'] : ['protected'],
          expectedRemovedScriptIds: mode === 'modify' ? ['script_old'] : ['script_old', 'script_spare']},
        scenarios: [{name: mode + ' preservation', maxTicks: 10, expect: {x: 20, y: mode === 'modify' ? 3 : 0, variables: {untouched: 7}}}],
      });
      expect(result.status).toBe('PASS');
      expect(result.scenarios[0].preservation.removedScripts).toBe(mode === 'modify' ? 1 : 2);
      expect(result.scenarios[0].preservation.preservedBlocks).toBeGreaterThanOrEqual(1);
    }, 30000);
  }

  it('rejects replacing explicitly preserved code even when its behavior is recreated', async () => {
    const spare = script('script_spare', [flag(), {opcode: 'motion_changeyby', inputs: {DY: n(3)}}]);
    await expect(verify({
      mode: 'modify',
      program: program([script('script_recreated', spare.blocks)], {remove_script_ids: ['script_spare']}),
      fixture: {program: program([spare]), expectedPreservedScriptIds: ['script_spare']},
      scenarios: [{maxTicks: 3, expect: {y: 3}}],
    })).rejects.toMatchObject({stdout: expect.stringContaining('explicitly preserved script removed: script_spare')});
  }, 30000);

  it('reports collision as SKIPPED without a renderer', async () => {
    const result = await verify({program: program([script('script_move', [flag(), move(3)])]), scenarios: [{maxTicks: 3, expect: {x: 3, collision: true}}]});
    expect(result.status).toBe('SKIPPED');
    expect(result.scenarios[0].assertions.find((value: {name: string}) => value.name === 'collision').status).toBe('SKIPPED');
  }, 30000);
});
