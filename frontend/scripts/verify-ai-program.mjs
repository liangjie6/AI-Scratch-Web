#!/usr/bin/env node
/** Real Scratch execution checker. Runs model output in a disposable worker. */
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {isMainThread, parentPort, Worker, workerData} from 'node:worker_threads';

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(path.join(frontendRoot, 'package.json'));

async function executeCase(input, helpers) {
  const {compileProgramIR, createScratchProgramPort, isAiEditableScript, preflightProgram} = helpers;
  const vmPath = path.join(frontendRoot, 'node_modules/@scratch/scratch-vm');
  const VirtualMachine = require(path.join(vmPath, 'src/virtual-machine.js'));
  const Blocks = require(path.join(vmPath, 'src/engine/blocks.js'));
  const Sprite = require(path.join(vmPath, 'src/sprites/sprite.js'));
  const StageLayering = require(path.join(vmPath, 'src/engine/stage-layering.js'));
  const {ScratchStorage} = require('@scratch/scratch-storage');
  // Keep stdout machine-readable. Errors from the worker are returned below.
  require(path.join(vmPath, 'src/util/log.js')).settings.minLevel = 7;
  if (Array.isArray(input.programs)) {
    assert(input.programs.length > 0 && input.programs.length <= 12, 'programs must contain 1..12 targets');
    input = {...input, ...input.programs[0], fixture: {...input.fixture, peers: [...(input.fixture?.peers ?? []), ...input.programs.slice(1)]}};
  }
  assert(input.program, 'input.program must be the generated ProgramIR');
  assert(Array.isArray(input.scenarios) && input.scenarios.length > 0, 'at least one independently specified scenario is required');
  assert(input.scenarios.length <= 30, 'at most 30 scenarios');
  assert(['append', 'modify', 'rewrite'].includes(input.mode ?? 'append'), 'invalid mode');
  assert(['stage', 'sprite'].includes(input.targetKind ?? 'sprite'), 'invalid target kind');
  const results = [];
  let roundTrip;
  const rendererOpcodes = new Set(['sensing_touchingobject', 'sensing_touchingcolor', 'sensing_coloristouchingcolor', 'motion_ifonedgebounce']);
  for (const scenario of input.scenarios) {
    const vm = new VirtualMachine();
    const runtime = vm.runtime;
    const storage = new ScratchStorage();
    vm.attachStorage(storage);
    let primitiveCalls = 0;
    let primitiveBudgetExceeded = false;
    try {
      const stage = new Sprite(new Blocks(runtime), runtime).createClone(StageLayering.BACKGROUND_LAYER);
      stage.isStage = true;
      stage.sprite.name = 'Stage';
      runtime.addTarget(stage);
      const sprite = new Sprite(new Blocks(runtime), runtime);
      sprite.name = input.targetKind === 'stage' ? 'AI verification target' : input.name ?? 'AI verification target';
      const actor = sprite.createClone(StageLayering.SPRITE_LAYER);
      runtime.addTarget(actor);
      const target = input.targetKind === 'stage' ? stage : actor;
      const targets = new Map([['Stage', stage], [sprite.name, actor]]);
      if (input.targetKind === 'stage' && input.name) targets.set(input.name, stage);
      const peers = [];
      let stageProgramUsed = input.targetKind === 'stage';
      for (const peer of input.fixture?.peers ?? []) {
        assert(typeof peer.name === 'string' && (!targets.has(peer.name) || (peer.targetKind === 'stage' && targets.get(peer.name) === stage)), 'peer name must be unique');
        let peerTarget = stage;
        if (peer.targetKind !== 'stage') {
          const peerSprite = new Sprite(new Blocks(runtime), runtime);
          peerSprite.name = peer.name;
          peerTarget = peerSprite.createClone(StageLayering.SPRITE_LAYER);
          runtime.addTarget(peerTarget);
          peerTarget.setXY(peer.x ?? 0, peer.y ?? 0);
          peerTarget.setDirection(peer.direction ?? 90);
        } else {assert(!stageProgramUsed, 'only one stage program'); stageProgramUsed = true;}
        targets.set(peer.name, peerTarget);
        peers.push({...peer, target: peerTarget});
      }
      vm.editingTarget = target;
      const fixture = input.fixture ?? {};
      // A genuine tiny SVG asset keeps every exported target a valid SB3,
      // without pretending that a renderer exists or claiming visual tests.
      const {sanitizeSvg} = require('@scratch/scratch-svg-renderer');
      const assetBytes = await sanitizeSvg.sanitizeByteStream(new TextEncoder().encode('<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><path d="M0 0h2v2H0z"/></svg>'));
      const asset = storage.createAsset(storage.AssetType.ImageVector, storage.DataFormat.SVG, assetBytes, null, true);
      for (const item of runtime.targets) {
        item.sprite.costumes.push({name: 'fixture', asset, assetId: asset.assetId, dataFormat: 'svg', md5: asset.assetId + '.svg', bitmapResolution: 1, rotationCenterX: 1, rotationCenterY: 1});
        item.currentCostume = 0;
      }
      actor.setXY(fixture.x ?? 0, fixture.y ?? 0);
      actor.setDirection(fixture.direction ?? 90);
      const stateRefs = new Map();
      const declarations = new Map();
      const mapsFor = owner => {
        if (!declarations.has(owner.id)) declarations.set(owner.id, {variable: new Map(), list: new Map(), broadcast: new Map()});
        return declarations.get(owner.id);
      };
      const own = (owner, name, type) => Object.values(owner.variables).filter(value => value.name === name && value.type === type);
      const install = (item, kind = 'variable', activeTarget = target) => {
        assert(typeof item.name === 'string' && item.name.length, 'state name required');
        const type = kind === 'list' ? 'list' : kind === 'broadcast' ? 'broadcast_msg' : '';
        const owner = kind === 'broadcast' || item.scope === 'global' || activeTarget.isStage ? stage : activeTarget;
        const existing = own(owner, item.name, type);
        assert(existing.length <= 1, 'ambiguous state: ' + item.name);
        const id = existing[0]?.id ?? `state_${owner.id}_${kind}_${Object.keys(owner.variables).length}`;
        if (!existing.length) {
          owner.createVariable(id, item.name, type, false);
          owner.variables[id].value = structuredClone(item.value ?? (kind === 'list' ? [] : kind === 'broadcast' ? item.name : 0));
        }
        const resolved = {name: item.name, id};
        mapsFor(activeTarget)[kind].set(item.name, resolved);
        if (item.ref_id) {
          const previous = stateRefs.get(item.ref_id);
          assert(!previous || previous.id === id && previous.kind === kind, 'duplicate logical state binding: ' + item.ref_id);
          stateRefs.set(item.ref_id, {...resolved, kind});
        }
        return resolved;
      };
      // Independent existing-state fixtures take precedence over generated
      // declarations. Installing a logical reference must never reset them.
      for (const item of fixture.variables ?? []) install(item);
      for (const item of fixture.lists ?? []) install(item, 'list');
      for (const name of fixture.broadcasts ?? []) install({name, scope: 'global'}, 'broadcast');
      for (const item of fixture.states ?? []) {
        const owner = item.owner ? targets.get(item.owner) : target;
        assert(owner, 'state owner does not exist: ' + item.owner);
        install(item, item.kind ?? 'variable', owner);
      }
      if (input.manifest) {
        for (const item of input.manifest.shared_state.variables) install({...item, scope: 'global'});
        for (const item of input.manifest.shared_state.lists) install({...item, scope: 'global'}, 'list');
        for (const item of input.manifest.shared_state.broadcasts) {
          assert(typeof item === 'object' && item.ref_id, 'manifest broadcast requires ref_id');
          install({...item, scope: 'global'}, 'broadcast');
        }
        for (const item of input.manifest.local_state ?? []) {
          const owner = item.target.kind === 'current_target' ? target : item.target.kind === 'stage' ? stage : targets.get(item.target.name);
          assert(owner, 'local state target requires a matching fixture target');
          install({...item, scope: 'local'}, item.kind, owner);
        }
      }
      const resolve = (owner, kind, name, type) => {
        const names = mapsFor(owner)[kind];
        if (names.has(name)) return names.get(name);
        const matches = own(owner, name, type);
        const inherited = owner === stage ? [] : own(stage, name, type);
        const values = matches.length ? matches : inherited;
        assert.equal(values.length, 1, 'state must resolve uniquely: ' + name);
        return {name: values[0].name, id: values[0].id};
      };
      const resolverFor = owner => ({
        variable: name => resolve(owner, 'variable', name, ''),
        list: name => resolve(owner, 'list', name, 'list'),
        broadcast: name => resolve(owner, 'broadcast', name, 'broadcast_msg'),
        state: (kind, ref) => {
          const binding = stateRefs.get(ref);
          assert(binding && binding.kind === kind, 'unbound logical state: ' + ref);
          assert(owner.lookupVariableById(binding.id), 'logical state is outside the target scope: ' + ref);
          return binding;
        }
      });
      const declare = (program, owner = target) => {
        for (const item of program.variables ?? []) install(item, 'variable', owner);
        for (const item of program.lists ?? []) install(item, 'list', owner);
        for (const name of program.broadcasts ?? []) install({name, scope: 'global'}, 'broadcast', owner);
      };
      const port = createScratchProgramPort({projectRevision: 1, refreshWorkspace() {}});
      const compile = (program, prefix, owner = target) => {
        if (input.manifest && prefix !== 'fixture') {
          const result = preflightProgram(program, owner.isStage ? 'stage' : 'sprite', new Set(), input.manifest);
          assert(result.valid, JSON.stringify(result.issues));
        }
        return compileProgramIR(program, {
        target: owner.isStage ? 'stage' : 'sprite', resolver: resolverFor(owner),
        // Extension execution needs a separately provisioned real extension
        // environment; this checker intentionally does not fake its primitives.
        enabledExtensions: new Set(),
        idGenerator: (kind, ordinal, scriptId) => `${prefix}_${scriptId}_${kind}_${ordinal}`,
        });
      };
      const scriptRefs = new Map();
      if (fixture.program) {
        declare(fixture.program);
        const existing = compile(fixture.program, 'fixture');
        port.apply({target, compiled: existing, removeTopIds: [], pending: [], targetName: target.getName()});
        for (const script of existing.scripts) scriptRefs.set(script.scriptId, script.topLevelId);
      }
      for (const script of fixture.protectedScripts ?? []) {
        assert(typeof script.id === 'string' && typeof script.opcode === 'string');
        target.blocks.createBlock({id: script.id, opcode: script.opcode, parent: null, next: null, inputs: {}, fields: {}, shadow: false, topLevel: true});
        assert(!isAiEditableScript(target.blocks, script.id), 'protected fixture must contain an unsupported opcode');
        scriptRefs.set(script.id, script.id);
      }
      const before = structuredClone(target.blocks._blocks);
      const beforeTopIds = target.blocks.getScripts();
      const mode = input.mode ?? 'append';
      const removals = input.program.remove_script_ids ?? [];
      assert(mode === 'modify' || removals.length === 0, 'only modify can supply remove_script_ids');
      const removeTopIds = mode === 'rewrite' ? beforeTopIds.filter(id => isAiEditableScript(target.blocks, id))
        : mode === 'modify' ? removals.map(ref => {
          const id = scriptRefs.get(ref);
          assert(id && isAiEditableScript(target.blocks, id), 'unknown or protected edit reference: ' + ref);
          return id;
        }) : [];
      // These expectations are provided independently of generated removal
      // requests. Recreating equivalent code does not count as preservation.
      for (const ref of fixture.expectedPreservedScriptIds ?? []) {
        const id = scriptRefs.get(ref);
        assert(id, 'unknown expected preserved script: ' + ref);
        assert(!removeTopIds.includes(id), 'explicitly preserved script removed: ' + ref);
      }
      if (fixture.expectedRemovedScriptIds !== undefined) {
        assert(Array.isArray(fixture.expectedRemovedScriptIds), 'expectedRemovedScriptIds must be an array');
        const expected = fixture.expectedRemovedScriptIds.map(ref => {
          const id = scriptRefs.get(ref);
          assert(id, 'unknown expected removed script: ' + ref);
          return id;
        });
        assert.deepEqual([...removeTopIds].sort(), expected.sort(), 'script removal set differs from independently specified authorization');
      }
      declare(input.program);
      const compiled = compile(input.program, 'generated');
      port.apply({target, compiled, removeTopIds, pending: [], targetName: target.getName()});
      for (const [index, peer] of peers.entries()) {
        assert(peer.program, 'peer.program required');
        assert(!(peer.program.remove_script_ids ?? []).length, 'peer programs currently support append only');
        declare(peer.program, peer.target);
        const graph = compile(peer.program, 'peer_' + index, peer.target);
        port.apply({target: peer.target, compiled: graph, removeTopIds: [], pending: [], targetName: peer.name});
      }
      const removed = new Set();
      const collect = id => {
        if (!id || removed.has(id)) return;
        removed.add(id);
        const block = before[id];
        if (!block) return;
        collect(block.next);
        for (const value of Object.values(block.inputs ?? {})) {collect(value.block); collect(value.shadow);}
      };
      removeTopIds.forEach(collect);
      // Check exact preservation using the production graph writer, including
      // all protected blocks, inputs, shadows and unrelated script positions.
      for (const [id, block] of Object.entries(before)) {
        if (removed.has(id)) assert(!target.blocks.getBlock(id), 'removed script retained block ' + id);
        else assert.deepEqual(target.blocks.getBlock(id), block, 'unrelated/protected block changed: ' + id);
      }
      const preservation = {status: 'PASS', mode, removedScripts: removeTopIds.length, preservedBlocks: Object.keys(before).length - removed.size};
      for (const block of Object.values(compiled.blocks)) {
        if (!block.shadow) assert(runtime._primitives[block.opcode] || runtime._hats[block.opcode] || block.opcode === 'procedures_definition', 'real VM has no implementation for ' + block.opcode);
      }
      if (input.roundTrip && !roundTrip) {
        if ((fixture.protectedScripts ?? []).length) roundTrip = {status: 'SKIPPED', reason: 'protected extension fixtures have no installed extension; no roundtrip claim'};
        else {
          const second = new VirtualMachine();
          second.attachStorage(new ScratchStorage());
          const third = new VirtualMachine();
          third.attachStorage(new ScratchStorage());
          try {
            const original = JSON.parse(vm.toJSON());
            const blob = await vm.saveProjectSb3();
            const bytes = new Uint8Array(await blob.arrayBuffer());
            await second.loadProject(bytes);
            const reexported = new Uint8Array(await (await second.saveProjectSb3()).arrayBuffer());
            await third.loadProject(reexported);
            const restored = JSON.parse(second.toJSON());
            assert.equal(restored.targets.length, original.targets.length);
            const canonicalGraph = owner => {
              const walk = (id, seen = new Set()) => {
                if (!id) return null;
                assert(!seen.has(id), 'cycle in VM graph');
                const block = owner.blocks.getBlock(id);
                assert(block, 'missing VM graph reference');
                const nextSeen = new Set([...seen, id]);
                return {
                  opcode: block.opcode, shadow: block.shadow,
                  fields: Object.fromEntries(Object.entries(block.fields ?? {}).map(([name, field]) => [name, {value: field.value, ...(field.id ? {id: field.id} : {})}])),
                  inputs: Object.fromEntries(Object.entries(block.inputs ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([name, value]) => [name, {block: walk(value.block, nextSeen), shadow: walk(value.shadow, nextSeen)}])),
                  next: walk(block.next, nextSeen),
                  ...(block.mutation ? {mutation: block.mutation} : {}),
                };
              };
              return owner.blocks.getScripts().map(id => walk(id)).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
            };
            for (let i = 0; i < original.targets.length; i++) {
              for (const key of ['variables', 'lists', 'broadcasts']) assert.deepEqual(restored.targets[i][key], original.targets[i][key], 'roundtrip lost ' + key);
              assert.deepEqual(canonicalGraph(second.runtime.targets[i]), canonicalGraph(runtime.targets[i]), 'roundtrip changed normalized graph');
              assert.deepEqual(canonicalGraph(third.runtime.targets[i]), canonicalGraph(second.runtime.targets[i]), 'second load changed normalized graph');
              assert.deepEqual(restored.targets[i].costumes, original.targets[i].costumes, 'roundtrip changed costume references');
            }
            roundTrip = {status: 'PASS', bytes: bytes.byteLength, scope: 'real Node source VM candidate load/export/new-VM load; normalized graph, state and costume reference comparison'};
          } catch (error) { roundTrip = {status: 'FAIL', error: String(error?.message ?? error)}; }
          finally {second.runtime.dispose(); third.runtime.dispose();}
        }
      }
      const maxPrimitiveCalls = input.maxPrimitiveCalls ?? 1000000;
      assert(Number.isInteger(maxPrimitiveCalls) && maxPrimitiveCalls > 0 && maxPrimitiveCalls <= 10000000, 'maxPrimitiveCalls must be 1..10000000');
      for (const [opcode, primitive] of Object.entries(runtime._primitives)) {
        runtime._primitives[opcode] = (...args) => {
          primitiveCalls += 1;
          if (primitiveCalls > maxPrimitiveCalls) {primitiveBudgetExceeded = true; throw new Error('primitive budget exceeded');}
          return primitive(...args);
        };
      }
      const keyName = key => ({space: ' ', 'up arrow': 'ArrowUp', 'down arrow': 'ArrowDown', 'left arrow': 'ArrowLeft', 'right arrow': 'ArrowRight', enter: 'Enter'}[key] ?? key);
      const trigger = event => {
        if (event.kind === 'green_flag') vm.greenFlag();
        else if (event.kind === 'key') runtime.ioDevices.keyboard.postData({key: keyName(event.key ?? event.value), isDown: event.isDown !== false});
        else if (event.kind === 'broadcast') runtime.startHats('event_whenbroadcastreceived', {BROADCAST_OPTION: event.name ?? event.value});
        else if (event.kind === 'sprite_click') runtime.startHats('event_whenthisspriteclicked', null, actor);
        else throw new Error('unsupported scenario trigger: ' + event.kind);
      };
      const maxTicks = scenario.maxTicks ?? 60;
      assert(Number.isInteger(maxTicks) && maxTicks > 0 && maxTicks <= 2000, 'maxTicks must be 1..2000');
      const delay = scenario.stepDelayMs ?? 0;
      assert(Number.isInteger(delay) && delay >= 0 && delay <= 100, 'stepDelayMs must be 0..100');
      const events = scenario.events ?? [{tick: 0, ...(scenario.trigger ?? {kind: 'green_flag'})}];
      runtime.currentStepTime = 1000 / 60;
      for (let tick = 0; tick < maxTicks; tick++) {
        for (const event of events.filter(item => item.tick === tick)) trigger(event);
        runtime._step();
        if (primitiveBudgetExceeded) break;
        if (delay) await new Promise(resolve => setTimeout(resolve, delay));
      }
      assert(!primitiveBudgetExceeded, 'primitive execution budget exceeded');
      const expected = scenario.expect ?? {};
      const assertions = [];
      for (const axis of ['x', 'y', 'direction']) {
        if (expected[axis] !== undefined) {
          const value = actor[axis];
          const requirement = expected[axis];
          const match = typeof requirement === 'number' ? Math.abs(value - requirement) <= 1e-7 : value >= requirement.min && value <= requirement.max;
          assertions.push({name: axis, status: match ? 'PASS' : 'FAIL', actual: value, expected: requirement});
        }
      }
      for (const [name, value] of Object.entries(expected.variables ?? {})) {
        const binding = resolve(target, 'variable', name, '');
        const actual = target.lookupVariableById(binding.id)?.value;
        const equal = typeof value === 'number' ? Number(actual) === value : actual === value;
        assertions.push({name: 'variable:' + name, status: equal ? 'PASS' : 'FAIL', actual, expected: value});
      }
      for (const [name, value] of Object.entries(expected.lists ?? {})) {
        const binding = resolve(target, 'list', name, 'list');
        const actual = target.lookupVariableById(binding.id)?.value;
        assertions.push({name: 'list:' + name, status: JSON.stringify(actual) === JSON.stringify(value) ? 'PASS' : 'FAIL', actual, expected: value});
      }
      if (expected.keyDown !== undefined) {
        const actual = runtime.ioDevices.keyboard.getKeyIsDown(expected.keyDown);
        assertions.push({name: 'keyboard_state', status: actual ? 'PASS' : 'FAIL', actual, expected: true});
      }
      for (const [key, down] of Object.entries(expected.keys ?? {})) {
        const actual = runtime.ioDevices.keyboard.getKeyIsDown(key);
        assertions.push({name: 'key:' + key, status: actual === down ? 'PASS' : 'FAIL', actual, expected: down});
      }
      for (const [name, values] of Object.entries(expected.targets ?? {})) {
        const peer = targets.get(name);
        assert(peer, 'expected target missing: ' + name);
        for (const axis of ['x', 'y', 'direction']) {
          if (values[axis] !== undefined) {
            const value = peer[axis];
            const requirement = values[axis];
            const match = typeof requirement === 'number' ? Math.abs(value - requirement) <= 1e-7 : value >= requirement.min && value <= requirement.max;
            assertions.push({name: name + ':' + axis, status: match ? 'PASS' : 'FAIL', actual: value, expected: requirement});
          }
        }
        for (const [variableName, value] of Object.entries(values.variables ?? {})) {
          const binding = resolve(peer, 'variable', variableName, '');
          const actual = peer.lookupVariableById(binding.id)?.value;
          const equal = typeof value === 'number' ? Number(actual) === value : actual === value;
          assertions.push({name: name + ':variable:' + variableName, status: equal ? 'PASS' : 'FAIL', actual, expected: value});
        }
      }
      if (expected.collision !== undefined) assertions.push({name: 'collision', status: 'SKIPPED', reason: 'No renderer attached; no collision claim'});
      const unsupportedRendering = [...new Set(runtime.targets.flatMap(owner => Object.values(owner.blocks._blocks).map(block => block.opcode).filter(opcode => rendererOpcodes.has(opcode))))];
      if (unsupportedRendering.length) assertions.push({name: 'renderer_dependent_program', status: 'SKIPPED', reason: 'No renderer attached; results do not validate these opcodes', opcodes: unsupportedRendering});
      assert(assertions.length > 0, 'scenario must specify an independent expected result');
      const status = assertions.some(item => item.status === 'FAIL') ? 'FAIL' : assertions.some(item => item.status === 'SKIPPED') ? 'SKIPPED' : 'PASS';
      results.push({name: scenario.name ?? 'scenario', status, ticks: maxTicks, primitiveCalls, assertions, preservation});
    } catch (error) {results.push({name: scenario.name ?? 'scenario', status: primitiveBudgetExceeded ? 'TIMEOUT' : 'FAIL', error: String(error?.message ?? error), primitiveCalls});}
    finally {runtime.dispose();}
  }
  return {
    status: results.some(item => ['FAIL', 'TIMEOUT'].includes(item.status)) || roundTrip?.status === 'FAIL' ? 'FAIL' : results.some(item => item.status === 'SKIPPED') || input.roundTrip && roundTrip?.status === 'SKIPPED' ? 'SKIPPED' : 'PASS',
    vmVersion: require(path.join(vmPath, 'package.json')).version,
    scope: 'real Scratch source VM, production compiler and graph writer; isolated fixture, no renderer or production transaction',
    scenarios: results,
    roundTrip: roundTrip ?? {status: 'SKIPPED', reason: 'not requested'},
  };
}

