import {strFromU8, unzipSync} from 'fflate';

interface Sb3Asset {
  assetId?: unknown;
  dataFormat?: unknown;
  md5ext?: unknown;
  name?: unknown;
}

interface Sb3Target {
  isStage?: unknown;
  name?: unknown;
  blocks?: unknown;
  variables?: unknown;
  lists?: unknown;
  costumes?: unknown;
  sounds?: unknown;
}

interface Sb3Project {
  targets?: unknown;
}

type BuiltinExtensionId = 'pen' | 'music' | 'text2speech';

interface Sb3Inspection {
  checks: string[];
  builtinExtensions: BuiltinExtensionId[];
}

export interface CandidateVerification {
  passed: boolean;
  checks: string[];
  error?: string;
  errorCode?: CandidateVerificationFailureCode;
  behavior?: CandidateBehaviorSummary;
}

export interface CandidateBehaviorSummary {
  totalCount: number;
  passedCount: number;
  mustTotal: number;
  mustPassed: number;
}

export type CandidateVerificationFailureCode =
  | 'SB3_ARCHIVE_INVALID'
  | 'SB3_RESOURCE_MISSING'
  | 'SB3_VM_ROUND_TRIP_FAILED';

export interface CandidateVerificationOptions {
  // 保持配置项兼容，但已移除行为测试
}

const MAX_CANDIDATE_BYTES = 50 * 1024 * 1024;
const MAX_UNCOMPRESSED_BYTES = 100 * 1024 * 1024;
const MAX_ARCHIVE_ENTRIES = 2_000;
const MAX_PROJECT_JSON_BYTES = 5 * 1024 * 1024;

function verificationFailureCode(message: string): CandidateVerificationFailureCode {
  if (message.includes('缺少资源文件')) return 'SB3_RESOURCE_MISSING';
  if (message.includes('首次加载') || message.includes('首次导出') || message.includes('二次新 VM') || message.includes('隔离 Scratch VM') || message.includes('验证运行时') || message.includes('Scratch VM 包') || message.includes('内建扩展')) {
    return 'SB3_VM_ROUND_TRIP_FAILED';
  }
  return 'SB3_ARCHIVE_INVALID';
}

function fail(checks: string[], message: string): CandidateVerification {
  return {passed: false, checks, error: message, errorCode: verificationFailureCode(message)};
}

function asRecord(value: unknown, label: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(label + ' 无效');
  return value as Record<string, unknown>;
}

function assetFileName(asset: Sb3Asset, label: string): string {
  if (typeof asset.md5ext === 'string' && /^[a-f0-9]{32}\.(?:svg|png|jpg|jpeg|wav|mp3)$/i.test(asset.md5ext)) return asset.md5ext;
  if (typeof asset.assetId !== 'string' || !/^[a-f0-9]{32}$/i.test(asset.assetId) || typeof asset.dataFormat !== 'string' || !/^(?:svg|png|jpg|jpeg|wav|mp3)$/i.test(asset.dataFormat)) {
    throw new Error(label + ' 缺少有效 assetId/dataFormat');
  }
  return asset.assetId + '.' + asset.dataFormat;
}

function validateAssetList(files: Record<string, Uint8Array>, value: unknown, label: string) {
  if (!Array.isArray(value)) throw new Error(label + ' 必须是数组');
  const names = new Set<string>();
  for (const raw of value) {
    const asset = asRecord(raw, label + ' 条目') as Sb3Asset;
    const fileName = assetFileName(asset, label);
    if (!files[fileName]?.byteLength) throw new Error(label + ' 缺少资源文件：' + fileName);
    if (typeof asset.name !== 'string' || !asset.name.trim()) throw new Error(label + ' 缺少名称');
    if (names.has(asset.name)) throw new Error(label + ' 存在重复名称：' + asset.name);
    names.add(asset.name);
  }
}

