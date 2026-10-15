import {afterEach, beforeAll, describe, expect, it, vi} from 'vitest';
import {BitmapStore} from '../src/canvas/model/bitmap-store';
import {IDENTITY_MATRIX, type RasterLayer} from '../src/canvas/model/canvas-types';
import {fillImageData, fillLayerAt} from '../src/canvas/pixels/paint-fill';
import {bitmapPoint, parsePaintColor, selectionContainsPixel} from '../src/canvas/pixels/paint-geometry';
import {createPaintStroke} from '../src/canvas/pixels/paint-stroke';
import {layerRasterCanvas} from '../src/canvas/pixels/rasterize';

beforeAll(() => {
  if (!globalThis.ImageData) {
    class ImageDataPolyfill {
      data: Uint8ClampedArray; width: number; height: number;
      constructor(dataOrWidth: Uint8ClampedArray | number, widthOrHeight: number, height?: number) {
        this.width = typeof dataOrWidth === 'number' ? dataOrWidth : widthOrHeight;
        this.height = typeof dataOrWidth === 'number' ? widthOrHeight : height ?? dataOrWidth.length / 4 / widthOrHeight;
        this.data = typeof dataOrWidth === 'number' ? new Uint8ClampedArray(this.width * this.height * 4) : dataOrWidth;
      }
    }
    Object.defineProperty(globalThis, 'ImageData', {value: ImageDataPolyfill, configurable: true});
  }
});
afterEach(() => vi.unstubAllGlobals());

function layerFor(store: BitmapStore, image: ImageData): RasterLayer {
  return {id: 'paint-layer', name: '绘制', bitmapId: store.put(image), bounds: {x: 0, y: 0, width: image.width, height: image.height}, transform: {...IDENTITY_MATRIX}, visible: true, locked: false, opacity: 1, blendMode: 'normal', chromaKey: null, createdAt: 1, updatedAt: 1};
}
function pixel(image: ImageData, x: number, y = 0) { return [...image.data.slice((y * image.width + x) * 4, (y * image.width + x + 1) * 4)]; }

describe('canvas paint fill', () => {
  it('decodes numeric and alphabetic hex color channels', () => {
    expect(parsePaintColor('#12aB90')).toEqual([18, 171, 144, 255]);
    expect(parsePaintColor('#09f')).toEqual([0, 153, 255, 255]);
  });

  it('fills only the connected region and respects a solid boundary', () => {
    const image = new ImageData(5, 3);
    for (let y = 0; y < 3; y++) for (let x = 0; x < 5; x++) image.data.set(x === 2 ? [0, 0, 0, 255] : [255, 255, 255, 255], (y * 5 + x) * 4);
    expect(fillImageData(image, {x: .2, y: 1.8}, {mode: 'region', color: [18, 171, 144, 255]})).toBe(true);
    expect(pixel(image, 0, 2)).toEqual([18, 171, 144, 255]);
    expect(pixel(image, 2, 2)).toEqual([0, 0, 0, 255]);
    expect(pixel(image, 4, 2)).toEqual([255, 255, 255, 255]);
  });

  it('fills transparent connected pixels regardless of hidden RGB', () => {
    const image = new ImageData(3, 1);
    image.data.set([10, 90, 210, 0, 210, 0, 70, 0, 1, 2, 3, 255]);
    fillImageData(image, {x: 0, y: 0}, {mode: 'region', color: [4, 5, 6, 255], tolerance: 0});
    expect(pixel(image, 0)).toEqual([4, 5, 6, 255]);
    expect(pixel(image, 1)).toEqual([4, 5, 6, 255]);
    expect(pixel(image, 2)).toEqual([1, 2, 3, 255]);
  });

  it('fills the complete layer or selection independently of the clicked color and position', () => {
    const image = new ImageData(4, 2);
    image.data.set([255, 0, 0, 255]);
    fillImageData(image, {x: -40, y: -20}, {mode: 'all', color: [4, 5, 6, 255], contains: x => x >= 2});
    expect(pixel(image, 0)).toEqual([255, 0, 0, 255]);
    expect(pixel(image, 1, 1)).toEqual([0, 0, 0, 0]);
    expect(pixel(image, 2)).toEqual([4, 5, 6, 255]);
    expect(pixel(image, 3, 1)).toEqual([4, 5, 6, 255]);
  });

  it('keeps a region fill inside its selection and ignores a seed outside it', () => {
    const image = new ImageData(4, 1), contains = (x: number) => x === 1 || x === 2;
    expect(fillImageData(image, {x: 0, y: 0}, {mode: 'region', color: [1, 2, 3, 255], contains})).toBe(false);
    expect(fillImageData(image, {x: 1, y: 0}, {mode: 'region', color: [1, 2, 3, 255], contains})).toBe(true);
    expect([pixel(image, 0)[3], pixel(image, 1)[3], pixel(image, 2)[3], pixel(image, 3)[3]]).toEqual([0, 255, 255, 0]);
  });

  it('maps rotated high resolution backing pixels into a world space selection', () => {
    const store = new BitmapStore(), layer = layerFor(store, new ImageData(4, 2));
    layer.bounds = {x: 10, y: 20, width: 2, height: 1};
    layer.transform = {a: 0, b: 2, c: -2, d: 0, e: 50, f: 0};
    const contains = selectionContainsPixel(layer, 4, 2, {x: 8, y: 22, width: 2, height: 2});
    expect(contains?.(0, 0)).toBe(false);
    expect(contains?.(2, 0)).toBe(true);
    expect(bitmapPoint(layer, 4, 2, {x: 9.5, y: 22.5})).toEqual({x: 2.5, y: .5});
    fillLayerAt(store, layer, {x: 9.5, y: 22.5}, {mode: 'all', color: '#123456', selection: {x: 8, y: 22, width: 2, height: 2}});
    const output = store.getImageData(layer.bitmapId);
    expect(pixel(output, 0)[3]).toBe(0);
    expect(pixel(output, 2)).toEqual([18, 52, 86, 255]);
    expect(pixel(output, 3, 1)).toEqual([18, 52, 86, 255]);
  });

  it('fills a large uniform region with no recursion or neighbor object queues', () => {
    const image = new ImageData(1024, 1024);
    expect(fillImageData(image, {x: 512, y: 512}, {mode: 'region', color: [1, 2, 3, 255]})).toBe(true);
    expect(pixel(image, 0)).toEqual([1, 2, 3, 255]);
    expect(pixel(image, 1023, 1023)).toEqual([1, 2, 3, 255]);
  });
});

