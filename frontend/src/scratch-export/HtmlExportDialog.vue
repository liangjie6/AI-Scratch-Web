<script setup lang="ts">
import {nextTick, onBeforeUnmount, ref, watch} from 'vue';
import {CheckCircle2, Download, FileCode, FileDown, FolderArchive, LoaderCircle, TriangleAlert, X} from 'lucide-vue-next';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import type {HtmlExportSourceMode, HtmlExportStatus} from './html-export-types';
import {
  packageSb3ToHtml,
  triggerBrowserDownload,
  validateExportFilename,
  validateSb3File
} from './packager-core';

const props = defineProps<{
  open: boolean;
  bridge: ScratchGuiBridge | null;
  scratchLocked: boolean;
  aiBlocksBusy: boolean;
}>();

const emit = defineEmits<{
  close: [];
}>();

const dialogRef = ref<HTMLElement | null>(null);
const titleInputRef = ref<HTMLInputElement | null>(null);
const closeButtonRef = ref<HTMLButtonElement | null>(null);
const fileInputRef = ref<HTMLInputElement | null>(null);

const sourceMode = ref<HtmlExportSourceMode>('current');
const projectTitle = ref('');
const isTitleUserModified = ref(false);
const filename = ref('');
const description = ref('');
const compatibleMode = ref(true);
const turboMode = ref(false);
const runDefault = ref(false);
const selectedFile = ref<File | null>(null);

const status = ref<HtmlExportStatus>('idle');
const statusMessage = ref('');
const errorMessage = ref('');
const validationError = ref('');
const lastDownloadResult = ref<{filename: string; size: number; blob: Blob} | null>(null);

let activeTaskToken = 0;
let isPackaging = false;
let currentAbortController: AbortController | null = null;

function queryDefaultTitle(): string {
  const input = document.querySelector<HTMLInputElement>(
    'input[placeholder="在此填写作品标题"], input[class*="project-title-input"]'
  );
  return input?.value?.trim() || 'Scratch作品';
}

function initDialog() {
  activeTaskToken += 1;
  isPackaging = false;
  sourceMode.value = 'current';
  isTitleUserModified.value = false;
  projectTitle.value = queryDefaultTitle();
  filename.value = '';
  description.value = '';
  compatibleMode.value = true;
  turboMode.value = false;
  runDefault.value = false;
  selectedFile.value = null;

  status.value = 'idle';
  statusMessage.value = '';
  errorMessage.value = '';
  validationError.value = '';
}

function cleanup() {
  activeTaskToken += 1;
  isPackaging = false;
  if (currentAbortController) {
    currentAbortController.abort();
    currentAbortController = null;
  }
  lastDownloadResult.value = null;
  status.value = 'idle';
  statusMessage.value = '';
}

onBeforeUnmount(cleanup);

watch(
  () => props.open,
  async isOpen => {
    if (isOpen) {
      initDialog();
      await nextTick();
      (titleInputRef.value || closeButtonRef.value)?.focus();
    } else {
      cleanup();
    }
  }
);

watch(
  status,
  newStatus => {
    // Packaging disables the form controls; keep keyboard focus on an enabled control.
    if (newStatus === 'packaging' && props.open) closeButtonRef.value?.focus();
  },
  {flush: 'post'}
);

watch(
  () => [props.scratchLocked, props.aiBlocksBusy],
  ([locked, busy]) => {
    if ((locked || busy) && status.value === 'packaging') {
      errorMessage.value = '编辑器状态已改变（处于锁定或任务运行中），导出已中断';
      status.value = 'error';
      cleanup();
    }
  }
);

watch(sourceMode, newMode => {
  if (!isTitleUserModified.value) {
    if (newMode === 'current') {
      projectTitle.value = queryDefaultTitle();
    } else if (newMode === 'file') {
      projectTitle.value = selectedFile.value
        ? selectedFile.value.name.replace(/\.sb3$/i, '')
        : '';
    }
  }
});

function handleClose() {
  cleanup();
  emit('close');
}

function onTitleInput() {
  isTitleUserModified.value = true;
}

function onFileChange(event: Event) {
  const target = event.target as HTMLInputElement;
  const file = target.files?.[0] || null;
  selectedFile.value = file;
  validationError.value = '';
  errorMessage.value = '';

  if (file && !isTitleUserModified.value) {
    projectTitle.value = file.name.replace(/\.sb3$/i, '');
  }
}

function triggerFilePick() {
  fileInputRef.value?.click();
}

