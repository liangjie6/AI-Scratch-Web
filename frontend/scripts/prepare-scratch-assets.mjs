import {access, cp, mkdir, readFile, rename, rm, writeFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {ProxyAgent, fetch as undiciFetch} from 'undici';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptDir, '..');
const guiDist = resolve(frontendRoot, 'node_modules/@scratch/scratch-gui/dist');
const scratchOutput = resolve(frontendRoot, 'public/assets/scratch');
const uiOutput = resolve(frontendRoot, 'public/assets/ui');
const blocksMediaOutput = resolve(frontendRoot, 'public/static/blocks-media');
const libraryAssetOutput = resolve(scratchOutput, 'library-assets');
const scratchAssetCdn = 'https://cdn.assets.scratch.mit.edu/internalapi/asset';
const scratchAssetProxy = process.env.SCRATCH_ASSET_PROXY
  ?? process.env.HTTPS_PROXY
  ?? (process.platform === 'win32' ? 'http://127.0.0.1:7897' : undefined);
const scratchAssetDispatcher = scratchAssetProxy ? new ProxyAgent(scratchAssetProxy) : undefined;
const competitorAssets = resolve(
  frontendRoot,
  '../../各种参考文档/Scratch网页竞品/图形化编程-自由创作区_files'
);

await mkdir(scratchOutput, {recursive: true});
await mkdir(uiOutput, {recursive: true});

for (const directory of ['static', 'chunks', 'libraries']) {
  await cp(resolve(guiDist, directory), resolve(scratchOutput, directory), {
    recursive: true
  });
}

const libraryFiles = ['sprites.json', 'costumes.json', 'backdrops.json', 'sounds.json'];
const libraryAssets = new Set();
const collectLibraryAssets = value => {
  if (!value || typeof value !== 'object') return;
  if (typeof value.md5ext === 'string') libraryAssets.add(value.md5ext);
  if (typeof value.md5 === 'string') libraryAssets.add(value.md5);
  Object.values(value).forEach(collectLibraryAssets);
};
for (const libraryFile of libraryFiles) {
  collectLibraryAssets(JSON.parse(await readFile(resolve(scratchOutput, 'libraries', libraryFile), 'utf8')));
}

await mkdir(libraryAssetOutput, {recursive: true});
const pendingAssets = [];
for (const assetName of libraryAssets) {
  try {
    await access(resolve(libraryAssetOutput, assetName));
  } catch {
    pendingAssets.push(assetName);
  }
}
const downloadAsset = async assetName => {
  const outputPath = resolve(libraryAssetOutput, assetName);
  const temporaryPath = `${outputPath}.tmp`;
  let lastError;
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    try {
      const response = await undiciFetch(`${scratchAssetCdn}/${assetName}/get/`, {
        dispatcher: scratchAssetDispatcher
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const data = new Uint8Array(await response.arrayBuffer());
      if (!data.length) throw new Error('empty response');
      await writeFile(temporaryPath, data);
      await rename(temporaryPath, outputPath);
      return;
    } catch (error) {
      lastError = error;
      await rm(temporaryPath, {force: true});
    }
  }
  throw new Error(`Failed to cache Scratch asset ${assetName}: ${lastError}`);
};
const downloadWorkers = Array.from({length: Math.min(16, pendingAssets.length)}, async () => {
  while (pendingAssets.length) await downloadAsset(pendingAssets.pop());
});
await Promise.all(downloadWorkers);

await cp(resolve(guiDist, 'static/blocks-media'), blocksMediaOutput, {
  recursive: true
});

for (const file of ['scratch-gui.js', 'scratch-gui.js.LICENSE.txt', 'extension-worker.js']) {
  await cp(resolve(guiDist, file), resolve(scratchOutput, file));
}

const standalonePath = resolve(scratchOutput, 'scratch-gui-standalone-local.js');
const standalone = await readFile(resolve(guiDist, 'scratch-gui-standalone.js'), 'utf8');
const deleteConfirmationMarker = 'withDeleteConfirmation:!0';
const markerCount = standalone.split(deleteConfirmationMarker).length - 1;
if (markerCount !== 1) {
  throw new Error(`Expected one Scratch sprite deletion confirmation marker, found ${markerCount}.`);
}
const scratchCdnPrefix = 'https://cdn.assets.scratch.mit.edu/internalapi/asset/';
const scratchCdnPrefixCount = standalone.split(scratchCdnPrefix).length - 1;
if (scratchCdnPrefixCount !== 2) {
  throw new Error(`Expected two Scratch library CDN references, found ${scratchCdnPrefixCount}.`);
}
const patchedStandalone = standalone
  .replace(deleteConfirmationMarker, 'withDeleteConfirmation:!1')
  .replaceAll(scratchCdnPrefix, '/assets/scratch/library-assets/')
  .replaceAll('.concat(e.dataFormat,"/get/")', '.concat(e.dataFormat)')
  .replaceAll('.concat(o,"/get/")', '.concat(o)')
  .replace(/([A-Za-z0-9_]+\.p)="\//g, '$1="/assets/scratch/');
await writeFile(standalonePath, patchedStandalone);

await cp(
  resolve(competitorAssets, 'scratch_logo.svg'),
  resolve(uiOutput, 'scratch_logo.svg')
);

console.log(`Prepared local Scratch GUI, ${libraryAssets.size} library assets, and UI assets.`);
