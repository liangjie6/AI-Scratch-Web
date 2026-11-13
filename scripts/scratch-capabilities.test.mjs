import {describe, it} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {canonical, staleFiles} from './generate-scratch-capabilities.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

describe('Scratch capability generation', () => {
  it('re-extracts pinned source facts and checks all generated outputs', () => {
    const output = execFileSync(process.execPath, ['scripts/generate-scratch-capabilities.mjs', '--check'], {cwd: root, encoding: 'utf8'});
    assert.match(output, /Scratch capabilities current: 138 opcodes/);
  });

  it('hashes canonical content including source provenance and reviewed policy', () => {
    const {catalog_hash: hash, ...content} = JSON.parse(fs.readFileSync(path.join(root, 'contracts/scratch-capabilities.json'), 'utf8'));
    assert.equal(createHash('sha256').update(JSON.stringify(canonical(content))).digest('hex'), hash);
    assert.ok(content.sources.files['scripts/generate-scratch-capabilities.mjs']);
    assert.ok(content.sources.files['contracts/scratch-policy-overlay.json']);
    assert.ok(content.sources.files['frontend/node_modules/@scratch/scratch-vm/src/serialization/sb3.js']);
  });

  it('rejects missing and stale output without repairing it in check mode', () => {
    const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'scratch-catalog-check-'));
    try {
      assert.deepEqual(staleFiles(temporary, {'expected.json': 'new'}), ['expected.json']);
      fs.writeFileSync(path.join(temporary, 'expected.json'), 'old');
      assert.deepEqual(staleFiles(temporary, {'expected.json': 'new'}), ['expected.json']);
      assert.equal(fs.readFileSync(path.join(temporary, 'expected.json'), 'utf8'), 'old');
      assert.deepEqual(staleFiles(temporary, {'expected.json': 'old'}), []);
    } finally { fs.rmSync(temporary, {recursive: true, force: true}); }
  });
});
