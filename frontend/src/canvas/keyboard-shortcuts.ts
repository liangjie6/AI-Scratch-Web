import type {CanvasTool} from './model/canvas-types';

export type CanvasShortcut =
  | {kind: 'tool'; tool: CanvasTool}
  | {kind: 'undo'}
  | {kind: 'redo'}
  | {kind: 'copy-selection'; cut: boolean}
  | {kind: 'fit-view'}
  | {kind: 'actual-size'}
  | {kind: 'escape'};

type ShortcutEvent = Pick<KeyboardEvent, 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>;

const toolByCode: Partial<Record<string, CanvasTool>> = {
  KeyV: 'move',
  KeyT: 'transform',
  KeyM: 'select',
  KeyC: 'crop',
  KeyK: 'chroma',
  KeyB: 'brush',
  KeyN: 'pencil',
  KeyE: 'eraser',
  KeyI: 'eyedropper',
  KeyH: 'hand'
};

export function resolveCanvasShortcut(event: ShortcutEvent): CanvasShortcut | null {
  const primary = event.ctrlKey || event.metaKey;

  if (primary && !event.altKey) {
    if (event.code === 'KeyZ') return {kind: event.shiftKey ? 'redo' : 'undo'};
    if (event.code === 'KeyY') return {kind: 'redo'};
    if (event.code === 'Digit0') return {kind: 'fit-view'};
    if (event.code === 'Digit1') return {kind: 'actual-size'};
    return null;
  }

  if (event.altKey) {
    if (primary) return null;
    return event.code === 'KeyJ' ? {kind: 'copy-selection', cut: event.shiftKey} : null;
  }

  if (event.code === 'Escape') return {kind: 'escape'};
  if (event.shiftKey) return null;
  const tool = toolByCode[event.code];
  return tool ? {kind: 'tool', tool} : null;
}
