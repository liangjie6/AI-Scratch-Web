import {beforeAll, describe, expect, it, vi} from 'vitest';
import {applyChromaKey} from '../src/canvas/pixels/chroma-key';
import type {ChromaKeyEffect} from '../src/canvas/model/canvas-types';

beforeAll(() => {
  if (!globalThis.ImageData) {
    class ImageDataPolyfill {
      data: Uint8ClampedArray;
      width: number;
      height: number;
      constructor(dataOrWidth: Uint8ClampedArray | number, widthOrHeight: number, height?: number) {
        if (typeof dataOrWidth === 'number') {
          this.width = dataOrWidth;
          this.height = widthOrHeight;
          this.data = new Uint8ClampedArray(this.width * this.height * 4);
        } else {
          this.data = dataOrWidth;
          this.width = widthOrHeight;
          this.height = height ?? dataOrWidth.length / 4 / widthOrHeight;
        }
      }
    }
    Object.defineProperty(globalThis, 'ImageData', {value: ImageDataPolyfill, configurable: true});
  }
});

const defaultEffect: ChromaKeyEffect = {
  keyColor: {r: 0, g: 0, b: 255},
  tolerance: 20,
  softness: 5,
  mode: 'global',
  seedMode: 'border',
  connectivity: 8,
  spillSuppression: 0,
  inverted: false,
  seedPoints: [],
  enabled: true
};

