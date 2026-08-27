import {describe, expect, it} from 'vitest';
import {BitmapStore} from '../src/canvas/model/bitmap-store';
import {buildScratchCanvasDocument, listScratchImportOptions, type RenderedScratchCostume, type ScratchImportOption} from '../src/canvas/io/scratch-importer';
import type {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';

function image(width: number, height: number) {
  return {width, height, data: new Uint8ClampedArray(width * height * 4)} as ImageData;
}

describe('listScratchImportOptions', () => {
  it('lists each backdrop and each original sprite without clones', () => {
    const targets: ScratchTargetLike[] = [
      {id: 'stage', isStage: true, getCostumes: () => [{name: '蓝天'}, {name: '操场'}]},
      {id: 'cat', isOriginal: true, getName: () => '小猫', getCostumes: () => [{name: '造型1'}, {name: '造型2'}]},
      {id: 'cat-clone', isOriginal: false, getName: () => '小猫', getCostumes: () => [{name: '造型1'}]}
    ];
    const bridge = {isReady: true, currentVm: {runtime: {targets}}} as unknown as ScratchGuiBridge;

    expect(listScratchImportOptions(bridge)).toEqual([
      {id: 'backdrop:stage:0', kind: 'backdrop', targetId: 'stage', costumeIndex: 0, name: '蓝天', detail: '舞台背景'},
      {id: 'backdrop:stage:1', kind: 'backdrop', targetId: 'stage', costumeIndex: 1, name: '操场', detail: '舞台背景'},
      {id: 'sprite:cat', kind: 'sprite', targetId: 'cat', name: '小猫', detail: '2 个造型'}
    ]);
  });
});

describe('buildScratchCanvasDocument', () => {
  it('creates sprite layers and costume entries in Scratch costume order', () => {
    const option: ScratchImportOption = {id: 'sprite:cat', kind: 'sprite', targetId: 'cat', name: '小猫', detail: '2 个造型'};
    const rendered: RenderedScratchCostume[] = [
      {name: '先出现', image: image(20, 30), pivot: {x: 5, y: 10}},
      {name: '后出现', image: image(40, 16), pivot: {x: 20, y: 8}}
    ];

    const document = buildScratchCanvasDocument(option, rendered, new BitmapStore(), {x: 30, y: -20, size: 50, currentCostume: 1});

    expect([...document.layers].reverse().map(layer => layer.name)).toEqual(['先出现', '后出现']);
    expect([...document.layers].reverse().map(layer => layer.bounds)).toEqual([
      {x: 268.75, y: 197.5, width: 5, height: 7.5},
      {x: 265, y: 198, width: 10, height: 4}
    ]);
    expect([...document.layers].reverse().map(layer => layer.visible)).toEqual([false, true]);
    expect(document.costumeEntries.map(entry => ({name: entry.name, order: entry.order, layerId: entry.layerId}))).toEqual([
      {name: '先出现', order: 0, layerId: document.layers[1].id},
      {name: '后出现', order: 1, layerId: document.layers[0].id}
    ]);
    expect(document.costumeEntries.map(entry => entry.pivot)).toEqual([{x: 270, y: 200}, {x: 270, y: 200}]);
    expect(document.costumeEntries.map(entry => entry.exportScale)).toEqual([2, 2]);
    expect(document.activeLayerIds).toEqual([document.layers[0].id]);
    expect(document.artboard).toEqual({x: 0, y: 0, width: 480, height: 360});
  });

  it('keeps a high-resolution SVG source without changing stage geometry', () => {
    const option: ScratchImportOption = {id: 'sprite:vector', kind: 'sprite', targetId: 'vector', name: '矢量角色', detail: '1 个造型'};
    const source = '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="30"/>';
    const store = new BitmapStore();
    const document = buildScratchCanvasDocument(option, [{name: '矢量造型', image: image(80, 120), pivot: {x: 20, y: 40}, pixelRatio: 4, vectorSource: source}], store);

    expect(document.layers[0].bounds).toEqual({x: 235, y: 170, width: 20, height: 30});
    expect(store.get(document.layers[0].bitmapId).vectorSource).toBe(source);
    expect(document.costumeEntries[0].exportScale).toBe(2);
  });

  it('imports a backdrop as a normal layer', () => {
    const option: ScratchImportOption = {id: 'backdrop:stage:0', kind: 'backdrop', targetId: 'stage', costumeIndex: 0, name: '蓝天', detail: '舞台背景'};
    const document = buildScratchCanvasDocument(option, [{name: '蓝天', image: image(480, 360), pivot: {x: 240, y: 180}}], new BitmapStore());

    expect(document.layers[0].bounds).toEqual({x: 0, y: 0, width: 480, height: 360});
    expect(document.costumeEntries).toEqual([]);
    expect(document.source).toMatchObject({kind: 'scratch', targetId: 'stage', targetType: 'backdrop'});
  });

  it('imports canvas content when crypto.randomUUID is unavailable', () => {
    const originalCrypto = Object.getOwnPropertyDescriptor(globalThis, 'crypto');
    let seed = 0;
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: {
        getRandomValues(bytes: Uint8Array) {
          for (let index = 0; index < bytes.length; index += 1) bytes[index] = seed + index;
          seed += 1;
          return bytes;
        }
      }
    });

    try {
      const option: ScratchImportOption = {id: 'sprite:fallback', kind: 'sprite', targetId: 'fallback', name: '兼容角色', detail: '1 个造型'};
      const store = new BitmapStore();
      const document = buildScratchCanvasDocument(option, [{name: '造型', image: image(20, 20), pivot: {x: 10, y: 10}}], store);
      const ids = [document.id, document.layers[0].id, document.layers[0].bitmapId, document.costumeEntries[0].id];

      expect(new Set(ids).size).toBe(ids.length);
      expect(ids.every(id => /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(id))).toBe(true);
    } finally {
      if (originalCrypto) Object.defineProperty(globalThis, 'crypto', originalCrypto);
      else delete (globalThis as {crypto?: Crypto}).crypto;
    }
  });
});
