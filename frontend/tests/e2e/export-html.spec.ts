import http from 'node:http';
import type {AddressInfo} from 'node:net';
import {readFile} from 'node:fs/promises';
import path from 'node:path';
import {expect, test, type Page} from '@playwright/test';

const EVIDENCE_DIR = path.resolve(process.cwd(), '../docs/evidence/html-export');

async function waitForEditor(page: Page) {
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 30_000});
  await expect(page.getByRole('button', {name: '角色1'})).toBeVisible({timeout: 30_000});
}

test.describe('Scratch 单文件 HTML 导出端到端测试', () => {
  test('当前编辑器作品导出、下载并可在 HTTP 服务中启动运行，验证绿旗与交互', async ({page, context}) => {
    await waitForEditor(page);

    // 1. 点击右上角“导出 HTML”入口
    const exportBtn = page.getByRole('button', {name: '导出单文件 HTML 播放器'});
    await expect(exportBtn).toBeVisible();
    await exportBtn.click();

    // 2. 验证弹窗呈现并捕获桌面端截图
    const dialog = page.getByRole('dialog', {name: '导出单文件 HTML'});
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('普通作品可本地打开；在线扩展需要联网，异常时使用 HTTP/HTTPS 预览')).toHaveCount(0);

    await page.screenshot({
      path: path.join(EVIDENCE_DIR, 'export-dialog-desktop.png')
    });

    // 3. 填写作品名称与说明
    const titleInput = dialog.getByLabel('作品名称');
    await titleInput.fill('默认猫猫漫步作品');

    const descInput = dialog.getByLabel(/作品说明/);
    await descInput.fill('这是自动化 E2E 测试生成的作品说明：点击绿旗猫咪交互');

    // 4. 触发导出并监听下载
    const downloadPromise = page.waitForEvent('download', {timeout: 60_000});
    await dialog.getByRole('button', {name: '开始导出并下载'}).click();
    const download = await downloadPromise;

    expect(download.suggestedFilename()).toBe('默认猫猫漫步作品.html');
    const downloadPath = await download.path();
    expect(downloadPath).toBeTruthy();

    const fileContent = await readFile(downloadPath!, 'utf-8');
    expect(fileContent.length).toBeGreaterThan(5_000_000); // 内嵌 5.1MB VM
    expect(fileContent).toContain('<!DOCTYPE html>');
    expect(fileContent).toContain('默认猫猫漫步作品');
    expect(fileContent).toContain('这是自动化 E2E 测试生成的作品说明');
    expect(fileContent).toContain('Copyright (c) 2026 凉介');
    expect(fileContent).toContain('Copyright (c) 2020 Sean');
    expect(fileContent).toContain('Third-Party Notices (THIRD_PARTY_NOTICES.md)');
    expect(fileContent).not.toContain('cocoet.cn');

    // 5. 启动本地 HTTP 服务（随机可用端口 listen(0)）承载导出的 HTML 文件进行真实运行测试
    let currentHtml = fileContent;
    const server = http.createServer((_req, res) => {
      res.writeHead(200, {'Content-Type': 'text/html; charset=utf-8'});
      res.end(currentHtml);
    });
    await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
    const address = server.address() as AddressInfo;
    const testPort = address.port;

    try {
      const playerPage = await context.newPage();
      const consoleErrors: string[] = [];
      const pageErrors: Error[] = [];
      const externalRequests: string[] = [];

      playerPage.on('console', msg => {
        if (msg.type() === 'error') {
          const text = msg.text();
          // 忽略无物理声卡沙箱环境下的 WebAudio/AudioContext 硬件告警
          if (!text.includes('AudioContext encountered an error from the audio device')) {
            consoleErrors.push(text);
          }
        }
      });
      playerPage.on('pageerror', err => {
        pageErrors.push(err);
      });
      playerPage.on('request', req => {
        if (!req.url().startsWith('http://127.0.0.1') && !req.url().startsWith('data:') && !req.url().startsWith('blob:')) {
          externalRequests.push(req.url());
        }
      });

      await playerPage.goto(`http://127.0.0.1:${testPort}`, {waitUntil: 'load', timeout: 30_000});
      await playerPage.waitForTimeout(3000);

      // 验证说明浮层显示
      const descModal = playerPage.locator('.desc-content, #description');
      await expect(descModal).toBeVisible();
      await expect(descModal).toContainText('这是自动化 E2E 测试生成的作品说明');

      // 关闭作品说明浮层
      const closeDescBtn = playerPage.locator('#close-desc, .close-btn');
      if (await closeDescBtn.count()) {
        await closeDescBtn.click();
      }

      // 验证舞台与绿旗
      const greenFlag = playerPage.locator('#green-flag');
      await expect(greenFlag).toBeVisible();
      const stage = playerPage.locator('#stage, canvas');
      await expect(stage.first()).toBeVisible();

      // 点击绿旗触发运行
      await greenFlag.click();
      await playerPage.waitForTimeout(1000);

      // 截取播放器运行证据图
      await playerPage.screenshot({
        path: path.join(EVIDENCE_DIR, 'exported-player-running.png')
      });

      expect(externalRequests).toEqual([]);
      expect(consoleErrors).toEqual([]);
      expect(pageErrors).toEqual([]);
      await playerPage.close();
    } finally {
      await new Promise<void>(resolve => server.close(() => resolve()));
    }
  });

  test('本地 .sb3 文件导入、校验与导出', async ({page}, testInfo) => {
    await waitForEditor(page);

    // 1. 通过 GUI “文件” -> “保存到电脑” 导出真实的当前作品 SB3 作为测试 fixture
    await page.getByText('文件', {exact: true}).click();
    const downloadFixturePromise = page.waitForEvent('download');
    await page.getByText('保存到电脑', {exact: true}).click();
    const fixtureDownload = await downloadFixturePromise;
    const fixturePath = testInfo.outputPath('saved-editor-fixture.sb3');
    await fixtureDownload.saveAs(fixturePath);

    // 2. 打开单文件 HTML 导出弹窗
    const exportBtn = page.getByRole('button', {name: '导出单文件 HTML 播放器'});
    await exportBtn.click();

    const dialog = page.getByRole('dialog', {name: '导出单文件 HTML'});
    await expect(dialog).toBeVisible();

    // 3. 切换至本地 .sb3 文件
    await dialog.getByLabel('本地 .sb3 文件').check();

    // 未选文件时点击导出应触发拦截
    await dialog.getByRole('button', {name: '开始导出并下载'}).click();
    await expect(dialog.getByRole('alert')).toContainText('请选择本地 .sb3 文件');

    // 4. 上传刚才真实生成的 SB3 fixture
    const fileChooserPromise = page.waitForEvent('filechooser');
    await dialog.getByRole('button', {name: '浏览文件'}).click();
    const fileChooser = await fileChooserPromise;
    await fileChooser.setFiles(fixturePath);

    // 验证文件已被选取展示
    await expect(dialog.locator('.file-name')).toContainText('saved-editor-fixture.sb3');

    // 5. 修改标题
    await dialog.getByLabel('作品名称').fill('本地导入猫咪项目');

    // 6. 触发导出
    const downloadPromise = page.waitForEvent('download', {timeout: 60_000});
    await dialog.getByRole('button', {name: '开始导出并下载'}).click();
    const download = await downloadPromise;

    expect(download.suggestedFilename()).toBe('本地导入猫咪项目.html');
    const downloadPath = await download.path();
    const fileContent = await readFile(downloadPath!, 'utf-8');
    expect(fileContent.length).toBeGreaterThan(5_000_000);
    expect(fileContent).toContain('本地导入猫咪项目');
  });

  test('特殊文字（XSS防范）与非法文件名拦截验证', async ({page}) => {
    await waitForEditor(page);

    const exportBtn = page.getByRole('button', {name: '导出单文件 HTML 播放器'});
    await exportBtn.click();

    const dialog = page.getByRole('dialog', {name: '导出单文件 HTML'});
    await expect(dialog).toBeVisible();

    // 1. 测试非法路径分隔符
    await dialog.getByLabel('导出文件名').fill('folder/game.html');
    await dialog.getByRole('button', {name: '开始导出并下载'}).click();
    await expect(dialog.getByRole('alert')).toContainText('路径分隔符');

    // 2. 测试非法字符 < > : * ? " |
    await dialog.getByLabel('导出文件名').fill('game<bad>.html');
    await dialog.getByRole('button', {name: '开始导出并下载'}).click();
    await expect(dialog.getByRole('alert')).toContainText('非法字符');

    // 3. 测试 . 或 ..
    await dialog.getByLabel('导出文件名').fill('..');
    await dialog.getByRole('button', {name: '开始导出并下载'}).click();
    await expect(dialog.getByRole('alert')).toContainText('不能为 "." 或 ".."');

    // 4. 输入带有特殊字符的作品名，留空文件名：应拦截并提示显式填写合法文件名
    const trickyTitle = '<script>alert("xss")</script> & "special" $& $1 🐱';
    await dialog.getByLabel('作品名称').fill(trickyTitle);
    await dialog.getByLabel('导出文件名').fill(''); // 留空
    await dialog.getByRole('button', {name: '开始导出并下载'}).click();
    await expect(dialog.getByRole('alert')).toContainText('作品名包含非法字符，请显式填写合法导出文件名');

    // 5. 显式填写合法导出文件名后，导出成功，验证 HTML 内 title 与说明正确转义无 XSS 漏洞
    await dialog.getByLabel('导出文件名').fill('xss-safe-export.html');
    const downloadPromise = page.waitForEvent('download', {timeout: 60_000});
    await dialog.getByRole('button', {name: '开始导出并下载'}).click();
    const download = await downloadPromise;

    expect(download.suggestedFilename()).toBe('xss-safe-export.html');
    const downloadPath = await download.path();
    const fileContent = await readFile(downloadPath!, 'utf-8');
    // 验证特殊字符在 title 标签中已被安全转义
    expect(fileContent).toContain('&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;');
    // 不应有未经转义的原始 <script>alert("xss")</script> 侵入 HTML 结构
    expect(fileContent).not.toContain('<title><script>alert("xss")</script>');
  });

  test('导出后超过60秒仍能再次下载回归验证（虚拟时钟）', async ({page}) => {
    await waitForEditor(page);

    const exportBtn = page.getByRole('button', {name: '导出单文件 HTML 播放器'});
    await exportBtn.click();

    const dialog = page.getByRole('dialog', {name: '导出单文件 HTML'});
    await expect(dialog).toBeVisible();

    // 安装虚拟时钟以精准控制 60 秒 revoke 超时
    await page.clock.install();

    // 首次导出
    const firstDownloadPromise = page.waitForEvent('download', {timeout: 60_000});
    await dialog.getByRole('button', {name: '开始导出并下载'}).click();
    const firstDownload = await firstDownloadPromise;
    expect(await firstDownload.failure()).toBeNull();

    // 快进 61 秒使首次下载的 URL 过期
    await page.clock.fastForward(61_000);

    // 点击“再次下载”
    const secondDownloadPromise = page.waitForEvent('download', {timeout: 60_000});
    await dialog.getByRole('button', {name: '再次下载'}).click();
    const secondDownload = await secondDownloadPromise;

    // 关键回归断言：再次下载必须成功，不得为 'canceled'
    expect(await secondDownload.failure()).toBeNull();
    expect(secondDownload.suggestedFilename()).toBe(firstDownload.suggestedFilename());
  });

  test('延迟资源请求下取消导出不触发下载，且重开旧任务不得自动下载', async ({page}) => {
    await waitForEditor(page);

    // 人为延迟 vm.min.js 请求以在 packaging 状态下触发取消
    await page.route('**/assets/htmlifier/vm.min.js', async route => {
      await new Promise(resolve => setTimeout(resolve, 2500));
      await route.continue();
    });

    const exportBtn = page.getByRole('button', {name: '导出单文件 HTML 播放器'});
    await exportBtn.click();

    const dialog = page.getByRole('dialog', {name: '导出单文件 HTML'});
    await expect(dialog).toBeVisible();

    const submitBtn = dialog.getByRole('button', {name: '开始导出并下载'});
    await submitBtn.click();

    // 验证状态为打包中，且取消按钮依然可点击（非 disabled）
    const cancelBtn = dialog.getByRole('button', {name: '取消'});
    await expect(cancelBtn).toBeEnabled();
    await expect(dialog.getByText('正在获取运行时与装配单文件 HTML...')).toBeVisible();

    let unexpectedDownloadFired = false;
    page.on('download', () => {
      unexpectedDownloadFired = true;
    });

    // 点击取消
    await cancelBtn.click();
    await expect(dialog).toHaveCount(0);

    // 等待延迟请求完成，确保已取消任务不会自动触发下载
    await page.waitForTimeout(3000);
    expect(unexpectedDownloadFired).toBe(false);

    // 重新打开对话框，确认状态干净，旧任务不会残留
    await exportBtn.click();
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText('开始导出并下载')).toBeVisible();
    expect(unexpectedDownloadFired).toBe(false);

    await page.unroute('**/assets/htmlifier/vm.min.js');
  });

  test('重复快速点击只触发一次导出与下载', async ({page}) => {
    await waitForEditor(page);

    const exportBtn = page.getByRole('button', {name: '导出单文件 HTML 播放器'});
    await exportBtn.click();

    const dialog = page.getByRole('dialog', {name: '导出单文件 HTML'});
    await expect(dialog).toBeVisible();

    let downloadCount = 0;
    page.on('download', () => {
      downloadCount += 1;
    });

    const submitBtn = dialog.getByRole('button', {name: '开始导出并下载'});
    await Promise.all([
      submitBtn.click(),
      submitBtn.click()
    ]);

    await page.waitForTimeout(4000);
    expect(downloadCount).toBe(1);
  });

  test('弹窗取消、遮罩关闭、键盘 Escape 与回焦', async ({page}) => {
    await waitForEditor(page);

    const exportBtn = page.getByRole('button', {name: '导出单文件 HTML 播放器'});
    await exportBtn.click();

    const dialog = page.getByRole('dialog', {name: '导出单文件 HTML'});
    await expect(dialog).toBeVisible();

    // 按 Escape 关闭
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    // 验证焦点回归触发按钮
    await expect(exportBtn).toBeFocused();

    // 重新打开并点击取消按钮
    await exportBtn.click();
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', {name: '取消'}).click();
    await expect(dialog).toHaveCount(0);
    await expect(exportBtn).toBeFocused();

    // 重新打开并点击遮罩外部关闭
    await exportBtn.click();
    await expect(dialog).toBeVisible();
    await page.locator('.html-export-overlay').click({position: {x: 10, y: 10}});
    await expect(dialog).toHaveCount(0);
  });

  test('390px 移动端视口适配与滚动走查', async ({page}) => {
    await page.setViewportSize({width: 390, height: 844});
    await waitForEditor(page);

    const exportBtn = page.getByRole('button', {name: '导出单文件 HTML 播放器'});
    await exportBtn.click();

    const dialog = page.getByRole('dialog', {name: '导出单文件 HTML'});
    await expect(dialog).toBeVisible();

    // 验证底部操作按钮可见且可交互
    const submitBtn = dialog.getByRole('button', {name: '开始导出并下载'});
    await expect(submitBtn).toBeVisible();
    const cancelBtn = dialog.getByRole('button', {name: '取消'});
    await expect(cancelBtn).toBeVisible();

    // 验证弹窗主体具有滚动能力
    const bodyBox = dialog.locator('.html-export-body');
    const isScrollable = await bodyBox.evaluate(el => el.scrollHeight >= el.clientHeight);
    expect(isScrollable).toBe(true);

    // 截取 390px 移动端截图作为证据
    await page.screenshot({
      path: path.join(EVIDENCE_DIR, 'export-dialog-mobile-390px.png')
    });

    // 在 390px 视口下点击取消关闭
    await cancelBtn.click();
    await expect(dialog).toHaveCount(0);
  });
});
