import {openDB} from 'idb';
import {sha256Hex} from '../shared/sha256';

const DATABASE_NAME = 'ai-scratch-run-checkpoints';
const DATABASE_VERSION = 1;
const CHECKPOINT_STORE = 'checkpoints';
const BLOB_STORE = 'blobs';
/** Storage key for the embedded editor's current local project slot. */
export const LOCAL_PROJECT_STORAGE_KEY = 'scratch-project';

export type CheckpointState =
  | 'READY_TO_COMMIT'
  | 'COMMITTING'
  | 'VERIFYING'
  | 'COMPLETED'
  | 'ROLLED_BACK'
  | 'FAILED_NEEDS_RECOVERY';

export interface LogicalTargetLocator {
  kind: 'stage' | 'sprite';
  ordinal?: number;
  name?: string;
  fingerprint?: string;
}

export interface SerializableApplyReceipt {
  sequence: number;
  kind: 'resource' | 'state' | 'program';
  logical_id: string;
  target_locator?: LogicalTargetLocator;
  after_fingerprint: string;
}

export interface RunCheckpoint {
  runId: string;
  state: CheckpointState;
  editorSessionId: string;
  projectEpoch: number;
  projectStorageKey: string;
  baseSnapshotHash: string;
  baseProjectFingerprint: string;
  lastAppliedProjectFingerprint?: string;
  candidateProjectFingerprint?: string;
  resolvedPlanHash: string;
  backupBlobKey?: string;
  candidateSb3BlobKey?: string;
  journal: SerializableApplyReceipt[];
  originalTargetLocator: LogicalTargetLocator;
  updatedAt: number;
}

export interface BeginCheckpointInput {
  runId: string;
  editorSessionId: string;
  projectEpoch: number;
  projectStorageKey: string;
  baseSnapshotHash: string;
  baseProjectFingerprint: string;
  resolvedPlanHash: string;
  originalTargetLocator: LogicalTargetLocator;
  backup: Uint8Array;
}

function cloneCheckpoint(value: RunCheckpoint): RunCheckpoint {
  return structuredClone(value);
}

function blobKey(runId: string, kind: 'backup' | 'candidate') {
  return `checkpoint:${runId}:${kind}`;
}

function isOpenableDatabase() {
  return typeof indexedDB !== 'undefined';
}

export class AiRunCheckpointStore {
  private databasePromise: ReturnType<typeof openDB> | null = null;

  private database() {
    if (!isOpenableDatabase()) throw new Error('当前浏览器不支持 AI 项目恢复存储');
    if (!this.databasePromise) {
      this.databasePromise = openDB(DATABASE_NAME, DATABASE_VERSION, {
        upgrade(database) {
          if (!database.objectStoreNames.contains(CHECKPOINT_STORE)) database.createObjectStore(CHECKPOINT_STORE, {keyPath: 'runId'});
          if (!database.objectStoreNames.contains(BLOB_STORE)) database.createObjectStore(BLOB_STORE);
        }
      });
    }
    return this.databasePromise;
  }

  async begin(input: BeginCheckpointInput): Promise<RunCheckpoint> {
    const backupBlobKey = blobKey(input.runId, 'backup');
    const checkpoint: RunCheckpoint = {
      runId: input.runId,
      state: 'READY_TO_COMMIT',
      editorSessionId: input.editorSessionId,
      projectEpoch: input.projectEpoch,
      projectStorageKey: input.projectStorageKey,
      baseSnapshotHash: input.baseSnapshotHash,
      baseProjectFingerprint: input.baseProjectFingerprint,
      resolvedPlanHash: input.resolvedPlanHash,
      backupBlobKey,
      journal: [],
      originalTargetLocator: input.originalTargetLocator,
      updatedAt: Date.now()
    };
    const database = await this.database();
    const transaction = database.transaction([CHECKPOINT_STORE, BLOB_STORE], 'readwrite');
    await transaction.objectStore(BLOB_STORE).put(new Blob([input.backup]), backupBlobKey);
    await transaction.objectStore(CHECKPOINT_STORE).put(checkpoint);
    await transaction.done;
    const reread = await database.get(BLOB_STORE, backupBlobKey);
    if (!(reread instanceof Blob) || await fingerprintBlob(reread) !== input.baseProjectFingerprint) {
      throw new Error('AI 项目恢复备份写入校验失败');
    }
    return this.setState(input.runId, 'COMMITTING');
  }

