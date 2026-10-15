import type {BitmapStore} from '../model/bitmap-store';
import type {Point, RasterLayer, Rect} from '../model/canvas-types';
import {bitmapPoint, parsePaintColor, selectionContainsPixel} from './paint-geometry';
import {layerImageData} from './rasterize';

export interface PaintFillOptions { color: string; mode: 'region' | 'all'; selection?: Rect | null; tolerance?: number }
interface FillImageOptions { color: [number, number, number, number]; mode: 'region' | 'all'; tolerance?: number; contains?: (x: number, y: number) => boolean }

/** Mutates a single image buffer, with a bounded typed queue instead of per-neighbor array objects. */
export function fillImageData(image: ImageData, start: Point, options: FillImageOptions): boolean {
  const {width, height, data} = image, color = options.color, contains = options.contains;
  let changed = false;
  const paint = (index: number) => {
    const offset = index * 4;
    if (data[offset] === color[0] && data[offset + 1] === color[1] && data[offset + 2] === color[2] && data[offset + 3] === color[3]) return;
    data[offset] = color[0]; data[offset + 1] = color[1]; data[offset + 2] = color[2]; data[offset + 3] = color[3];
    changed = true;
  };
  if (options.mode === 'all') {
    for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) if (!contains || contains(x, y)) paint(y * width + x);
    return changed;
  }
  const x = Math.floor(start.x), y = Math.floor(start.y);
  if (x < 0 || y < 0 || x >= width || y >= height || (contains && !contains(x, y))) return false;
  const first = y * width + x, offset = first * 4;
  const r = data[offset], g = data[offset + 1], b = data[offset + 2], a = data[offset + 3];
  const tolerance = Math.max(0, options.tolerance ?? 18), count = width * height;
  const seen = new Uint8Array(count), queue = new Uint32Array(count);
  let cursor = 0, length = 0;
  const add = (index: number, px: number, py: number) => {
    if (seen[index]) return;
    seen[index] = 1;
    if (contains && !contains(px, py)) return;
    const i = index * 4;
    if (Math.abs(data[i + 3] - a) > tolerance) return;
    // RGB under fully transparent pixels has no visual meaning.
    if ((a !== 0 || data[i + 3] !== 0) && (Math.abs(data[i] - r) > tolerance || Math.abs(data[i + 1] - g) > tolerance || Math.abs(data[i + 2] - b) > tolerance)) return;
    queue[length++] = index;
  };
  add(first, x, y);
  while (cursor < length) {
    const index = queue[cursor++], px = index % width, py = Math.floor(index / width);
    paint(index);
    if (px > 0) add(index - 1, px - 1, py);
    if (px + 1 < width) add(index + 1, px + 1, py);
    if (py > 0) add(index - width, px, py - 1);
    if (py + 1 < height) add(index + width, px, py + 1);
  }
  return changed;
}

export function fillLayerAt(bitmaps: BitmapStore, layer: RasterLayer, world: Point, options: PaintFillOptions): boolean {
  const image = layerImageData(layer, bitmaps);
  const changed = fillImageData(image, bitmapPoint(layer, image.width, image.height, world), {
    color: parsePaintColor(options.color), mode: options.mode, tolerance: options.tolerance,
    contains: selectionContainsPixel(layer, image.width, image.height, options.selection)
  });
  if (changed) {
    bitmaps.update(layer.bitmapId, image);
    layer.chromaKey = null;
    layer.updatedAt = Date.now();
  }
  return changed;
}
