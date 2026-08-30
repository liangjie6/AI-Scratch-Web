import { chromium } from '@playwright/test';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
page.on('pageerror', e => console.log('PAGEERROR', e.message));
await page.goto('http://127.0.0.1:5173/', { waitUntil: 'domcontentloaded' });
await page.waitForSelector('.blocklySvg', { timeout: 30000 });
await page.getByRole('button', { name: '打开 AI 写积木' }).click();
await page.waitForSelector('.ai-blocks-modes', { timeout: 30000 });
await page.route('**/api/ai-blocks/**', route => route.abort());
const modes = page.locator('.ai-blocks-modes');
if (await modes.evaluate(el => el.classList.contains('is-collapsed'))) {
  await modes.locator('button.ai-blocks-modes__toggle').click();
  await page.waitForTimeout(500);
}
await page.getByText('整个项目', { exact: true }).click();
await page.getByLabel('描述要实现的积木功能').fill('让角色移动');
await page.getByRole('button', { name: '发送需求' }).click();
await page.waitForSelector('.ai-blocks-alert--error', { timeout: 15000 });
await page.waitForTimeout(400);
const m = await modes.evaluate(el => {
  const cards = [...el.querySelectorAll('.ai-blocks-mode-card')];
  const er = el.getBoundingClientRect();
  return { modesH: Math.round(er.height), cards: cards.map(c => {
    const cr = c.getBoundingClientRect();
    return { h: Math.round(cr.height), vh: Math.round(cr.height), bottom: Math.round(cr.bottom - er.top), visible: cr.bottom <= er.bottom + 1 };
  })};
});
console.log(JSON.stringify(m));
await modes.screenshot({ path: 'C:/Users/Think/AppData/Local/Temp/ai-modes-alert.png' });
await page.locator('.ai-blocks-panel').screenshot({ path: 'C:/Users/Think/AppData/Local/Temp/ai-modes-alert-panel.png' });
console.log('saved');
await browser.close();
