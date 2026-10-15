import type {BitmapStore} from '../model/bitmap-store';
import type {Point, RasterLayer, Rect} from '../model/canvas-types';
import {parsePaintColor, worldToBitmapMatrix} from './paint-geometry';
import {imageDataCanvas, layerImageData} from './rasterize';

export interface PaintStrokeOptions { size: number; color: string; erase?: boolean; selection?: Rect | null }
export interface PaintStrokeSession {
  readonly layerId: string;
  /** Draw this canvas while the gesture is active; bitmap/history state is committed once. */
  readonly canvas: HTMLCanvasElement;
  append(point: Point): void;
  commit(): boolean;
}

/** Native continuous strokes avoid bitmap clones, PNG encoding and pixel readback per pointer event. */
export function createPaintStroke(bitmaps: BitmapStore, layer: RasterLayer, start: Point, options: PaintStrokeOptions): PaintStrokeSession {
  const source = layerImageData(layer, bitmaps), canvas = imageDataCanvas(source);
  const context = canvas.getContext('2d');
  if (!context) throw new Error('当前浏览器无法创建绘制画布');
  const t = worldToBitmapMatrix(layer, canvas.width, canvas.height);
  context.setTransform(t.a, t.b, t.c, t.d, t.e, t.f);
  if (options.selection) {
    const r = options.selection;
    context.beginPath(); context.rect(r.x, r.y, r.width, r.height); context.clip();
  }
  const color = parsePaintColor(options.color);
  context.globalCompositeOperation = options.erase ? 'destination-out' : 'source-over';
  context.fillStyle = context.strokeStyle = `rgb(${color[0]},${color[1]},${color[2]})`;
  context.lineWidth = Math.max(.1, Number.isFinite(options.size) ? options.size : 1);
  context.lineCap = context.lineJoin = 'round';
  let last = {...start}, finished = false;
  if (Number.isFinite(start.x) && Number.isFinite(start.y)) {
    context.beginPath(); context.arc(start.x, start.y, context.lineWidth / 2, 0, Math.PI * 2); context.fill();
  }
  return {
    layerId: layer.id,
    canvas,
    append(point) {
      if (finished || !Number.isFinite(point.x) || !Number.isFinite(point.y)) return;
      if (point.x === last.x && point.y === last.y) return;
      context.beginPath(); context.moveTo(last.x, last.y); context.lineTo(point.x, point.y); context.stroke();
      last = {...point};
    },
    commit() {
      if (finished) return false;
      finished = true;
      const image = context.getImageData(0, 0, canvas.width, canvas.height);
      let changed = false;
      for (let i = 0; i < image.data.length; i++) {
        if (image.data[i] !== source.data[i]) { changed = true; break; }
      }
      if (changed) {
        bitmaps.update(layer.bitmapId, image);
        layer.chromaKey = null;
        layer.updatedAt = Date.now();
      }
      return changed;
    }
  };
}
