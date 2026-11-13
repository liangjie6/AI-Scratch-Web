#!/usr/bin/env node
// Explicit paid model smoke test through the real UI, compiler and commit flow.
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import assert from 'node:assert/strict';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(path.join(root, 'frontend/package.json'));
const {chromium} = require('@playwright/test');
const args = process.argv.slice(2);
const option = (name, fallback) => {const i=args.indexOf(name);return i<0?fallback:args[i+1];};
assert(args.includes('--live-model-calls')||args.includes('--live-agnes'),'Pass --live-model-calls explicitly for paid testing.');
const modelTier=option('--model-tier','normal');assert(['normal','advanced'].includes(modelTier));const provider=modelTier==='normal'?'agnes':'deepseek';
const baseURL=option('--base-url','http://127.0.0.1:18066');
const output=path.resolve(option('--output','/tmp/ai-scratch-agnes-refactor/browser'));
await fs.mkdir(output,{recursive:true});
const browserArgs=['--no-sandbox','--use-gl=angle','--use-angle=swiftshader','--enable-unsafe-swiftshader'];
if(args.includes('--local-deployment'))browserArgs.push('--host-resolver-rules=MAP '+new URL(baseURL).hostname+' 127.0.0.1');
const browser=await chromium.launch({headless:true,args:browserArgs});
const page=await browser.newPage({viewport:{width:1440,height:1000},ignoreHTTPSErrors:true});
const errors=[];page.on('pageerror',e=>errors.push(e.message));
const captured=[];
const requestRecords=new WeakMap();
const pendingResponses=[];
page.on('request',request=>{if(request.url().includes('/api/ai-blocks/generate')&&request.method()==='POST'){try{const item={url:request.url(),request:request.postDataJSON()};captured.push(item);requestRecords.set(request,item);}catch{}}});
page.on('response',response=>{const item=requestRecords.get(response.request());if(item)pendingResponses.push((async()=>{item.status=response.status();try{item.stream=await response.text();}catch{}})());});
const start=Date.now();
const report={provider,modelTier,baseURL,startedAt:new Date().toISOString()};
const readEvents=stream=>stream.replace(/\r\n/g,'\n').split('\n\n').flatMap(frame=>{const lines=frame.split('\n');const data=lines.filter(line=>line.startsWith('data:')).map(line=>line.slice(5).trimStart()).join('\n');return data?[{type:lines.find(line=>line.startsWith('event:'))?.slice(6).trim()??'message',...JSON.parse(data)}]:[];});
try{
 await page.goto(baseURL,{waitUntil:'domcontentloaded'});
 report.loadedScripts=await page.locator('script[src]').evaluateAll(nodes=>nodes.map(node=>node.src));
 await page.locator('.blocklySvg').waitFor({timeout:60000});
 await page.getByRole('button',{name:'打开 AI 写积木'}).click();
 const panel=page.getByRole('region',{name:'AI 写积木'});
 // Model tier is mandatory; changes in UI defaults must never change this test.
 const tierLabel=panel.locator('.ai-blocks-model-options label').filter({has:page.locator('input[type="radio"][value="'+modelTier+'"]')});
 assert.equal(await tierLabel.count(),1,'the requested model tier must exist in the actual UI');
 await tierLabel.click();
 assert(await tierLabel.locator('input').isChecked(),'the requested model tier must be selected before sending');
 await page.getByLabel('写入模式',{exact:true}).selectOption('append');
 if(await page.getByLabel('素材策略',{exact:true}).count())await page.getByLabel('素材策略',{exact:true}).selectOption('existing_only');
 await panel.locator('input[aria-label="思考强度"]').evaluate(element=>{element.value='0';element.dispatchEvent(new Event('input',{bubbles:true}));element.dispatchEvent(new Event('change',{bubbles:true}));});
 await page.getByLabel('描述要实现的积木功能').fill('点击绿旗后移动到x=0、y=0，然后重复3次，每次将x增加10。只修改当前角色，不添加资源。');
 await page.getByRole('button',{name:'发送需求'}).click();
 await Promise.race([page.getByText(/已完成统一生成并写入 1 个目标/).waitFor({timeout:180000}),panel.locator('.ai-blocks-message__error').waitFor({timeout:180000}).then(async()=>{throw new Error(await panel.locator('.ai-blocks-message__error').innerText());})]);
 // Read the actual ScratchHost bridge from Vue's mounted component tree; no mock VM.
 const state=await page.evaluate(()=>{
  const app=document.querySelector('#app').__vue_app__;
  const seen=new Set();
  const find=node=>{if(!node||seen.has(node))return null;seen.add(node);const component=node.component;if(component?.exposed?.bridge?.currentVm)return component.exposed.bridge;const tree=component&&find(component.subTree);if(tree)return tree;for(const child of Array.isArray(node.children)?node.children:[]){const result=find(child);if(result)return result;}return null;};
  const bridge=find(app?._instance?.subTree??app?._container?._vnode);
  if(!bridge)throw new Error('Mounted ScratchHost bridge unavailable');
  window.__agnesTestVm=bridge.currentVm;
  window.__agnesTestBridge=bridge;
  window.__targetTrace=[];
  const trace=(event,extra={})=>window.__targetTrace.push({event,...extra,editingId:bridge.currentVm.editingTarget?.id,targets:bridge.currentVm.runtime.targets.filter(item=>item.isOriginal!==false).map(item=>({id:item.id,name:item.getName(),stage:item.isStage}))});
  const setTarget=bridge.currentVm.setEditingTarget.bind(bridge.currentVm);
  bridge.currentVm.setEditingTarget=(id)=>{trace('setEditingTarget',{id,stack:new Error().stack});return setTarget(id);};
  const load=bridge.loadProject.bind(bridge);
  bridge.loadProject=async(...params)=>{trace('beforeLoad');try {const result=await load(...params);trace('afterLoad');return result;}catch(error){trace('loadError',{message:error.message,stack:error.stack});throw error;}};
  const vm=window.__agnesTestVm;const target=vm.editingTarget;
  return {id:target.id,name:target.getName(),isStage:target.isStage,scripts:target.blocks.getScripts().length,opcodes:Object.values(target.blocks._blocks).map(block=>block.opcode)};
 });
 assert(state.scripts>0&&state.opcodes.includes('control_repeat'),'committed script must contain a bounded loop');
 assert(!state.opcodes.includes('control_forever'),'this finite movement case must not contain a forever loop');
 await page.getByRole('button',{name:'运行查看',exact:true}).click();
 await page.waitForFunction(()=>window.__agnesTestVm.editingTarget.x===30&&window.__agnesTestVm.editingTarget.y===0,undefined,{timeout:10000});
 await page.waitForFunction(()=>window.__agnesTestVm.runtime.threads.every(thread=>!window.__agnesTestVm.runtime.isActiveThread(thread)),undefined,{timeout:10000});
 const actual=await page.evaluate(()=>({x:window.__agnesTestVm.editingTarget.x,y:window.__agnesTestVm.editingTarget.y}));
 assert.deepEqual(actual,{x:30,y:0},'the final stopped program must end at the expected coordinates');
 await page.screenshot({path:path.join(output,'committed.png')});
 await page.getByRole('button',{name:'撤销生成',exact:true}).click();
 await page.getByText('已撤销本次 AI 生成。',{exact:true}).waitFor({timeout:30000});
 const undone=await page.evaluate(()=>{const target=window.__agnesTestVm.editingTarget;return {id:target.id,name:target.getName(),isStage:target.isStage,remainingScripts:target.blocks.getScripts().length};});
 assert.equal(undone.remainingScripts,0,'undo must restore the initial empty Scratch graph');
 assert.equal(undone.name,state.name,'undo must restore the same logical editing target');
 assert.equal(undone.isStage,state.isStage,'undo must preserve editing target kind');
 assert.notEqual(undone.id,state.id,'actual SB3 reload should allocate a new target ID');
 assert.equal(await panel.locator('.ai-blocks-message__error').count(),0,'undo must leave no visible error');
 await page.screenshot({path:path.join(output,'undone.png')});
 Object.assign(report,{committed:state,actual,undo:undone});
 const planning=captured.filter(item=>item.request.pipeline_version==='scratch-source/1');
 assert.equal(planning.length,1,'one initial source plan is expected');
 const request=planning[0].request;
 const programs=captured.filter(item=>item.url.includes('/generate-source-program'));
 assert.equal(programs.length,request.scope==='project'?1:0,'project scope streams its one target after planning; single-sprite scope includes the body in its initial stream');
 assert.equal(captured.length,1+programs.length,'no duplicate generation requests');
 assert.equal(request.model_tier,modelTier);
 for(const item of programs){assert.equal(item.request.request.request_id,request.request_id);assert.equal(item.request.request.model_tier,modelTier);}
 await Promise.all(pendingResponses);
 const observedProviders=new Set();
 for(const item of captured){
  assert.equal(item.status,200);
  const events=readEvents(item.stream);
  assert.equal(events.at(-1)?.type,'complete','each generation stream must end with complete');
  assert(!events.some(event=>event.type==='error'),'generation must contain no error event');
  const providers=events.filter(event=>event.type==='usage'&&event.data?.provider).map(event=>event.data.provider.toLowerCase());
  assert(providers.length,'each generation response must identify its actual provider');
  for(const observed of providers){assert.equal(observed,provider,'actual usage provider must match the requested model');observedProviders.add(observed);}
 }
 report.observedProviders=[...observedProviders];
 assert(!errors.length,errors.join('\n'));
 Object.assign(report,{status:'PASS',durationSeconds:(Date.now()-start)/1000,committed:state,actual,undo:undone,pageErrors:errors});
}catch(error){Object.assign(report,{status:'FAIL',durationSeconds:(Date.now()-start)/1000,error:String(error.message??error),pageErrors:errors,visibleText:(await page.locator('.ai-blocks-panel').innerText().catch(()=>page.locator('body').innerText())).slice(-2500)});await page.screenshot({path:path.join(output,'failure.png')}).catch(()=>{});process.exitCode=1;}
finally{await Promise.all(pendingResponses);report.targetTrace=await page.evaluate(()=>window.__targetTrace??[]).catch(()=>[]);await fs.writeFile(path.join(output,'generation.json'),JSON.stringify(captured,null,2));await fs.writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2));await browser.close();console.log(JSON.stringify(report,null,2));}
