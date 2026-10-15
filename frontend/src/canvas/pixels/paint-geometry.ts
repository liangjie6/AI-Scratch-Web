import type {Matrix2D, Point, RasterLayer, Rect} from '../model/canvas-types';

/** Map world coordinates directly into the backing bitmap, including Scratch's 2x pixels. */
export function worldToBitmapMatrix(layer: RasterLayer, width: number, height: number): Matrix2D {
  const t = layer.transform, determinant = t.a * t.d - t.b * t.c;
  if (Math.abs(determinant) < 1e-12 || layer.bounds.width <= 0 || layer.bounds.height <= 0) {
    throw new Error('图层尺寸或变换无效，无法绘制');
  }
  const sx = width / layer.bounds.width, sy = height / layer.bounds.height;
  return {
    a: t.d * sx / determinant, b: -t.b * sy / determinant,
    c: -t.c * sx / determinant, d: t.a * sy / determinant,
    e: ((t.c * t.f - t.d * t.e) / determinant - layer.bounds.x) * sx,
    f: ((t.b * t.e - t.a * t.f) / determinant - layer.bounds.y) * sy
  };
}

export function bitmapPoint(layer: RasterLayer, width: number, height: number, world: Point): Point {
  const t = worldToBitmapMatrix(layer, width, height);
  return {x: t.a * world.x + t.c * world.y + t.e, y: t.b * world.x + t.d * world.y + t.f};
}

/** Pixel centers are tested in world space so rotated/scaled layers honor rectangular selections. */
export function selectionContainsPixel(layer: RasterLayer, width: number, height: number, selection?: Rect | null): ((x: number, y: number) => boolean) | undefined {
  if (!selection) return undefined;
  const t = layer.transform, sx = layer.bounds.width / width, sy = layer.bounds.height / height;
  const a = t.a * sx, b = t.b * sx, c = t.c * sy, d = t.d * sy;
  const e = t.a * layer.bounds.x + t.c * layer.bounds.y + t.e;
  const f = t.b * layer.bounds.x + t.d * layer.bounds.y + t.f;
  const right = selection.x + selection.width, bottom = selection.y + selection.height;
  return (x, y) => {
    const worldX = a * (x + .5) + c * (y + .5) + e;
    const worldY = b * (x + .5) + d * (y + .5) + f;
    return worldX >= selection.x && worldX < right && worldY >= selection.y && worldY < bottom;
  };
}

export function parsePaintColor(color: string): [number, number, number, number] {
  const value = color.replace(/^#/, '');
  if (/^[0-9a-f]{6}$/i.test(value)) return [parseInt(value.slice(0, 2), 16), parseInt(value.slice(2, 4), 16), parseInt(value.slice(4, 6), 16), 255];
  if (/^[0-9a-f]{3}$/i.test(value)) return [parseInt(value[0] + value[0], 16), parseInt(value[1] + value[1], 16), parseInt(value[2] + value[2], 16), 255];
  return [47, 128, 237, 255];
}
