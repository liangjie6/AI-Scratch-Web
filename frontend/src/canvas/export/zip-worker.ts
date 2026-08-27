/// <reference lib="webworker" />
import {zipSync} from 'fflate';
const workerScope=self as unknown as DedicatedWorkerGlobalScope;
workerScope.onmessage=(event:MessageEvent<{entries:Array<{name:string;data:ArrayBuffer}>}>)=>{try{const files:Record<string,Uint8Array>={};for(const entry of event.data.entries)files[entry.name]=new Uint8Array(entry.data);const zipped=zipSync(files);workerScope.postMessage({ok:true,data:zipped.buffer},[zipped.buffer]);}catch(error){workerScope.postMessage({ok:false,error:error instanceof Error?error.message:String(error)});}};
