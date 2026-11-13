const path = require('node:path');
const projectRoot = path.resolve(process.argv[2] ?? path.join(__dirname, '../../..'));
const base = path.join(projectRoot, 'frontend/node_modules/@scratch/scratch-vm');
const Runtime = require(path.join(base, 'src/engine/runtime.js'));
const Blocks = require(path.join(base, 'src/engine/blocks.js'));
const Sprite = require(path.join(base, 'src/sprites/sprite.js'));
const StageLayering = require(path.join(base, 'src/engine/stage-layering.js'));
const runtime = new Runtime();
const catalog = {
  version: require(path.join(base, 'package.json')).version,
  dynamicBlocks: runtime.getBlocksJSON().length,
  primitives: Object.keys(runtime._primitives).length,
  hats: Object.keys(runtime._hats).length
};
const stage = new Sprite(new Blocks(runtime), runtime).createClone(StageLayering.BACKGROUND_LAYER);
stage.isStage = true;
stage.sprite.name = 'Stage';
runtime.addTarget(stage);
const blocks = new Blocks(runtime);
const sprite = new Sprite(blocks, runtime);
sprite.name = 'Probe';
const target = sprite.createClone(StageLayering.SPRITE_LAYER);
runtime.addTarget(target);
target.setXY(0, 0);
const add = (id, opcode, next, parent, inputs = {}, fields = {}, topLevel = false) => blocks.createBlock({id, opcode, next, parent, inputs, fields, shadow: false, topLevel});
add('hat', 'event_whenflagclicked', 'repeat', null, {}, {}, true);
add('repeat', 'control_repeat', null, 'hat', {TIMES: {name: 'TIMES', block: 'three', shadow: 'three'}, SUBSTACK: {name: 'SUBSTACK', block: 'move', shadow: null}});
blocks.createBlock({id:'three',opcode:'math_whole_number',next:null,parent:'repeat',inputs:{},fields:{NUM:{name:'NUM',value:3}},shadow:true,topLevel:false});
add('move', 'motion_movesteps', null, 'repeat', {STEPS: {name: 'STEPS', block: 'ten', shadow: 'ten'}});
blocks.createBlock({id:'ten',opcode:'math_number',next:null,parent:'move',inputs:{},fields:{NUM:{name:'NUM',value:10}},shadow:true,topLevel:false});
runtime.currentStepTime = 1000 / 60;
runtime.greenFlag();
const samples = [];
for (let tick = 0; tick < 5; tick += 1) {
  runtime._step();
  samples.push({tick:tick+1,x:target.x,y:target.y,threads:runtime.threads.length});
}
process.stdout.write(JSON.stringify({catalog,motionRepeat:{expectedX:30,actualX:target.x,passed:Math.abs(target.x-30)<1e-9,samples},rendererCollision:{status:'SKIPPED',reason:'No renderer attached; sprite collision cannot be validated'}})+'\n');
runtime.dispose();