function archivePathIsSafe(path: string): boolean {
  return Boolean(path) && !path.endsWith('/') && !path.includes('..') && !path.startsWith('/') && !path.includes('\\') && !path.includes('\0');
}

function builtinExtensionForOpcode(opcode: unknown): BuiltinExtensionId | undefined {
  if (typeof opcode !== 'string') return undefined;
  if (opcode.startsWith('pen_')) return 'pen';
  if (opcode.startsWith('music_')) return 'music';
  if (opcode.startsWith('text2speech_')) return 'text2speech';
  return undefined;
}

/**
 * Verify ZIP central-directory limits before fflate inflates any entry. The
 * standard SB3 writer uses a single, non-ZIP64 archive, which makes this a
 * deterministic preflight rather than an after-the-fact decompression check.
 */
function assertArchiveEnvelope(candidate: Uint8Array) {
  if (candidate.byteLength < 22) throw new Error('候选 SB3 ZIP 头无效');
  const view = new DataView(candidate.buffer, candidate.byteOffset, candidate.byteLength);
  const earliestEocd = Math.max(0, candidate.byteLength - 65_557);
  let eocdOffset = -1;
  for (let offset = candidate.byteLength - 22; offset >= earliestEocd; offset -= 1) {
    if (view.getUint32(offset, true) === 0x06054b50) {
      eocdOffset = offset;
      break;
    }
  }
  if (eocdOffset < 0) throw new Error('候选 SB3 缺少 ZIP 中央目录');
  const diskNumber = view.getUint16(eocdOffset + 4, true);
  const directoryDisk = view.getUint16(eocdOffset + 6, true);
  const entriesOnDisk = view.getUint16(eocdOffset + 8, true);
  const entryCount = view.getUint16(eocdOffset + 10, true);
  const directorySize = view.getUint32(eocdOffset + 12, true);
  const directoryOffset = view.getUint32(eocdOffset + 16, true);
  if (diskNumber !== 0 || directoryDisk !== 0 || entriesOnDisk !== entryCount || entryCount === 0 || entryCount === 0xffff || directorySize === 0xffffffff || directoryOffset === 0xffffffff) {
    throw new Error('候选 SB3 使用不受支持的 ZIP 目录格式');
  }
  if (entryCount > MAX_ARCHIVE_ENTRIES || directoryOffset + directorySize > eocdOffset) throw new Error('候选 SB3 的 ZIP 目录超限或损坏');
  const decoder = new TextDecoder('utf-8', {fatal: true});
  const paths = new Set<string>();
  let offset = directoryOffset;
  let uncompressedBytes = 0;
  for (let index = 0; index < entryCount; index += 1) {
    if (offset + 46 > directoryOffset + directorySize || view.getUint32(offset, true) !== 0x02014b50) throw new Error('候选 SB3 的 ZIP 条目无效');
    const compressedBytes = view.getUint32(offset + 20, true);
    const entryUncompressedBytes = view.getUint32(offset + 24, true);
    const fileNameLength = view.getUint16(offset + 28, true);
    const extraLength = view.getUint16(offset + 30, true);
    const commentLength = view.getUint16(offset + 32, true);
    const entryLength = 46 + fileNameLength + extraLength + commentLength;
    if (offset + entryLength > directoryOffset + directorySize || compressedBytes > MAX_CANDIDATE_BYTES || entryUncompressedBytes > MAX_UNCOMPRESSED_BYTES) throw new Error('候选 SB3 的 ZIP 条目超限');
    let path: string;
    try {
      path = decoder.decode(candidate.subarray(offset + 46, offset + 46 + fileNameLength));
    } catch {
      throw new Error('候选 SB3 包含无效的 ZIP 文件名');
    }
    if (!archivePathIsSafe(path) || paths.has(path)) throw new Error('候选 SB3 含有非法或重复资源路径');
    paths.add(path);
    uncompressedBytes += entryUncompressedBytes;
    if (uncompressedBytes > MAX_UNCOMPRESSED_BYTES) throw new Error('候选 SB3 解压后大小超限');
    offset += entryLength;
  }
  if (offset !== directoryOffset + directorySize || !paths.has('project.json')) throw new Error('候选 SB3 的 ZIP 目录不完整');
}