describe('chroma key algorithm', () => {
  describe('max tolerance keyed out completely', () => {
    it('keys out all opaque colors with zero blue/color residue when tolerance is 100', () => {
      const image = new ImageData(6, 1);
      // Various colors: pure blue, sky blue, navy blue, red, yellow, and transparent
      image.data.set([
        0, 0, 255, 255,      // Pure blue
        100, 180, 255, 255,  // Sky blue
        0, 0, 80, 255,       // Navy blue
        255, 0, 0, 255,      // Red
        255, 255, 0, 255,    // Yellow (opposite color)
        0, 0, 255, 0         // Transparent blue
      ]);

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 255},
        tolerance: 100,
        softness: 0,
        mode: 'global'
      });

      // All opaque pixels must be completely keyed out (alpha = 0)
      expect(result.data[3]).toBe(0);
      expect(result.data[7]).toBe(0);
      expect(result.data[11]).toBe(0);
      expect(result.data[15]).toBe(0);
      expect(result.data[19]).toBe(0);
      // Pre-existing transparent pixel remains transparent
      expect(result.data[23]).toBe(0);
    });

    it('keys out completely at tolerance 100 even if softness is specified', () => {
      const image = new ImageData(3, 1);
      image.data.set([
        0, 0, 255, 255,      // Blue
        255, 255, 0, 255,    // Yellow (maximum RGB distance = 1.0)
        255, 255, 255, 255   // White
      ]);

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 255},
        tolerance: 100,
        softness: 10,
        mode: 'global'
      });

      expect(result.data[3]).toBe(0);
      expect(result.data[7]).toBe(0);
      expect(result.data[11]).toBe(0);
    });
  });

  describe('zero softness hard threshold and boundary', () => {
    it('applies a strict binary threshold without smoothstep feathering when softness is 0', () => {
      const image = new ImageData(3, 1);
      // For grayscale [v, v, v] against keyColor [0, 0, 0], colorDistance = v / 255
      // tolerance: 50 => threshold = 0.50 => 255 * 0.5 = 127.5
      // v=100 (dist ~0.392 <= 0.5) => keyed out (alpha = 0)
      // v=127 (dist ~0.498 <= 0.5) => keyed out (alpha = 0)
      // v=128 (dist ~0.502 > 0.5)  => kept (alpha = 255)
      image.data.set([
        100, 100, 100, 255,
        127, 127, 127, 255,
        128, 128, 128, 255
      ]);

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 50,
        softness: 0,
        mode: 'global'
      });

      expect(result.data[3]).toBe(0);
      expect(result.data[7]).toBe(0);
      expect(result.data[11]).toBe(255);
    });

    it('matches exact color only when tolerance is 0 and softness is 0', () => {
      const image = new ImageData(3, 1);
      image.data.set([
        0, 255, 0, 255,   // exact green
        1, 255, 0, 255,   // slight difference
        0, 254, 0, 255    // slight difference
      ]);

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 0,
        softness: 0,
        mode: 'global'
      });

      expect(result.data[3]).toBe(0);
      expect(result.data[7]).toBe(255);
      expect(result.data[11]).toBe(255);
    });
  });

  describe('soft edge and spill suppression', () => {
    it('produces monotonically increasing alpha through the transition zone', () => {
      const image = new ImageData(5, 1);
      // KeyColor: black [0, 0, 0], tolerance: 40, softness: 20
      // inner = (40 - 20) / 100 = 0.20 (v ~ 51)
      // outer = (40 + 20) / 100 = 0.60 (v ~ 153)
      image.data.set([
        25, 25, 25, 255,     // dist ~ 0.098 <= inner => alpha = 0
        76, 76, 76, 255,     // dist ~ 0.298 (transition)
        102, 102, 102, 255,  // dist ~ 0.400 (transition)
        128, 128, 128, 255,  // dist ~ 0.502 (transition)
        180, 180, 180, 255   // dist ~ 0.706 >= outer => alpha = 255
      ]);

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 40,
        softness: 20,
        mode: 'global'
      });

      const a0 = result.data[3];
      const a1 = result.data[7];
      const a2 = result.data[11];
      const a3 = result.data[15];
      const a4 = result.data[19];

      expect(a0).toBe(0);
      expect(a1).toBeGreaterThan(0);
      expect(a1).toBeLessThan(a2);
      expect(a2).toBeLessThan(a3);
      expect(a3).toBeLessThan(a4);
      expect(a4).toBe(255);
    });

    it('applies spill suppression on semi-transparent transition edge pixels', () => {
      const image = new ImageData(2, 1);
      // Key color: pure green [0, 255, 0]
      // Pixel with green fringe in transition zone
      image.data.set([
        100, 200, 100, 255,
        220, 20, 30, 255
      ]);

      const withoutSpill = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 40,
        softness: 20,
        spillSuppression: 0,
        mode: 'global'
      });

      const withSpill = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 40,
        softness: 20,
        spillSuppression: 100,
        mode: 'global'
      });

      // Semi-transparent pixel has spill suppressed
      expect(withoutSpill.data[3]).toBeGreaterThan(0);
      expect(withoutSpill.data[3]).toBeLessThan(255);
      expect(withSpill.data[3]).toBe(withoutSpill.data[3]);
      // Green channel on the edge should be attenuated
      expect(withSpill.data[1]).toBeLessThan(withoutSpill.data[1]);
      expect(withSpill.data.slice(4, 8)).toEqual(image.data.slice(4, 8));
    });
  });

  describe('global vs connected selection, border vs points, and 4/8 connectivity', () => {
    it.each([4, 8] as const)('does not let a soft color bridge join a separate island with %s-way connectivity', connectivity => {
      // The middle pixels pass tolerance + softness but fail the actual
      // tolerance. Only the first one touches the connected black background.
      const image = new ImageData(new Uint8ClampedArray([
        0, 0, 0, 255, 56, 56, 56, 255, 56, 56, 56, 255, 0, 0, 0, 255
      ]), 4, 1);
      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 18,
        softness: 8,
        mode: 'connected',
        seedMode: 'points',
        seedPoints: [{x: 0, y: 0}],
        connectivity
      });

      expect(result.data[3]).toBe(0);
      expect(result.data[7]).toBeGreaterThan(0);
      expect(result.data[7]).toBeLessThan(255);
      expect(result.data[11]).toBe(255);
      expect(result.data[15]).toBe(255);
      const global = applyChromaKey(image, {...defaultEffect, keyColor: {r: 0, g: 0, b: 0}, tolerance: 18, softness: 8});
      expect(global.data[15]).toBe(0);
    });

    it('does not reach an exact-color island through a single soft fringe pixel', () => {
      const image = new ImageData(new Uint8ClampedArray([
        0, 0, 0, 255, 56, 56, 56, 255, 0, 0, 0, 255
      ]), 3, 1);
      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 18,
        softness: 8,
        mode: 'connected',
        seedMode: 'points',
        seedPoints: [{x: 0, y: 0}]
      });
      expect(result.data[3]).toBe(0);
      expect(result.data[7]).toBeLessThan(255);
      expect(result.data[11]).toBe(255);
    });

    it('does not remove a soft-only seed or a separate matching island', () => {
      const image = new ImageData(new Uint8ClampedArray([
        56, 56, 56, 255, 56, 56, 56, 255, 0, 0, 0, 255
      ]), 3, 1);
      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 18,
        softness: 8,
        mode: 'connected',
        seedMode: 'points',
        seedPoints: [{x: 0, y: 0}]
      });
      expect(result.data).toEqual(image.data);
    });

    it('feathers an already checked border pixel when it neighbors a strict member', () => {
      const image = new ImageData(new Uint8ClampedArray([
        56, 56, 56, 255, 0, 0, 0, 255
      ]), 2, 1);
      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 18,
        softness: 8,
        mode: 'connected',
        seedMode: 'border'
      });
      expect(result.data[3]).toBeGreaterThan(0);
      expect(result.data[3]).toBeLessThan(255);
      expect(result.data[7]).toBe(0);
    });

    it('applies the inverse connected mask without preserving an island beyond the soft fringe', () => {
      const image = new ImageData(new Uint8ClampedArray([
        0, 0, 0, 128, 56, 56, 56, 128, 0, 0, 0, 128, 0, 0, 0, 0
      ]), 4, 1);
      const config: ChromaKeyEffect = {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 18,
        softness: 8,
        mode: 'connected',
        seedMode: 'points',
        seedPoints: [{x: 0, y: 0}]
      };
      const normal = applyChromaKey(image, config);
      const inverted = applyChromaKey(image, {...config, inverted: true});
      expect(inverted.data[3]).toBe(128);
      expect(inverted.data[7]).toBeGreaterThan(0);
      expect(normal.data[7] + inverted.data[7]).toBe(128);
      expect(inverted.data[11]).toBe(0);
      expect(inverted.data[15]).toBe(0);
    });

    it('only measures colors near a small connected region in a large image', () => {
      const image = new ImageData(2048, 1024);
      image.data.fill(255);
      const x = 1000;
      const y = 500;
      image.data.set([0, 0, 0, 255], (y * image.width + x) * 4);
      // Instrumenting the key color records color-distance checks without a
      // timing-dependent assertion. A full-frame candidate scan is a regression.
      let distanceChecks = 0;
      const keyColor = {get r() {distanceChecks += 1; return 0;}, g: 0, b: 0};
      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor,
        tolerance: 18,
        softness: 8,
        mode: 'connected',
        seedMode: 'points',
        seedPoints: [{x, y}],
        connectivity: 4
      });
      expect(result.data[(y * image.width + x) * 4 + 3]).toBe(0);
      expect(result.data[3]).toBe(255);
      expect(distanceChecks).toBe(5);
    });

    it('processes a large fully connected background through queue growth and wraparound', () => {
      const image = new ImageData(1024, 4096);
      image.data.fill(255);
      // Hard thresholds should never run sqrt, even on a full-size frame.
      const squareRoot = vi.spyOn(Math, 'sqrt');
      let result: ImageData;
      let squareRootCalls: number;
      try {
        result = applyChromaKey(image, {
          ...defaultEffect,
          keyColor: {r: 255, g: 255, b: 255},
          tolerance: 0,
          softness: 0,
          mode: 'connected',
          seedMode: 'border',
          connectivity: 4
        });
        squareRootCalls = squareRoot.mock.calls.length;
      } finally {
        squareRoot.mockRestore();
      }
      let remainingOpaque = 0;
      for (let offset = 3; offset < result!.data.length; offset += 4) remainingOpaque += result!.data[offset];
      expect(remainingOpaque).toBe(0);
      expect(squareRootCalls!).toBe(0);
      expect(image.data[3]).toBe(255);
      expect(image.data[image.data.length - 1]).toBe(255);
    });

    it.each([4, 8] as const)('matches a reference flood on fragmented rows with %s-way connectivity', connectivity => {
      const width = 37;
      const height = 29;
      const image = new ImageData(width, height);
      let randomState = 123456;
      for (let index = 0; index < width * height; index += 1) {
        randomState = (Math.imul(randomState, 1664525) + 1013904223) >>> 0;
        const value = randomState % 10;
        const gray = value < 6 ? 0 : value < 8 ? 56 : 255;
        image.data.set([gray, gray, gray, value === 9 ? 0 : 255], index * 4);
      }
      const offsets = connectivity === 8
        ? [[-1, -1], [0, -1], [1, -1], [-1, 0], [1, 0], [-1, 1], [0, 1], [1, 1]]
        : [[-1, 0], [1, 0], [0, -1], [0, 1]];
      for (const seedMode of ['border', 'points'] as const) {
        const queue: number[] = [];
        const members = new Set<number>();
        const add = (x: number, y: number) => {
          if (x < 0 || y < 0 || x >= width || y >= height) return;
          const index = y * width + x;
          if (members.has(index) || image.data[index * 4 + 3] === 0 || image.data[index * 4] !== 0) return;
          members.add(index);
          queue.push(index);
        };
        if (seedMode === 'border') {
          for (let x = 0; x < width; x += 1) {add(x, 0); add(x, height - 1);}
          for (let y = 0; y < height; y += 1) {add(0, y); add(width - 1, y);}
        } else {
          add(11, 13);
          add(24, 20);
        }
        for (let cursor = 0; cursor < queue.length; cursor += 1) {
          const index = queue[cursor];
          for (const [dx, dy] of offsets) add(index % width + dx, Math.floor(index / width) + dy);
        }
        const fringe = new Set<number>();
        for (const index of members) {
          const x = index % width;
          const y = Math.floor(index / width);
          for (const [dx, dy] of offsets) {
            if (x + dx < 0 || y + dy < 0 || x + dx >= width || y + dy >= height) continue;
            const neighbor = (y + dy) * width + x + dx;
            if (image.data[neighbor * 4] === 56 && image.data[neighbor * 4 + 3] > 0) fringe.add(neighbor);
          }
        }
        const result = applyChromaKey(image, {
          ...defaultEffect,
          keyColor: {r: 0, g: 0, b: 0},
          tolerance: 18,
          softness: 8,
          mode: 'connected',
          seedMode,
          seedPoints: [{x: 11, y: 13}, {x: 24, y: 20}],
          connectivity
        });
        const t = (56 / 255 - 0.1) / 0.16;
        const fringeAlpha = Math.round(255 * t * t * (3 - 2 * t));
        const expected = new Uint8ClampedArray(image.data);
        for (const index of members) expected[index * 4 + 3] = 0;
        for (const index of fringe) expected[index * 4 + 3] = fringeAlpha;
        expect(result.data).toEqual(expected);
      }
    });

    it('does not flood across already transparent pixels', () => {
      const image = new ImageData(new Uint8ClampedArray([
        0, 0, 255, 255, 0, 0, 255, 0, 0, 0, 255, 255
      ]), 3, 1);
      const result = applyChromaKey(image, {
        ...defaultEffect,
        mode: 'connected',
        seedMode: 'points',
        seedPoints: [{x: 0, y: 0}]
      });
      expect([result.data[3], result.data[7], result.data[11]]).toEqual([0, 0, 255]);
    });

    it('differentiates global and connected modes with enclosed islands', () => {
      // 5x5 image:
      // Outer border is green.
      // Layer 1 ring is red wall.
      // Center (2, 2) is enclosed green.
      const image = new ImageData(5, 5);
      for (let y = 0; y < 5; y++) {
        for (let x = 0; x < 5; x++) {
          const i = (y * 5 + x) * 4;
          const isOuter = x === 0 || y === 0 || x === 4 || y === 4;
          const isCenter = x === 2 && y === 2;
          image.data.set(isOuter || isCenter ? [0, 255, 0, 255] : [220, 20, 30, 255], i);
        }
      }

      const connected = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 20,
        softness: 0,
        mode: 'connected',
        seedMode: 'border'
      });

      // Outer border green is keyed out
      expect(connected.data[3]).toBe(0);
      // Red wall is preserved
      expect(connected.data[(1 * 5 + 1) * 4 + 3]).toBe(255);
      // Enclosed green is preserved in connected mode
      expect(connected.data[(2 * 5 + 2) * 4 + 3]).toBe(255);

      const global = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 20,
        softness: 0,
        mode: 'global'
      });

      // Outer border green is keyed out
      expect(global.data[3]).toBe(0);
      // Enclosed green is ALSO keyed out in global mode
      expect(global.data[(2 * 5 + 2) * 4 + 3]).toBe(0);
    });

    it('keys out from clicked seed points instead of border', () => {
      // 5x3 image:
      // x=0,1: green region A
      // x=2: red barrier
      // x=3,4: green region B
      const image = new ImageData(5, 3);
      for (let y = 0; y < 3; y++) {
        for (let x = 0; x < 5; x++) {
          const i = (y * 5 + x) * 4;
          image.data.set(x === 2 ? [220, 20, 30, 255] : [0, 255, 0, 255], i);
        }
      }

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 20,
        softness: 0,
        mode: 'connected',
        seedMode: 'points',
        seedPoints: [{x: 4, y: 1}]
      });

      // Region A is not reached
      expect(result.data[(1 * 5 + 0) * 4 + 3]).toBe(255);
      // Barrier is not reached
      expect(result.data[(1 * 5 + 2) * 4 + 3]).toBe(255);
      // Region B is keyed out
      expect(result.data[(1 * 5 + 4) * 4 + 3]).toBe(0);
    });

    it('respects 4-way vs 8-way connectivity across diagonals', () => {
      // 2x2 image:
      // (0, 0): green
      // (1, 0): red barrier
      // (0, 1): red barrier
      // (1, 1): green
      const image = new ImageData(2, 2);
      image.data.set([
        0, 255, 0, 255,    220, 20, 30, 255,
        220, 20, 30, 255,  0, 255, 0, 255
      ]);

      const conn4 = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 20,
        softness: 0,
        mode: 'connected',
        seedMode: 'points',
        seedPoints: [{x: 0, y: 0}],
        connectivity: 4
      });

      // (0, 0) keyed out
      expect(conn4.data[3]).toBe(0);
      // (1, 1) cannot be reached via 4-connectivity
      expect(conn4.data[(1 * 2 + 1) * 4 + 3]).toBe(255);

      const conn8 = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 20,
        softness: 0,
        mode: 'connected',
        seedMode: 'points',
        seedPoints: [{x: 0, y: 0}],
        connectivity: 8
      });

      // (0, 0) keyed out
      expect(conn8.data[3]).toBe(0);
      // (1, 1) IS reached via diagonal 8-connectivity and keyed out
      expect(conn8.data[(1 * 2 + 1) * 4 + 3]).toBe(0);
    });
  });

  describe('inverted mask semantics', () => {
    it('inverts selection in global mode: preserves matching color and removes other colors', () => {
      const image = new ImageData(3, 1);
      image.data.set([
        0, 255, 0, 255,    // Green (key color)
        220, 20, 30, 255,  // Red
        0, 0, 255, 255     // Blue
      ]);

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 20,
        softness: 0,
        mode: 'global',
        inverted: true
      });

      // Key color is KEPT
      expect(result.data[3]).toBe(255);
      // Non-matching colors are REMOVED
      expect(result.data[7]).toBe(0);
      expect(result.data[11]).toBe(0);
    });

    it('inverts selection in connected mode: keeps connected region and removes everything else', () => {
      // 5x5 image with outer green, red ring, and enclosed green center
      const image = new ImageData(5, 5);
      for (let y = 0; y < 5; y++) {
        for (let x = 0; x < 5; x++) {
          const i = (y * 5 + x) * 4;
          const isOuter = x === 0 || y === 0 || x === 4 || y === 4;
          const isCenter = x === 2 && y === 2;
          image.data.set(isOuter || isCenter ? [0, 255, 0, 255] : [220, 20, 30, 255], i);
        }
      }

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 20,
        softness: 0,
        mode: 'connected',
        seedMode: 'border',
        inverted: true
      });

      // Connected outer green is KEPT
      expect(result.data[3]).toBe(255);
      // Red ring is REMOVED
      expect(result.data[(1 * 5 + 1) * 4 + 3]).toBe(0);
      // Enclosed green is NOT connected, so it is REMOVED
      expect(result.data[(2 * 5 + 2) * 4 + 3]).toBe(0);
    });

    it('inverts soft edge transition smoothly', () => {
      const image = new ImageData(3, 1);
      // KeyColor: black [0, 0, 0], tolerance: 40, softness: 20
      // inner = 0.20 (v ~ 51), outer = 0.60 (v ~ 153)
      image.data.set([
        25, 25, 25, 255,     // dist ~ 0.098 <= inner
        102, 102, 102, 255,  // dist ~ 0.400 (transition)
        180, 180, 180, 255   // dist ~ 0.706 >= outer
      ]);

      const normal = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 40,
        softness: 20,
        mode: 'global',
        inverted: false
      });

      const inverted = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 40,
        softness: 20,
        mode: 'global',
        inverted: true
      });

      expect(normal.data[3]).toBe(0);
      expect(inverted.data[3]).toBe(255);

      expect(normal.data[11]).toBe(255);
      expect(inverted.data[11]).toBe(0);

      // In the transition zone: inverted alpha + normal alpha ~ 255
      expect(normal.data[7] + inverted.data[7]).toBe(255);
    });

    it('preserves pre-existing source transparency when inverted', () => {
      const image = new ImageData(3, 1);
      image.data.set([
        0, 255, 0, 255,   // Green opaque
        220, 20, 30, 0,   // Red transparent
        0, 255, 0, 0      // Green transparent
      ]);

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        tolerance: 20,
        softness: 0,
        mode: 'global',
        inverted: true
      });

      expect(result.data[3]).toBe(255);
      // Pre-existing transparent pixels must NEVER become opaque
      expect(result.data[7]).toBe(0);
      expect(result.data[11]).toBe(0);
    });
  });

  describe('disabled semantics and source immutability', () => {
    it.each([false, true])('scales existing partial alpha with the mask when inverted=%s', inverted => {
      const image = new ImageData(new Uint8ClampedArray([
        0, 0, 0, 128, 102, 102, 102, 128, 180, 180, 180, 128, 0, 0, 0, 0
      ]), 4, 1);
      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 0, b: 0},
        tolerance: 40,
        softness: 20,
        inverted
      });
      expect([result.data[3], result.data[7], result.data[11], result.data[15]])
        .toEqual(inverted ? [128, 64, 0, 0] : [0, 64, 128, 0]);
    });

    it('returns a clone and does not modify source when disabled', () => {
      const image = new ImageData(2, 1);
      image.data.set([0, 255, 0, 255, 220, 20, 30, 128]);
      const originalCopy = new Uint8ClampedArray(image.data);

      const result = applyChromaKey(image, {
        ...defaultEffect,
        enabled: false
      });

      // Output equals source
      expect(result.data).toEqual(originalCopy);
      // Output is not the same reference
      expect(result).not.toBe(image);
      expect(result.data).not.toBe(image.data);

      // Mutating output does not affect source
      result.data[0] = 99;
      expect(image.data[0]).toBe(0);
      // Source was not mutated during processing
      expect(image.data).toEqual(originalCopy);
    });

    it('does not mutate source data when enabled', () => {
      const image = new ImageData(2, 1);
      image.data.set([0, 255, 0, 255, 220, 20, 30, 255]);
      const originalCopy = new Uint8ClampedArray(image.data);

      const result = applyChromaKey(image, {
        ...defaultEffect,
        keyColor: {r: 0, g: 255, b: 0},
        enabled: true
      });

      expect(result.data[3]).toBe(0);
      expect(image.data).toEqual(originalCopy);
    });
  });
});
