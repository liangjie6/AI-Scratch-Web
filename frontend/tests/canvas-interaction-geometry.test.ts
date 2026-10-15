import {describe, expect, it} from 'vitest';
import {IDENTITY_MATRIX, type Matrix2D, type Rect} from '../src/canvas/model/canvas-types';
import {CONTROL_HANDLES, layerControlPoints, oppositeControlHandle, rectControlPoints, resizeLayerFromHandle, resizeRectFromHandle} from '../src/canvas/model/interaction-geometry';
import {preciseTransformedRectBounds, snapLayerResize, snapRectResize, snapRectTranslation} from '../src/canvas/model/alignment-snapping';

const rect: Rect = {x: 10, y: 20, width: 100, height: 50};
const target: Rect = {x: 150, y: 100, width: 100, height: 100};
const expectPoint = (actual: {x: number; y: number}, expected: {x: number; y: number}) => {
  expect(actual.x).toBeCloseTo(expected.x, 8);
  expect(actual.y).toBeCloseTo(expected.y, 8);
};
const expectAnchor = (start: Matrix2D, next: Matrix2D, handle: typeof CONTROL_HANDLES[number]) => {
  const opposite = oppositeControlHandle(handle);
  expectPoint(layerControlPoints(rect, next)[opposite], layerControlPoints(rect, start)[opposite]);
};

describe('canvas resize and rectangle snapping', () => {
  it('snaps rectangle translation to edges and centers without changing its size', () => {
    const result = snapRectTranslation({x: 47, y: 26, width: 100, height: 50}, [target], 6);
    expect(result.rect).toEqual({x: 50, y: 26, width: 100, height: 50});
    expect(result.guides).toEqual({x: [150], y: []});
  });

  it('snaps crop corner edges on both axes while keeping the opposite corner fixed', () => {
    const result = snapRectResize(rect, 'se', {x: 148, y: 98}, [target], 6);
    expect(result.rect).toMatchObject({x: 10, y: 20});
    expect(result.rect.width).toBeCloseTo(140, 8);
    expect(result.rect.height).toBeCloseTo(80, 8);
    expect(result.guides).toEqual({x: [150], y: [100]});
    expectPoint(rectControlPoints(result.rect).nw, rectControlPoints(rect).nw);
  });

  it('does not move the untouched dimension when snapping a side control', () => {
    const result = snapRectResize(rect, 'e', {x: 148, y: 98}, [target], 6);
    expect(result.rect).toMatchObject({x: 10, y: 20, height: 50});
    expect(result.rect.width).toBeCloseTo(140, 8);
    expect(result.guides).toEqual({x: [150], y: []});
  });

  it('keeps a positive minimum size when a pointer crosses the fixed corner', () => {
    const resized = snapRectResize(rect, 'nw', {x: 200, y: 200}, [{x: 110, y: 70, width: 5, height: 5}], 6);
    expect(resized.rect.width).toBeGreaterThanOrEqual(1 - 1e-8);
    expect(resized.rect.height).toBeGreaterThanOrEqual(1 - 1e-8);
    expectPoint(rectControlPoints(resized.rect).se, rectControlPoints(rect).se);
    expect(resizeRectFromHandle(rect, 'nw', {x: 200, y: 200})).toEqual({x: 109, y: 69, width: 1, height: 1});
  });

  it('keeps the aspect lock and anchor for all eight controls', () => {
    for (const handle of CONTROL_HANDLES) {
      const control = rectControlPoints(rect)[handle];
      const opposite = rectControlPoints(rect)[oppositeControlHandle(handle)];
      const pointer = {x: opposite.x + (control.x - opposite.x) * 1.4, y: opposite.y + (control.y - opposite.y) * 1.4};
      const result = snapLayerResize(IDENTITY_MATRIX, rect, handle, pointer, [target], 6, true);
      expect(Math.hypot(result.transform.a, result.transform.b) / Math.hypot(result.transform.c, result.transform.d)).toBeCloseTo(1, 8);
      expectAnchor(IDENTITY_MATRIX, result.transform, handle);
    }
  });

  it('snaps a locked corner along its single scale dimension', () => {
    const result = snapLayerResize(IDENTITY_MATRIX, rect, 'se', {x: 148, y: 89}, [target], 6, true);
    const bounds = preciseTransformedRectBounds(rect, result.transform);
    expect(bounds.width / bounds.height).toBeCloseTo(2, 8);
    expect(bounds.x + bounds.width).toBeCloseTo(150, 8);
    expect(result.guides.x).toContain(150);
    expectAnchor(IDENTITY_MATRIX, result.transform, 'se');
  });

  it('preserves scale and the opposite side midpoint when locked side handles resize', () => {
    const result = resizeLayerFromHandle(IDENTITY_MATRIX, rect, 'e', {x: 210, y: 45}, true);
    expect(result.a).toBe(2);
    expect(result.d).toBe(2);
    expectAnchor(IDENTITY_MATRIX, result, 'e');
    const minimum = resizeRectFromHandle(rect, 'se', {x: -20, y: -20}, 1, true);
    expect(minimum.width / minimum.height).toBeCloseTo(2, 8);
    expect(minimum.width).toBeGreaterThanOrEqual(1);
    expect(minimum.height).toBeGreaterThanOrEqual(1);
  });

  it('snaps a rotated side control in world space without moving the opposite control', () => {
    const start = {a: 0, b: 1, c: -1, d: 0, e: 200, f: 30};
    const result = snapLayerResize(start, rect, 'e', {x: 155, y: 188}, [{x: 100, y: 190, width: 50, height: 20}], 6);
    expect(preciseTransformedRectBounds(rect, result.transform).y + preciseTransformedRectBounds(rect, result.transform).height).toBeCloseTo(190, 8);
    expect(result.guides.y).toContain(190);
    expectAnchor(start, result.transform, 'e');
  });

  it('preserves skew and reflections through resize and snapping', () => {
    const start = {a: -1, b: 0.2, c: 0.4, d: 1, e: 200, f: 30};
    const controls = layerControlPoints(rect, start);
    const anchor = controls.nw;
    const point = {x: anchor.x + (controls.se.x - anchor.x) * 1.5, y: anchor.y + (controls.se.y - anchor.y) * 1.5};
    const result = snapLayerResize(start, rect, 'se', point, [], 6, true).transform;
    expect(result.a / start.a).toBeCloseTo(1.5);
    expect(result.b / start.b).toBeCloseTo(1.5);
    expect(result.c / start.c).toBeCloseTo(1.5);
    expect(result.d / start.d).toBeCloseTo(1.5);
    expectAnchor(start, result, 'se');
  });

  it('does not report fixed-edge guides or snap to targets beyond the threshold', () => {
    const result = snapRectResize(rect, 'se', {x: 130, y: 90}, [{x: 10, y: 20, width: 200, height: 200}], 3);
    expect(result.guides).toEqual({x: [], y: []});
    expect(result.rect).toEqual({x: 10, y: 20, width: 120, height: 70});
  });

  it('leaves degenerate transforms unchanged', () => {
    const matrix = {a: 0, b: 0, c: 0, d: 0, e: 20, f: 30};
    expect(snapLayerResize(matrix, rect, 'se', {x: 150, y: 100}, [target], 6).transform).toEqual(matrix);
  });
});
