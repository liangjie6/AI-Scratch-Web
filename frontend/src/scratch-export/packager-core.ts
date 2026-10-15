import {strFromU8, unzip} from 'fflate';
import type {HtmlExportOptions, HtmlExportResult} from './html-export-types';

export const MAX_SB3_SIZE = 50 * 1024 * 1024; // 50 MiB
export const MAX_PROJECT_JSON_SIZE = 10 * 1024 * 1024; // 10 MiB

let cachedTemplate: string | null = null;
let cachedVmCode: string | null = null;

/**
 * Validates and normalizes the export HTML filename.
 * - Can be empty: inferred safely from fallbackBase and appends .html.
 * - If fallbackBase is invalid when filename is empty: throws error prompting user for valid filename.
 * - If contains / or \, ., .., control characters or illegal chars: throws error.
 * - Does NOT silently strip path or take basename.
 */
export function validateExportFilename(filename: string, fallbackBase: string): string {
  const raw = String(filename ?? '').trim();

  // If user left filename empty, derive safely from fallbackBase
  if (!raw) {
    const base = String(fallbackBase ?? '').trim();
    if (!base) {
      return 'Scratch作品.html';
    }
    if (base.includes('/') || base.includes('\\')) {
      throw new Error('作品名包含非法字符，请显式填写合法导出文件名');
    }
    if (base === '.' || base === '..') {
      throw new Error('作品名包含非法字符，请显式填写合法导出文件名');
    }
    if (/[\x00-\x1f\x7f<>\:"\/\\\|\?\*]/.test(base)) {
      throw new Error('作品名包含非法字符，请显式填写合法导出文件名');
    }
    let candidate = base;
    if (!/\.html?$/i.test(candidate)) {
      candidate += '.html';
    }
    if (candidate === '.html' || candidate === '.htm') {
      throw new Error('作品名包含非法字符，请显式填写合法导出文件名');
    }
    return candidate;
  }

  // If user explicitly provided a filename, strictly check and error on invalid input
  if (raw.includes('/') || raw.includes('\\')) {
    throw new Error('文件名不能包含路径分隔符（/ 或 \\）');
  }

  if (raw === '.' || raw === '..') {
    throw new Error('文件名不能为 "." 或 ".."');
  }

  if (/[\x00-\x1f\x7f<>\:"\/\\\|\?\*]/.test(raw)) {
    throw new Error('文件名包含非法字符');
  }

  let candidate = raw;
  if (!/\.html?$/i.test(candidate)) {
    candidate += '.html';
  }

  if (candidate === '.html' || candidate === '.htm') {
    throw new Error('文件名不能为空');
  }

  return candidate;
}

/**
 * Escapes characters for safe inclusion in HTML body or attributes.
 */
export function escapeHtml(value: unknown): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * Escapes </script inside script blocks to avoid breaking out of <script> tag.
 */
export function escapeScriptText(value: unknown): string {
  return String(value ?? '').replace(/<\/script/gi, '<\\/script');
}

/**
 * Cleans conditional section blocks from the HTML template.
 */
export function removePercentSection(input: string, key: string): string {
  const start = `% ${key} %`;
  const end = `% /${key} %`;
  let output = input;
  while (output.includes(start) && output.includes(end)) {
    output = output.slice(0, output.indexOf(start)) + output.slice(output.indexOf(end) + end.length);
  }
  return output;
}

/**
 * Asynchronously converts a Blob to a Base64 data URL using FileReader.
 */
export function blobToDataUrl(blob: Blob, signal?: AbortSignal): Promise<string> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      return reject(new DOMException('Operation aborted', 'AbortError'));
    }
    const reader = new FileReader();

    const onAbort = () => {
      reader.abort();
      reject(new DOMException('Operation aborted', 'AbortError'));
    };

    if (signal) {
      signal.addEventListener('abort', onAbort, {once: true});
    }

    reader.onload = () => {
      if (signal) signal.removeEventListener('abort', onAbort);
      if (typeof reader.result === 'string') {
        resolve(reader.result);
      } else {
        reject(new Error('FileReader 未能返回 Data URL 字符串'));
      }
    };

    reader.onerror = () => {
      if (signal) signal.removeEventListener('abort', onAbort);
      reject(reader.error || new Error('读取项目二进制流失败'));
    };

    reader.onabort = () => {
      if (signal) signal.removeEventListener('abort', onAbort);
      reject(new DOMException('Operation aborted', 'AbortError'));
    };

    reader.readAsDataURL(blob);
  });
}

