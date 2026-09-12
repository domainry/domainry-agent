// Run by the opt-in Identity/HTTP test, using its real built client and storage.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const inspect = (await import(pathToFileURL(process.env.AGENT_UI_QUALITY_CORE).href)).inspectStaticUiDocument;
const origin = process.env.AGENT_UI_ORIGIN, output = process.env.AGENT_UI_TEST_OUTPUT;
const conversation = process.env.AGENT_UI_CONVERSATION, attachment = process.env.AGENT_UI_ATTACHMENT;
const mobileOnly = process.env.AGENT_UI_CITATION_CASE === 'mobile';
assert.match(conversation, /^conv_[a-f0-9]{32}$/); assert.match(attachment, /^att_[a-f0-9]{32}$/);
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id, 'private-index-workspace');
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, acceptDownloads: true });
const page = await context.newPage(); page.setDefaultTimeout(15000);
const report = { complete: false, steps: [], baselines: [], errors: [], externalRequests: [], contentRequests: [] };
page.on('pageerror', error => report.errors.push(String(error)));
page.on('request', request => {
 const url = new URL(request.url());
 if (/^https?:/.test(url.protocol) && url.origin !== origin) report.externalRequests.push(url.href);
 if (url.pathname.endsWith('/content')) report.contentRequests.push(url.pathname);
});
const step = name => { report.steps.push(name); console.log(`PASS ${name}`); };
const source = () => page.getByRole('button', { name: '查看来源：private.pdf', exact: true });
const preview = () => page.getByRole('dialog').filter({ has: page.getByText('原文件在线预览', { exact: true }) });
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`, { method: 'POST' })).status, 204);
const baseline = async label => { report.baselines.push({ label, ...await page.evaluate(inspect, {}) }); await page.screenshot({ path: join(output, `${label}.png`), animations: 'disabled' }); };
async function openOriginal() {
 await source().click();
 const dialog = page.getByRole('dialog', { name: 'private.pdf', exact: true });
 await dialog.getByText('附件检索验收片段，来自 Connector。', { exact: true }).waitFor();
 const before = report.contentRequests.length;
 await dialog.getByRole('button', { name: '预览原文件', exact: true }).click();
 await preview().locator('canvas[data-rendered="true"]').waitFor();
 assert.equal(report.contentRequests.length - before, 1);
 assert.equal(report.contentRequests.at(-1), `/agent/conversations/${conversation}/attachments/${attachment}/content`);
}
try {
 await page.goto(`${origin}/#${conversation}`);
 await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
 await page.getByLabel('密码', { exact: true }).fill('Changed-Private-Index!3');
 await page.getByRole('button', { name: '登录', exact: true }).click();
 await page.getByRole('navigation', { name: '会话列表', exact: true }).getByRole('button', { name: /^附件引用验收/ }).click();
 await source().waitFor();
 await openOriginal();
 const reads = report.contentRequests.length;
 await preview().getByText('第 1 / 2 页', { exact: true }).waitFor();
 await preview().getByRole('button', { name: '下一页', exact: true }).click();
 await preview().getByText('第 2 / 2 页', { exact: true }).waitFor();
 await preview().locator('canvas[data-rendered="true"]').waitFor();
 assert.equal(report.contentRequests.length, reads, 'page navigation stays in the viewer');
 if (!mobileOnly) await baseline('desktop-private-citation');
 await page.setViewportSize({ width: 390, height: 844 });
 // ResizeObserver and PDF rendering complete after the viewport changes.
 // Wait for the actual fitted canvas, not the previous desktop render marker.
 await preview().locator('canvas').evaluate(async canvas => {
  await new Promise((resolve, reject) => {
   const deadline = performance.now() + 10000;
   const check = () => {
    if (canvas.dataset.rendered === 'true' && canvas.clientWidth <= canvas.parentElement.clientWidth) return resolve();
    if (performance.now() > deadline) return reject(new Error('PDF did not fit mobile viewport'));
    requestAnimationFrame(check);
   }; requestAnimationFrame(check);
  });
 });
 await baseline('mobile-private-citation');
 if (!mobileOnly) {
 await page.setViewportSize({ width: 1280, height: 900 });
 const downloading = page.waitForEvent('download');
 await preview().getByRole('button', { name: '下载原文件', exact: true }).click();
 const download = await downloading; await download.saveAs(join(output, 'download.pdf'));
 assert.deepEqual(await readFile(join(output, 'download.pdf')), await readFile(join(output, 'original.pdf')));
 step('persisted Connector citation opens exact authorized PDF; pages and download work');
 await control('revoke');
 await preview().getByRole('button', { name: '重新打开', exact: true }).click();
 await preview().getByRole('alert').waitFor();
 assert.equal(await preview().locator('canvas').count(), 0);
 await baseline('revoked-private-citation');
 await control('restore');
 await preview().getByRole('button', { name: '重新打开', exact: true }).click();
 await preview().locator('canvas[data-rendered="true"]').waitFor();
 step('Identity download revocation clears rendered bytes; restoration reopens original');
 await page.reload(); await source().waitFor(); await openOriginal();
 step('browser reload retains private citation and original preview');
 await control('revoke'); await page.reload();
 await page.getByRole('button', { name: '附件', exact: true }).waitFor();
 await source().waitFor({ state: 'hidden' });
 assert(!(await page.locator('body').innerText()).includes('附件检索验收片段'));
 await control('restore'); await page.reload(); await source().waitFor();
 step('revoked reply and citation hidden after reload; live permission restoration recovers them');
 } else step('mobile citation preview captured after fitted PDF finished rendering');
 assert.deepEqual(report.errors, []); assert.deepEqual(report.externalRequests, []);
 for (const result of report.baselines) { assert.deepEqual(result.qualityFailures, [], result.label); assert(!result.pageOverflow, result.label); }
 report.complete = true;
} catch (error) { report.failure = String(error); await page.screenshot({ path: join(output, 'failure.png') }).catch(() => {}); throw error; }
finally { await writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2)); await context.close(); await browser.close(); }
