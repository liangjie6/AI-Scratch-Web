import {beforeEach, describe, expect, it, vi} from 'vitest';
import {clearScratchLibraryCache, scratchAssetUrl, scoreScratchLibraryItem, searchScratchLibrary} from '../src/ai/scratch-library';

describe('Scratch library matcher', () => {
  beforeEach(() => {clearScratchLibraryCache(); vi.restoreAllMocks();});

  it('scores exact names before tag matches', () => {
    expect(scoreScratchLibraryItem({name: 'Cat', tags: ['animal']}, ['cat'])).toBeGreaterThan(scoreScratchLibraryItem({name: 'Animal', tags: ['cat']}, ['cat']));
  });

  it('loads and ranks local sprite manifest candidates', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify([{name: '猫', tags: ['animal']}, {name: 'Ball', tags: ['thing']}]), {status: 200})));
    const result = await searchScratchLibrary('sprite', ['猫']);
    expect(result.map(item => item.name)).toEqual(['猫']);
  });

  it('rejects URLs with untrusted asset identifiers', () => {
    expect(() => scratchAssetUrl('https://example.com/image.png')).toThrow();
    expect(scratchAssetUrl('a'.repeat(32) + '.svg')).toContain('/assets/scratch/library-assets/');
  });
});