export const THIRD_PARTY_NOTICES_DECLARATION = `
--------------------------------------------------------------------------------
Third-Party Notices (THIRD_PARTY_NOTICES.md)
--------------------------------------------------------------------------------
This module includes or is derived from third-party open-source work. The
project-level MIT License does not replace the original copyright notices and
license terms of bundled third-party components.

* SheepTester/htmlifier
  - Project: https://github.com/SheepTester/htmlifier
  - Copyright: Copyright (c) 2020 Sean
  - License: MIT License
  - Relevance: the self-contained Scratch HTML packaging model, player-template
    architecture, and SheepTester Scratch VM/HTMLifier resource lineage.
  The upstream MIT copyright and permission notice must be retained in copies
  or substantial portions derived from that project.

* scratch-converter-research-review
  - Project: scratch-converter-research-review
  - Copyright: Copyright (c) 2026 凉介
  - License: MIT License
  - Relevance: standalone HTML export packager logic and verified clean templates.

* Pinned Scratch VM bundle
  vm.min.js is a pinned research copy with SHA-256:
  29f52618abaa41a060862bd3981f846ab4b249ece56762cd2423e8048b1a732e
  (CRLF variant: 42B75BB2591272C43C1250F06068DE2A3957E7486AA68EF6EC05A43EAAE8BDED)

  The minified bundle contains Scratch VM-related code and bundled third-party
  modules. License comments embedded in the bundle must not be removed. Anyone
  redistributing the bundle should audit and preserve the notices applicable to
  its individual components.
--------------------------------------------------------------------------------`;

/**
 * Generates an embedded license comment for the output HTML,
 * preserving upstream MIT license notices and third-party notices.
 */
export function buildEmbeddedLicenseComment(): string {
  const text = `
================================================================================
Scratch Standalone Single-File HTML Player
Packaged by AI Scratch Web

This packaged player includes or is derived from third-party open-source work:

[1] scratch-converter-research-review
MIT License
Copyright (c) 2026 凉介

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

[2] SheepTester/htmlifier
MIT License
Copyright (c) 2020 Sean

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

[3] Third-Party Notices & Upstream Attributions
${THIRD_PARTY_NOTICES_DECLARATION}

[4] Bundled Scratch VM and Third-Party Dependencies
The embedded Scratch VM bundle retains all original copyright and license
notices in accordance with their respective open-source licenses (MIT,
Apache 2.0, BSD-3-Clause).
================================================================================
`;
  // Replace --> to prevent closing HTML comment early
  const safeText = text.replace(/-->/g, '-- >');
  return `<!--${safeText}-->\n`;
}

/**
 * Asynchronously extracts and decodes project.json from an SB3 (ZIP) binary using fflate.
 * Filters out all other entries to prevent decompressing media assets into memory.
 * Enforces a strict decompressed size limit.
 */
export async function extractProjectJson(bytes: Uint8Array): Promise<string> {
  return new Promise((resolve, reject) => {
    let projectJsonBytes: Uint8Array | undefined;
    let oversized = false;

    unzip(
      bytes,
      {
        filter: file => {
          if (file.name === 'project.json') {
            if (file.originalSize && file.originalSize > MAX_PROJECT_JSON_SIZE) {
              oversized = true;
              return false;
            }
            return true;
          }
          return false;
        }
      },
      (err, unzipped) => {
        if (err) {
          return reject(new Error(`文件不是有效的 SB3 (ZIP) 格式: ${err.message}`));
        }
        if (oversized) {
          return reject(new Error('project.json 解压大小超过 10MB 上限'));
        }
        projectJsonBytes = unzipped['project.json'];
        if (!projectJsonBytes) {
          return reject(new Error('SB3 压缩包中缺少 project.json'));
        }
        if (projectJsonBytes.byteLength > MAX_PROJECT_JSON_SIZE) {
          return reject(new Error('project.json 解压大小超过 10MB 上限'));
        }
        try {
          const text = strFromU8(projectJsonBytes);
          resolve(text);
        } catch {
          reject(new Error('project.json 编码不是合法的 UTF-8'));
        }
      }
    );
  });
}

/**
 * Validates a local File to ensure it's a valid SB3 (ZIP) file under 50MB,
 * and verifies that project.json is present, parseable, and contains the minimal Scratch targets array.
 */
