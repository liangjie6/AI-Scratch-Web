import type {Matrix2D, Point, Rect} from './canvas-types';
import {layerControlPoints, oppositeControlHandle, rectControlPoints, resizeLayerFromHandle, type ControlHandle} from './interaction-geometry';

export interface AlignmentGuides {
  x: number[];
  y: number[];
}

export interface TranslationSnapResult {
  transform: Matrix2D;
  guides: AlignmentGuides;
}

export interface RectSnapResult {
  rect: Rect;
  guides: AlignmentGuides;
}

type AxisSnap = {delta: number; coordinate: number; distance: number};

function transformPoint(matrix: Matrix2D, point: Point): Point {
  return {
    x: matrix.a * point.x + matrix.c * point.y + matrix.e,
    y: matrix.b * point.x + matrix.d * point.y + matrix.f
  };
}

export function preciseTransformedRectBounds(bounds: Rect, transform: Matrix2D): Rect {
  const corners = [
    {x: bounds.x, y: bounds.y},
    {x: bounds.x + bounds.width, y: bounds.y},
    {x: bounds.x, y: bounds.y + bounds.height},
    {x: bounds.x + bounds.width, y: bounds.y + bounds.height}
  ].map(point => transformPoint(transform, point));
  const left = Math.min(...corners.map(point => point.x));
  const top = Math.min(...corners.map(point => point.y));
  const right = Math.max(...corners.map(point => point.x));
  const bottom = Math.max(...corners.map(point => point.y));
  return {x: left, y: top, width: right - left, height: bottom - top};
}

function anchors(start: number, size: number): number[] {
  return [start, start + size / 2, start + size];
}

function closestSnap(moving: number[], targets: number[], threshold: number): AxisSnap | null {
  let closest: AxisSnap | null = null;
  for (const movingCoordinate of moving) {
    for (const targetCoordinate of targets) {
      const delta = targetCoordinate - movingCoordinate;
      const distance = Math.abs(delta);
      if (distance <= threshold && (!closest || distance < closest.distance)) {
        closest = {delta, coordinate: targetCoordinate, distance};
      }
    }
  }
  return closest;
}

export function snapLayerTranslation(
  bounds: Rect,
  proposedTransform: Matrix2D,
  targetRects: Rect[],
  threshold: number
): TranslationSnapResult {
  const moving = preciseTransformedRectBounds(bounds, proposedTransform);
  const targetX = targetRects.flatMap(rect => anchors(rect.x, rect.width));
  const targetY = targetRects.flatMap(rect => anchors(rect.y, rect.height));
  const xSnap = closestSnap(anchors(moving.x, moving.width), targetX, threshold);
  const ySnap = closestSnap(anchors(moving.y, moving.height), targetY, threshold);
  return {
    transform: {
      ...proposedTransform,
      e: proposedTransform.e + (xSnap?.delta ?? 0),
      f: proposedTransform.f + (ySnap?.delta ?? 0)
    },
    guides: {
      x: xSnap ? [xSnap.coordinate] : [],
      y: ySnap ? [ySnap.coordinate] : []
    }
  };
}

export function snapRectTranslation(proposedRect: Rect, targetRects: Rect[], threshold: number): RectSnapResult {
  const snapped = snapLayerTranslation(proposedRect, {a: 1, b: 0, c: 0, d: 1, e: 0, f: 0}, targetRects, threshold);
  return {rect: {...proposedRect, x: proposedRect.x + snapped.transform.e, y: proposedRect.y + snapped.transform.f}, guides: snapped.guides};
}

type ResizeConstraint = {axis: 'x' | 'y'; coordinate: number; delta: number; coefficients: number[]};
const EPSILON = 1e-7;

/** Resize and snap without translating the fixed opposite control or breaking
 * an aspect lock. The threshold is measured in world coordinates, so callers
 * should divide their screen-pixel threshold by the viewport zoom. */