if (!isMainThread) {
  try {
    const helpers = await import(workerData.bundleUrl);
    parentPort.postMessage(await executeCase(workerData.input, helpers));
  } catch (error) {parentPort.postMessage({status: 'FAIL', error: String(error?.message ?? error)});}
} else {
  const args = process.argv.slice(2);
  const option = flag => {const index = args.indexOf(flag); return index >= 0 ? args[index + 1] : undefined;};
  const inputPath = option('--input');
  if (!inputPath) {
    process.stderr.write('Usage: node frontend/scripts/verify-ai-program.mjs --input case.json [--output result.json]\n');
    process.exitCode = 2;
  } else {
    const temporaryDir = await fs.mkdtemp(path.join(os.tmpdir(), 'scratch-ai-verify-'));
    try {
      const contents = await fs.readFile(inputPath, 'utf8');
      assert(Buffer.byteLength(contents) <= 2 * 1024 * 1024, 'input is over 2 MiB');
      const input = JSON.parse(contents);
      const bundlePath = path.join(temporaryDir, 'helpers.mjs');
      await require('esbuild').build({
        stdin: {contents: [
          'export {compileProgramIR} from "./src/ai-blocks/program-compiler.ts";',
          'export {createScratchProgramPort} from "./src/scratch/scratch-program-port.ts";',
          'export {isAiEditableScript} from "./src/ai-blocks/target-context.ts";',
          'export {preflightProgram} from "./src/ai-blocks/program-preflight.ts";',
        ].join('\n'), resolveDir: frontendRoot, loader: 'ts'},
        bundle: true, platform: 'node', format: 'esm', outfile: bundlePath, logLevel: 'silent',
        // The isolated checker permits core Scratch only, independent of the
        // application's Vite deployment flags. It never fakes an extension.
        define: {'import.meta.env': '{}'},
      });
      const timeoutMs = input.timeoutMs ?? 15000;
      assert(Number.isInteger(timeoutMs) && timeoutMs > 0 && timeoutMs <= 120000, 'timeoutMs must be 1..120000');
      const worker = new Worker(fileURLToPath(import.meta.url), {workerData: {input, bundleUrl: pathToFileURL(bundlePath).href}, resourceLimits: {maxOldGenerationSizeMb: 256}});
      const result = await new Promise(resolve => {
        const deadline = setTimeout(() => {void worker.terminate(); resolve({status: 'TIMEOUT', reason: 'worker wall-clock budget exceeded', timeoutMs});}, timeoutMs);
        worker.once('message', value => {clearTimeout(deadline); void worker.terminate(); resolve(value);});
        worker.once('error', error => {clearTimeout(deadline); resolve({status: 'FAIL', error: error.message});});
        worker.once('exit', code => {if (code !== 0) {clearTimeout(deadline); resolve({status: 'FAIL', error: 'worker exited with code ' + code});}});
      });
      const output = JSON.stringify(result, null, 2) + '\n';
      if (option('--output')) await fs.writeFile(option('--output'), output);
      process.stdout.write(output);
      if (['FAIL', 'TIMEOUT'].includes(result.status)) process.exitCode = 1;
    } catch (error) {process.stdout.write(JSON.stringify({status: 'FAIL', error: String(error?.message ?? error)}) + '\n'); process.exitCode = 1;}
    finally {await fs.rm(temporaryDir, {recursive: true, force: true});}
  }
}
