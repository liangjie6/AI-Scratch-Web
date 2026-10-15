import type {ChromaKeyEffect} from '../model/canvas-types';

// Compare raw squared RGB distances in the hot path. Only partially keyed edge
// pixels need the square root used by the existing smoothstep color transition.
const MAX_DISTANCE_SQUARED = 3 * 255 * 255;
const colorDistanceSquared = (data: Uint8ClampedArray, offset: number, color: ChromaKeyEffect['keyColor']) => {
  const dr = data[offset] - color.r;
  const dg = data[offset + 1] - color.g;
  const db = data[offset + 2] - color.b;
  return dr * dr + dg * dg + db * db;
};

export function applyChromaKey(source: ImageData, effect: ChromaKeyEffect) {
  const {width, height, data: sourceData} = source;
  const output = new Uint8ClampedArray(sourceData);
  if (!effect.enabled) return new ImageData(output, width, height);

  const tol = Math.max(0, Math.min(100, Number.isFinite(effect.tolerance) ? effect.tolerance : 0)) / 100;
  const soft = Math.max(0, Math.min(100, Number.isFinite(effect.softness) ? effect.softness : 0)) / 100;
  const inner = tol >= 1 ? 1 : Math.max(0, tol - soft);
  const outer = soft === 0 ? tol : Math.min(1, tol + soft);
  const innerSquared = inner * inner * MAX_DISTANCE_SQUARED;
  const outerSquared = outer * outer * MAX_DISTANCE_SQUARED;
  const toleranceSquared = tol * tol * MAX_DISTANCE_SQUARED;

  const applyPixel = (offset: number, distanceSquared: number) => {
    const sourceAlpha = sourceData[offset + 3];
    let keyAlpha: number;
    if (distanceSquared <= innerSquared) keyAlpha = 0;
    else if (distanceSquared >= outerSquared || outer <= inner) keyAlpha = 1;
    else {
      const distance = Math.sqrt(distanceSquared / MAX_DISTANCE_SQUARED);
      const t = (distance - inner) / (outer - inner);
      keyAlpha = t * t * (3 - 2 * t);
    }

    output[offset + 3] = Math.round(sourceAlpha * (effect.inverted ? 1 - keyAlpha : keyAlpha));

    if (!effect.inverted && effect.spillSuppression > 0 && output[offset + 3] > 0 && output[offset + 3] < sourceAlpha) {
      const strength = (effect.spillSuppression / 100) * (1 - keyAlpha);
      const neutral = (sourceData[offset] + sourceData[offset + 1] + sourceData[offset + 2]) / 3;
      output[offset] = Math.round(sourceData[offset] + (neutral - effect.keyColor.r) * strength * 0.25);
      output[offset + 1] = Math.round(sourceData[offset + 1] + (neutral - effect.keyColor.g) * strength * 0.25);
      output[offset + 2] = Math.round(sourceData[offset + 2] + (neutral - effect.keyColor.b) * strength * 0.25);
    }
  };

  if (effect.mode === 'global') {
    for (let offset = 0; offset < sourceData.length; offset += 4) {
      if (sourceData[offset + 3] === 0) continue;
      const distanceSquared = colorDistanceSquared(sourceData, offset, effect.keyColor);
      if (distanceSquared <= outerSquared) applyPixel(offset, distanceSquared);
      else if (effect.inverted) output[offset + 3] = 0;
    }
  } else {
    // Outside the connected region the normal mask keeps the source, while the
    // inverted mask removes it. Preserve original alpha when applying members.
    if (effect.inverted) {
      for (let offset = 3; offset < output.length; offset += 4) output[offset] = 0;
    }
    applyConnectedRegion(source, effect, toleranceSquared, outerSquared, applyPixel);
  }

  return new ImageData(output, width, height);
}

function applyConnectedRegion(
  source: ImageData,
  effect: ChromaKeyEffect,
  toleranceSquared: number,
  outerSquared: number,
  applyPixel: (offset: number, distanceSquared: number) => void
) {
  const {width, height, data} = source;
  const pixelCount = width * height;
  if (!pixelCount) return;
  const visited = new Uint8Array(pixelCount);

  // A growable typed ring holds pairs of endpoints for horizontal spans. Large
  // solid backgrounds do not require an enqueue/dequeue for every single pixel.
  let queue = new Uint32Array(Math.min(pixelCount * 2, 4096));
  let head = 0;
  let count = 0;
  const enqueue = (index: number) => {
    if (count === queue.length) {
      const expanded = new Uint32Array(Math.min(pixelCount * 2, queue.length * 2));
      expanded.set(queue.subarray(head));
      expanded.set(queue.subarray(0, head), queue.length - head);
      queue = expanded;
      head = 0;
    }
    queue[(head + count) % queue.length] = index;
    count += 1;
  };

  const visit = (index: number, seed = false) => {
    if (visited[index]) return false;
    const offset = index * 4;
    if (data[offset + 3] === 0) {
      visited[index] = 1;
      return false;
    }
    const distanceSquared = colorDistanceSquared(data, offset, effect.keyColor);
    if (distanceSquared <= toleranceSquared) {
      visited[index] = 1;
      applyPixel(offset, distanceSquared);
      return true;
    } else if (!seed) {
      visited[index] = 1;
      // Feather just the immediate fringe. These pixels never enter the flood
      // queue, so a soft color bridge cannot reach another matching island.
      if (distanceSquared < outerSquared) applyPixel(offset, distanceSquared);
    }
    return false;
  };

  const fillSpan = (index: number, seed = false) => {
    if (!visit(index, seed)) return index;
    const rowStart = index - index % width;
    const rowEnd = rowStart + width - 1;
    let left = index;
    let right = index;
    while (left > rowStart && visit(left - 1)) left -= 1;
    while (right < rowEnd && visit(right + 1)) right += 1;
    enqueue(left);
    enqueue(right);
    return right;
  };

  if (effect.seedMode === 'points') {
    for (const point of effect.seedPoints || []) {
      const x = Math.round(point.x);
      const y = Math.round(point.y);
      if (x >= 0 && y >= 0 && x < width && y < height) fillSpan(y * width + x, true);
    }
  } else {
    for (let x = 0; x < width; x += 1) {
      fillSpan(x, true);
      fillSpan((height - 1) * width + x, true);
    }
    for (let y = 1; y < height - 1; y += 1) {
      fillSpan(y * width, true);
      fillSpan(y * width + width - 1, true);
    }
  }

  const diagonal = effect.connectivity === 8;
  while (count > 0) {
    const left = queue[head];
    const right = queue[(head + 1) % queue.length];
    head = (head + 2) % queue.length;
    count -= 2;
    const rowStart = left - left % width;
    const start = diagonal && left > rowStart ? left - 1 : left;
    const end = diagonal && right < rowStart + width - 1 ? right + 1 : right;
    if (rowStart > 0) {
      for (let index = start - width; index <= end - width; index += 1) index = fillSpan(index);
    }
    if (rowStart + width < pixelCount) {
      for (let index = start + width; index <= end + width; index += 1) index = fillSpan(index);
    }
  }
}
