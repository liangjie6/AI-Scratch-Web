import type {Matrix2D, Point, Rect} from './canvas-types';

export interface AlignmentGuides {
  x: number[];
  y: number[];
}

export interface TranslationSnapResult {
  transform: Matrix2D;
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
