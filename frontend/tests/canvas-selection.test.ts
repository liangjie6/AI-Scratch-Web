import {beforeAll, describe, expect, it} from 'vitest';
import {BitmapStore} from '../src/canvas/model/bitmap-store';
import {copySelectionToLayer} from '../src/canvas/model/document-store';
import {IDENTITY_MATRIX, type CanvasDocument, type ChromaKeyEffect, type RasterLayer} from '../src/canvas/model/canvas-types';
import {layerImageData} from '../src/canvas/pixels/rasterize';
import {HistoryManager} from '../src/canvas/history/history-manager';

beforeAll(() => {
  if (globalThis.ImageData) return;
  class ImageDataPolyfill {
    data: Uint8ClampedArray; width: number; height: number;
    constructor(dataOrWidth: Uint8ClampedArray | number, widthOrHeight: number, height?: number) {
      if (typeof dataOrWidth === 'number') {
        this.width = dataOrWidth; this.height = widthOrHeight;
        this.data = new Uint8ClampedArray(this.width * this.height * 4);
      } else {
        this.data = dataOrWidth; this.width = widthOrHeight; this.height = height ?? dataOrWidth.length / 4 / widthOrHeight;
      }
    }
  }
  Object.defineProperty(globalThis, 'ImageData', {value: ImageDataPolyfill, configurable: true});
});

function fixture() {
  const bitmaps = new BitmapStore(), image = new ImageData(4, 1);
  image.data.set([0, 255, 0, 255, 0, 255, 0, 255, 255, 0, 0, 255, 0, 255, 0, 255]);
  const effect: ChromaKeyEffect = {
    keyColor: {r: 0, g: 255, b: 0}, tolerance: 20, softness: 0,
    mode: 'connected', seedMode: 'points', connectivity: 4, spillSuppression: 0,
    inverted: false, seedPoints: [{x: 0, y: 0}], enabled: true
  };
  const layer: RasterLayer = {
    id: 'source', name: '原图', bitmapId: bitmaps.put(image, 'source-bitmap'),
    bounds: {x: 0, y: 0, width: 4, height: 1}, transform: {...IDENTITY_MATRIX},
    visible: true, locked: false, opacity: 1, blendMode: 'normal', chromaKey: effect,
    createdAt: 1, updatedAt: 1
  };
  const document: CanvasDocument = {
    schemaVersion: 1, id: 'selection-document', name: 'selection', createdAt: 1, updatedAt: 1,
    artboard: {...layer.bounds}, layers: [layer], costumeEntries: [], activeLayerIds: [layer.id],
    selection: {rect: {x: 0, y: 0, width: 1, height: 1}, mode: 'replace'},
    viewport: {x: 0, y: 0, zoom: 1}, source: {kind: 'upload'}
  };
  return {bitmaps, image, effect, layer, document};
}
const alpha = (image: {data: Uint8ClampedArray}) => Array.from(image.data).filter((_, index) => index % 4 === 3);

describe('selection edits with connected chroma effects', () => {
  it('keeps the visible source transparency when cutting away a connected key seed', () => {
    const {bitmaps, layer, document} = fixture();
    expect(alpha(layerImageData(layer, bitmaps))).toEqual([0, 0, 255, 255]);
    const result = copySelectionToLayer(document, bitmaps, true);
    expect(alpha(bitmaps.get(layer.bitmapId))).toEqual([0, 0, 255, 255]);
    expect(alpha(layerImageData(layer, bitmaps))).toEqual([0, 0, 255, 255]);
    expect(layer.chromaKey).toBeNull();
    expect(layer.updatedAt).toBeGreaterThan(1);
    expect(alpha(bitmaps.get(result.layer.bitmapId))).toEqual([0]);
    expect(document.activeLayerIds).toEqual([layer.id]);
  });

  it('copies the visible selection while preserving the original bitmap and live key effect', () => {
    const {bitmaps, image, effect, layer, document} = fixture();
    const beforeRevision = bitmaps.get(layer.bitmapId).revision;
    const result = copySelectionToLayer(document, bitmaps);
    expect(bitmaps.get(layer.bitmapId).data).toEqual(image.data);
    expect(bitmaps.get(layer.bitmapId).revision).toBe(beforeRevision);
    expect(layer.chromaKey).toBe(effect);
    expect(layer.updatedAt).toBe(1);
    expect(alpha(layerImageData(layer, bitmaps))).toEqual([0, 0, 255, 255]);
    expect(alpha(bitmaps.get(result.layer.bitmapId))).toEqual([0]);
    expect(document.activeLayerIds).toEqual([layer.id]);
  });

  it('rolls back pending bitmap and metadata edits without discarding prior undo history', () => {
    const {bitmaps, image, layer, document} = fixture(), history = new HistoryManager();
    history.begin('rename', document, bitmaps);
    document.name = 'committed'; history.commit(document, bitmaps);
    history.begin('cut selection', document, bitmaps);
    document.name = 'pending'; layer.transform.e = 50;
    document.selection!.rect.x = 50;
    copySelectionToLayer(document, bitmaps, true);
    expect(layer.chromaKey).toBeNull();
    expect(bitmaps.entries()).toHaveLength(2);
    const restored = history.rollback(bitmaps)!;
    expect(restored.name).toBe('committed');
    expect(restored.layers).toHaveLength(1);
    expect(restored.layers[0]!.transform).toEqual(IDENTITY_MATRIX);
    expect(restored.layers[0]!.chromaKey).not.toBeNull();
    expect(restored.activeLayerIds).toEqual([layer.id]);
    expect(bitmaps.entries()).toHaveLength(1);
    expect(bitmaps.get(layer.bitmapId).data).toEqual(image.data);
    expect(bitmaps.get(layer.bitmapId).revision).toBe(1);
    expect(history.undoLabel).toBe('rename');
    expect(history.canUndo).toBe(true); expect(history.canRedo).toBe(false);
    expect(history.undo(bitmaps)!.name).toBe('selection');
    expect(history.redo(bitmaps)!.name).toBe('committed');
    expect(history.rollback(bitmaps)).toBeNull();
  });
});
