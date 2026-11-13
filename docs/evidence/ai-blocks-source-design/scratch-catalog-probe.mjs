import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath, pathToFileURL} from 'node:url';

// Read-only experiment. Run with the project root as the optional first argument.
const projectRoot = path.resolve(process.argv[2] ?? path.join(path.dirname(fileURLToPath(import.meta.url)), '../../..'));
const frontend = path.join(projectRoot, 'frontend');
const require = createRequire(path.join(frontend, 'package.json'));
const packageRoot = name => path.join(frontend, 'node_modules', name);
const packageVersion = name => JSON.parse(fs.readFileSync(path.join(packageRoot(name), 'package.json'), 'utf8')).version;
const report = {versions: {scratchBlocks: packageVersion('scratch-blocks'), scratchVm: packageVersion('@scratch/scratch-vm'), scratchGui: packageVersion('@scratch/scratch-gui')}};

// The package's public Node bundle has a missing stylesheet in this installation.
// Use the installed, readable source entry for this isolated runtime probe.
const Runtime = require(path.join(packageRoot('@scratch/scratch-vm'), 'src/engine/runtime.js'));
const runtime = new Runtime();
report.runtimeBeforeExtension = {
  primitives: Object.keys(runtime._primitives).length,
  motionPrimitive: typeof runtime._primitives.motion_movesteps,
  ifPrimitive: typeof runtime._primitives.control_if,
  blocksJSON: runtime.getBlocksJSON()
};
runtime._registerExtensionPrimitives({id: 'probeExtension', name: 'Probe', blocks: [{opcode: 'ping', blockType: 'command', text: 'ping [NUM]', arguments: {NUM: {type: 'number', defaultValue: 1}}}], menus: {}});
report.runtimeAfterExtension = runtime.getBlocksJSON().map(block => block.type);

const {JSDOM} = require('jsdom');
const ts = require('typescript');
const dom = new JSDOM('<!doctype html><html><body><div id="workspace"></div></body></html>', {pretendToBeVisual: true, url: 'http://localhost/'});
for (const key of ['window', 'document', 'Element', 'HTMLElement', 'SVGElement', 'Node', 'XMLSerializer', 'DOMParser', 'navigator', 'MutationObserver', 'getComputedStyle']) {
  if (dom.window[key]) Object.defineProperty(globalThis, key, {value: dom.window[key], configurable: true});
}
const Blocks = await import(pathToFileURL(require.resolve('scratch-blocks')).href);
Blocks.ScratchMsgs.setLocale('en');
report.blocklyVersion = Blocks.VERSION;
report.registeredBlockCount = Object.keys(Blocks.Blocks).length;
report.menuInitializerBeforeGui = typeof Blocks.Blocks.motion_goto_menu.init;

// These two installed GUI source modules have no browser side effects at import.
// Transpile into memory and supply their explicit dependencies; no package files change.
function loadGuiModule(relative, dependencies) {
  const filename = path.join(packageRoot('@scratch/scratch-gui'), relative);
  const source = fs.readFileSync(filename, 'utf8');
  const compiled = ts.transpileModule(source, {compilerOptions: {module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, allowJs: true}}).outputText;
  const module = {exports: {}};
  new Function('require', 'module', 'exports', compiled)(name => {
    if (!(name in dependencies)) throw new Error('Unexpected dependency: ' + name);
    return dependencies[name];
  }, module, module.exports);
  return module.exports.default;
}

const stage = {isStage: true, isOriginal: true, sprite: {name: 'Stage', sounds: [], costumes: [{name: '背景1'}]}};
const cat = {isStage: false, isOriginal: true, sprite: {name: '小猫', sounds: [{name: '喵'}], costumes: [{name: '造型1'}]}};
const apple = {isStage: false, isOriginal: true, sprite: {name: '苹果', sounds: [], costumes: []}};
const vm = {editingTarget: cat, runtime: {targets: [stage, cat, apple], getTargetForStage: () => stage, monitorBlocks: {_blocks: {}}}};
loadGuiModule('src/lib/blocks.js', {'scratch-blocks': Blocks})(vm);

