import type {CanvasDocument} from '../model/canvas-types';
import type {BitmapSnapshot, BitmapStore} from '../model/bitmap-store';
import {applyBitmapDiffs,createBitmapDiffs,type BitmapDiff} from './pixel-tile-diff';
interface HistoryEntry {label:string;before:CanvasDocument;after:CanvasDocument;bitmapDiffs:BitmapDiff[]}
const copyDocument=(document:CanvasDocument)=>JSON.parse(JSON.stringify(document)) as CanvasDocument;
export class HistoryManager {
  private undoStack:HistoryEntry[]=[];private redoStack:HistoryEntry[]=[];private pending:{label:string;document:CanvasDocument;bitmaps:BitmapSnapshot[]|null}|null=null;
  constructor(private readonly limit=100){}
  begin(label:string,document:CanvasDocument,bitmaps:BitmapStore){if(!this.pending)this.pending={label,document:copyDocument(document),bitmaps:bitmaps.snapshot()};}
  beginMetadata(label:string,document:CanvasDocument){if(!this.pending)this.pending={label,document:copyDocument(document),bitmaps:null};}
  commit(document:CanvasDocument,bitmaps:BitmapStore){if(!this.pending)return;this.undoStack.push({label:this.pending.label,before:this.pending.document,after:copyDocument(document),bitmapDiffs:this.pending.bitmaps?createBitmapDiffs(this.pending.bitmaps,bitmaps.snapshot()):[]});this.pending=null;this.redoStack=[];if(this.undoStack.length>this.limit)this.undoStack.shift();}
  rollback(bitmaps:BitmapStore){if(!this.pending)return null;const before=this.pending;this.pending=null;if(before.bitmaps)bitmaps.restore(before.bitmaps);return copyDocument(before.document);}
  cancel(){this.pending=null;}
  reset(){this.pending=null;this.undoStack=[];this.redoStack=[];}
  undo(bitmaps:BitmapStore){const entry=this.undoStack.pop();if(!entry)return null;this.redoStack.push(entry);applyBitmapDiffs(bitmaps,entry.bitmapDiffs,'undo');return copyDocument(entry.before);}
  redo(bitmaps:BitmapStore){const entry=this.redoStack.pop();if(!entry)return null;this.undoStack.push(entry);applyBitmapDiffs(bitmaps,entry.bitmapDiffs,'redo');return copyDocument(entry.after);}
  get canUndo(){return this.undoStack.length>0;}get canRedo(){return this.redoStack.length>0;}get undoLabel(){return this.undoStack.at(-1)?.label??'';}get redoLabel(){return this.redoStack.at(-1)?.label??'';}
}
