#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {execFileSync} from 'node:child_process';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const OVERLAY = 'contracts/scratch-policy-overlay.json';
const CAPABILITIES = 'contracts/scratch-capabilities.json';
const TYPESCRIPT = 'frontend/src/ai-blocks/block-catalog.ts';
const GOLANG = 'backend-go/internal/blockcatalog/catalog.go';
const sha256 = value => createHash('sha256').update(value).digest('hex');
export function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])]));
  return value;
}
const json = value => JSON.stringify(value, null, 2);
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const keysEqual = (left, right) => JSON.stringify([...left].sort()) === JSON.stringify([...right].sort());

function sourceManifest(root, overlay, packages) {
  const files = new Set(['frontend/package-lock.json', OVERLAY, 'scripts/generate-scratch-capabilities.mjs']);
  const addTree = relative => {
    const absolute = path.join(root, relative);
    for (const item of fs.readdirSync(absolute, {withFileTypes: true})) {
      const child = `${relative}/${item.name}`;
      if (item.isDirectory()) addTree(child);
      else if (/\.(ts|js|mjs)$/.test(item.name)) files.add(child);
    }
  };
  addTree('frontend/node_modules/scratch-blocks/src/blocks');
  addTree('frontend/node_modules/scratch-blocks/src/fields');
  addTree('frontend/node_modules/@scratch/scratch-vm/src/blocks');
  for (const extension of overlay.allowedExtensions) addTree(`frontend/node_modules/@scratch/scratch-vm/src/extensions/scratch3_${extension}`);
  for (const relative of [
    'scratch-blocks/src/index.ts', 'scratch-blocks/src/constants.ts', 'scratch-blocks/src/data_category.ts',
    'scratch-blocks/src/variables.ts', 'scratch-blocks/src/procedures.ts', 'scratch-blocks/msg/messages.js',
    'scratch-blocks/dist/main.mjs', '@scratch/scratch-gui/src/lib/blocks.js',
    '@scratch/scratch-gui/src/lib/make-toolbox-xml.js', '@scratch/scratch-vm/src/engine/runtime.js',
    '@scratch/scratch-vm/src/serialization/sb3.js'
  ]) files.add(`frontend/node_modules/${relative}`);
  for (const name of Object.keys(packages)) files.add(`frontend/node_modules/${name}/package.json`);
  return {packages, files: Object.fromEntries([...files].sort().map(relative => [relative, sha256(fs.readFileSync(path.join(root, relative)))]))};
}