  async appendReceipt(runId: string, receipt: SerializableApplyReceipt): Promise<RunCheckpoint> {
    return this.update(runId, checkpoint => {
      if (checkpoint.state !== 'COMMITTING') throw new Error('AI 项目恢复记录不在提交状态');
      if (receipt.sequence !== checkpoint.journal.length + 1) throw new Error('AI 项目恢复日志序号不连续');
      checkpoint.journal.push({...receipt});
      checkpoint.lastAppliedProjectFingerprint = receipt.after_fingerprint;
      return checkpoint;
    });
  }

  async markVerifying(runId: string, candidate: Uint8Array, fingerprint: string): Promise<RunCheckpoint> {
    const candidateSb3BlobKey = blobKey(runId, 'candidate');
    const database = await this.database();
    const transaction = database.transaction([CHECKPOINT_STORE, BLOB_STORE], 'readwrite');
    const checkpoint = await transaction.objectStore(CHECKPOINT_STORE).get(runId) as RunCheckpoint | undefined;
    if (!checkpoint || checkpoint.state !== 'COMMITTING') throw new Error('AI 项目恢复记录不在提交状态');
    await transaction.objectStore(BLOB_STORE).put(new Blob([candidate]), candidateSb3BlobKey);
    checkpoint.state = 'VERIFYING';
    checkpoint.candidateSb3BlobKey = candidateSb3BlobKey;
    checkpoint.candidateProjectFingerprint = fingerprint;
    checkpoint.updatedAt = Date.now();
    await transaction.objectStore(CHECKPOINT_STORE).put(checkpoint);
    await transaction.done;
    const reread = await database.get(BLOB_STORE, candidateSb3BlobKey);
    if (!(reread instanceof Blob) || await fingerprintBlob(reread) !== fingerprint) throw new Error('AI 项目候选文件写入校验失败');
    return cloneCheckpoint(checkpoint);
  }

  async markCompleted(runId: string): Promise<RunCheckpoint> {
    return this.finalize(runId, 'COMPLETED');
  }

  async markRolledBack(runId: string): Promise<RunCheckpoint> {
    return this.finalize(runId, 'ROLLED_BACK');
  }

  async markFailedNeedsRecovery(runId: string): Promise<RunCheckpoint> {
    return this.setState(runId, 'FAILED_NEEDS_RECOVERY');
  }

  async get(runId: string): Promise<RunCheckpoint | undefined> {
    const value = await (await this.database()).get(CHECKPOINT_STORE, runId) as RunCheckpoint | undefined;
    return value ? cloneCheckpoint(value) : undefined;
  }

  async unfinished(projectStorageKey?: string): Promise<RunCheckpoint[]> {
    const values = await (await this.database()).getAll(CHECKPOINT_STORE) as RunCheckpoint[];
    return values.filter(checkpoint => (
      checkpoint.state !== 'COMPLETED'
      && checkpoint.state !== 'ROLLED_BACK'
      && (!projectStorageKey || checkpoint.projectStorageKey === projectStorageKey)
    )).map(cloneCheckpoint);
  }

  async backup(runId: string): Promise<Uint8Array | undefined> {
    const checkpoint = await this.get(runId);
    if (!checkpoint?.backupBlobKey) return undefined;
    const value = await (await this.database()).get(BLOB_STORE, checkpoint.backupBlobKey);
    return value instanceof Blob ? new Uint8Array(await value.arrayBuffer()) : undefined;
  }

