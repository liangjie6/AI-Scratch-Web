<script setup lang="ts">
import {computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch} from 'vue';
import {ArrowLeft, Download, FileArchive, FolderInput, ImagePlus, Images, Link, Unlink, LoaderCircle, Redo2, SlidersHorizontal, Undo2, Upload, X} from 'lucide-vue-next';
import CanvasToolbar from './components/CanvasToolbar.vue';
import LayersPanel from './components/LayersPanel.vue';
import CostumesPanel from './components/CostumesPanel.vue';
import CanvasAssetImportDialog from './components/CanvasAssetImportDialog.vue';
import type {GeneratedAsset} from '../ai/asset-library-store';
import {createPaintStroke, type PaintStrokeSession} from './pixels/paint-stroke';
import {fillLayerAt} from './pixels/paint-fill';
import {bitmapPoint} from './pixels/paint-geometry';
import ScratchImportDialog from './components/ScratchImportDialog.vue';
import ScratchExportDialog from './components/ScratchExportDialog.vue';
import {BitmapStore} from './model/bitmap-store';
import type {CanvasDocument, CanvasInput, CanvasTool, ChromaKeyEffect, Matrix2D, Point, RasterLayer, Rect} from './model/canvas-types';
import {clamp, IDENTITY_MATRIX, normalizeRect} from './model/canvas-types';
import {createId} from './model/create-id';
import {activeLayer, applyLayerTransform, copySelectionToLayer, cropActiveLayer, deleteActiveLayer, duplicateLayer, hitTestLayer, reorderLayer, unifyCostumeFrames} from './model/document-store';
import {CONTROL_HANDLES, hitControl, layerControlPoints, rectControlPoints, transformedRectBounds, type ControlHandle, type ControlPoints} from './model/interaction-geometry';
import {preciseTransformedRectBounds, snapLayerTranslation, snapLayerResize, snapRectResize, snapRectTranslation, type AlignmentGuides} from './model/alignment-snapping';
import {HistoryManager} from './history/history-manager';
import {importCanvasImage} from './io/image-importer';
import {clearRecovery, loadRecovery, saveRecovery} from './io/recovery-store';
import {importScratchSelection, listScratchImportOptions, type ScratchImportOption} from './io/scratch-importer';
import {layerRasterCanvas, prepareLayerRaster, warmLayerRaster, prepareLayersRaster, cacheLayerRaster, retainPreparedLayerRaster, readLayerPixel} from './pixels/rasterize';
import {applyChromaKeyAsync, serializableChromaEffect} from './pixels/pixel-client';
import {downloadComposite, downloadCostumeZip} from './export/png-exporter';
import {exportCostumeSet} from './export/costume-exporter';
import {fitSpriteInitialSize, importBackdropCostume, importCostumeSet} from '../scratch/asset-adapter';
import {exportCanvasTarget} from './export/canvas-target-exporter';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import {resolveCanvasShortcut} from './keyboard-shortcuts';

export interface CanvasPreparedAsset {kind: 'sprite' | 'backdrop'; costumes?: Awaited<ReturnType<typeof exportCostumeSet>>; backdrop?: Awaited<ReturnType<typeof exportCanvasTarget>>}

const props=defineProps<{initialInput:CanvasInput|null;bridge:ScratchGuiBridge|null;handoffMode?: 'direct-import' | 'return-prepared-asset'}>(); const emit=defineEmits<{close:[];prepared: [payload: {kind: 'sprite' | 'backdrop'; costumes?: Awaited<ReturnType<typeof exportCostumeSet>>; backdrop?: Awaited<ReturnType<typeof exportCanvasTarget>>}]}>();
const documentState=ref<CanvasDocument|null>(null); const bitmaps=new BitmapStore(); const history=new HistoryManager(); const canvas=ref<HTMLCanvasElement|null>(null); const viewport=ref<HTMLElement|null>(null); const fileInput=ref<HTMLInputElement|null>(null); const tool=ref<CanvasTool>('move'); const panelTab=ref<'layers'|'costumes'>('layers'); const status=ref('导入图片或从 Scratch 导入以开始'); const busy=ref(false); const scratchImportOpen=ref(false); const scratchExportOpen=ref(false); const scratchOptions=ref<ScratchImportOption[]>([]); const zoomLabel=computed(()=>Math.round((documentState.value?.viewport.zoom??1)*100)+'%'); const active=computed(()=>documentState.value?activeLayer(documentState.value):null); const costumeLayerIds=computed(()=>new Set(documentState.value?.costumeEntries.map(entry=>entry.layerId)??[]));
const historyRevision=ref(0); const historyState=computed(()=>{void historyRevision.value;return{canUndo:history.canUndo,canRedo:history.canRedo,undoLabel:history.undoLabel,redoLabel:history.redoLabel};});
const assetImportOpen=ref(false); const assetImportError=ref(''); const aspectLocked=ref(true); const fillMode=ref<'region'|'all'>('region'); let paintStroke:PaintStrokeSession|null=null;
const previewCostumeId=ref<string|null>(null); const alignmentGuides=ref<AlignmentGuides>({x:[],y:[]}); const shortcutHelpOpen=ref(false); const inspectorOpen=ref(!window.matchMedia('(max-width: 700px)').matches); const chromaTolerance=ref(26); const chromaSoftness=ref(8); const chromaMode=ref<'connected'|'global'>('connected'); const spill=ref(35); const brushSize=ref(16); const paintColor=ref('#2f80ed'); const cropRect=ref<Rect|null>(null); const manualSelection=ref(false); let disposed=false; let resizeObserver:ResizeObserver|null=null; let recoveryTimer:number|null=null; let skipRecoveryOnUnmount=false; let frame=0; let spacePressed=false;
let pointer:{id:number;startScreen:Point;startWorld:Point;lastWorld:Point;mode:'pan'|'select'|'select-move'|'select-resize'|'crop-resize'|'crop-move'|'move'|'resize'|'draw';layerStart?:Point;layerId?:string;handle?:ControlHandle;transformStart?:Matrix2D;rectStart?:Rect;handleOffset?:Point}|null=null;

