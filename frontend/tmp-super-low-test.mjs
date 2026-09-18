import { chromium } from '@playwright/test';
const browser = await chromium.launch({headless: true});
const page = await browser.newPage({viewport: {width: 1440, height: 1000}});
const started = Date.now();
const elapsed = () => Math.round((Date.now() - started) / 1000) + 's';
page.on('pageerror', e => console.log('PAGE_ERROR', elapsed(), e.stack || e.message));
page.on('requestfailed', r => console.log('REQUEST_FAILED', elapsed(), r.url(), r.failure()?.errorText));
try {
  await page.goto('http://127.0.0.1:5173/', {waitUntil: 'domcontentloaded', timeout: 60000});
  await page.locator('.blocklySvg').waitFor({state: 'visible', timeout: 60000});
  await page.getByRole('button', {name: '打开 AI 写积木'}).click();
  await page.getByRole('heading', {name: 'AI 写积木'}).waitFor({state: 'visible', timeout: 30000});
  await page.locator('input[type=radio][value="super"]').check();
  await page.locator('details.ai-blocks-thinking-control summary').click();
  const thinking = page.getByLabel('思考强度');
  if (await thinking.count()) await thinking.fill('1');
  console.log('READY', elapsed(), 'super=', await page.locator('input[type=radio][value="super"]').isChecked(), 'thinking=', await thinking.inputValue().catch(() => 'unknown'));
  await page.getByLabel('描述要实现的积木功能').fill('使用原始的背景。除小猫外，添加角色Milk作为红包，并建立一个得分变量用于计分。小猫在舞台下方，只能通过键盘左右键控制左右移动；不断克隆红包，克隆体出现在舞台上方水平随机位置并降落下来；红包碰到小猫，红包消失并加10分，否则落地消失；当得分等于50分，小猫隐藏，出现“游戏结束”四个字，游戏结束。');
  await page.getByRole('button', {name: '发送需求'}).click();
  console.log('SUBMITTED', elapsed());
  let last = '';
  for (let i = 0; i < 96; i += 1) {
    await page.waitForTimeout(5000);
    const body = (await page.locator('body').innerText()).replace(/\s+/g, ' ').trim();
    const marker = ['正在整理需求', '正在理解玩法规则', '正在编排项目职责', '正在规划项目', '正在编写积木', '资源确认', '已完成统一生成', '生成失败', '生成出错', '请求失败', '无法完成'].find(x => body.includes(x));
    if (marker && marker !== last) { last = marker; console.log('STATE', elapsed(), marker, body.slice(-500)); }
    if (/已完成统一生成|生成失败|生成出错|请求失败|无法完成/.test(body)) break;
  }
  await page.screenshot({path: 'test-results/red-packet-super-low.png', fullPage: true});
  console.log('FINAL', elapsed(), (await page.locator('body').innerText()).replace(/\s+/g, ' ').trim().slice(-3500));
  console.log('BLOCK_COUNT', await page.locator('.blocklyDraggable').count());
} catch (e) {
  console.log('TEST_ERROR', elapsed(), e.stack || e.message);
  await page.screenshot({path: 'test-results/red-packet-super-low-error.png', fullPage: true}).catch(() => {});
} finally { await browser.close(); }