async function handleExport() {
  if (status.value === 'packaging' || isPackaging) return;
  isPackaging = true;
  validationError.value = '';
  errorMessage.value = '';

  if (props.scratchLocked || props.aiBlocksBusy) {
    validationError.value = 'Scratch 编辑器处于锁定或事务处理中，请稍后再试';
    isPackaging = false;
    return;
  }

  const fallback = projectTitle.value.trim()
    || (sourceMode.value === 'file' && selectedFile.value
      ? selectedFile.value.name.replace(/\.sb3$/i, '')
      : 'Scratch作品');

  let validFilename = '';
  try {
    validFilename = validateExportFilename(filename.value, fallback);
  } catch (err: unknown) {
    validationError.value = err instanceof Error ? err.message : '文件名不合法';
    isPackaging = false;
    return;
  }

  if (sourceMode.value === 'file') {
    if (!selectedFile.value) {
      validationError.value = '请选择本地 .sb3 文件';
      isPackaging = false;
      return;
    }
  }

  const thisToken = ++activeTaskToken;
  currentAbortController?.abort();
  const controller = new AbortController();
  currentAbortController = controller;

  status.value = 'packaging';
  statusMessage.value = '正在准备项目资源...';

  try {
    let sb3Bytes: Uint8Array;

    if (sourceMode.value === 'current') {
      if (!props.bridge) {
        throw new Error('Scratch 编辑器未就绪');
      }
      statusMessage.value = '正在从当前编辑器导出作品...';
      if (props.scratchLocked || props.aiBlocksBusy) {
        throw new Error('Scratch 编辑器处于锁定或事务忙状态');
      }
      sb3Bytes = await props.bridge.saveProject();
    } else {
      statusMessage.value = '正在校验并读取本地 .sb3 文件...';
      sb3Bytes = await validateSb3File(selectedFile.value!);
    }

    if (thisToken !== activeTaskToken || controller.signal.aborted) return;

    statusMessage.value = '正在获取运行时与装配单文件 HTML...';
    const result = await packageSb3ToHtml(
      sb3Bytes,
      {
        sourceMode: sourceMode.value,
        title: projectTitle.value.trim() || fallback,
        filename: validFilename,
        description: description.value.trim(),
        compatibleMode: compatibleMode.value,
        turboMode: turboMode.value,
        runDefault: runDefault.value
      },
      controller.signal
    );

    if (thisToken !== activeTaskToken || controller.signal.aborted) {
      return;
    }

    // Trigger download using the Blob directly so a fresh URL is created per download
    triggerBrowserDownload(result.blob, result.filename);

    lastDownloadResult.value = {
      filename: result.filename,
      size: result.size,
      blob: result.blob
    };

    status.value = 'success';
    statusMessage.value = `导出成功！已自动下载：${result.filename} (${(result.size / 1024 / 1024).toFixed(2)} MB)`;
  } catch (err: unknown) {
    if (thisToken !== activeTaskToken || controller.signal.aborted) return;
    status.value = 'error';
    errorMessage.value = err instanceof Error ? err.message : '导出失败，请重试';
    statusMessage.value = '';
  } finally {
    if (thisToken === activeTaskToken) {
      isPackaging = false;
    }
  }
}

function handleReDownload() {
  if (lastDownloadResult.value) {
    triggerBrowserDownload(lastDownloadResult.value.blob, lastDownloadResult.value.filename);
  }
}