interface ChromaWork {
  document:CanvasDocument; layer:RasterLayer; bitmap:ReturnType<BitmapStore['get']>; revision:number;
  originalEffect:string; effect:ChromaKeyEffect; key:string; kind:'click'|'preview'; commit:boolean;
  controller:AbortController; timer:number|null; ready:boolean; result:ImageData|null; bakedSource:ImageData|null;
  done:Promise<void>; finish:()=>void;
}
const chromaProcessing=ref(false),rasterLoading=ref(false),rasterRevision=ref(0);
const chromaPreview=shallowRef<ChromaWork|null>(null);
let chromaWork:ChromaWork|null=null,rasterController:AbortController|null=null;
const effectKey=(effect:ChromaKeyEffect|null)=>JSON.stringify(effect?serializableChromaEffect(effect):null);
function syncChromaSettings(effect:ChromaKeyEffect){chromaTolerance.value=effect.tolerance;chromaSoftness.value=effect.softness;chromaMode.value=effect.mode;spill.value=effect.spillSuppression;paintColor.value=hex(effect.keyColor);}
function validChromaWork(work:ChromaWork){
  if(disposed||documentState.value!==work.document||!work.document.layers.includes(work.layer))return false;
  try{return bitmaps.get(work.layer.bitmapId)===work.bitmap&&work.bitmap.revision===work.revision&&effectKey(work.layer.chromaKey)===work.originalEffect;}catch{return false;}
}
function displayLayer(layer:RasterLayer){const preview=chromaPreview.value;return preview?.layer.id===layer.id&&validChromaWork(preview)?{...layer,chromaKey:preview.effect}:layer;}
function cancelChromaWork(){
  const work=chromaWork;chromaWork=null;
  if(work){if(work.timer!==null)window.clearTimeout(work.timer);work.controller.abort();work.finish();}
  chromaPreview.value=null;chromaProcessing.value=false;
}
function commitChromaWork(work:ChromaWork){
  if(chromaWork!==work||!work.ready||!validChromaWork(work))return;
  const doc=work.document,layer=work.layer;
  if(work.bakedSource)history.begin('保存色度抠图',doc,bitmaps);else history.beginMetadata(work.kind==='click'?'保存色度抠图':'调整色度抠图',doc);
  if(work.bakedSource)bitmaps.update(layer.bitmapId,work.bakedSource);
  layer.chromaKey=serializableChromaEffect(work.effect);layer.updatedAt=doc.updatedAt=Date.now();
  if(work.bakedSource)cacheLayerRaster(layer,bitmaps,work.result!);else retainPreparedLayerRaster(layer,bitmaps);
  history.commit(doc,bitmaps);chromaWork=null;chromaPreview.value=null;chromaProcessing.value=false;
  rasterRevision.value++;touch(work.kind==='click'?'保存色度抠图':'已调整色度抠图');work.finish();
}
function requestChromaWork(layer:RasterLayer,effect:ChromaKeyEffect,kind:'click'|'preview',commit:boolean){
  const doc=documentState.value;if(!doc||disposed)return;
  const bitmap=bitmaps.get(layer.bitmapId),key=effectKey(effect),current=chromaWork;
  if(current&&current.layer===layer&&current.key===key&&validChromaWork(current)){
    current.commit ||= commit;if(current.ready&&current.commit)commitChromaWork(current);return;
  }
  const previousPreview=chromaPreview.value;
  cancelChromaWork();
  if(kind==='preview'&&previousPreview&&validChromaWork(previousPreview))chromaPreview.value=previousPreview;
  let finish!:()=>void;const done=new Promise<void>(resolve=>{finish=resolve;});
  const work:ChromaWork={document:doc,layer,bitmap,revision:bitmap.revision,originalEffect:effectKey(layer.chromaKey),effect:serializableChromaEffect(effect),key,kind,commit,controller:new AbortController(),timer:null,ready:false,result:null,bakedSource:null,done,finish};
  chromaWork=work;chromaProcessing.value=true;status.value='正在处理抠图…';
  work.timer=window.setTimeout(()=>{work.timer=null;void runChromaWork(work);},kind==='preview'?40:0);
}
async function runChromaWork(work:ChromaWork){
  try{
    if(!validChromaWork(work)||chromaWork!==work)return;
    if(work.kind==='click'&&work.layer.chromaKey){
      work.bakedSource=await prepareLayerRaster(work.layer,bitmaps,{signal:work.controller.signal});
      if(!validChromaWork(work)||chromaWork!==work)return;
      work.result=await applyChromaKeyAsync(work.bakedSource,work.effect,{signal:work.controller.signal});
    }else await warmLayerRaster({...work.layer,chromaKey:work.effect},bitmaps,{signal:work.controller.signal});
    if(!validChromaWork(work)||chromaWork!==work)return;
    work.ready=true;
    if(work.commit)commitChromaWork(work);
    else{chromaPreview.value=work;chromaProcessing.value=false;rasterRevision.value++;status.value='已预览色度抠图';requestRender();}
  }catch(error){if(chromaWork===work&&!disposed){cancelChromaWork();if(!(error instanceof DOMException&&error.name==='AbortError'))status.value=error instanceof Error?error.message:'抠图处理失败';requestRender();}}
  finally{work.finish();if(chromaWork===work&&!validChromaWork(work))cancelChromaWork();}
}
async function settleChroma(){while(chromaWork){const work=chromaWork;if(!validChromaWork(work)){cancelChromaWork();continue;}work.commit=true;if(work.ready)commitChromaWork(work);await work.done;}}
async function prepareDocumentRasters(){
  rasterController?.abort();const controller=new AbortController(),doc=documentState.value;rasterController=controller;
  if(!doc)return;rasterLoading.value=doc.layers.some(layer=>layer.chromaKey?.enabled);
  try{await prepareLayersRaster(doc.layers,bitmaps,{signal:controller.signal});if(!disposed&&documentState.value===doc){rasterRevision.value++;requestRender();}}
  catch(error){if(!disposed&&!controller.signal.aborted)status.value=error instanceof Error?error.message:'抠图预览加载失败';}
  finally{if(rasterController===controller){rasterLoading.value=false;rasterController=null;}}
}

