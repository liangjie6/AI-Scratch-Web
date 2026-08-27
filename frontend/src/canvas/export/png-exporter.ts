import {createZip} from './zip-client';
import type {BitmapStore} from '../model/bitmap-store';
import type {CanvasDocument} from '../model/canvas-types';
import {canvasToBlob, rasterizeLayers} from '../pixels/rasterize';
import {sanitizeFileName} from '../model/canvas-types';
import {exportCostumeSet} from './costume-exporter';
function download(blob: Blob, name: string) { const url = URL.createObjectURL(blob); const link = document.createElement('a'); link.href = url; link.download = name; link.click(); window.setTimeout(() => URL.revokeObjectURL(url), 0); }
export async function downloadComposite(document: CanvasDocument, bitmaps: BitmapStore) { const layers = document.layers.filter(layer => layer.visible); if (!layers.length) throw new Error('没有可见图层可导出'); const blob = await canvasToBlob(rasterizeLayers(document, bitmaps, document.artboard, layers)); download(blob, sanitizeFileName(document.name) + '.png'); }
export async function downloadCostumeZip(document: CanvasDocument, bitmaps: BitmapStore) { const costumes = await exportCostumeSet(document, bitmaps); const names = new Set<string>(); const entries: Record<string, Uint8Array> = {}; for (let index = 0; index < costumes.length; index += 1) { let base = sanitizeFileName(costumes[index].name, '造型' + (index + 1)); const extension=costumes[index].dataFormat; let name = String(index + 1).padStart(2, '0') + '-' + base + '.'+extension; let suffix = 2; while (names.has(name)) name = String(index + 1).padStart(2, '0') + '-' + base + '-' + suffix++ + '.'+extension; names.add(name); entries[name] = new Uint8Array(await costumes[index].blob.arrayBuffer()); } download(new Blob([await createZip(entries)], {type: 'application/zip'}), sanitizeFileName(document.name) + '-造型.zip'); }
