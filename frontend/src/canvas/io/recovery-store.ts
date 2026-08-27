import {openDB} from 'idb';
import type {CanvasDocument} from '../model/canvas-types';
import type {BitmapSnapshot, BitmapStore} from '../model/bitmap-store';

const DB_NAME='ai-scratch-canvas';
const DOCUMENTS='documents';
const BITMAP_REVISIONS='bitmapRevisions';
const RECOVERY_HEADS='recoveryHeads';
const HEAD='latest';

interface BitmapReference {key:string;id:string;width:number;height:number;revision:number;byteLength:number}
interface DocumentManifest {schemaVersion:1;document:CanvasDocument;bitmapRefs:BitmapReference[];savedAt:number}
interface RecoveryHead {documentId:string;bitmapKeys:string[];savedAt:number}
export interface RecoveryRecord {schemaVersion:1;document:CanvasDocument;bitmaps:BitmapSnapshot[];savedAt:number}

async function database(){return openDB(DB_NAME,2,{upgrade(db){if(!db.objectStoreNames.contains(DOCUMENTS))db.createObjectStore(DOCUMENTS);if(!db.objectStoreNames.contains(BITMAP_REVISIONS))db.createObjectStore(BITMAP_REVISIONS);if(!db.objectStoreNames.contains(RECOVERY_HEADS))db.createObjectStore(RECOVERY_HEADS);}});}
const plainDocument=(document:CanvasDocument)=>JSON.parse(JSON.stringify(document)) as CanvasDocument;

export async function saveRecovery(document:CanvasDocument,bitmaps:BitmapStore){
  const db=await database();const snapshots=bitmaps.snapshot();const savedAt=Date.now();const refs=snapshots.map(item=>({key:document.id+':'+item.id+':'+item.revision,id:item.id,width:item.width,height:item.height,revision:item.revision,byteLength:item.data.byteLength}));
  const oldHead=await db.get(RECOVERY_HEADS,HEAD) as RecoveryHead|undefined;
  const tx=db.transaction([DOCUMENTS,BITMAP_REVISIONS,RECOVERY_HEADS],'readwrite');
  for(let index=0;index<snapshots.length;index+=1)await tx.objectStore(BITMAP_REVISIONS).put(snapshots[index],refs[index].key);
  const manifest:DocumentManifest={schemaVersion:1,document:plainDocument(document),bitmapRefs:refs,savedAt};
  await tx.objectStore(DOCUMENTS).put(manifest,document.id);
  await tx.objectStore(RECOVERY_HEADS).put({documentId:document.id,bitmapKeys:refs.map(ref=>ref.key),savedAt} satisfies RecoveryHead,HEAD);
  await tx.done;
  if(oldHead){const retained=new Set(refs.map(ref=>ref.key));const obsolete=oldHead.bitmapKeys.filter(key=>!retained.has(key));if(obsolete.length){const cleanup=db.transaction(BITMAP_REVISIONS,'readwrite');for(const key of obsolete)await cleanup.store.delete(key);await cleanup.done;}}
}

export async function loadRecovery():Promise<RecoveryRecord|undefined>{
  const db=await database();const head=await db.get(RECOVERY_HEADS,HEAD) as RecoveryHead|undefined;if(!head)return undefined;const manifest=await db.get(DOCUMENTS,head.documentId) as DocumentManifest|undefined;if(!manifest||manifest.schemaVersion!==1)throw new Error('画布恢复清单已损坏');
  const bitmaps:BitmapSnapshot[]=[];for(const ref of manifest.bitmapRefs){const stored=await db.get(BITMAP_REVISIONS,ref.key) as BitmapSnapshot|undefined;if(!stored||stored.id!==ref.id||stored.width!==ref.width||stored.height!==ref.height||stored.revision!==ref.revision||stored.data.byteLength!==ref.byteLength||stored.data.length!==ref.width*ref.height*4)throw new Error('画布恢复位图不完整');bitmaps.push({...stored,data:new Uint8ClampedArray(stored.data)});}
  return{schemaVersion:1,document:plainDocument(manifest.document),bitmaps,savedAt:manifest.savedAt};
}

export async function clearRecovery(){const db=await database();const tx=db.transaction([DOCUMENTS,BITMAP_REVISIONS,RECOVERY_HEADS],'readwrite');await Promise.all([tx.objectStore(DOCUMENTS).clear(),tx.objectStore(BITMAP_REVISIONS).clear(),tx.objectStore(RECOVERY_HEADS).clear()]);await tx.done;}
