#!/usr/bin/env node
/**
 * Browser smoke probe for a running AI Scratch Web deployment.
 *
 *   node deploy/scripts/probe-page.mjs [origin]
 *
 * Verifies that the page actually boots in a *browser* — including from an
 * insecure origin such as http://<lan-ip>:9966/ where crypto.subtle is
 * undefined — and that the embedded Scratch editor paints without any console
 * or page errors. Requires Playwright from frontend/node_modules.
 */
import {createRequire} from 'node:module';

const require = createRequire(new URL('../../frontend/', import.meta.url));
const {chromium} = require('playwright');

const ORIGIN = process.argv[2] ?? process.env.TARGET_ORIGIN ?? 'http://127.0.0.1:9966/';

const browser = await chromium.launch();
const context = await browser.newContext({viewport: {width: 1440, height: 900}, ignoreHTTPSErrors: true});
const page = await context.newPage();

const pageErrors = [];
const consoleErrors = [];
page.on('pageerror', error => pageErrors.push(String(error && error.message ? error.message : error)));
page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });

await page.goto(ORIGIN, {waitUntil: 'load', timeout: 60000});

let editorReady = false;
try {
  await page.waitForFunction(() => {
    const text = document.body.innerText;
    return !!document.querySelector('canvas') && /代码|造型|Motion|运动/.test(text);
  }, {timeout: 45000});
  editorReady = true;
} catch {
  editorReady = false;
}

const probe = await page.evaluate(() => ({
  title: document.title,
  secureContext: globalThis.isSecureContext,
  subtleCrypto: !!(globalThis.crypto && globalThis.crypto.subtle),
  canvases: document.querySelectorAll('canvas').length,
  textSample: document.body.innerText.replace(/\s+/g, ' ').trim().slice(0, 120)
}));

await browser.close();

const ok = editorReady && pageErrors.length === 0 && consoleErrors.length === 0;
console.log(JSON.stringify({origin: ORIGIN, editorReady, ok, probe, consoleErrors, pageErrors}, null, 2));
process.exit(ok ? 0 : 1);
