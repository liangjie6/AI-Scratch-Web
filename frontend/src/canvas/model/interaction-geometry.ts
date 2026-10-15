import type {Matrix2D, Point, Rect} from './canvas-types';

export type ControlHandle = 'nw' | 'n' | 'ne' | 'e' | 'se' | 's' | 'sw' | 'w';
export type ControlPoints = Record<ControlHandle, Point>;

const HANDLE_FACTORS: Record<ControlHandle, Point> = {
  nw: {x: 0, y: 0}, n: {x: 0.5, y: 0}, ne: {x: 1, y: 0}, e: {x: 1, y: 0.5},
  se: {x: 1, y: 1}, s: {x: 0.5, y: 1}, sw: {x: 0, y: 1}, w: {x: 0, y: 0.5}
};
const OPPOSITE: Record<ControlHandle, ControlHandle> = {nw: 'se', n: 's', ne: 'sw', e: 'w', se: 'nw', s: 'n', sw: 'ne', w: 'e'};
export const CONTROL_HANDLES = Object.keys(HANDLE_FACTORS) as ControlHandle[];

export function oppositeControlHandle(handle: ControlHandle): ControlHandle {
  return OPPOSITE[handle];
}

export function transformPoint(matrix: Matrix2D, point: Point): Point {
  return {x: matrix.a * point.x + matrix.c * point.y + matrix.e, y: matrix.b * point.x + matrix.d * point.y + matrix.f};
}

function rectPoint(rect: Rect, handle: ControlHandle): Point {
  const factor = HANDLE_FACTORS[handle];
  return {x: rect.x + rect.width * factor.x, y: rect.y + rect.height * factor.y};
}

export function rectControlPoints(rect: Rect): ControlPoints {
  return Object.fromEntries(CONTROL_HANDLES.map(handle => [handle, rectPoint(rect, handle)])) as unknown as ControlPoints;
}

export function transformedRectBounds(bounds: Rect, transform: Matrix2D): Rect {
  const corners = [
    {x: bounds.x, y: bounds.y}, {x: bounds.x + bounds.width, y: bounds.y},
    {x: bounds.x, y: bounds.y + bounds.height}, {x: bounds.x + bounds.width, y: bounds.y + bounds.height}
  ].map(point => transformPoint(transform, point));
  const left = Math.floor(Math.min(...corners.map(point => point.x)));
  const top = Math.floor(Math.min(...corners.map(point => point.y)));
  const right = Math.ceil(Math.max(...corners.map(point => point.x)));
  const bottom = Math.ceil(Math.max(...corners.map(point => point.y)));
  return {x: left, y: top, width: Math.max(1, right - left), height: Math.max(1, bottom - top)};
}

export function layerControlPoints(bounds: Rect, transform: Matrix2D): ControlPoints {
  const points = rectControlPoints(bounds);
  for (const handle of CONTROL_HANDLES) points[handle] = transformPoint(transform, points[handle]);
  return points;
}

export function hitControl(points: ControlPoints, point: Point, tolerance: number): ControlHandle | null {
  let closest: {handle: ControlHandle; distance: number} | null = null;
  for (const handle of CONTROL_HANDLES) {
    const candidate = points[handle];
    const distance = Math.hypot(candidate.x - point.x, candidate.y - point.y);
    if (distance <= tolerance && (!closest || distance < closest.distance)) closest = {handle, distance};
  }
  return closest?.handle ?? null;
}

export function resizeRectFromHandle(start: Rect, handle: ControlHandle, point: Point, minimum = 1, lockAspect = false): Rect {
  if (lockAspect) {
    const transform = resizeLayerFromHandle({a: 1, b: 0, c: 0, d: 1, e: 0, f: 0}, start, handle, point, true, minimum);
    const topLeft = transformPoint(transform, start);
    return {x: topLeft.x, y: topLeft.y, width: start.width * transform.a, height: start.height * transform.d};
  }
  let left = start.x, right = start.x + start.width, top = start.y, bottom = start.y + start.height;
  if (handle.includes('w')) left = Math.min(point.x, right - minimum);
  if (handle.includes('e')) right = Math.max(point.x, left + minimum);
  if (handle.includes('n')) top = Math.min(point.y, bottom - minimum);
  if (handle.includes('s')) bottom = Math.max(point.y, top + minimum);
  return {x: left, y: top, width: right - left, height: bottom - top};
}

export function resizeLayerFromHandle(start: Matrix2D, bounds: Rect, handle: ControlHandle, point: Point, lockAspect = false, minimum = 0.5): Matrix2D {
  const scaleX = Math.hypot(start.a, start.b);
  const scaleY = Math.hypot(start.c, start.d);
  const determinant = start.a * start.d - start.b * start.c;
  if (scaleX < 1e-8 || scaleY < 1e-8 || Math.abs(determinant) < 1e-12 || bounds.width <= 0 || bounds.height <= 0) return {...start};
  const anchorLocal = rectPoint(bounds, OPPOSITE[handle]);
  const anchorWorld = transformPoint(start, anchorLocal);
  const currentWidth = bounds.width * scaleX, currentHeight = bounds.height * scaleY;
  // Solve in the original affine basis so rotation, reflection and skew keep
  // their orientation while the opposite control stays fixed in world space.
  const dx = point.x - anchorWorld.x, dy = point.y - anchorWorld.y;
  const localX = (start.d * dx - start.c * dy) / determinant;
  const localY = (start.a * dy - start.b * dx) / determinant;
  const changesX = handle.includes('w') || handle.includes('e');
  const changesY = handle.includes('n') || handle.includes('s');
  let ratioX = changesX ? Math.max(minimum / currentWidth, (handle.includes('w') ? -localX : localX) / bounds.width) : 1;
  let ratioY = changesY ? Math.max(minimum / currentHeight, (handle.includes('n') ? -localY : localY) / bounds.height) : 1;
  if (lockAspect) {
    const ratio = changesX && changesY
      ? (Math.abs(ratioX - 1) >= Math.abs(ratioY - 1) ? ratioX : ratioY)
      : changesX ? ratioX : ratioY;
    ratioX = ratioY = Math.max(minimum / currentWidth, minimum / currentHeight, ratio);
  }
  const next = {a: start.a * ratioX, b: start.b * ratioX, c: start.c * ratioY, d: start.d * ratioY, e: start.e, f: start.f};
  next.e = anchorWorld.x - next.a * anchorLocal.x - next.c * anchorLocal.y;
  next.f = anchorWorld.y - next.b * anchorLocal.x - next.d * anchorLocal.y;
  return next;
}
