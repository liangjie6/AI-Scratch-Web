import {access, cp, mkdir, readFile, rename, rm, writeFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
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
  try {
    const srcDir = resolve(guiDist, directory);
    await access(srcDir);
    await cp(srcDir, resolve(scratchOutput, directory), {
      recursive: true
    });
  } catch {
    // Scratch output already has assets in repo; ignore missing dist source
  }
}

const libraryFiles = ['sprites.json', 'costumes.json', 'backdrops.json', 'sounds.json'];
const libraryAssets = new Set();
const collectLibraryAssets = value => {
  if (!value || typeof value !== 'object') return;
  if (typeof value.md5ext === 'string') libraryAssets.add(value.md5ext);
  else if (typeof value.md5 === 'string' && typeof value.dataFormat === 'string') {
    libraryAssets.add(`${value.md5}.${value.dataFormat}`);
  }
  Object.values(value).forEach(collectLibraryAssets);
};
for (const libraryFile of libraryFiles) {
  try {
    collectLibraryAssets(JSON.parse(await readFile(resolve(scratchOutput, 'libraries', libraryFile), 'utf8')));
  } catch {
    // ignore missing library files
  }
}

const assetNamePattern = /^([a-f0-9]{32})\.(svg|png|jpg|jpeg|wav|mp3)$/i;
const assertLibraryAssetName = assetName => {
  const match = assetNamePattern.exec(assetName);
  if (!match) throw new Error(`Scratch library manifest contains an invalid asset name: ${assetName}`);
  return match[1].toLowerCase();
};
const hasValidCachedAsset = async assetName => {
  const expectedHash = assertLibraryAssetName(assetName);
  try {
    const bytes = await readFile(resolve(libraryAssetOutput, assetName));
    return bytes.length > 0 && createHash('md5').update(bytes).digest('hex') === expectedHash;
  } catch {
    return false;
  }
};

await mkdir(libraryAssetOutput, {recursive: true});
const pendingAssets = [];
for (const assetName of libraryAssets) {
  if (!await hasValidCachedAsset(assetName)) pendingAssets.push(assetName);
}
const downloadAsset = async assetName => {
  const expectedHash = assertLibraryAssetName(assetName);
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
      if (createHash('md5').update(data).digest('hex') !== expectedHash) throw new Error('content hash mismatch');
      await writeFile(temporaryPath, data);
      await rm(outputPath, {force: true});
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

// Keep the upstream standalone bundle byte-for-byte intact. Runtime asset routing
// belongs to ScratchHost's ScratchStorage configuration, never string replacement.
await cp(
  resolve(guiDist, 'scratch-gui-standalone.js'),
  resolve(scratchOutput, 'scratch-gui-standalone.js')
);

const packageVersion = async packageName => {
  const value = JSON.parse(await readFile(resolve(frontendRoot, 'node_modules', packageName, 'package.json'), 'utf8'));
  return String(value.version);
};
const standaloneBytes = await readFile(resolve(guiDist, 'scratch-gui-standalone.js'));
const libraryManifestSha256 = Object.fromEntries(await Promise.all(
  libraryFiles.map(async file => [file, createHash('sha256').update(await readFile(resolve(scratchOutput, 'libraries', file))).digest('hex')])
));
await writeFile(resolve(scratchOutput, 'editor-capabilities.json'), JSON.stringify({
  schema_version: 1,
  packages: {
    scratch_gui: await packageVersion('@scratch/scratch-gui'),
    scratch_vm: await packageVersion('@scratch/scratch-vm'),
    scratch_storage: await packageVersion('@scratch/scratch-storage')
  },
  standalone: {
    file: 'scratch-gui-standalone.js',
    sha256: createHash('sha256').update(standaloneBytes).digest('hex')
  },
  library_manifest_sha256: libraryManifestSha256,
  library_asset_count: libraryAssets.size
}, null, 2) + '\n');

await cp(
  resolve(competitorAssets, 'scratch_logo.svg'),
  resolve(uiOutput, 'scratch_logo.svg')
);

console.log(`Prepared local Scratch GUI, ${libraryAssets.size} library assets, and UI assets.`);
