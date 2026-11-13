#!/usr/bin/env node
// Exercise the package's actual browser exports; no fake VM/renderer.
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {createRequire} from 'node:module';

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const require = createRequire(path.join(frontendRoot, 'package.json'));
const {build} = require('esbuild');
const {chromium} = require('playwright');
const {zipSync, strToU8} = require('fflate');
const bundled = await build({
  stdin: {contents: `
    import VM from '@scratch/scratch-vm';
    import * as Storage from '@scratch/scratch-storage';
    globalThis.__verifyBrowserRoundTrip = async bytes => {
      const first = new VM(); first.attachStorage(new Storage.ScratchStorage());
      const second = new VM(); second.attachStorage(new Storage.ScratchStorage());
      try {
        await first.loadProject(new Uint8Array(bytes));
        const output = new Uint8Array(await (await first.saveProjectSb3()).arrayBuffer());
        await second.loadProject(output);
        return {loadedTargets: second.runtime.targets.length, bytes: output.length,
          costumeNames: second.runtime.targets.map(target => target.getCostumes().map(costume => costume.name))};
      } finally {first.quit(); second.quit();}
    };`, resolveDir: frontendRoot},
  bundle: true, platform: 'browser', format: 'iife', write: false, logLevel: 'silent',
});
const asset = strToU8('<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><path d="M0 0h2v2H0z"/></svg>');
const assetId = createHash('md5').update(asset).digest('hex');
const project = {targets: [{
  isStage: true, name: 'Stage', variables: {}, lists: {}, broadcasts: {}, blocks: {}, comments: {}, currentCostume: 0,
  costumes: [{name: 'fixture', assetId, md5ext: assetId + '.svg', dataFormat: 'svg', bitmapResolution: 1, rotationCenterX: 1, rotationCenterY: 1}],
  sounds: [], volume: 100, layerOrder: 0, tempo: 60, videoTransparency: 50, videoState: 'on',
}], monitors: [], extensions: [], meta: {semver: '3.0.0', vm: '15.1.1', agent: 'browser-verification-probe'}};
const candidate = zipSync({'project.json': strToU8(JSON.stringify(project)), [assetId + '.svg']: asset});
const browser = await chromium.launch({headless: true, args: ['--no-sandbox']});
try {
  const page = await browser.newPage();
  await page.goto('about:blank');
  await page.addScriptTag({content: bundled.outputFiles[0].text});
  const result = await page.evaluate(bytes => globalThis.__verifyBrowserRoundTrip(bytes), Array.from(candidate));
  if (result.loadedTargets !== 1 || result.costumeNames[0]?.[0] !== 'fixture') throw new Error('browser roundtrip lost fixture target/costume');
  process.stdout.write(JSON.stringify({status: 'PASS', bundleBytes: bundled.outputFiles[0].contents.length, browser: browser.version(),
    scope: 'actual browser package bundle and one tiny SB3 load/export/load, no renderer, audio, extension or semantic quality claim', roundTrip: result}, null, 2) + '\n');
} finally {await browser.close();}