async function persistRecovery(){if(!documentState.value)return;try{await saveRecovery(documentState.value,bitmaps);}catch{}}
function scheduleRecovery(){if(disposed||!documentState.value)return;if(recoveryTimer!==null)window.clearTimeout(recoveryTimer);recoveryTimer=window.setTimeout(()=>{recoveryTimer=null;if(chromaProcessing.value||rasterLoading.value){scheduleRecovery();return;}void persistRecovery();},300);}
function touch(message?:string){if(disposed)return;historyRevision.value++;if(message)status.value=message; if(!pointer)scheduleRecovery(); requestRender();}
function requestRender(){if(!disposed&&!frame)frame=window.requestAnimationFrame(()=>{frame=0;render();});}
function drawControlFrame(ctx:CanvasRenderingContext2D,points:ControlPoints,zoom:number,color='#2f80ed'){const size=8/zoom;ctx.save();ctx.strokeStyle=color;ctx.fillStyle='#fff';ctx.lineWidth=1.5/zoom;ctx.setLineDash([]);ctx.beginPath();ctx.moveTo(points.nw.x,points.nw.y);ctx.lineTo(points.ne.x,points.ne.y);ctx.lineTo(points.se.x,points.se.y);ctx.lineTo(points.sw.x,points.sw.y);ctx.closePath();ctx.stroke();for(const handle of CONTROL_HANDLES){const point=points[handle],wide=handle==='n'||handle==='s',tall=handle==='e'||handle==='w';ctx.beginPath();ctx.rect(point.x-(wide?size*.75:size/2),point.y-(tall?size*.75:size/2),wide?size*1.5:size,tall?size*1.5:size);ctx.fill();ctx.stroke();}ctx.restore();}
function drawCropOverlay(ctx:CanvasRenderingContext2D,board:Rect,crop:Rect){ctx.save();ctx.fillStyle='rgba(0,0,0,.48)';ctx.beginPath();ctx.rect(board.x,board.y,board.width,board.height);ctx.rect(crop.x,crop.y,crop.width,crop.height);ctx.fill('evenodd');ctx.restore();}
function drawAlignmentGuides(ctx:CanvasRenderingContext2D,screen:DOMRect,view:CanvasDocument['viewport']){const guides=alignmentGuides.value;if(!guides.x.length&&!guides.y.length)return;const left=-view.x/view.zoom,right=(screen.width-view.x)/view.zoom,top=-view.y/view.zoom,bottom=(screen.height-view.y)/view.zoom;ctx.save();ctx.strokeStyle='#ff4fa3';ctx.lineWidth=1/view.zoom;ctx.setLineDash([]);ctx.beginPath();for(const x of guides.x){ctx.moveTo(x,top);ctx.lineTo(x,bottom);}for(const y of guides.y){ctx.moveTo(left,y);ctx.lineTo(right,y);}ctx.stroke();ctx.restore();}
function render(){const target=canvas.value,host=viewport.value,doc=documentState.value;if(!target||!host)return;const rect=host.getBoundingClientRect(),dpr=window.devicePixelRatio||1;const width=Math.max(1,Math.round(rect.width*dpr)),height=Math.max(1,Math.round(rect.height*dpr));if(target.width!==width)target.width=width;if(target.height!==height)target.height=height;target.style.width=rect.width+'px';target.style.height=rect.height+'px';const ctx=target.getContext('2d');if(!ctx)return;ctx.setTransform(dpr,0,0,dpr,0,0);ctx.fillStyle='#34383f';ctx.fillRect(0,0,rect.width,rect.height);if(!doc)return;const view=doc.viewport;ctx.save();ctx.translate(view.x,view.y);ctx.scale(view.zoom,view.zoom);const board=doc.artboard;ctx.fillStyle='#f8f8f9';ctx.fillRect(board.x,board.y,board.width,board.height);const checker=12/view.zoom,columns=Math.ceil(board.width/checker),rows=Math.ceil(board.height/checker);ctx.save();ctx.beginPath();ctx.rect(board.x,board.y,board.width,board.height);ctx.clip();ctx.fillStyle='#e8e9eb';for(let row=Math.max(0,Math.floor((-view.y/view.zoom-board.y)/checker));row<Math.min(rows,Math.ceil(((rect.height-view.y)/view.zoom-board.y)/checker));row++)for(let column=Math.max(0,Math.floor((-view.x/view.zoom-board.x)/checker));column<Math.min(columns,Math.ceil(((rect.width-view.x)/view.zoom-board.x)/checker));column++){if((column+row)%2===0)continue;ctx.fillRect(board.x+column*checker,board.y+row*checker,checker,checker);}ctx.restore();ctx.strokeStyle='#aab0ba';ctx.lineWidth=1/view.zoom;ctx.strokeRect(board.x,board.y,board.width,board.height);const previewEntry=doc.costumeEntries.find(entry=>entry.id===previewCostumeId.value);const visibleLayers=previewEntry?doc.layers.filter(layer=>layer.id===previewEntry.layerId):doc.layers;for(const layer of visibleLayers){if(!previewEntry&&!layer.visible)continue;const source=paintStroke?.layerId===layer.id?paintStroke.canvas:layerRasterCanvas(displayLayer(layer),bitmaps),t=layer.transform;ctx.save();ctx.globalAlpha=layer.opacity;ctx.transform(t.a,t.b,t.c,t.d,t.e,t.f);ctx.imageSmoothingEnabled=doc.viewport.zoom<8;ctx.drawImage(source,layer.bounds.x,layer.bounds.y,layer.bounds.width,layer.bounds.height);ctx.restore();}drawAlignmentGuides(ctx,rect,view);if(previewEntry){const r=previewEntry.frameRect,p=previewEntry.pivot;ctx.save();ctx.strokeStyle='#ffcc33';ctx.lineWidth=1.5/view.zoom;ctx.strokeRect(r.x,r.y,r.width,r.height);ctx.beginPath();ctx.moveTo(p.x-8/view.zoom,p.y);ctx.lineTo(p.x+8/view.zoom,p.y);ctx.moveTo(p.x,p.y-8/view.zoom);ctx.lineTo(p.x,p.y+8/view.zoom);ctx.stroke();ctx.restore();}if(tool.value==='crop'&&cropRect.value){drawCropOverlay(ctx,board,cropRect.value);drawControlFrame(ctx,rectControlPoints(cropRect.value),view.zoom,active.value?.locked?'#8b929d':'#2f80ed');}else if(tool.value==='transform'&&active.value?.visible&&!previewEntry)drawControlFrame(ctx,layerControlPoints(active.value.bounds,active.value.transform),view.zoom,active.value.locked?'#8b929d':'#2f80ed');if(doc.selection){const r=doc.selection.rect;if(tool.value==='select')drawControlFrame(ctx,rectControlPoints(r),view.zoom);ctx.save();ctx.setLineDash([6/view.zoom,4/view.zoom]);ctx.lineWidth=1.5/view.zoom;ctx.strokeStyle='#fff';ctx.strokeRect(r.x,r.y,r.width,r.height);ctx.lineDashOffset=5/view.zoom;ctx.strokeStyle='#111';ctx.strokeRect(r.x,r.y,r.width,r.height);ctx.restore();}ctx.restore();}
const layerPreviewCache=new Map<string,{signature:string;url:string}>();
function layerPreviewUrl(layer:RasterLayer){try{
  const bitmap=bitmaps.get(layer.bitmapId),signature=bitmap.revision+':'+rasterRevision.value+':'+effectKey(displayLayer(layer).chromaKey);
  const cached=layerPreviewCache.get(layer.id);if(cached?.signature===signature)return cached.url;
  const source=layerRasterCanvas(displayLayer(layer),bitmaps),thumbnail=document.createElement('canvas');
  const scale=Math.min(1,80/Math.max(source.width,source.height));thumbnail.width=Math.max(1,Math.round(source.width*scale));thumbnail.height=Math.max(1,Math.round(source.height*scale));
  thumbnail.getContext('2d')?.drawImage(source,0,0,thumbnail.width,thumbnail.height);
  const url=thumbnail.toDataURL('image/png');layerPreviewCache.set(layer.id,{signature,url});return url;
}catch{return '';}}
function screenPoint(event:PointerEvent|WheelEvent){const rect=canvas.value!.getBoundingClientRect();return{x:event.clientX-rect.left,y:event.clientY-rect.top};}
function worldPoint(event:PointerEvent|WheelEvent){const p=screenPoint(event),view=documentState.value!.viewport;return{x:(p.x-view.x)/view.zoom,y:(p.y-view.y)/view.zoom};}
function fitView(){const doc=documentState.value,host=viewport.value;if(!doc||!host)return;const rect=host.getBoundingClientRect(),padding=72;const zoom=clamp(Math.min((rect.width-padding)/doc.artboard.width,(rect.height-padding)/doc.artboard.height),.05,32);doc.viewport.zoom=zoom;doc.viewport.x=(rect.width-doc.artboard.width*zoom)/2-doc.artboard.x*zoom;doc.viewport.y=(rect.height-doc.artboard.height*zoom)/2-doc.artboard.y*zoom;touch('已适应内容');}
async function loadInput(input:CanvasInput){if(busy.value)return;finishInteraction();cancelChromaWork();busy.value=true;try{const staging=new BitmapStore(),imported=await importCanvasImage(input,staging);if(disposed)return;bitmaps.restore(staging.snapshot());documentState.value=imported;layerPreviewCache.clear();history.reset();await nextTick();fitView();status.value='原图已载入，可直接编辑';}catch(error){status.value=error instanceof Error?error.message:'图片导入失败';}finally{busy.value=false;}}
async function importLibraryAsset(asset:GeneratedAsset){
  if(busy.value)return;finishInteraction();cancelChromaWork();busy.value=true;assetImportError.value='';
  const staging=new BitmapStore();try{
    const imported=await importCanvasImage({kind:'base64',base64:asset.result.image_base64,mimeType:asset.result.mime_type,target:asset.target,requestId:asset.result.request_id},staging);
    if(disposed)return;const layer=imported.layers[0]!;layer.name=asset.name;layer.locked=false;
    if(!documentState.value){for(const bitmap of staging.snapshot())bitmaps.setSnapshot(bitmap);documentState.value=imported;history.reset();await nextTick();fitView();}
    else mutate('从素材库导入',()=>{
      const doc=documentState.value!;for(const bitmap of staging.snapshot())bitmaps.setSnapshot(bitmap);
      layer.transform.e=doc.artboard.x+(doc.artboard.width-layer.bounds.width)/2-layer.bounds.x;
      layer.transform.f=doc.artboard.y+(doc.artboard.height-layer.bounds.height)/2-layer.bounds.y;
      doc.layers.push(layer);doc.activeLayerIds=[layer.id];doc.selection=null;
    });
    assetImportOpen.value=false;previewCostumeId.value=null;cropRect.value=tool.value==='crop'?layerBounds():null;panelTab.value='layers';touch('已从素材库导入：'+asset.name);
  }catch(error){assetImportError.value=error instanceof Error?error.message:'素材导入失败';status.value=assetImportError.value;}finally{busy.value=false;}
}
async function onFiles(files:FileList|null|undefined,source:'upload'|'drop'='upload'){const file=files?.[0];if(!file)return;await loadInput({kind:'blob',blob:file,fileName:file.name,source});if(fileInput.value)fileInput.value.value='';}
function showScratchImport(){if(!props.bridge)return;try{scratchOptions.value=listScratchImportOptions(props.bridge);scratchImportOpen.value=true;}catch(error){status.value=error instanceof Error?error.message:String(error);}}
async function chooseScratchImport(option:ScratchImportOption){
  if(!props.bridge||busy.value)return;finishInteraction();cancelChromaWork();busy.value=true;
  try{const staging=new BitmapStore(),imported=await importScratchSelection(props.bridge,option,staging);if(disposed)return;
    bitmaps.restore(staging.snapshot());documentState.value=imported;layerPreviewCache.clear();history.reset();scratchImportOpen.value=false;panelTab.value=option.kind==='sprite'?'costumes':'layers';await nextTick();if(disposed)return;fitView();
    status.value=option.kind==='sprite'?'已从 Scratch 导入角色“'+option.name+'”，共 '+imported.layers.length+' 个造型图层':'已从 Scratch 导入背景“'+option.name+'”';touch();
  }catch(error){if(!disposed)status.value=error instanceof Error?error.message:String(error);}finally{busy.value=false;}
}
function mutate(label:string,fn:()=>void){const doc=documentState.value;if(!doc)return;history.begin(label,doc,bitmaps);try{fn();doc.updatedAt=Date.now();history.commit(doc,bitmaps);if(doc.layers.some(layer=>layer.chromaKey?.enabled))void prepareDocumentRasters();touch(label);}catch(error){history.cancel();status.value=error instanceof Error?error.message:String(error);}}
function undo(){cancelChromaWork();const restored=history.undo(bitmaps);if(restored){documentState.value=restored;void prepareDocumentRasters();touch('已撤销：'+history.redoLabel);}}
function redo(){cancelChromaWork();const restored=history.redo(bitmaps);if(restored){documentState.value=restored;void prepareDocumentRasters();touch('已重做：'+history.undoLabel);}}
function zoomAt(point:Point,factor:number){const doc=documentState.value;if(!doc)return;const old=doc.viewport.zoom,next=clamp(old*factor,.05,32);doc.viewport.x=point.x-(point.x-doc.viewport.x)*(next/old);doc.viewport.y=point.y-(point.y-doc.viewport.y)*(next/old);doc.viewport.zoom=next;touch();}
function onWheel(event:WheelEvent){event.preventDefault();zoomAt(screenPoint(event),Math.exp(-event.deltaY*.0015));}
function layerCoordinates(layer:RasterLayer,p:Point){const bitmap=bitmaps.get(layer.bitmapId),point=bitmapPoint(layer,bitmap.width,bitmap.height,p);return{x:Math.floor(point.x),y:Math.floor(point.y)};}
function transformValue(field:'x'|'y'|'width'|'height'|'angle'){const layer=active.value;if(!layer)return 0;const t=layer.transform;if(field==='x')return t.a*layer.bounds.x+t.c*layer.bounds.y+t.e;if(field==='y')return t.b*layer.bounds.x+t.d*layer.bounds.y+t.f;if(field==='width')return layer.bounds.width*Math.hypot(t.a,t.b);if(field==='height')return layer.bounds.height*Math.hypot(t.c,t.d);return Math.atan2(t.b,t.a)*180/Math.PI;}
function updateArtboard(field:'x'|'y'|'width'|'height',value:number){if(!documentState.value||!Number.isFinite(value))return;mutate('调整合成画框',()=>{documentState.value!.artboard[field]=field==='width'||field==='height'?Math.max(1,value):value;});}
function setArtboardRatio(ratio:number){const doc=documentState.value;if(!doc)return;mutate('设置画框比例',()=>{const centerY=doc.artboard.y+doc.artboard.height/2;doc.artboard.height=Math.max(1,doc.artboard.width/ratio);doc.artboard.y=centerY-doc.artboard.height/2;});}
function updateTransform(field:'x'|'y'|'width'|'height'|'angle',value:number){
  const layer=active.value;if(!layer||layer.locked||!Number.isFinite(value))return;
  mutate('变换图层',()=>{
    const t=layer.transform,b=layer.bounds,origin={x:t.a*b.x+t.c*b.y+t.e,y:t.b*b.x+t.d*b.y+t.f};
    if(field==='x'){t.e+=value-origin.x;return;}if(field==='y'){t.f+=value-origin.y;return;}
    const sx=Math.hypot(t.a,t.b),sy=Math.hypot(t.c,t.d);
    if(field==='width'||field==='height'){
      const factor=field==='width'?Math.max(.001,value)/(b.width*sx):Math.max(.001,value)/(b.height*sy);
      if(field==='width'||aspectLocked.value){t.a*=factor;t.b*=factor;}
      if(field==='height'||aspectLocked.value){t.c*=factor;t.d*=factor;}
    }else{const angle=value*Math.PI/180-Math.atan2(t.b,t.a),cos=Math.cos(angle),sin=Math.sin(angle),{a,b:by,c,d}=t;t.a=cos*a-sin*by;t.b=sin*a+cos*by;t.c=cos*c-sin*d;t.d=sin*c+cos*d;}
    t.e=origin.x-t.a*b.x-t.c*b.y;t.f=origin.y-t.b*b.x-t.d*b.y;
  });
}
function sampleAt(layer:RasterLayer,p:Point,raw=false){const local=layerCoordinates(layer,p);return readLayerPixel(displayLayer(layer),bitmaps,local.x,local.y,raw);}
function hex(color:{r:number;g:number;b:number}){return'#'+[color.r,color.g,color.b].map(value=>value.toString(16).padStart(2,'0')).join('');}
function pointInRect(point:Point,rect:Rect){return point.x>=rect.x&&point.x<=rect.x+rect.width&&point.y>=rect.y&&point.y<=rect.y+rect.height;}
function layerBounds(){const layer=active.value;return layer?transformedRectBounds(layer.bounds,layer.transform):null;}
function beginCrop(){cropRect.value=layerBounds();if(!cropRect.value)status.value='请先选择图像图层';else touch('拖动八向控制柄调整裁剪范围，按回车确认');}
function setTool(next:CanvasTool){finishInteraction();if(next!==tool.value&&next!=='select'&&!manualSelection.value)clearSelection(false);previewCostumeId.value=null;tool.value=next;alignmentGuides.value={x:[],y:[]};if(next==='crop')beginCrop();else cropRect.value=null;touch();}
function updateCropRect(field:keyof Rect,value:number){if(!cropRect.value||!Number.isFinite(value))return;cropRect.value={...cropRect.value,[field]:field==='width'||field==='height'?Math.max(1,value):value};touch();}
function confirmCrop(){if(!documentState.value||!cropRect.value)return;let confirmed=false;mutate('确认裁剪',()=>{cropRect.value=cropActiveLayer(documentState.value!,bitmaps,cropRect.value!);confirmed=true;});if(confirmed)setTool('move');}
function handleCursor(handle:ControlHandle){return({n:'ns-resize',s:'ns-resize',e:'ew-resize',w:'ew-resize',nw:'nwse-resize',se:'nwse-resize',ne:'nesw-resize',sw:'nesw-resize'} as Record<ControlHandle,string>)[handle];}
function updatePointerCursor(world:Point){
  const doc=documentState.value,target=canvas.value;if(!doc||!target)return;const tolerance=10/doc.viewport.zoom;
  if(tool.value==='crop'||tool.value==='select'){
    const rect=tool.value==='crop'?cropRect.value:doc.selection?.rect;
    const handle=rect?hitControl(rectControlPoints(rect),world,tolerance):null;
    target.style.cursor=handle?handleCursor(handle):rect&&pointInRect(world,rect)?'move':tool.value==='select'?'crosshair':'default';return;
  }
  if(tool.value==='transform'&&active.value?.visible){const handle=hitControl(layerControlPoints(active.value.bounds,active.value.transform),world,tolerance);target.style.cursor=handle?handleCursor(handle):'move';return;}
  target.style.cursor=tool.value==='move'?'move':'';
}
function onPointerDown(event:PointerEvent){
  const doc=documentState.value;if(!doc||busy.value||pointer||event.button!==0&&event.button!==1)return;
  viewport.value?.focus();if(recoveryTimer!==null){window.clearTimeout(recoveryTimer);recoveryTimer=null;}(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
  const screen=screenPoint(event),world=worldPoint(event),base={id:event.pointerId,startScreen:screen,startWorld:world,lastWorld:world};
  if(spacePressed||event.button===1||tool.value==='hand'){pointer={...base,mode:'pan'};return;}
  previewCostumeId.value=null;
  if(tool.value==='zoom'){zoomAt(screen,event.shiftKey?.8:1.25);return;}if(rasterLoading.value){status.value='正在准备图层抠图预览…';return;}
  if(tool.value==='select'){
    const rect=doc.selection?.rect,handle=rect?hitControl(rectControlPoints(rect),world,10/doc.viewport.zoom):null;
    if(rect&&handle)pointer={...base,mode:'select-resize',handle,rectStart:{...rect},handleOffset:{x:rectControlPoints(rect)[handle].x-world.x,y:rectControlPoints(rect)[handle].y-world.y}};
    else if(rect&&pointInRect(world,rect))pointer={...base,mode:'select-move',rectStart:{...rect}};
    else{pointer={...base,mode:'select',rectStart:rect?{...rect}:undefined};doc.selection={rect:{x:world.x,y:world.y,width:0,height:0},mode:'replace'};}
    touch();return;
  }
  if(tool.value==='transform'){
    cancelChromaWork();const layer=active.value,bounds=layerBounds();if(!layer||!bounds||!layer.visible){status.value='请先选择可见图像图层';return;}
    if(layer.locked){status.value='请先解锁当前图层';return;}
    const handle=hitControl(layerControlPoints(layer.bounds,layer.transform),world,10/doc.viewport.zoom);
    if(handle){history.begin('缩放图层',doc,bitmaps);pointer={...base,mode:'resize',layerId:layer.id,handle,transformStart:{...layer.transform},handleOffset:{x:layerControlPoints(layer.bounds,layer.transform)[handle].x-world.x,y:layerControlPoints(layer.bounds,layer.transform)[handle].y-world.y}};}
    else if(pointInRect(world,bounds)){history.begin('移动图层',doc,bitmaps);pointer={...base,mode:'move',layerId:layer.id,layerStart:{x:layer.transform.e,y:layer.transform.f}};}
    else status.value='请拖动图层边缘、四角或框内区域';return;
  }
  if(tool.value==='crop'){
    const crop=cropRect.value;if(!active.value||!crop){status.value='请先选择图像图层';return;}
    if(active.value.locked){status.value='请先解锁当前图层';return;}
    const handle=hitControl(rectControlPoints(crop),world,10/doc.viewport.zoom);
    if(handle)pointer={...base,mode:'crop-resize',handle,rectStart:{...crop},handleOffset:{x:rectControlPoints(crop)[handle].x-world.x,y:rectControlPoints(crop)[handle].y-world.y}};
    else if(pointInRect(world,crop))pointer={...base,mode:'crop-move',rectStart:{...crop}};
    else status.value='请拖动裁剪框边缘、四角或框内区域';return;
  }
  const pixelTool=['brush','eraser','fill'].includes(tool.value);
  const hit=pixelTool?null:hitTestLayer(doc,bitmaps,world,tool.value==='chroma');
  if(hit&&hit.id!==doc.activeLayerIds[0]){doc.activeLayerIds=[hit.id];panelTab.value='layers';touch('已选择图层：'+hit.name);}
  const layer=pixelTool?active.value:(hit??active.value);if(!layer)return;
  if(tool.value==='chroma'||tool.value==='eyedropper'){
    const local=layerCoordinates(layer,world),color=sampleAt(layer,world,tool.value==='chroma');if(!color)return;paintColor.value=hex(color);
    if(tool.value==='chroma'){
      const effect:ChromaKeyEffect={keyColor:{r:color.r,g:color.g,b:color.b},tolerance:chromaTolerance.value,softness:chromaSoftness.value,mode:chromaMode.value,seedMode:'points',connectivity:4,spillSuppression:spill.value,inverted:false,seedPoints:[local],enabled:true};
      requestChromaWork(layer,effect,'click',true);
    }else status.value='已取色 '+paintColor.value;return;
  }
  cancelChromaWork();if(layer.locked){status.value='请先解锁当前图层';return;}
  if(!layer.visible){status.value='请先显示当前图层';return;}
  if(tool.value==='move'){history.begin('移动图层',doc,bitmaps);pointer={...base,mode:'move',layerId:layer.id,layerStart:{x:layer.transform.e,y:layer.transform.f}};return;}
  if(tool.value==='brush'||tool.value==='eraser'){
    history.begin(tool.value==='eraser'?'橡皮擦':'绘制',doc,bitmaps);
    try{paintStroke=createPaintStroke(bitmaps,layer,world,{size:brushSize.value,color:paintColor.value,erase:tool.value==='eraser',selection:doc.selection?.rect});pointer={...base,mode:'draw',layerId:layer.id};requestRender();}
    catch(error){history.cancel();status.value=error instanceof Error?error.message:String(error);}return;
  }
  if(tool.value==='fill')mutate(fillMode.value==='all'?'全部填充':'区域填充',()=>{fillLayerAt(bitmaps,layer,world,{color:paintColor.value,mode:fillMode.value,selection:doc.selection?.rect});});
}
function snapTargets(excludeLayerId?:string){const doc=documentState.value!;return[doc.artboard,...doc.layers.filter(layer=>layer.id!==excludeLayerId&&layer.visible).map(layer=>preciseTransformedRectBounds(layer.bounds,layer.transform))];}
function onPointerMove(event:PointerEvent){
  const doc=documentState.value;if(!doc)return;const screen=screenPoint(event),world=worldPoint(event);
  if(!pointer||pointer.id!==event.pointerId){updatePointerCursor(world);return;}
  if(pointer.mode==='pan'){doc.viewport.x+=screen.x-pointer.startScreen.x;doc.viewport.y+=screen.y-pointer.startScreen.y;pointer.startScreen=screen;requestRender();return;}
  const resizePoint={x:world.x+(pointer.handleOffset?.x??0),y:world.y+(pointer.handleOffset?.y??0)};
  if(pointer.mode==='select'){doc.selection={rect:normalizeRect(pointer.startWorld,world),mode:'replace'};requestRender();return;}
  const isSelection=pointer.mode==='select-move'||pointer.mode==='select-resize';
  if(pointer.rectStart&&(isSelection||pointer.mode.startsWith('crop-'))){
    const targets=snapTargets(isSelection?undefined:active.value?.id),threshold=6/doc.viewport.zoom;
    const snapped=pointer.handle?snapRectResize(pointer.rectStart,pointer.handle,resizePoint,targets,threshold,event.shiftKey):snapRectTranslation({...pointer.rectStart,x:pointer.rectStart.x+world.x-pointer.startWorld.x,y:pointer.rectStart.y+world.y-pointer.startWorld.y},targets,threshold);
    if(isSelection)doc.selection={rect:snapped.rect,mode:'replace'};else cropRect.value=snapped.rect;
    alignmentGuides.value=snapped.guides;requestRender();return;
  }
  const layer=pointer.layerId?doc.layers.find(item=>item.id===pointer!.layerId):null;if(!layer)return;
  if(pointer.mode==='resize'&&pointer.handle&&pointer.transformStart){
    const snapped=snapLayerResize(pointer.transformStart,layer.bounds,pointer.handle,resizePoint,snapTargets(layer.id),6/doc.viewport.zoom,aspectLocked.value!==event.shiftKey);
    layer.transform=snapped.transform;alignmentGuides.value=snapped.guides;requestRender();return;
  }
  if(pointer.mode==='move'&&pointer.layerStart){
    const proposed={...layer.transform,e:pointer.layerStart.x+world.x-pointer.startWorld.x,f:pointer.layerStart.y+world.y-pointer.startWorld.y};
    const snapped=snapLayerTranslation(layer.bounds,proposed,snapTargets(layer.id),6/doc.viewport.zoom);layer.transform=snapped.transform;alignmentGuides.value=snapped.guides;requestRender();return;
  }
  if(pointer.mode==='draw'&&paintStroke){
    const samples=typeof event.getCoalescedEvents==='function'?event.getCoalescedEvents():[];
    for(const sample of samples)paintStroke.append(worldPoint(sample));paintStroke.append(world);pointer.lastWorld=world;requestRender();
  }
}
function finishInteraction(){
  const doc=documentState.value;if(!doc||!pointer)return;
  const mode=pointer.mode;
  if(mode==='draw'&&paintStroke){const changed=paintStroke.commit();paintStroke=null;if(changed)history.commit(doc,bitmaps);else history.cancel();}
  else if(mode==='move'||mode==='resize')history.commit(doc,bitmaps);
  if(mode==='select'&&doc.selection&&(doc.selection.rect.width<1||doc.selection.rect.height<1))doc.selection=null;
  pointer=null;alignmentGuides.value={x:[],y:[]};doc.updatedAt=Date.now();
  touch(mode==='resize'?'已缩放图层':mode.startsWith('crop')?'已调整裁剪范围，按回车确认':undefined);
}
function cancelInteraction(){
  const doc=documentState.value,gesture=pointer;if(!doc||!gesture)return;
  paintStroke=null;
  if(['draw','move','resize'].includes(gesture.mode)){const restored=history.rollback(bitmaps);if(restored)documentState.value=restored;}
  else if(gesture.mode.startsWith('select'))doc.selection=gesture.rectStart?{rect:{...gesture.rectStart},mode:'replace'}:null;
  else if(gesture.mode.startsWith('crop'))cropRect.value=gesture.rectStart?{...gesture.rectStart}:null;
  pointer=null;alignmentGuides.value={x:[],y:[]};touch('已取消当前操作');
}
function onPointerCancel(event:PointerEvent){if(pointer?.id===event.pointerId)finishInteraction();}
function onWindowBlur(){spacePressed=false;finishInteraction();}
function onPointerUp(event:PointerEvent){if(!pointer||pointer.id!==event.pointerId)return;onPointerMove(event);finishInteraction();updatePointerCursor(worldPoint(event));}
function clearSelection(showStatus=true){if(!documentState.value?.selection)return;documentState.value.selection=null;touch(showStatus?'已取消选区':undefined);}
function createLayerFromSelection(cut=false){finishInteraction();mutate(cut?'剪切选区为新图层':'复制选区为新图层',()=>copySelectionToLayer(documentState.value!,bitmaps,cut));previewCostumeId.value=null;panelTab.value='layers';inspectorOpen.value=true;}
function toggleCostume(layerId:string){mutate('更新造型队列',()=>{const doc=documentState.value!,existing=doc.costumeEntries.find(entry=>entry.layerId===layerId);if(existing)doc.costumeEntries=doc.costumeEntries.filter(entry=>entry.id!==existing.id).map((entry,order)=>({...entry,order}));else{const layer=doc.layers.find(item=>item.id===layerId)!;const frame=transformedRectBounds(layer.bounds,layer.transform);doc.costumeEntries.push({id:createId(),layerId,name:layer.name,order:doc.costumeEntries.length,frameRect:frame,pivot:{x:frame.x+frame.width/2,y:frame.y+frame.height/2},alignment:'center',exportScale:2,enabled:true});}});}
function addBlank(){mutate('新建空白图层',()=>{const doc=documentState.value!,image=new ImageData(Math.max(1,Math.round(doc.artboard.width)),Math.max(1,Math.round(doc.artboard.height))),now=Date.now(),layer:RasterLayer={id:createId(),name:'图层 '+(doc.layers.length+1),bitmapId:bitmaps.put(image),bounds:{...doc.artboard},transform:{...IDENTITY_MATRIX},visible:true,locked:false,opacity:1,blendMode:'normal',chromaKey:null,createdAt:now,updatedAt:now};doc.layers.push(layer);doc.activeLayerIds=[layer.id];});}
function previewChroma(commit=false){
  const layer=active.value;if(!layer)return;
  const work=chromaWork?.layer===layer?chromaWork:null,base=work?.effect??layer.chromaKey;if(!base)return;
  const color=paintColor.value.match(/[a-f0-9]{2}/gi)?.map(value=>parseInt(value,16));
  const effect:ChromaKeyEffect={...serializableChromaEffect(base),...(color?{keyColor:{r:color[0],g:color[1],b:color[2]}}:{}),tolerance:chromaTolerance.value,softness:chromaSoftness.value,mode:chromaMode.value,spillSuppression:spill.value};
  if(!work&&effectKey(effect)===effectKey(layer.chromaKey))return;
  requestChromaWork(layer,effect,work?.kind??'preview',commit||Boolean(work?.commit));
}
function commitChroma(){previewChroma(true);}

watch(()=>active.value,layer=>{
  if(chromaWork&&chromaWork.layer!==layer)cancelChromaWork();
  const effect=chromaWork?.layer===layer&&validChromaWork(chromaWork)?chromaWork.effect:layer?.chromaKey;
  if(effect)syncChromaSettings(effect);
  if(tool.value==='crop')cropRect.value=layerBounds();previewCostumeId.value=null;requestRender();
});

function moveCostume(id:string,direction:-1|1){mutate('调整造型顺序',()=>{const list=documentState.value!.costumeEntries,index=list.findIndex(entry=>entry.id===id),target=index+direction;if(target<0||target>=list.length)return;[list[index],list[target]]=[list[target],list[index]];list.forEach((entry,order)=>entry.order=order);});}
async function exportComposite(){if(!documentState.value||busy.value)return;busy.value=true;try{await settleChroma();await prepareLayersRaster(documentState.value.layers,bitmaps);if(disposed)return;await downloadComposite(documentState.value,bitmaps);status.value='合成 PNG 已下载';}catch(e){status.value=e instanceof Error?e.message:String(e);}finally{busy.value=false;}}
async function exportZip(){if(!documentState.value||busy.value)return;busy.value=true;try{await settleChroma();await prepareLayersRaster(documentState.value.layers,bitmaps);if(disposed)return;await downloadCostumeZip(documentState.value,bitmaps);status.value='造型 ZIP 已下载';}catch(e){status.value=e instanceof Error?e.message:String(e);}finally{busy.value=false;}}
async function importScratch(target:'sprite'|'backdrop'){if(!documentState.value||busy.value)return;busy.value=true;try{await settleChroma();await prepareLayersRaster(documentState.value.layers,bitmaps);if(disposed)return;if(target==='backdrop'){const backdrop=await exportCanvasTarget(documentState.value,bitmaps,'backdrop');if(props.handoffMode==='return-prepared-asset'){emit('prepared',{kind:'backdrop',backdrop});status.value='背景已准备，等待项目提交';}else{if(!props.bridge) return;await importBackdropCostume(props.bridge,backdrop);status.value='已将完整画板导入为 Scratch 背景';}}else{const costumes=documentState.value.costumeEntries.length?await exportCostumeSet(documentState.value,bitmaps):[await exportCanvasTarget(documentState.value,bitmaps,'sprite')];if(props.handoffMode==='return-prepared-asset'){emit('prepared',{kind:'sprite',costumes});status.value='角色造型已准备，等待项目提交';}else{if(!props.bridge) return;const result=await importCostumeSet(props.bridge,{name:documentState.value.name,costumes,initialSize:fitSpriteInitialSize(costumes)});status.value='已创建角色，包含 '+costumes.length+' 个造型（'+result.targetId+'）';}}if(props.handoffMode!=='return-prepared-asset'){await clearRecovery();skipRecoveryOnUnmount=true;scratchExportOpen.value=false;emit('close');}}catch(e){status.value=e instanceof Error?e.message:String(e);}finally{busy.value=false;}}
function onKey(event:KeyboardEvent){const editing=event.composedPath().some(target=>target instanceof HTMLElement&&(['INPUT','TEXTAREA','SELECT'].includes(target.tagName)||target.isContentEditable));const consume=()=>{event.preventDefault();event.stopImmediatePropagation();};if(event.type==='keyup'&&event.code==='Space'){spacePressed=false;return;}if(assetImportOpen.value||scratchImportOpen.value||scratchExportOpen.value)return;if(pointer&&event.type==='keydown'&&event.code!=='Space'&&event.code!=='Escape')return;if(pointer&&event.code==='Escape'){consume();cancelInteraction();return;}if(event.code==='Space'){if(event.type==='keyup'){if(spacePressed){spacePressed=false;consume();}return;}if(editing)return;spacePressed=true;consume();return;}if(event.type==='keydown'&&event.code==='Escape'&&(chromaWork||chromaPreview.value)){consume();cancelChromaWork();if(active.value?.chromaKey)syncChromaSettings(active.value.chromaKey);status.value='已取消抠图处理';requestRender();return;}if(event.type!=='keydown'||editing)return;if(busy.value)return;if(event.code==='Enter'&&tool.value==='crop'){consume();if(!event.repeat)confirmCrop();return;}const shortcut=resolveCanvasShortcut(event);if(!shortcut)return;consume();if(shortcut.kind==='undo'){undo();return;}if(shortcut.kind==='redo'){redo();return;}if(shortcut.kind==='copy-selection'){if(!event.repeat)createLayerFromSelection(shortcut.cut);return;}if(shortcut.kind==='fit-view'){fitView();return;}if(shortcut.kind==='actual-size'){if(documentState.value){documentState.value.viewport.zoom=1;touch();}return;}if(shortcut.kind==='tool'){setTool(shortcut.tool);return;}if(tool.value==='crop'){setTool('move');return;}if(shortcutHelpOpen.value)shortcutHelpOpen.value=false;else if(documentState.value?.selection)clearSelection();else emit('close');}
async function initializeCanvas(){
  if(props.initialInput){await loadInput(props.initialInput);return;}
  busy.value=true;try{const recovery=await loadRecovery();if(disposed)return;if(recovery){bitmaps.restore(recovery.bitmaps);documentState.value=recovery.document;history.reset();await nextTick();if(disposed)return;await prepareDocumentRasters();if(disposed)return;fitView();status.value='已恢复上次画布';}}
  catch(error){if(!disposed)status.value=error instanceof Error?error.message:'画布恢复失败';}finally{busy.value=false;}
}
onMounted(async()=>{resizeObserver=new ResizeObserver(()=>documentState.value?fitView():touch());if(viewport.value)resizeObserver.observe(viewport.value);window.addEventListener('keydown',onKey,true);window.addEventListener('keyup',onKey,true);window.addEventListener('paste',onPaste);window.addEventListener('blur',onWindowBlur);await initializeCanvas();if(!disposed){viewport.value?.focus();touch();}});
function onPaste(event:ClipboardEvent){const file=[...(event.clipboardData?.files??[])].find(item=>item.type.startsWith('image/'));if(file&&!assetImportOpen.value&&!scratchImportOpen.value&&!scratchExportOpen.value)loadInput({kind:'blob',blob:file,fileName:'剪贴板图片.png',source:'clipboard'});}
onBeforeUnmount(()=>{cancelChromaWork();rasterController?.abort();finishInteraction();if(recoveryTimer!==null)window.clearTimeout(recoveryTimer);if(!skipRecoveryOnUnmount)void persistRecovery();disposed=true;resizeObserver?.disconnect();window.removeEventListener('keydown',onKey,true);window.removeEventListener('keyup',onKey,true);window.removeEventListener('paste',onPaste);window.removeEventListener('blur',onWindowBlur);window.cancelAnimationFrame(frame);});
</script>

<template><section class="canvas-workspace" role="dialog" aria-modal="true" aria-label="无边画布编辑器"><header class="canvas-commandbar"><button class="canvas-commandbar__back" type="button" @click="emit('close')"><ArrowLeft :size="18"/>返回 Scratch</button><input v-if="documentState" v-model="documentState.name" class="canvas-document-name" aria-label="文档名称" @change="touch('文档已重命名')"/><span v-else class="canvas-document-name canvas-document-name--empty">无边画布</span><div class="canvas-commandbar__history"><button type="button" :disabled="!historyState.canUndo" :title="historyState.undoLabel?'撤销：'+historyState.undoLabel:'撤销'" aria-label="撤销" @click="undo"><Undo2 :size="17"/></button><button type="button" :disabled="!historyState.canRedo" :title="historyState.redoLabel?'重做：'+historyState.redoLabel:'重做'" aria-label="重做" @click="redo"><Redo2 :size="17"/></button></div><div class="canvas-commandbar__actions"><input ref="fileInput" class="visually-hidden" type="file" accept="image/png,image/jpeg,image/webp" @change="onFiles(($event.target as HTMLInputElement).files)"/><button type="button" :disabled="busy" aria-label="从素材库导入" @click="assetImportError='';assetImportOpen=true"><Images :size="17"/>从素材库导入</button><button type="button" @click="fileInput?.click()"><ImagePlus :size="17"/>导入图片</button><button type="button" :disabled="!props.bridge?.isReady||busy" @click="showScratchImport"><FolderInput :size="17"/>从 Scratch 导入</button><button type="button" :disabled="!documentState||busy" @click="exportComposite"><Download :size="17"/>PNG</button><button type="button" :disabled="!documentState?.costumeEntries.length||busy" @click="exportZip"><FileArchive :size="17"/>造型 ZIP</button><button class="inspector-toggle" type="button" :title="inspectorOpen?'收起属性面板':'展开属性面板'" :aria-label="inspectorOpen?'收起属性面板':'展开属性面板'" @click="inspectorOpen=!inspectorOpen"><SlidersHorizontal :size="17"/></button><button class="is-primary" type="button" :disabled="!documentState?.layers.length||!props.bridge||busy" @click="scratchExportOpen=true"><LoaderCircle v-if="busy" class="spin" :size="17"/><Upload v-else :size="17"/>导入 Scratch</button></div></header><div class="canvas-main"><CanvasToolbar :model-value="tool" :help-open="shortcutHelpOpen" @update:model-value="setTool" @fit="fitView" @help="shortcutHelpOpen=!shortcutHelpOpen"/><section v-if="shortcutHelpOpen" class="canvas-shortcuts" role="dialog" aria-label="画布快捷键帮助"><header><strong>快捷键</strong><button type="button" title="关闭快捷键帮助" aria-label="关闭快捷键帮助" @click="shortcutHelpOpen=false"><X :size="16"/></button></header><div><span>移动</span><kbd>V</kbd><span>变换</span><kbd>T</kbd><span>矩形选区</span><kbd>M</kbd><span>裁剪</span><kbd>C</kbd><span>色度抠图</span><kbd>K</kbd><span>画笔</span><kbd>B</kbd><span>橡皮擦</span><kbd>E</kbd><span>吸管</span><kbd>I</kbd><span>抓手</span><kbd>H</kbd><span>临时抓手</span><kbd>Space</kbd></div><div class="canvas-shortcuts__commands"><span>复制选区为新图层</span><kbd>Alt J</kbd><span>剪切选区为新图层</span><kbd>Alt Shift J</kbd><span>适应内容</span><kbd>Ctrl 0</kbd><span>100% 显示</span><kbd>Ctrl 1</kbd><span>撤销</span><kbd>Ctrl Z</kbd><span>重做</span><kbd>Ctrl Shift Z</kbd></div></section><main ref="viewport" class="canvas-viewport" tabindex="0" :data-tool="tool" @wheel="onWheel" @pointerdown="onPointerDown" @pointermove="onPointerMove" @pointerup="onPointerUp" @pointercancel="onPointerCancel" @lostpointercapture="onPointerCancel" @dragover.prevent @drop.prevent="onFiles($event.dataTransfer?.files,'drop')"><canvas ref="canvas"/><div v-if="!documentState" class="canvas-empty"><ImagePlus :size="34"/><strong>导入一张角色素材</strong><span>支持 PNG、JPEG、WebP，也可以拖放或粘贴图片</span><button type="button" @click="fileInput?.click()"><ImagePlus :size="16"/>选择图片</button></div></main><aside class="canvas-inspector" :class="{'is-mobile-open':inspectorOpen}"><section class="tool-options"><h2>工具属性</h2><template v-if="tool==='chroma'"><label>目标颜色<input v-model="paintColor" type="color" @input="previewChroma(false)" @change="commitChroma"/></label><label>容差 <span>{{chromaTolerance}}</span><input v-model.number="chromaTolerance" type="range" min="0" max="100" @input="previewChroma(false)" @change="commitChroma"/></label><label>柔化 <span>{{chromaSoftness}}</span><input v-model.number="chromaSoftness" type="range" min="0" max="40" @input="previewChroma(false)" @change="commitChroma"/></label><label>作用模式<select v-model="chromaMode" @input="previewChroma(false)" @change="commitChroma"><option value="connected">相邻背景</option><option value="global">全图同色</option></select></label><label>去色边 <span>{{spill}}</span><input v-model.number="spill" type="range" min="0" max="100" @input="previewChroma(false)" @change="commitChroma"/></label><p v-if="chromaProcessing||rasterLoading" role="status" aria-live="polite"><LoaderCircle class="spin" :size="14"/>正在处理抠图…</p><p v-else>相邻背景只沿点击位置相连的颜色扩展，柔化仅处理边缘。全图同色会处理所有匹配区域。</p></template><template v-else-if="['brush','eraser'].includes(tool)"><label>笔刷大小 <span>{{brushSize}} px</span><input v-model.number="brushSize" type="range" min="1" max="128"/></label><label v-if="tool==='brush'">颜色<input v-model="paintColor" type="color"/></label></template><template v-else-if="tool==='fill'||tool==='eyedropper'"><label>颜色<input v-model="paintColor" type="color" aria-label="填充颜色"/></label><label v-if="tool==='fill'">填充方式<select v-model="fillMode" aria-label="填充方式"><option value="region">区域填充</option><option value="all">全部填充</option></select></label><p v-if="tool==='fill'">区域填充只影响相连同色区域；全部填充覆盖当前图层。有选区时仅填充选区。</p></template><template v-else-if="tool==='select'"><label class="manual-selection-option"><input v-model="manualSelection" type="checkbox"/>手动建立选区</label><p>拖动选区内部移动，拖动八向手柄调整范围。开启后切换工具时保留选区。</p><div class="selection-actions"><button type="button" :disabled="!documentState?.selection" @click.stop.prevent="createLayerFromSelection(false)">复制为新图层</button><button type="button" :disabled="!documentState?.selection" @click.stop.prevent="createLayerFromSelection(true)">剪切为新图层</button><button type="button" :disabled="!documentState?.selection" @click.stop.prevent="clearSelection()">取消选区</button></div></template><template v-else-if="tool==='crop'"><div v-if="cropRect" class="crop-options"><div class="transform-grid"><label>X<input type="number" step="1" :disabled="active?.locked" :value="cropRect.x" @change="updateCropRect('x',($event.target as HTMLInputElement).valueAsNumber)"/></label><label>Y<input type="number" step="1" :disabled="active?.locked" :value="cropRect.y" @change="updateCropRect('y',($event.target as HTMLInputElement).valueAsNumber)"/></label><label>宽<input type="number" min="1" step="1" :disabled="active?.locked" :value="cropRect.width" @change="updateCropRect('width',($event.target as HTMLInputElement).valueAsNumber)"/></label><label>高<input type="number" min="1" step="1" :disabled="active?.locked" :value="cropRect.height" @change="updateCropRect('height',($event.target as HTMLInputElement).valueAsNumber)"/></label><button type="button" :disabled="active?.locked" @click="confirmCrop">确认裁剪</button></div><p>调整裁剪框后按回车确认，图层位置保持不变。</p></div><p v-else>请先选择图层</p></template><template v-else-if="tool==='transform'"><button type="button" class="aspect-lock" aria-label="锁定比例" :aria-pressed="aspectLocked" @click="aspectLocked=!aspectLocked"><Link v-if="aspectLocked" :size="16"/><Unlink v-else :size="16"/>{{aspectLocked?'比例已锁定':'自由比例'}}</button><p>拖动八向手柄缩放并吸附。按住 Shift 临时切换比例锁定。</p><div v-if="active" class="transform-grid"><label>X<input type="number" step="0.5" :disabled="active.locked" :value="transformValue('x')" @change="updateTransform('x',($event.target as HTMLInputElement).valueAsNumber)"/></label><label>Y<input type="number" step="0.5" :disabled="active.locked" :value="transformValue('y')" @change="updateTransform('y',($event.target as HTMLInputElement).valueAsNumber)"/></label><label>宽<input type="number" min="0.1" step="0.5" :disabled="active.locked" :value="transformValue('width')" @change="updateTransform('width',($event.target as HTMLInputElement).valueAsNumber)"/></label><label>高<input type="number" min="0.1" step="0.5" :disabled="active.locked" :value="transformValue('height')" @change="updateTransform('height',($event.target as HTMLInputElement).valueAsNumber)"/></label><label>角度<input type="number" step="1" :disabled="active.locked" :value="transformValue('angle')" @change="updateTransform('angle',($event.target as HTMLInputElement).valueAsNumber)"/></label><button type="button" :disabled="active.locked" @click="mutate('应用图层变换',()=>applyLayerTransform(documentState!,bitmaps))">应用变换</button></div><p v-else>请先选择图层</p></template><p v-else>在画布中操作，视图导航不进入撤销历史。</p></section><div class="inspector-tabs" role="tablist"><button :class="{'is-active':panelTab==='layers'}" role="tab" :aria-selected="panelTab==='layers'" @click="panelTab='layers'">图层 <span>{{documentState?.layers.length??0}}</span></button><button :class="{'is-active':panelTab==='costumes'}" role="tab" :aria-selected="panelTab==='costumes'" @click="panelTab='costumes'">造型 <span>{{documentState?.costumeEntries.length??0}}</span></button></div><LayersPanel v-if="panelTab==='layers'" :layers="documentState?.layers??[]" :active-id="documentState?.activeLayerIds[0]" :costume-layer-ids="costumeLayerIds" :preview-url="layerPreviewUrl" @select="id=>{if(documentState){documentState.activeLayerIds=[id];if(tool==='crop')beginCrop();else touch()}}" @toggle-visible="id=>mutate('切换图层显隐',()=>{const l=documentState!.layers.find(x=>x.id===id);if(l)l.visible=!l.visible})" @toggle-lock="id=>mutate('切换图层锁定',()=>{const l=documentState!.layers.find(x=>x.id===id);if(l)l.locked=!l.locked})" @rename="(id,name)=>mutate('重命名图层',()=>{const l=documentState!.layers.find(x=>x.id===id);if(l){l.name=name;const e=documentState!.costumeEntries.find(x=>x.layerId===id&&!x.manuallyNamed);if(e)e.name=name}})" @duplicate="mutate('复制图层',()=>duplicateLayer(documentState!,bitmaps))" @remove="mutate('删除图层',()=>deleteActiveLayer(documentState!,bitmaps))" @blank="addBlank" @toggle-costume="toggleCostume" @move="(id,direction)=>mutate('调整图层顺序',()=>reorderLayer(documentState!,id,direction))" @opacity="(id,value)=>mutate('调整图层透明度',()=>{const l=documentState!.layers.find(item=>item.id===id);if(l)l.opacity=value})"/><CostumesPanel v-else :entries="documentState?.costumeEntries??[]" :preview-id="previewCostumeId" @rename="(id,name)=>mutate('重命名造型',()=>{const e=documentState!.costumeEntries.find(x=>x.id===id);if(e){e.name=name;e.manuallyNamed=true}})" @remove="id=>mutate('移出造型队列',()=>{documentState!.costumeEntries=documentState!.costumeEntries.filter(x=>x.id!==id).map((x,order)=>({...x,order}))})" @move="moveCostume" @align="mode=>mutate('统一造型画框',()=>unifyCostumeFrames(documentState!,mode))" @pivot="(id,x,y)=>mutate('调整旋转中心',()=>{const e=documentState!.costumeEntries.find(item=>item.id===id);if(e){e.pivot={x,y};e.alignment='custom'}})" @scale="(id,value)=>mutate('调整输出比例',()=>{const e=documentState!.costumeEntries.find(item=>item.id===id);if(e&&Number.isFinite(value)&&value>0)e.exportScale=value})" @preview="id=>{previewCostumeId=previewCostumeId===id?null:id;touch(previewCostumeId?'正在预览造型':'已退出造型预览')}"/></aside></div><footer class="canvas-status"><span>{{status}}</span><span v-if="documentState?.selection">选区 {{Math.round(documentState.selection.rect.width)}} × {{Math.round(documentState.selection.rect.height)}}</span><span v-if="active">当前图层 {{Math.round(active.bounds.width)}} × {{Math.round(active.bounds.height)}}{{active.locked?' · 已锁定':''}}</span><span>{{zoomLabel}}</span></footer><CanvasAssetImportDialog :open="assetImportOpen" :busy="busy" :import-error="assetImportError" @close="assetImportOpen=false" @choose="importLibraryAsset"/><ScratchImportDialog v-if="scratchImportOpen" :options="scratchOptions" :busy="busy" @close="scratchImportOpen=false" @select="chooseScratchImport"/><ScratchExportDialog v-if="scratchExportOpen" :busy="busy" @close="scratchExportOpen=false" @select="importScratch"/></section></template>
