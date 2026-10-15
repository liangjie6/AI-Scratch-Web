// @vitest-environment jsdom
import {beforeEach, describe, expect, it, vi} from 'vitest';
import {flushPromises, mount} from '@vue/test-utils';
import HtmlExportDialog from '../src/scratch-export/HtmlExportDialog.vue';
import type {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';
import * as packager from '../src/scratch-export/packager-core';

describe('HtmlExportDialog component unit tests', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders modal dialog when open is true with correct accessibility attributes', () => {
    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: null,
        scratchLocked: false,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });

    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog).toBeTruthy();
    expect(dialog?.getAttribute('aria-modal')).toBe('true');
    expect(dialog?.getAttribute('aria-labelledby')).toBe('html-export-title');
    expect(dialog?.textContent).toContain('导出单文件 HTML');
    expect(dialog?.textContent).not.toContain('普通作品可本地打开；在线扩展需要联网，异常时使用 HTTP/HTTPS 预览');

    wrapper.unmount();
  });

  it('emits close event when close button or cancel button is clicked', async () => {
    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: null,
        scratchLocked: false,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });

    const closeBtn = document.querySelector('button[aria-label="关闭导出对话框"]') as HTMLButtonElement;
    expect(closeBtn).toBeTruthy();
    closeBtn.click();
    await wrapper.vm.$nextTick();

    expect(wrapper.emitted('close')).toBeTruthy();
    wrapper.unmount();
  });

  it('cancel button is enabled and clickable to cancel during packaging', async () => {
    let resolveSave: ((data: Uint8Array) => void) | undefined;
    const fakeBridge = {
      saveProject: () => new Promise<Uint8Array>(resolve => {
        resolveSave = resolve;
      })
    } as unknown as ScratchGuiBridge;

    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: fakeBridge,
        scratchLocked: false,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });

    const exportBtn = document.querySelector('.html-export-footer .button--primary') as HTMLButtonElement;
    const cancelBtn = document.querySelector('.html-export-footer .button--quiet') as HTMLButtonElement;

    expect(cancelBtn.disabled).toBe(false);

    // Trigger export so status becomes packaging
    exportBtn.click();
    await wrapper.vm.$nextTick();

    // Verify cancel button remains enabled and clickable while packaging
    expect(cancelBtn.disabled).toBe(false);

    // Click cancel button
    cancelBtn.click();
    await wrapper.vm.$nextTick();

    // Verify close event was emitted
    expect(wrapper.emitted('close')).toBeTruthy();

    resolveSave?.(new Uint8Array(10));
    wrapper.unmount();
  });

  it('keeps focus inside the dialog when export disables the focused button so Escape still closes it', async () => {
    let resolveSave!: (bytes: Uint8Array) => void;
    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: {saveProject: () => new Promise<Uint8Array>(resolve => {resolveSave = resolve;})} as unknown as ScratchGuiBridge,
        scratchLocked: false,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });
    try {
      const exportBtn = document.querySelector('.html-export-footer .button--primary') as HTMLButtonElement;
      const closeBtn = document.querySelector('button[aria-label="关闭导出对话框"]') as HTMLButtonElement;
      exportBtn.focus();
      exportBtn.click();
      await wrapper.vm.$nextTick();
      expect(exportBtn.disabled).toBe(true);
      expect(document.activeElement).toBe(closeBtn);
      closeBtn.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
      expect(wrapper.emitted('close')).toHaveLength(1);
    } finally {
      wrapper.unmount();
      resolveSave(new Uint8Array([0x50, 0x4b, 0x03, 0x04]));
      await flushPromises();
    }
  });

  it('discards a pending project export when the dialog is unmounted', async () => {
    let resolveSave!: (bytes: Uint8Array) => void;
    const packaging = vi.spyOn(packager, 'packageSb3ToHtml').mockRejectedValue(new Error('Unexpected packaging after unmount'));
    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: {saveProject: () => new Promise<Uint8Array>(resolve => {resolveSave = resolve;})} as unknown as ScratchGuiBridge,
        scratchLocked: false,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });
    (document.querySelector('.html-export-footer .button--primary') as HTMLButtonElement).click();
    await wrapper.vm.$nextTick();
    wrapper.unmount();
    resolveSave(new Uint8Array([0x50, 0x4b, 0x03, 0x04]));
    await flushPromises();
    expect(packaging).not.toHaveBeenCalled();
  });

  it('pre-fills title with local file name when user has not edited title', async () => {
    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: null,
        scratchLocked: false,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });

    // Switch to file mode
    const radioFile = document.querySelector('input[value="file"]') as HTMLInputElement;
    radioFile.checked = true;
    radioFile.dispatchEvent(new Event('change'));
    await wrapper.vm.$nextTick();

    const titleInput = document.querySelector('#export-project-title') as HTMLInputElement;
    expect(titleInput.value).toBe('');

    // Simulate file input change
    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement;
    const testFile = new File(['mock'], '太空探险.sb3', {type: 'application/x.scratch.sb3'});
    Object.defineProperty(fileInput, 'files', {
      value: [testFile],
      configurable: true
    });
    fileInput.dispatchEvent(new Event('change'));
    await wrapper.vm.$nextTick();

    expect(titleInput.value).toBe('太空探险');

    wrapper.unmount();
  });

  it('preserves user explicitly edited title when selecting a file', async () => {
    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: null,
        scratchLocked: false,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });

    const titleInput = document.querySelector('#export-project-title') as HTMLInputElement;
    titleInput.value = '用户自定义标题';
    titleInput.dispatchEvent(new Event('input'));
    await wrapper.vm.$nextTick();

    // Switch to file mode
    const radioFile = document.querySelector('input[value="file"]') as HTMLInputElement;
    radioFile.checked = true;
    radioFile.dispatchEvent(new Event('change'));
    await wrapper.vm.$nextTick();

    // Title should still be preserved
    expect(titleInput.value).toBe('用户自定义标题');

    // Select file
    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement;
    const testFile = new File(['mock'], '太空探险.sb3', {type: 'application/x.scratch.sb3'});
    Object.defineProperty(fileInput, 'files', {
      value: [testFile],
      configurable: true
    });
    fileInput.dispatchEvent(new Event('change'));
    await wrapper.vm.$nextTick();

    // Must NOT be overwritten by the file name
    expect(titleInput.value).toBe('用户自定义标题');

    wrapper.unmount();
  });

  it('validates illegal filename and shows warning message', async () => {
    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: null,
        scratchLocked: false,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });

    const filenameInput = document.querySelector('#export-filename') as HTMLInputElement;
    const exportBtn = document.querySelector('.html-export-footer .button--primary') as HTMLButtonElement;

    filenameInput.value = 'invalid/path.html';
    filenameInput.dispatchEvent(new Event('input'));
    await wrapper.vm.$nextTick();

    exportBtn.click();
    await wrapper.vm.$nextTick();

    const alert = document.querySelector('[role="alert"]');
    expect(alert).toBeTruthy();
    expect(alert?.textContent).toContain('路径分隔符');

    wrapper.unmount();
  });

  it('validates empty filename with invalid title and prompts user for explicit filename', async () => {
    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: null,
        scratchLocked: false,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });

    const titleInput = document.querySelector('#export-project-title') as HTMLInputElement;
    const filenameInput = document.querySelector('#export-filename') as HTMLInputElement;
    const exportBtn = document.querySelector('.html-export-footer .button--primary') as HTMLButtonElement;

    titleInput.value = 'bad:name*<project>';
    titleInput.dispatchEvent(new Event('input'));
    filenameInput.value = '';
    filenameInput.dispatchEvent(new Event('input'));
    await wrapper.vm.$nextTick();

    exportBtn.click();
    await wrapper.vm.$nextTick();

    const alert = document.querySelector('[role="alert"]');
    expect(alert).toBeTruthy();
    expect(alert?.textContent).toContain('作品名包含非法字符，请显式填写合法导出文件名');

    wrapper.unmount();
  });

  it('disables export when editor is locked or ai blocks busy', () => {
    const wrapper = mount(HtmlExportDialog, {
      props: {
        open: true,
        bridge: null,
        scratchLocked: true,
        aiBlocksBusy: false
      },
      attachTo: document.body
    });

    const exportBtn = document.querySelector('.html-export-footer .button--primary') as HTMLButtonElement;
    expect(exportBtn.disabled).toBe(true);

    wrapper.unmount();
  });
});