const jsonInitCaptures = [];
const originalJsonInit = Blocks.Block.prototype.jsonInit;
Blocks.Block.prototype.jsonInit = function (json) {
  jsonInitCaptures.push({type: this.type, json});
  return originalJsonInit.call(this, json);
};
const workspace = Blocks.inject(document.querySelector('#workspace'), {media: 'http://localhost/media/', toolbox: null, sounds: false});
const variables = workspace.getVariableMap();
variables.createVariable('i', '', 'fixture-i');
variables.createVariable('得分', '', 'fixture-score');
variables.createVariable('项目', 'list', 'fixture-list');

function snapshot(block) {
  return {type: block.type, previous: Boolean(block.previousConnection), next: Boolean(block.nextConnection), output: block.outputConnection?.getCheck() ?? null, inputs: block.inputList.map(input => ({name: input.name, type: input.type, check: input.connection?.getCheck() ?? null, hasShadow: Boolean(input.connection?.getShadowDom()), fields: input.fieldRow.map(field => ({name: field.name ?? null, value: field.getValue(), options: typeof field.getOptions === 'function' ? field.getOptions(false) : undefined}))}))};
}
const move = workspace.newBlock('motion_movesteps');
const conditional = workspace.newBlock('control_if_else');
const setVariable = workspace.newBlock('data_setvariableto');
setVariable.setFieldValue('fixture-score', 'VARIABLE');
report.nativeBlocks = [move, conditional, setVariable].map(snapshot);
const stateField = setVariable.getField('VARIABLE');
const stateOptions = stateField.getOptions(false);
report.variableMenu = {
  raw: stateOptions,
  actualVariables: stateOptions.filter(option => variables.getVariableById(option[1])),
  excludedUiCommands: stateOptions.filter(option => !variables.getVariableById(option[1]))
};

const stop = workspace.newBlock('control_stop');
report.stopVariants = [];
for (const option of ['all', 'this script', 'other scripts in sprite']) {
  stop.setFieldValue(option, 'STOP_OPTION');
  report.stopVariants.push({option, previous: Boolean(stop.previousConnection), next: Boolean(stop.nextConnection)});
}

const goTo = workspace.newBlock('motion_goto_menu');
const clone = workspace.newBlock('control_create_clone_of_menu');
report.dynamicMenus = [];
for (const editingTarget of [cat, stage]) {
  vm.editingTarget = editingTarget;
  report.dynamicMenus.push({editingTarget: editingTarget.sprite.name, goTo: goTo.getField('TO').getOptions(false), clone: clone.getField('CLONE_OPTION').getOptions(false)});
}

const colors = Object.fromEntries(['motion', 'looks', 'sounds', 'event', 'control', 'sensing', 'operators', 'data', 'more'].map(category => [category, {colourPrimary: '#ff0000', colourTertiary: '#cc0000'}]));
const makeToolbox = loadGuiModule('src/lib/make-toolbox-xml.js', {'scratch-blocks': Blocks, './settings/color-mode': {defaultColors: colors}});
report.toolbox = [];
for (const isStage of [false, true]) {
  const xml = makeToolbox(false, isStage, 'fixture-target', [], '造型1', '背景1', '喵', colors);
  const doc = new DOMParser().parseFromString(xml, 'text/xml');
  report.toolbox.push({isStage, movement: doc.querySelector('block[type="motion_movesteps"]')?.outerHTML ?? null, repeat: doc.querySelector('block[type="control_repeat"]')?.outerHTML ?? null, ifElse: doc.querySelector('block[type="control_if_else"]')?.outerHTML ?? null});
}
const variableXml = Blocks.ScratchVariables.getVariablesCategory(workspace);
report.variableToolbox = ['data_setvariableto', 'data_changevariableby'].map(type => ({type, xml: variableXml.find(element => element.getAttribute('type') === type)?.outerHTML ?? null}));
report.jsonInitCaptures = jsonInitCaptures;
report.notes = ['No installed package or project file changed.', 'Variable dropdown UI action entries are excluded by membership in the variable model, not accepted as data.', 'Connection check null is an unconstrained Scratch connection; numeric defaults are obtained from toolbox shadows separately.', 'Fixture menu choices are examples; production dynamic menus must resolve against the actual target and project.'];
workspace.dispose();
process.stdout.write(JSON.stringify(report, (_key, value) => typeof value === 'function' ? '[dynamic function]' : value, 2) + '\n');