function inspectSb3(candidate: Uint8Array): Sb3Inspection {
  if (!candidate.byteLength || candidate.byteLength > MAX_CANDIDATE_BYTES) throw new Error('候选 SB3 文件大小无效');
  assertArchiveEnvelope(candidate);
  const files = unzipSync(candidate);
  const entries = Object.entries(files);
  if (entries.length > MAX_ARCHIVE_ENTRIES) throw new Error('候选 SB3 的资源文件数量超限');
  let uncompressedBytes = 0;
  for (const [path, bytes] of entries) {
    if (!archivePathIsSafe(path)) throw new Error('候选 SB3 含有非法资源路径');
    uncompressedBytes += bytes.byteLength;
    if (uncompressedBytes > MAX_UNCOMPRESSED_BYTES) throw new Error('候选 SB3 解压后大小超限');
  }
  const projectBytes = files['project.json'];
  if (!projectBytes?.byteLength) throw new Error('候选 SB3 缺少 project.json');
  if (projectBytes.byteLength > MAX_PROJECT_JSON_BYTES) throw new Error('候选 SB3 的 project.json 过大');
  let project: Sb3Project;
  try {
    project = JSON.parse(strFromU8(projectBytes)) as Sb3Project;
  } catch {
    throw new Error('候选 SB3 的 project.json 无法解析');
  }
  if (!Array.isArray(project.targets) || !project.targets.length) throw new Error('候选 SB3 没有项目目标');
  let stageCount = 0;
  const targetNames = new Set<string>();
  const builtinExtensions = new Set<BuiltinExtensionId>();
  for (const raw of project.targets) {
    const target = asRecord(raw, '项目目标') as Sb3Target;
    if (target.isStage === true) stageCount += 1;
    if (typeof target.name !== 'string' || !target.name.trim()) throw new Error('项目目标缺少名称');
    if (targetNames.has(target.name)) throw new Error('候选 SB3 存在重复目标名称：' + target.name);
    targetNames.add(target.name);
    const blocks = asRecord(target.blocks, '项目目标积木');
    for (const block of Object.values(blocks)) {
      if (!block || typeof block !== 'object' || Array.isArray(block)) continue;
      const extension = builtinExtensionForOpcode((block as Record<string, unknown>).opcode);
      if (extension) builtinExtensions.add(extension);
    }
    asRecord(target.variables, '项目目标变量');
    if (target.lists !== undefined) asRecord(target.lists, '项目目标列表');
    validateAssetList(files, target.costumes, target.name + ' 的造型');
    validateAssetList(files, target.sounds, target.name + ' 的声音');
  }
  if (stageCount !== 1) throw new Error('候选 SB3 必须且只能包含一个舞台');
  return {
    checks: ['sb3_archive', 'zip_envelope', 'resource_files', 'project_structure'],
    builtinExtensions: [...builtinExtensions].sort(),
  };
}


/** 兼容旧接口：格式校验无需预热隔离 Scratch VM */
export async function prewarmCandidateVerificationRuntime(): Promise<void> {
  // no-op: 仅格式校验无需预热隔离运行时
}

/** 仅校验候选 SB3 的文件大小、ZIP 目录封装、project.json 语法与资源完整性，不执行行为测试。 */
export async function verifyCandidateSb3(candidate: Uint8Array, _options: CandidateVerificationOptions = {}): Promise<CandidateVerification> {
  const checks: string[] = [];
  try {
    const inspection = inspectSb3(candidate);
    checks.push(...inspection.checks);
    return {passed: true, checks};
  } catch (error) {
    return fail(checks, error instanceof Error ? error.message : '候选 SB3 验证失败');
  }
}

