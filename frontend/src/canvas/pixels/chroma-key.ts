import type {ChromaKeyEffect} from '../model/canvas-types';
const colorDistance = (data: Uint8ClampedArray, offset: number, color: ChromaKeyEffect['keyColor']) => { const dr = (data[offset] - color.r) / 255; const dg = (data[offset + 1] - color.g) / 255; const db = (data[offset + 2] - color.b) / 255; return Math.sqrt((dr * dr + dg * dg + db * db) / 3); };
const smoothstep = (edge0: number, edge1: number, value: number) => { const t = Math.min(1, Math.max(0, (value - edge0) / Math.max(0.0001, edge1 - edge0))); return t * t * (3 - 2 * t); };
export function applyChromaKey(source: ImageData, effect: ChromaKeyEffect) {
  if (!effect.enabled) return new ImageData(new Uint8ClampedArray(source.data), source.width, source.height);
  const {width, height, data: sourceData} = source; const output = new Uint8ClampedArray(sourceData); const inner = Math.max(0, effect.tolerance - effect.softness) / 100; const outer = Math.min(1, effect.tolerance + effect.softness) / 100; const candidates = new Uint8Array(width * height);
  for (let index = 0; index < candidates.length; index += 1) { const offset = index * 4; if (sourceData[offset + 3] > 0 && colorDistance(sourceData, offset, effect.keyColor) <= outer) candidates[index] = 1; }
  const members = effect.mode === 'global' ? candidates : connectedMembers(candidates, width, height, effect);
  for (let index = 0; index < members.length; index += 1) { const offset = index * 4; const selected = Boolean(members[index]) !== effect.inverted; if (!selected) continue; const distance = colorDistance(sourceData, offset, effect.keyColor); const keyAlpha = smoothstep(inner, outer, distance); const sourceAlpha = sourceData[offset + 3]; output[offset + 3] = Math.round(sourceAlpha * keyAlpha); if (effect.spillSuppression > 0 && output[offset + 3] > 0 && output[offset + 3] < sourceAlpha) { const strength = (effect.spillSuppression / 100) * (1 - keyAlpha); const neutral = (sourceData[offset] + sourceData[offset + 1] + sourceData[offset + 2]) / 3; output[offset] = Math.round(sourceData[offset] + (neutral - effect.keyColor.r) * strength * 0.25); output[offset + 1] = Math.round(sourceData[offset + 1] + (neutral - effect.keyColor.g) * strength * 0.25); output[offset + 2] = Math.round(sourceData[offset + 2] + (neutral - effect.keyColor.b) * strength * 0.25); } }
  return new ImageData(output, width, height);
}
function connectedMembers(candidates: Uint8Array, width: number, height: number, effect: ChromaKeyEffect) {
  const members = new Uint8Array(candidates.length); const queue: number[] = []; const add = (x: number, y: number) => { if (x < 0 || y < 0 || x >= width || y >= height) return; const index = y * width + x; if (!candidates[index] || members[index]) return; members[index] = 1; queue.push(index); };
  if (effect.seedMode === 'points') effect.seedPoints.forEach(point => add(Math.round(point.x), Math.round(point.y))); else { for (let x = 0; x < width; x += 1) { add(x, 0); add(x, height - 1); } for (let y = 0; y < height; y += 1) { add(0, y); add(width - 1, y); } }
  const neighbors = effect.connectivity === 8 ? [[-1,-1],[0,-1],[1,-1],[-1,0],[1,0],[-1,1],[0,1],[1,1]] : [[0,-1],[-1,0],[1,0],[0,1]];
  for (let cursor = 0; cursor < queue.length; cursor += 1) { const index = queue[cursor]; const x = index % width; const y = Math.floor(index / width); neighbors.forEach(([dx, dy]) => add(x + dx, y + dy)); }
  return members;
}