export async function validateSb3File(file: File): Promise<Uint8Array> {
  if (file.size > MAX_SB3_SIZE) {
    throw new Error(`文件大小（${(file.size / 1024 / 1024).toFixed(1)} MB）超过 50MB 上限`);
  }
  if (!/\.sb3$/i.test(file.name)) {
    throw new Error('仅支持 .sb3 格式的 Scratch 项目文件');
  }

  const buffer = await file.arrayBuffer();
  const bytes = new Uint8Array(buffer);
  if (bytes.length < 4 || bytes[0] !== 0x50 || bytes[1] !== 0x4b || bytes[2] !== 0x03 || bytes[3] !== 0x04) {
    throw new Error('文件不是有效的 SB3 (ZIP) 格式');
  }

  const projectJsonText = await extractProjectJson(bytes);
  let parsed: unknown;
  try {
    parsed = JSON.parse(projectJsonText);
  } catch {
    throw new Error('project.json 格式错误，无法解析为有效 JSON');
  }

  if (
    !parsed ||
    typeof parsed !== 'object' ||
    !Array.isArray((parsed as Record<string, unknown>).targets)
  ) {
    throw new Error('project.json 不是有效的 Scratch 项目（缺少 targets 列表）');
  }

  return bytes;
}

/**
 * Fetches htmlifier static assets on demand with memory caching.
 * Failed requests are not cached so retry is possible.
 */
export async function loadHtmlifierAssets(options?: {
  signal?: AbortSignal;
  forceReload?: boolean;
}): Promise<{template: string; vmCode: string}> {
  if (options?.forceReload) {
    cachedTemplate = null;
    cachedVmCode = null;
  }
  if (cachedTemplate && cachedVmCode) {
    return {template: cachedTemplate, vmCode: cachedVmCode};
  }

  const [templateRes, vmRes] = await Promise.all([
    fetch('/assets/htmlifier/template.html', {signal: options?.signal}),
    fetch('/assets/htmlifier/vm.min.js', {signal: options?.signal})
  ]);

  if (!templateRes.ok) {
    throw new Error(`加载播放器模板失败: HTTP ${templateRes.status}`);
  }
  if (!vmRes.ok) {
    throw new Error(`加载 Scratch 虚拟机核心失败: HTTP ${vmRes.status}`);
  }

  const [template, vmCode] = await Promise.all([
    templateRes.text(),
    vmRes.text()
  ]);

  cachedTemplate = template;
  cachedVmCode = vmCode;
  return {template, vmCode};
}

export interface AssembleOptions {
  template: string;
  vmCode: string;
  projectDataUrl: string;
  title: string;
  description?: string;
  compatibleMode?: boolean;
  turboMode?: boolean;
  runDefault?: boolean;
  width?: number;
  height?: number;
}

/**
 * Assembles the standalone single-file HTML.
 * Uses function replacers to prevent $& / $1 / $' substitution pattern vulnerabilities.
 */
