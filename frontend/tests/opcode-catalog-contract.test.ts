import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {describe, expect, it} from 'vitest';
import {BLOCK_CATALOG} from '../src/ai-blocks/block-catalog';

describe('AI blocks opcode contract', () => {
  it('keeps the backend whitelist identical to the frontend compiler catalog', () => {
    const backendPath = fileURLToPath(new URL('../../backend/app/scratch_block_catalog.py', import.meta.url));
    const source = readFileSync(backendPath, 'utf8');
    const match = source.match(/BLOCK_CONTRACTS:[^=]+ = MappingProxyType\(\{([\s\S]*?)\}\)/);
    expect(match).not.toBeNull();
    const backend = [...match![1].matchAll(/^    '([a-z][a-z0-9_]*)': _block/gm)]
      .map(value => value[1])
      .sort();
    const frontend = Object.keys(BLOCK_CATALOG).sort();
    expect(backend).toEqual(frontend);
  });
});