export function snapLayerResize(
  start: Matrix2D,
  bounds: Rect,
  handle: ControlHandle,
  point: Point,
  targetRects: Rect[],
  threshold: number,
  lockAspect = false,
  minimum = 0.5
): TranslationSnapResult {
  const proposed = resizeLayerFromHandle(start, bounds, handle, point, lockAspect, minimum);
  const empty = {transform: proposed, guides: {x: [], y: []}};
  const width = bounds.width * Math.hypot(start.a, start.b);
  const height = bounds.height * Math.hypot(start.c, start.d);
  const determinant = start.a * start.d - start.b * start.c;
  if (threshold < 0 || !targetRects.length || width <= EPSILON || height <= EPSILON || Math.abs(determinant) < 1e-12) return empty;

  const changesX = handle.includes('w') || handle.includes('e');
  const changesY = handle.includes('n') || handle.includes('s');
  const axes = lockAspect ? ['both'] as const : [changesX ? 'x' : null, changesY ? 'y' : null].filter((axis): axis is 'x' | 'y' => axis !== null);
  const anchorLocal = rectControlPoints(bounds)[oppositeControlHandle(handle)];
  const anchorWorld = transformPoint(start, anchorLocal);
  const ratioX = Math.hypot(proposed.a, proposed.b) / Math.hypot(start.a, start.b);
  const ratioY = Math.hypot(proposed.c, proposed.d) / Math.hypot(start.c, start.d);
  const evaluate = (offsets: number[]): Matrix2D | null => {
    let x = ratioX, y = ratioY;
    for (let i = 0; i < axes.length; i++) {
      if (axes[i] !== 'y') x += offsets[i]!;
      if (axes[i] !== 'x') y += offsets[i]!;
    }
    if (((changesX || lockAspect) && x < minimum / width - EPSILON) || ((changesY || lockAspect) && y < minimum / height - EPSILON)) return null;
    const transform = {a: start.a * x, b: start.b * x, c: start.c * y, d: start.d * y, e: 0, f: 0};
    transform.e = anchorWorld.x - transform.a * anchorLocal.x - transform.c * anchorLocal.y;
    transform.f = anchorWorld.y - transform.b * anchorLocal.x - transform.d * anchorLocal.y;
    return transform;
  };
  const zero = axes.map(() => 0);
  const movingBounds = preciseTransformedRectBounds(bounds, proposed);
  const movingControl = layerControlPoints(bounds, proposed)[handle];
  // Normalize each degree of freedom to one world pixel of handle movement.
  // This also prevents a nearly perpendicular alignment from causing a large
  // jump just because one coordinate of the handle barely changes.
  const bases = axes.map((_, index) => {
    const offset = [...zero]; offset[index] = 1;
    const transform = evaluate(offset)!;
    const control = layerControlPoints(bounds, transform)[handle];
    return {bounds: preciseTransformedRectBounds(bounds, transform), length: Math.hypot(control.x - movingControl.x, control.y - movingControl.y)};
  });
  if (bases.some(basis => basis.length < EPSILON)) return empty;
  const constraints: ResizeConstraint[] = [];
  for (const axis of ['x', 'y'] as const) {
    const size = axis === 'x' ? 'width' : 'height';
    const moving = anchors(movingBounds[axis], movingBounds[size]);
    const target = [...new Set(targetRects.flatMap(rect => anchors(rect[axis], rect[size])))];
    for (let feature = 0; feature < moving.length; feature++) {
      const coefficients = bases.map(basis => (anchors(basis.bounds[axis], basis.bounds[size])[feature]! - moving[feature]!) / basis.length);
      if (coefficients.every(coefficient => Math.abs(coefficient) < EPSILON)) continue;
      const nearbyTargets = target.filter(coordinate => Number.isFinite(coordinate) && Math.abs(coordinate - moving[feature]!) <= threshold)
        .sort((a, b) => Math.abs(a - moving[feature]!) - Math.abs(b - moving[feature]!)).slice(0, 3);
      for (const coordinate of nearbyTargets) {
        const delta = coordinate - moving[feature]!;
        if (Number.isFinite(delta) && Math.abs(delta) <= threshold) constraints.push({axis, coordinate, delta, coefficients});
      }
    }
  }
  const result: {best: {transform: Matrix2D; guides: AlignmentGuides; axes: number; distance: number} | null} = {best: null};
  const consider = (normalizedOffsets: number[]) => {
    if (normalizedOffsets.some(offset => !Number.isFinite(offset))) return;
    const transform = evaluate(normalizedOffsets.map((offset, index) => offset / bases[index]!.length));
    if (!transform) return;
    const control = layerControlPoints(bounds, transform)[handle];
    const distance = Math.hypot(control.x - movingControl.x, control.y - movingControl.y);
    if (distance > threshold * Math.SQRT2 + EPSILON) return;
    const guides: AlignmentGuides = {x: [], y: []};
    for (const constraint of constraints) {
      const correction = constraint.coefficients.reduce((sum, coefficient, index) => sum + coefficient * normalizedOffsets[index]!, 0);
      if (Math.abs(correction - constraint.delta) < EPSILON && !guides[constraint.axis].includes(constraint.coordinate)) guides[constraint.axis].push(constraint.coordinate);
    }
    const axisCount = Number(guides.x.length > 0) + Number(guides.y.length > 0);
    const best = result.best;
    if (axisCount && (!best || axisCount > best.axes || (axisCount === best.axes && distance < best.distance))) result.best = {transform, guides, axes: axisCount, distance};
  };
  for (let i = 0; i < constraints.length; i++) {
    const constraint = constraints[i]!;
    const denominator = constraint.coefficients.reduce((sum, coefficient) => sum + coefficient * coefficient, 0);
    consider(constraint.coefficients.map(coefficient => coefficient * constraint.delta / denominator));
    if (axes.length !== 2) continue;
    for (let j = i + 1; j < constraints.length; j++) {
      const other = constraints[j]!;
      if (constraint.axis === other.axis) continue;
      const [a, b] = constraint.coefficients as [number, number];
      const [c, d] = other.coefficients as [number, number];
      const determinant = a * d - b * c;
      if (Math.abs(determinant) < EPSILON) continue;
      consider([(constraint.delta * d - b * other.delta) / determinant, (a * other.delta - constraint.delta * c) / determinant]);
    }
  }
  return result.best ? {transform: result.best.transform, guides: result.best.guides} : empty;
}

export function snapRectResize(
  start: Rect,
  handle: ControlHandle,
  point: Point,
  targetRects: Rect[],
  threshold: number,
  lockAspect = false,
  minimum = 1
): RectSnapResult {
  const snapped = snapLayerResize({a: 1, b: 0, c: 0, d: 1, e: 0, f: 0}, start, handle, point, targetRects, threshold, lockAspect, minimum);
  return {rect: preciseTransformedRectBounds(start, snapped.transform), guides: snapped.guides};
}