export function assembleSingleHtml(options: AssembleOptions): string {
  const {
    template: rawTemplate,
    vmCode,
    projectDataUrl,
    title,
    description = '',
    compatibleMode = true,
    turboMode = false,
    runDefault = false,
    width = 480,
    height = 360
  } = options;

  let template = rawTemplate;
  template = removePercentSection(template, 'no-vm');
  template = removePercentSection(template, 'vanilla');
  template = removePercentSection(template, 'modded');
  template = removePercentSection(template, 'default-ratio');
  template = removePercentSection(template, 'extension-url');
  template = removePercentSection(template, 'loading-image');
  template = removePercentSection(template, 'monitor-colour');
  template = removePercentSection(template, 'limits');
  template = removePercentSection(template, 'pointer-lock');
  template = removePercentSection(template, 'stretch');
  template = removePercentSection(template, 'no-cursor');
  template = removePercentSection(template, 'special-cloud');
  template = removePercentSection(template, 'cloud-ws');
  template = template.replace(/% \/?[a-z0-9-]+ %/g, '');

  const escapedVm = escapeScriptText(vmCode);
  const cleanDescription = String(description || '');

  const scripts = [
    `var TYPE = 'file',\nFILE = ${JSON.stringify(projectDataUrl)},\n`,
    `DESIRED_USERNAME = '',\n`,
    `COMPAT = ${Boolean(compatibleMode)},\nTURBO = ${Boolean(turboMode)},\n`,
    `PROJECT_ID = null,\nWIDTH = ${width},\nHEIGHT = ${height},\n`,
    `EXTENSION_URL = null,\nGENERATED = ${Date.now()};\n`,
    `DESCRIPTION = ${escapeScriptText(JSON.stringify(cleanDescription))};\n`,
    `OPEN_DESC_DEFAULT = ${Boolean(cleanDescription.trim())};\nRUN_DEFAULT = ${Boolean(runDefault)};\n`,
    escapedVm
  ].join('');

  // Use function replacers to avoid $&, $1, $', $` injection bugs in large strings
  let html = template
    .replace(/\{TITLE\}/g, () => escapeHtml(title))
    .replace(/\{HEIGHT\/WIDTH%\}/g, () => String(100 * (height + 24) / width))
    .replace(/\{WIDTH\/HEIGHT%\}/g, () => String(100 * width / (height + 24)))
    .replace(/\{PROJECT_RATIO\}/g, () => `${width}/${height}`)
    .replace(/\{MONITOR_TEXT\}/g, () => 'white')
    .replace(/\{DESCRIPTION\}/g, () => escapeHtml(cleanDescription))
    .replace(/\{LOADING_IMAGE\}/g, () => '')
    .replace(/\{SCRIPTS\}/g, () => scripts);

  if (html.includes('{SCRIPTS}') || /% \/?[a-z0-9-]+ %/.test(html)) {
    throw new Error('模板替换未能完全展开占位符');
  }

  // Prepend internal open-source license comments
  const licenseComment = buildEmbeddedLicenseComment();
  if (html.startsWith('<!DOCTYPE html>')) {
    html = html.replace('<!DOCTYPE html>', `<!DOCTYPE html>\n${licenseComment}`);
  } else {
    html = `${licenseComment}\n${html}`;
  }

  return html;
}

/**
 * Packages an SB3 binary payload into a downloadable HTML result.
 */
export async function packageSb3ToHtml(
  sb3Bytes: Uint8Array,
  options: HtmlExportOptions,
  signal?: AbortSignal
): Promise<HtmlExportResult> {
  if (sb3Bytes.byteLength > MAX_SB3_SIZE) {
    throw new Error(`作品大小（${(sb3Bytes.byteLength / 1024 / 1024).toFixed(1)} MB）超过 50MB 上限`);
  }

  const blob = new Blob([sb3Bytes], {type: 'application/x.scratch.sb3'});
  const projectDataUrl = await blobToDataUrl(blob, signal);

  if (signal?.aborted) {
    throw new DOMException('Operation aborted', 'AbortError');
  }

  const {template, vmCode} = await loadHtmlifierAssets({signal});

  if (signal?.aborted) {
    throw new DOMException('Operation aborted', 'AbortError');
  }

  const finalHtml = assembleSingleHtml({
    template,
    vmCode,
    projectDataUrl,
    title: options.title,
    description: options.description,
    compatibleMode: options.compatibleMode,
    turboMode: options.turboMode,
    runDefault: options.runDefault,
    width: options.width ?? 480,
    height: options.height ?? 360
  });

  const outputFilename = validateExportFilename(options.filename, options.title);
  const htmlBlob = new Blob([finalHtml], {type: 'text/html;charset=utf-8'});

  return {
    html: finalHtml,
    filename: outputFilename,
    size: htmlBlob.size,
    blob: htmlBlob
  };
}

/**
 * Triggers browser download for a blob or blobUrl and returns an auto/manual cleanup function.
 * If passed a Blob, creates a dedicated ObjectURL for this download action.
 */
export function triggerBrowserDownload(
  target: Blob | string,
  filename: string
): {revoke: () => void; url: string} {
  let blobUrl: string;
  let isCreatedHere = false;
  if (typeof target === 'string') {
    blobUrl = target;
  } else {
    blobUrl = URL.createObjectURL(target);
    isCreatedHere = true;
  }

  const link = document.createElement('a');
  link.href = blobUrl;
  link.download = filename;
  link.style.display = 'none';
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);

  let revoked = false;
  const revoke = () => {
    if (!revoked) {
      revoked = true;
      if (isCreatedHere) {
        URL.revokeObjectURL(blobUrl);
      }
    }
  };

  // Schedule auto revocation after 60 seconds
  if (isCreatedHere) {
    setTimeout(revoke, 60000);
  }

  return {revoke, url: blobUrl};
}