  async candidate(runId: string): Promise<Uint8Array | undefined> {
    const checkpoint = await this.get(runId);
    if (!checkpoint?.candidateSb3BlobKey) return undefined;
    const value = await (await this.database()).get(BLOB_STORE, checkpoint.candidateSb3BlobKey);
    return value instanceof Blob ? new Uint8Array(await value.arrayBuffer()) : undefined;
  }

  private async setState(runId: string, state: CheckpointState): Promise<RunCheckpoint> {
    return this.update(runId, checkpoint => {
      checkpoint.state = state;
      return checkpoint;
    });
  }

  /**
   * Terminal checkpoints no longer need their potentially large SB3 blobs.
   * Delete both blobs in the same IndexedDB transaction as the state change so
   * a failed cleanup cannot leave a checkpoint claiming it is finished while
   * silently losing the recovery artifact.
   */
  private async finalize(runId: string, state: 'COMPLETED' | 'ROLLED_BACK'): Promise<RunCheckpoint> {
    const database = await this.database();
    const transaction = database.transaction([CHECKPOINT_STORE, BLOB_STORE], 'readwrite');
    const existing = await transaction.objectStore(CHECKPOINT_STORE).get(runId) as RunCheckpoint | undefined;
    if (!existing) throw new Error('找不到 AI 项目恢复记录');
    const next = cloneCheckpoint(existing);
    const blobStore = transaction.objectStore(BLOB_STORE);
    if (next.backupBlobKey) await blobStore.delete(next.backupBlobKey);
    if (next.candidateSb3BlobKey) await blobStore.delete(next.candidateSb3BlobKey);
    next.backupBlobKey = undefined;
    next.candidateSb3BlobKey = undefined;
    next.state = state;
    next.updatedAt = Date.now();
    await transaction.objectStore(CHECKPOINT_STORE).put(next);
    await transaction.done;
    return cloneCheckpoint(next);
  }

  private async update(runId: string, mutate: (checkpoint: RunCheckpoint) => RunCheckpoint): Promise<RunCheckpoint> {
    const database = await this.database();
    const transaction = database.transaction(CHECKPOINT_STORE, 'readwrite');
    const existing = await transaction.store.get(runId) as RunCheckpoint | undefined;
    if (!existing) throw new Error('找不到 AI 项目恢复记录');
    const next = mutate(cloneCheckpoint(existing));
    next.updatedAt = Date.now();
    await transaction.store.put(next);
    await transaction.done;
    return cloneCheckpoint(next);
  }
}

export async function fingerprintBytes(value: Uint8Array): Promise<string> {
  // The helper prefers WebCrypto and falls back to a portable SHA-256, so
  // checkpoints keep working when the deployment is opened over plain HTTP
  // from a non-localhost origin (no secure context, no crypto.subtle).
  return sha256Hex(value);
}

export async function fingerprintBlob(value: Blob): Promise<string> {
  return fingerprintBytes(new Uint8Array(await value.arrayBuffer()));
}

export async function hashCheckpointPlan(value: unknown): Promise<string> {
  return fingerprintBytes(new TextEncoder().encode(canonicalJson(value)));
}

export function canonicalJson(value: unknown): string {
  if (Array.isArray(value)) return '[' + value.map(canonicalJson).join(',') + ']';
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>;
    return '{' + Object.keys(record).sort().map(key => JSON.stringify(key) + ':' + canonicalJson(record[key])).join(',') + '}';
  }
  return JSON.stringify(value);
}

export function createCheckpointStore() {
  return new AiRunCheckpointStore();
}

export function createEditorSessionId(): string {
  const key = 'ai-scratch-editor-session-id';
  const existing = sessionStorage.getItem(key);
  if (existing) return existing;
  const value = globalThis.crypto?.randomUUID?.() ?? `session-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  sessionStorage.setItem(key, value);
  return value;
}
