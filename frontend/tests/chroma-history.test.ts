import {describe, expect, it, vi} from 'vitest';
import {HistoryManager} from '../src/canvas/history/history-manager';
import {BitmapStore} from '../src/canvas/model/bitmap-store';
import type {CanvasDocument} from '../src/canvas/model/canvas-types';

const document = ():CanvasDocument => ({schemaVersion:1,id:'doc',name:'before',createdAt:1,updatedAt:1,artboard:{x:0,y:0,width:3072,height:3072},layers:[],costumeEntries:[],activeLayerIds:[],selection:null,viewport:{x:0,y:0,zoom:1},source:{kind:'upload'}});
describe('metadata-only chroma history',()=>{
  it('records and restores effect metadata without copying or scanning bitmap buffers',()=>{
    const history=new HistoryManager(),bitmaps=new BitmapStore(),doc=document();
    const snapshot=vi.spyOn(bitmaps,'snapshot').mockImplementation(()=>{throw new Error('unexpected full bitmap copy');});
    history.beginMetadata('key color',doc);doc.name='after';history.commit(doc,bitmaps);
    expect(history.canUndo).toBe(true);expect(history.undo(bitmaps)?.name).toBe('before');
    expect(history.redo(bitmaps)?.name).toBe('after');expect(snapshot).not.toHaveBeenCalled();
  });
  it('rolls back an unfinished parameter edit without replacing the bitmap store',()=>{
    const history=new HistoryManager(),bitmaps=new BitmapStore(),doc=document();
    const restore=vi.spyOn(bitmaps,'restore');history.beginMetadata('preview',doc);doc.name='preview';
    expect(history.rollback(bitmaps)?.name).toBe('before');expect(restore).not.toHaveBeenCalled();expect(history.canUndo).toBe(false);
  });
});