function onKeydown(event: KeyboardEvent) {
  event.stopPropagation();

  if (event.key === 'Escape') {
    handleClose();
    return;
  }

  if (event.key === 'Tab') {
    const dialog = dialogRef.value;
    if (!dialog) return;
    const focusable = dialog.querySelectorAll<HTMLElement>(
      'button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])'
    );
    if (!focusable.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];

    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
}

function onKeyup(event: KeyboardEvent) {
  event.stopPropagation();
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="html-export-overlay"
      @click.self="handleClose"
      @keydown="onKeydown"
      @keyup="onKeyup"
    >
      <section
        ref="dialogRef"
        class="html-export-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="html-export-title"
        @keydown="onKeydown"
        @keyup="onKeyup"
      >
        <header class="html-export-header">
          <div>
            <h2 id="html-export-title">
              <FileDown :size="19" aria-hidden="true" />
              <span>导出单文件 HTML</span>
            </h2>
          </div>
          <button
            ref="closeButtonRef"
            class="icon-button"
            type="button"
            title="关闭"
            aria-label="关闭导出对话框"
            @click="handleClose"
          >
            <X :size="18" aria-hidden="true" />
          </button>
        </header>

        <form class="html-export-body" @submit.prevent="handleExport">
          <!-- 来源选择 -->
          <div class="form-group">
            <span class="field-label">项目来源</span>
            <div class="target-toggle" role="radiogroup" aria-label="项目来源">
              <label :class="{'is-active': sourceMode === 'current'}">
                <input
                  v-model="sourceMode"
                  type="radio"
                  name="sourceMode"
                  value="current"
                  :disabled="status === 'packaging'"
                />
                <FileCode :size="15" aria-hidden="true" />
                当前编辑器作品
              </label>
              <label :class="{'is-active': sourceMode === 'file'}">
                <input
                  v-model="sourceMode"
                  type="radio"
                  name="sourceMode"
                  value="file"
                  :disabled="status === 'packaging'"
                />
                <FolderArchive :size="15" aria-hidden="true" />
                本地 .sb3 文件
              </label>
            </div>
          </div>

          <!-- 本地文件选择区 -->
          <div v-if="sourceMode === 'file'" class="file-picker-box">
            <input
              ref="fileInputRef"
              type="file"
              accept=".sb3"
              style="display: none"
              @change="onFileChange"
            />
            <div class="file-picker-content">
              <div class="file-picker-info">
                <span v-if="selectedFile" class="file-name" :title="selectedFile.name">
                  已选择: {{ selectedFile.name }} ({{ (selectedFile.size / 1024 / 1024).toFixed(2) }} MB)
                </span>
                <span v-else class="file-placeholder">
                  选择本地 .sb3 格式作品文件（最大 50MB）
                </span>
              </div>
              <button
                class="button button--quiet"
                type="button"
                :disabled="status === 'packaging'"
                @click="triggerFilePick"
              >
                {{ selectedFile ? '更换文件' : '浏览文件' }}
              </button>
            </div>
          </div>

          <!-- 作品名称 -->
          <div class="form-group">
            <label class="field-label" for="export-project-title">作品名称</label>
            <input
              id="export-project-title"
              ref="titleInputRef"
              v-model="projectTitle"
              class="field-control"
              type="text"
              maxlength="80"
              placeholder="例如：太空漫游"
              :disabled="status === 'packaging'"
              @input="onTitleInput"
            />
          </div>

          <!-- 导出文件名 -->
          <div class="form-group">
            <label class="field-label" for="export-filename">
              导出文件名
            </label>
            <input
              id="export-filename"
              v-model="filename"
              class="field-control"
              type="text"
              maxlength="80"
              :placeholder="(projectTitle.trim() || 'Scratch作品') + '.html'"
              :disabled="status === 'packaging'"
            />
          </div>

          <!-- 作品简介 -->
          <div class="form-group">
            <label class="field-label" for="export-description">
              作品说明
            </label>
            <textarea
              id="export-description"
              v-model="description"
              class="field-control field-textarea"
              rows="2"
              maxlength="500"
              placeholder="作品玩法或按键操作介绍..."
              :disabled="status === 'packaging'"
            ></textarea>
          </div>

          <!-- 高级运行选项 -->
          <div class="form-group">
            <span class="field-label">运行选项</span>
            <div class="export-options">
              <label class="checkbox-label">
                <input
                  v-model="compatibleMode"
                  type="checkbox"
                  :disabled="status === 'packaging'"
                />
                <span>兼容模式 (30 FPS 限制)</span>
              </label>
              <label class="checkbox-label">
                <input
                  v-model="turboMode"
                  type="checkbox"
                  :disabled="status === 'packaging'"
                />
                <span>加速模式 (Turbo Mode)</span>
              </label>
              <label class="checkbox-label">
                <input
                  v-model="runDefault"
                  type="checkbox"
                  :disabled="status === 'packaging'"
                />
                <span>打开网页时自动运行</span>
              </label>
            </div>
          </div>

          <!-- 状态与反馈 -->
          <div v-if="validationError" class="feedback-msg feedback-msg--warning" role="alert">
            <TriangleAlert :size="15" aria-hidden="true" />
            <span>{{ validationError }}</span>
          </div>
          <div v-if="errorMessage" class="feedback-msg feedback-msg--error" role="alert">
            <TriangleAlert :size="15" aria-hidden="true" />
            <span>{{ errorMessage }}</span>
          </div>
          <div v-if="status === 'packaging'" class="feedback-msg feedback-msg--info" role="status">
            <LoaderCircle class="spin" :size="15" aria-hidden="true" />
            <span>{{ statusMessage }}</span>
          </div>
          <div v-if="status === 'success'" class="feedback-msg feedback-msg--success" role="status">
            <CheckCircle2 :size="15" aria-hidden="true" />
            <div class="success-content">
              <span>{{ statusMessage }}</span>
              <button
                class="button button--quiet text-button"
                type="button"
                @click="handleReDownload"
              >
                再次下载
              </button>
            </div>
          </div>
        </form>

        <footer class="html-export-footer">
          <button
            class="button button--quiet"
            type="button"
            @click="handleClose"
          >
            取消
          </button>
          <button
            class="button button--primary"
            type="button"
            :disabled="status === 'packaging' || scratchLocked || aiBlocksBusy"
            @click="handleExport"
          >
            <LoaderCircle v-if="status === 'packaging'" class="spin" :size="15" aria-hidden="true" />
            <Download v-else :size="15" aria-hidden="true" />
            <span>{{ status === 'packaging' ? '正在导出...' : '开始导出并下载' }}</span>
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>