function definitionIndex(root, ts) {
  const directory = path.join(root, 'frontend/node_modules/scratch-blocks/src/blocks');
  const result = new Map();
  for (const filename of fs.readdirSync(directory).filter(name => name.endsWith('.ts')).sort()) {
    const absolute = path.join(directory, filename);
    const source = ts.createSourceFile(absolute, fs.readFileSync(absolute, 'utf8'), ts.ScriptTarget.Latest, true);
    const visit = node => {
      if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken) {
        const left = node.left.getText(source);
        const match = left.match(/^Blockly\.Blocks\.(\w+)$/) ?? left.match(/^Blockly\.Blocks\[['"]([^'"]+)['"]\]$/);
        if (match) {
          const messages = new Set();
          const walk = child => {
            if (ts.isPropertyAccessExpression(child) && child.expression.getText(source) === 'Blockly.Msg') messages.add(child.name.text);
            ts.forEachChild(child, walk);
          };
          walk(node.right);
          result.set(match[1], {file: path.relative(root, absolute).split(path.sep).join('/'), line: source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1, message_keys: [...messages].sort()});
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(source);
  }
  return result;
}

function loadGuiSource(root, ts, relative, dependencies) {
  const source = fs.readFileSync(path.join(root, 'frontend/node_modules/@scratch/scratch-gui', relative), 'utf8');
  const compiled = ts.transpileModule(source, {compilerOptions: {module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, allowJs: true}}).outputText;
  const module = {exports: {}};
  // Execute the pinned installed GUI module in this isolated build process.
  new Function('require', 'module', 'exports', compiled)(name => {
    assert(Object.hasOwn(dependencies, name), `Unexpected GUI source dependency: ${name}`);
    return dependencies[name];
  }, module, module.exports);
  return module.exports.default;
}

function shadowFromXml(element, dynamicMenus) {
  const shadows = {};
  for (const value of element.children) {
    if (value.tagName !== 'value') continue;
    const shadow = [...value.children].find(child => child.tagName === 'shadow');
    if (!shadow) continue;
    const opcode = shadow.getAttribute('type');
    const fields = Object.fromEntries([...shadow.children].filter(child => child.tagName === 'field').map(field => {
      const name = field.getAttribute('name');
      const provider = dynamicMenus[`${opcode}.${name}`];
      // A selected costume/sound in the probe is fixture state, not a native
      // literal default. Record its provider instead of shipping the asset name.
      return [name, provider && !field.textContent.startsWith('_') ? {menu_provider: provider} : field.textContent];
    }));
    shadows[value.getAttribute('name')] = {opcode, fields};
  }
  return shadows;
}

export async function extractCapabilities(root = ROOT) {
  const overlay = JSON.parse(fs.readFileSync(path.join(root, OVERLAY), 'utf8'));
  const require = createRequire(path.join(root, 'frontend/package.json'));
  const packages = Object.fromEntries(Object.keys(overlay.pinnedVersions).map(name => [name, JSON.parse(fs.readFileSync(path.join(root, 'frontend/node_modules', name, 'package.json'), 'utf8')).version]));
  for (const [name, expected] of Object.entries(overlay.pinnedVersions)) assert(packages[name] === expected, `${name} version ${packages[name]} differs from reviewed ${expected}`);
  assert(keysEqual(overlay.allowedExtensions, ['pen', 'music', 'text2speech']), 'Extension allowlist must remain pen/music/text2speech');

  const vmSource = path.join(root, 'frontend/node_modules/@scratch/scratch-vm/src');
  const Runtime = require(path.join(vmSource, 'engine/runtime.js'));
  const runtime = new Runtime();
  const nativePrimitives = new Set(Object.keys(runtime._primitives));
  const nativeHats = new Set(Object.keys(runtime._hats));
  assert(runtime.getBlocksJSON().length === 0, 'Core Runtime getBlocksJSON changed; extraction requires review');
  require('format-message').setup({locale: 'en', missingTranslation: 'ignore'});
  const extensions = new Map();
  for (const name of overlay.allowedExtensions) {
    const Extension = require(path.join(vmSource, `extensions/scratch3_${name}/index.js`));
    const instance = new Extension(runtime);
    const info = instance.getInfo();
    assert(info.id === name, `Unexpected extension ID: ${info.id}`);
    for (const block of info.blocks) {
      if (typeof block !== 'object' || !block.opcode) continue;
      extensions.set(`${name}_${block.opcode}`, {name, info: block, menus: info.menus, primitive: typeof instance[block.func ?? block.opcode] === 'function', source: `frontend/node_modules/@scratch/scratch-vm/src/extensions/scratch3_${name}/index.js`});
    }
  }

  const {JSDOM} = require('jsdom');
  const ts = require('typescript');
  const dom = new JSDOM('<!doctype html><html><body><div id="workspace"></div></body></html>', {pretendToBeVisual: true, url: 'http://localhost/'});
  for (const key of ['window', 'document', 'Element', 'HTMLElement', 'SVGElement', 'Node', 'XMLSerializer', 'DOMParser', 'navigator', 'MutationObserver', 'getComputedStyle']) {
    if (dom.window[key]) Object.defineProperty(globalThis, key, {value: dom.window[key], configurable: true});
  }
  const Blockly = await import(pathToFileURL(require.resolve('scratch-blocks')).href);
  Blockly.ScratchMsgs.setLocale('en');
  const stage = {isStage: true, isOriginal: true, sprite: {name: 'fixture-stage', sounds: [], costumes: [{name: 'fixture-backdrop'}]}};
  const sprite = {isStage: false, isOriginal: true, sprite: {name: 'fixture-owner', sounds: [{name: 'fixture-sound'}], costumes: [{name: 'fixture-costume'}]}};
  const other = {isStage: false, isOriginal: true, sprite: {name: 'fixture-other', sounds: [], costumes: []}};
  for (const target of [stage, sprite, other]) target.getCostumes = () => target.sprite.costumes;
  const vm = {editingTarget: sprite, runtime: {targets: [stage, sprite, other], getTargetForStage: () => stage, monitorBlocks: {_blocks: {}}}};
  loadGuiSource(root, ts, 'src/lib/blocks.js', {'scratch-blocks': Blockly})(vm);
  const workspace = Blockly.inject(document.querySelector('#workspace'), {media: 'http://localhost/media/', toolbox: null, sounds: false});
  Blockly.Events.disable();
  for (const [name, type, id] of [['i', '', 'fixture-variable'], ['fixture-list', 'list', 'fixture-list'], ['fixture-message', 'broadcast_msg', 'fixture-broadcast']]) workspace.getVariableMap().createVariable(name, type, id);
  const definitions = definitionIndex(root, ts);
  const rawDefinitions = new Map();
  const originalInit = Blockly.Block.prototype.jsonInit;
  Blockly.Block.prototype.jsonInit = function (definition) { rawDefinitions.set(this.type, definition); return originalInit.call(this, definition); };
  const excludedValues = new Set(overlay.excludedMenuValues);
  const instances = new Map();
  const instance = opcode => {
    if (!instances.has(opcode)) instances.set(opcode, workspace.newBlock(opcode));
    return instances.get(opcode);
  };
  const fieldFact = (opcode, field) => {
    const declared = [...Object.entries(rawDefinitions.get(opcode) ?? {})].filter(([key]) => /^args\d+$/.test(key)).flatMap(([, value]) => value).find(argument => argument.name === field.name);
    const result = {name: field.name, field_type: declared?.type ?? (typeof field.getOptions === 'function' ? 'field_dropdown' : 'custom_field')};
    const provider = overlay.dynamicMenus[`${opcode}.${field.name}`] ?? (field.referencesVariables?.() ? (declared?.allowedVariableType === 'list' || declared?.defaultType === 'list' ? 'lists_in_scope' : declared?.defaultType === 'broadcast_msg' ? 'broadcasts_in_scope' : 'variables_in_scope') : undefined);
    if (provider) result.menu_provider = provider;
    // Variable models supply runtime state; their dropdown also contains rename,
    // delete and create UI commands. Never capture those entries as data enums.
    const options = !field.referencesVariables?.() && typeof field.getOptions === 'function' ? field.getOptions(false) : [];
    const values = options.filter(option => Array.isArray(option) && option.length === 2).map(option => String(option[1])).filter(value => value && !excludedValues.has(value));
    if (provider) result.builtin_values = [...new Set(values.filter(value => value.startsWith('_') && value.endsWith('_')))];
    // Colour pickers and editable number dropdowns expose suggestions, not an
    // exhaustive value set. Only a static dropdown defines an authoring enum.
    else if (options.length && result.field_type === 'field_dropdown') {
      assert(!field.isOptionListDynamic?.(), `Dynamic menu lacks a reviewed provider: ${opcode}.${field.name}`);
      result.allowed_values = [...new Set(values)];
    }
    if (declared?.variableTypes) result.variable_types = declared.variableTypes;
    if (declared?.allowedVariableType !== undefined) result.variable_types = [declared.allowedVariableType];
    return result;
  };

  const colors = Object.fromEntries(['motion', 'looks', 'sounds', 'event', 'control', 'sensing', 'operators', 'data', 'more'].map(category => [category, {colourPrimary: '#ff0000', colourTertiary: '#cc0000'}]));
  const toolbox = loadGuiSource(root, ts, 'src/lib/make-toolbox-xml.js', {'scratch-blocks': Blockly, './settings/color-mode': {defaultColors: colors}});
  const palette = new Map();
  const defaults = new Map();
  const collectXml = (element, target) => {
    const opcode = element.getAttribute('type');
    if (!opcode) return;
    if (!palette.has(opcode)) palette.set(opcode, new Set());
    palette.get(opcode).add(target);
    const shadows = shadowFromXml(element, overlay.dynamicMenus);
    if (Object.keys(shadows).length) defaults.set(opcode, shadows);
  };
  for (const target of ['sprite', 'stage']) {
    vm.editingTarget = target === 'stage' ? stage : sprite;
    const doc = new DOMParser().parseFromString(toolbox(false, target === 'stage', 'fixture-target', [], 'fixture-costume', 'fixture-backdrop', 'fixture-sound', colors), 'text/xml');
    for (const element of doc.querySelectorAll('category > block')) collectXml(element, target);
    for (const element of Blockly.ScratchVariables.getVariablesCategory(workspace)) if (element.tagName === 'block') collectXml(element, target);
  }
  vm.editingTarget = sprite;

  const blocks = {};
  try {
    for (const [opcode, policy] of Object.entries(overlay.blocks).sort(([left], [right]) => left.localeCompare(right, 'en'))) {
      const extension = extensions.get(opcode);
      let facts;
      if (extension) {
        const info = extension.info;
        const fields = [];
        const inputs = [];
        const shadows = {};
        for (const [name, argument] of Object.entries(info.arguments ?? {})) {
          const menu = argument.menu ? extension.menus[argument.menu] : undefined;
          const fact = {name, connection_check: argument.type === 'Boolean' ? ['Boolean'] : null};
          if (menu) {
            assert(Array.isArray(menu.items), `Unsupported dynamic extension menu: ${opcode}.${name}`);
            fact.allowed_values = menu.items.map(item => String(typeof item === 'object' ? item.value : item));
          }
          if (menu && !menu.acceptReporters) fields.push({...fact, field_type: 'field_dropdown'});
          else inputs.push(fact);
          if (argument.defaultValue !== undefined) shadows[name] = {argument_type: argument.type, default_value: argument.defaultValue};
        }
        facts = {source: {file: extension.source}, shape: info.blockType === 'reporter' ? 'reporter' : info.blockType === 'Boolean' ? 'boolean' : info.isTerminal ? 'cap' : 'stack', fields, inputs, substacks: [], palette_targets: info.filter ?? ['stage', 'sprite'], default_shadows: shadows, vm: {primitive: extension.primitive, hat: false, extension: extension.name}, messages: [typeof info.text === 'string' ? info.text : info.text?.default ?? '']};
      } else {
        assert(definitions.has(opcode), `Allowed opcode has no pinned native definition: ${opcode}`);
        const block = instance(opcode);
        const fields = block.inputList.flatMap(input => input.fieldRow).filter(field => field.name).map(field => fieldFact(opcode, field));
        const inputs = block.inputList.filter(input => input.type === Blockly.inputs.inputTypes.VALUE).map(input => ({name: input.name, connection_check: input.connection?.getCheck() ?? null}));
        const substacks = block.inputList.filter(input => input.type === Blockly.inputs.inputTypes.STATEMENT).map(input => input.name);
        const shape = block.outputConnection ? (block.outputConnection.getCheck()?.includes('Boolean') ? 'boolean' : 'reporter') : block.previousConnection ? (block.nextConnection ? 'stack' : 'cap') : 'hat';
        const raw = rawDefinitions.get(opcode) ?? {};
        facts = {source: definitions.get(opcode), shape, fields, inputs, substacks, palette_targets: [...(palette.get(opcode) ?? [])].sort(), default_shadows: defaults.get(opcode) ?? {}, vm: {primitive: nativePrimitives.has(opcode), hat: nativeHats.has(opcode)}, messages: Object.entries(raw).filter(([key]) => /^message\d+$/.test(key)).sort(([left], [right]) => left.localeCompare(right)).map(([, value]) => value)};
        if (opcode === 'control_stop') {
          facts.variants = ['all', 'this script', 'other scripts in sprite'].map(value => { block.setFieldValue(value, 'STOP_OPTION'); return {field: 'STOP_OPTION', value, has_next: Boolean(block.nextConnection)}; });
        }
        // The compiler may deliberately use a different equivalent shadow. Keep
        // native defaults separate and only attach menu metadata to authoring ports.
        for (const input of facts.inputs) {
          const shadow = facts.default_shadows[input.name];
          const compiler = policy.inputs?.[input.name];
          const menuOpcode = compiler?.shadowOpcode ?? shadow?.opcode;
          if (!menuOpcode || !Blockly.Blocks[menuOpcode]?.init) continue;
          const menu = instance(menuOpcode);
          const menuFields = menu.inputList.flatMap(value => value.fieldRow).filter(field => field.name);
          if (menuFields.length === 1) input.menu = fieldFact(menuOpcode, menuFields[0]);
        }
      }
      assert(facts.vm.primitive || facts.vm.hat, `Allowed opcode lacks VM primitive/hat: ${opcode}`);
      assert(keysEqual(facts.inputs.map(input => input.name), Object.keys(policy.inputs ?? {})), `Source input drift for ${opcode}`);
      for (const field of Object.keys(policy.fieldReferences ?? {})) assert(facts.fields.some(item => item.name === field), `Source reference field drift: ${opcode}.${field}`);
      assert(policy.targets.length && policy.targets.every(target => target === 'stage' || target === 'sprite'), `Invalid target policy: ${opcode}`);
      const authoring = {shape: policy.shapeOverride ?? facts.shape, targets: policy.targets};
      const fieldSpecs = Object.fromEntries(facts.fields.map(field => {
        const spec = {required: true, ...(policy.fieldReferences?.[field.name] ?? {})};
        if (field.allowed_values?.length) spec.allowedValues = field.allowed_values;
        if (field.menu_provider) spec.menuProvider = field.menu_provider;
        if (field.builtin_values?.length) spec.builtinValues = field.builtin_values;
        return [field.name, spec];
      }));
      if (Object.keys(fieldSpecs).length) authoring.fields = fieldSpecs;
      if (policy.inputs) authoring.inputs = Object.fromEntries(Object.entries(policy.inputs).map(([name, input]) => {
        const native = facts.inputs.find(item => item.name === name);
        const menu = native.menu ?? native;
        return [name, {...input, ...(menu.allowed_values?.length ? {allowedValues: menu.allowed_values} : {}), ...(menu.menu_provider ? {menuProvider: menu.menu_provider} : {}), ...(menu.builtin_values?.length ? {builtinValues: menu.builtin_values} : {})}];
      }));
      if (facts.substacks.length) authoring.substacks = facts.substacks;
      blocks[opcode] = {authoring, input_order: Object.keys(policy.inputs ?? {}), source_facts: facts, ...(policy.unsupportedVariants ? {unsupported_variants: policy.unsupportedVariants} : {})};
    }
    const body = canonical({schema_version: 1, sources: sourceManifest(root, overlay, packages), allowed_extensions: overlay.allowedExtensions, runtime_facts: {native_primitives: nativePrimitives.size, native_hats: nativeHats.size, core_blocks_json_count: 0}, registered_native_types: Object.keys(Blockly.Blocks).sort(), unsupported_native_types: Object.keys(Blockly.Blocks).filter(opcode => !overlay.blocks[opcode]).sort(), blocks});
    return {catalog_hash: sha256(JSON.stringify(body)), ...body};
  } finally {
    Blockly.Block.prototype.jsonInit = originalInit;
    workspace.dispose();
    Blockly.Events.enable();
    dom.window.close();
  }
}

function renderTypescript(capabilities) {
  const catalog = Object.fromEntries(Object.entries(capabilities.blocks).map(([opcode, block]) => [opcode, {...block.authoring, ...(block.authoring.inputs ? {inputs: Object.fromEntries(block.input_order.map(name => [name, block.authoring.inputs[name]]))} : {})}]));
  return `// Generated by scripts/generate-scratch-capabilities.mjs. Do not edit manually.
import type {InputKind, TargetKind} from './program-ir';

export type BlockShape = 'hat' | 'stack' | 'cap' | 'reporter' | 'boolean';
export type FieldResolverKind = 'variable' | 'list' | 'broadcast';
export type ResourceMenuAssetType = 'sprite' | 'costume' | 'backdrop' | 'sound';
export interface MenuMetadata {
  /** Serialized values from a static source menu, never display labels. */
  allowedValues?: readonly string[];
  /** Project-dependent data is resolved against the frozen target at request time. */
  menuProvider?: string;
  builtinValues?: readonly string[];
}
export interface InputSpec extends MenuMetadata {
  kind: InputKind | 'boolean' | 'any';
  required?: boolean;
  shadow?: Exclude<InputKind, 'block' | 'resource_ref' | 'existing_asset_ref'>;
  shadowOpcode?: string;
  shadowField?: string;
  shadowResolver?: FieldResolverKind;
  resourceType?: ResourceMenuAssetType;
}
export interface FieldSpec extends MenuMetadata {
  required?: boolean;
  resolver?: FieldResolverKind;
  resourceType?: ResourceMenuAssetType;
}
export interface BlockSpec {
  shape: BlockShape;
  targets: readonly TargetKind[];
  inputs?: Readonly<Record<string, InputSpec>>;
  fields?: Readonly<Record<string, FieldSpec>>;
  substacks?: readonly ('SUBSTACK' | 'SUBSTACK2')[];
}

export const SCRATCH_CATALOG_HASH = ${JSON.stringify(capabilities.catalog_hash)};
export const SCRATCH_SOURCE_VERSIONS = ${json(capabilities.sources.packages)} as const;
export const BLOCK_CATALOG = ${json(catalog)} as const satisfies Record<string, BlockSpec>;
export type NativeOpcode = keyof typeof BLOCK_CATALOG;
export const getBlockSpec = (opcode: string): BlockSpec | undefined =>
  (BLOCK_CATALOG as Record<string, BlockSpec>)[opcode];
export type BuiltinAiExtension = 'pen' | 'music' | 'text2speech';
export const isBuiltinAiExtension = (value: unknown): value is BuiltinAiExtension =>
  value === 'pen' || value === 'music' || value === 'text2speech';
export const extensionForOpcode = (opcode: string): BuiltinAiExtension | undefined =>
  opcode.startsWith('pen_') ? 'pen' : opcode.startsWith('music_') ? 'music' : opcode.startsWith('text2speech_') ? 'text2speech' : undefined;
`;
}

function renderGo(capabilities) {
  const quote = JSON.stringify;
  const goMap = (entries, type = 'bool') => entries.length ? `map[string]${type}{${entries.map(([key, value]) => `${quote(key)}: ${value}`).join(', ')}}` : 'nil';
  const boolMap = values => goMap(values.map(value => [value, 'true']));
  const stringSlice = values => `[]string{${values.map(quote).join(', ')}}`;
  const enumMap = specs => goMap(Object.entries(specs ?? {}).filter(([, spec]) => spec.allowedValues?.length).map(([name, spec]) => [name, stringSlice(spec.allowedValues)]), '[]string');
  const providerMap = specs => goMap(Object.entries(specs ?? {}).filter(([, spec]) => spec.menuProvider).map(([name, spec]) => [name, quote(spec.menuProvider)]), 'string');
  const builtinMap = specs => goMap(Object.entries(specs ?? {}).filter(([, spec]) => spec.builtinValues?.length).map(([name, spec]) => [name, stringSlice(spec.builtinValues)]), '[]string');
  const blocks = Object.entries(capabilities.blocks).map(([opcode, block]) => {
    const spec = block.authoring;
    const fields = spec.fields ?? {};
    const inputs = spec.inputs ?? {};
    return `${quote(opcode)}: {
Shape: ${quote(spec.shape)}, Targets: ${boolMap(spec.targets)},
Fields: ${boolMap(Object.keys(fields))}, RequiredFields: ${boolMap(Object.keys(fields).filter(name => fields[name].required))},
ResolvedFields: ${boolMap(Object.keys(fields).filter(name => fields[name].resolver))},
Inputs: ${goMap(Object.entries(inputs).map(([name, input]) => [name, quote(input.kind)]), 'InputKind')}, InputOrder: ${stringSlice(block.input_order)},
ResourceInputTypes: ${goMap(Object.entries(inputs).filter(([, input]) => input.resourceType).map(([name, input]) => [name, quote(input.resourceType)]), 'AssetType')},
ResourceFieldTypes: ${goMap(Object.entries(fields).filter(([, field]) => field.resourceType).map(([name, field]) => [name, quote(field.resourceType)]), 'AssetType')},
RequiredInputs: ${boolMap(Object.keys(inputs).filter(name => inputs[name].required))}, Substacks: ${boolMap(spec.substacks ?? [])}, RequiredSubstacks: ${boolMap(spec.substacks ?? [])},
FieldEnums: ${enumMap(fields)}, InputEnums: ${enumMap(inputs)}, FieldProviders: ${providerMap(fields)}, InputProviders: ${providerMap(inputs)},
FieldBuiltins: ${builtinMap(fields)}, InputBuiltins: ${builtinMap(inputs)},
},`;
  }).join('\n');
  const source = `// Code generated by scripts/generate-scratch-capabilities.mjs. DO NOT EDIT.
package blockcatalog
import "strings"
type Shape string
const (ShapeHat Shape = "hat"; ShapeStack Shape = "stack"; ShapeCap Shape = "cap"; ShapeReporter Shape = "reporter"; ShapeBoolean Shape = "boolean")
type InputKind string
const (InputNumber InputKind = "number"; InputText InputKind = "text"; InputAngle InputKind = "angle"; InputColor InputKind = "color"; InputBoolean InputKind = "boolean"; InputAny InputKind = "any")
type AssetType string
const (AssetSprite AssetType = "sprite"; AssetCostume AssetType = "costume"; AssetBackdrop AssetType = "backdrop"; AssetSound AssetType = "sound")
type BlockContract struct {
Shape Shape; Targets map[string]bool; Fields map[string]bool; RequiredFields map[string]bool; ResolvedFields map[string]bool
Inputs map[string]InputKind; InputOrder []string; ResourceInputTypes map[string]AssetType; ResourceFieldTypes map[string]AssetType
RequiredInputs map[string]bool; Substacks map[string]bool; RequiredSubstacks map[string]bool
FieldEnums map[string][]string; InputEnums map[string][]string
FieldProviders map[string]string; InputProviders map[string]string
FieldBuiltins map[string][]string; InputBuiltins map[string][]string
}
const CatalogHash = ${quote(capabilities.catalog_hash)}
var SourceVersions = ${goMap(Object.entries(capabilities.sources.packages).map(([name, version]) => [name, quote(version)]), 'string')}
var BlockContracts = map[string]BlockContract{${blocks}}
func ExtensionForOpcode(opcode string) string {
switch {case strings.HasPrefix(opcode, "pen_"): return "pen"; case strings.HasPrefix(opcode, "music_"): return "music"; case strings.HasPrefix(opcode, "text2speech_"): return "text2speech"}; return ""
}
`;
  return execFileSync('gofmt', [], {input: source, encoding: 'utf8'});
}

export function generatedFiles(capabilities) {
  return {[CAPABILITIES]: json(capabilities) + '\n', [TYPESCRIPT]: renderTypescript(capabilities), [GOLANG]: renderGo(capabilities)};
}

export function staleFiles(root, outputs) {
  return Object.entries(outputs).filter(([relative, expected]) => !fs.existsSync(path.join(root, relative)) || fs.readFileSync(path.join(root, relative), 'utf8') !== expected).map(([relative]) => relative);
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try {
    assert(process.argv.slice(2).every(value => value === '--check'), 'Usage: node scripts/generate-scratch-capabilities.mjs [--check]');
    const capabilities = await extractCapabilities();
    const outputs = generatedFiles(capabilities);
    if (process.argv.includes('--check')) {
      const stale = staleFiles(ROOT, outputs);
      assert(stale.length === 0, `Stale Scratch capabilities: ${stale.join(', ')}. Run node scripts/generate-scratch-capabilities.mjs`);
      console.log(`Scratch capabilities current: ${Object.keys(capabilities.blocks).length} opcodes, ${capabilities.catalog_hash}`);
    } else {
      for (const [relative, content] of Object.entries(outputs)) { fs.mkdirSync(path.dirname(path.join(ROOT, relative)), {recursive: true}); fs.writeFileSync(path.join(ROOT, relative), content); }
      console.log(`Generated Scratch capabilities: ${Object.keys(capabilities.blocks).length} opcodes, ${capabilities.catalog_hash}`);
    }
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