function mockCanvas() {
  const context = {
    putImageData: vi.fn(), getImageData: vi.fn(), setTransform: vi.fn(), beginPath: vi.fn(), rect: vi.fn(), clip: vi.fn(),
    arc: vi.fn(), fill: vi.fn(), moveTo: vi.fn(), lineTo: vi.fn(), stroke: vi.fn(),
    lineWidth: 1, lineCap: '', lineJoin: '', globalCompositeOperation: '', fillStyle: '', strokeStyle: ''
  };
  const canvas = {width: 0, height: 0, getContext: vi.fn(() => context)};
  const createElement = vi.fn(() => canvas);
  vi.stubGlobal('document', {createElement});
  return {context, canvas, createElement};
}

describe('native canvas paint sessions', () => {
  it('draws a continuous clipped gesture without any bitmap readback until a single commit', () => {
    const store = new BitmapStore(), layer = layerFor(store, new ImageData(10, 10));
    layer.bounds = {x: 0, y: 0, width: 5, height: 5};
    const {context} = mockCanvas(), sourceRevision = store.get(layer.bitmapId).revision;
    const stroke = createPaintStroke(store, layer, {x: 1, y: 1}, {size: 3, color: '#123456', selection: {x: 0, y: 0, width: 4, height: 3}});
    stroke.append({x: 2, y: 1}); stroke.append({x: 4, y: 3});
    expect(context.setTransform).toHaveBeenCalledWith(2, -0, -0, 2, 0, 0);
    expect(context.rect).toHaveBeenCalledWith(0, 0, 4, 3);
    expect(context.clip).toHaveBeenCalledOnce();
    expect(context.arc).toHaveBeenCalledWith(1, 1, 1.5, 0, Math.PI * 2);
    expect(context.moveTo.mock.calls).toEqual([[1, 1], [2, 1]]);
    expect(context.lineTo.mock.calls).toEqual([[2, 1], [4, 3]]);
    expect(context.strokeStyle).toBe('rgb(18,52,86)');
    expect(context.getImageData).not.toHaveBeenCalled();
    expect(store.get(layer.bitmapId).revision).toBe(sourceRevision);
    const output = new ImageData(10, 10); output.data.set([18, 52, 86, 255]);
    context.getImageData.mockReturnValue(output);
    expect(stroke.commit()).toBe(true);
    expect(context.getImageData).toHaveBeenCalledOnce();
    expect(store.get(layer.bitmapId).revision).toBe(sourceRevision + 1);
    expect(stroke.commit()).toBe(false);
    stroke.append({x: 8, y: 8});
    expect(context.stroke).toHaveBeenCalledTimes(2);
  });

  it('uses destination-out for erasing and does not commit an unchanged gesture', () => {
    const store = new BitmapStore(), layer = layerFor(store, new ImageData(10, 10));
    const {context} = mockCanvas(), revision = store.get(layer.bitmapId).revision;
    const stroke = createPaintStroke(store, layer, {x: 1, y: 1}, {size: 10, color: '#000000', erase: true});
    expect(context.globalCompositeOperation).toBe('destination-out');
    context.getImageData.mockReturnValue(new ImageData(10, 10));
    expect(stroke.commit()).toBe(false);
    expect(store.get(layer.bitmapId).revision).toBe(revision);
  });

  it('reuses a raster during view/transform changes and invalidates bitmap edits and restored identities', () => {
    const store = new BitmapStore(), layer = layerFor(store, new ImageData(2, 2));
    const {createElement} = mockCanvas();
    layerRasterCanvas(layer, store); layer.transform.e = 40; layerRasterCanvas(layer, store);
    expect(createElement).toHaveBeenCalledOnce();
    store.update(layer.bitmapId, new ImageData(2, 2)); layerRasterCanvas(layer, store);
    expect(createElement).toHaveBeenCalledTimes(2);
    store.restore(store.snapshot()); layerRasterCanvas(layer, store);
    expect(createElement).toHaveBeenCalledTimes(3);
  });
});
