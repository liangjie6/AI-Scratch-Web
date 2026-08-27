/// <reference lib="webworker" />
import {applyChromaKey} from './chroma-key';
import type {ChromaKeyEffect} from '../model/canvas-types';
const workerScope=self as unknown as DedicatedWorkerGlobalScope;
workerScope.onmessage=(event:MessageEvent<{revision:number;width:number;height:number;data:ArrayBuffer;effect:ChromaKeyEffect}>)=>{try{const source=new ImageData(new Uint8ClampedArray(event.data.data),event.data.width,event.data.height);const output=applyChromaKey(source,event.data.effect);workerScope.postMessage({ok:true,revision:event.data.revision,width:output.width,height:output.height,data:output.data.buffer},[output.data.buffer]);}catch(error){workerScope.postMessage({ok:false,revision:event.data.revision,error:error instanceof Error?error.message:String(error)});}};
