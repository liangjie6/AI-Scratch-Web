export interface Point { x: number; y: number }
export interface Rect extends Point { width: number; height: number }
export interface Matrix2D { a: number; b: number; c: number; d: number; e: number; f: number }
export type CanvasTool = 'move' | 'transform' | 'select' | 'crop' | 'chroma' | 'brush' | 'eraser' | 'fill' | 'eyedropper' | 'hand' | 'zoom';
export interface ChromaKeyEffect { keyColor: {r: number; g: number; b: number}; tolerance: number; softness: number; mode: 'connected' | 'global'; seedMode: 'border' | 'points'; connectivity: 4 | 8; spillSuppression: number; inverted: boolean; seedPoints: Point[]; enabled: boolean }
export interface RasterLayer { id: string; name: string; bitmapId: string; bounds: Rect; transform: Matrix2D; visible: boolean; locked: boolean; opacity: number; blendMode: 'normal'; chromaKey: ChromaKeyEffect | null; createdAt: number; updatedAt: number }
export interface CostumeEntry { id: string; layerId: string; name: string; order: number; frameRect: Rect; pivot: Point; alignment: 'center' | 'bottom-center' | 'custom'; exportScale: number; enabled: boolean; manuallyNamed?: boolean }
export interface ViewportState { x: number; y: number; zoom: number }
export interface SelectionState { rect: Rect; mode: 'replace' | 'add' | 'subtract' | 'intersect' }
export interface ProcessingHints { backgroundMode?: 'solid' | 'transparent'; suggestedKeyColor?: string; sheetLayout?: {columns: number; rows: number} }
export type CanvasInput = | {kind: 'blob'; blob: Blob; fileName?: string; source?: 'upload' | 'drop' | 'clipboard'} | {kind: 'base64'; base64: string; mimeType: 'image/png'; target?: 'backdrop' | 'sprite'; requestId?: string; processingHints?: ProcessingHints};
export interface CanvasDocument { schemaVersion: 1; id: string; name: string; createdAt: number; updatedAt: number; artboard: Rect; layers: RasterLayer[]; costumeEntries: CostumeEntry[]; activeLayerIds: string[]; selection: SelectionState | null; viewport: ViewportState; source: {kind: 'ai' | 'upload' | 'drop' | 'clipboard' | 'scratch'; fileName?: string; requestId?: string; processingHints?: ProcessingHints; targetId?: string; targetType?: 'backdrop' | 'sprite'} }
export const IDENTITY_MATRIX: Matrix2D = {a: 1, b: 0, c: 0, d: 1, e: 0, f: 0};
export const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));
export const normalizeRect = (start: Point, end: Point): Rect => ({x: Math.min(start.x, end.x), y: Math.min(start.y, end.y), width: Math.abs(end.x - start.x), height: Math.abs(end.y - start.y)});
export const sanitizeFileName = (name: string, fallback = '未命名') => (name.replace(/[<>:"/\\|?*\u0000-\u001f]/g, '').trim() || fallback).slice(0, 120);
