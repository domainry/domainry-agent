import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.AGENT_UI_ORIGIN, output = process.env.AGENT_UI_TEST_OUTPUT;
assert.ok(origin && output, 'AGENT_UI_ORIGIN and AGENT_UI_TEST_OUTPUT are required');
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(30000);
const report = { steps: [], javascriptErrors: [], consoleErrors: [], consoleWarnings: [], scheduleRequests: [], snapshots: [] };
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
page.on('console', message => {
  if (message.type() === 'error') report.consoleErrors.push(message.text());
  if (message.type() === 'warning') report.consoleWarnings.push(message.text());
});
page.on('response', response => {
  if (response.url().includes('/app/product/plans')) report.scheduleRequests.push({ method: response.request().method(), url: response.url(), status: response.status() });
});
const step = name => { report.steps.push(name); console.log('PASS ' + name); };
const dialog = () => page.getByRole('dialog', { name: '计划与提醒', exact: true });
const navigation = () => page.getByRole('button', { name: /^计划与提醒/ });

async function login() {
  await page.goto(origin);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill('Changed-Accounts-Test!3');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await navigation().waitFor();
  report.consoleErrors = [];
  report.consoleWarnings = [];
}

async function openPlans() {
  if (await page.evaluate(() => matchMedia('(max-width: 650px)').matches)) await page.getByRole('button', { name: '打开工作导航', exact: true }).click();
  await navigation().click();
  await dialog().waitFor();
  await dialog().getByText('正在读取计划…', { exact: true }).waitFor({ state: 'hidden' });
}

async function restart() {
  assert.equal((await fetch(origin + '/__acceptance/restart', { method: 'POST' })).status, 204);
}

try {
  await login();
  await openPlans();
  assert.equal(await dialog().locator('[aria-label="计划列表"] button').count(), 1);
  await dialog().locator('[aria-label="计划列表"] button').click();
  await dialog().getByText('后台跟进 · 版本 1', { exact: true }).waitFor();
  assert.equal(await dialog().locator('label').filter({ hasText: '完成条件' }).locator('textarea').inputValue(), '所有发布阻塞项均已关闭');
  await dialog().getByText('首次运行建立基线；之后只有状态变化、完成、失败或需要操作时才通知。', { exact: true }).waitFor();
  await page.screenshot({ path: join(output, 'follow-up-enabled.png'), fullPage: true });
  step('compiled page exposes the bounded follow-up scope and sparse notification rule');

  const pause = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/pause'));
  await dialog().getByRole('button', { name: '停止跟进', exact: true }).click();
  assert.equal((await pause).status(), 200);
  await dialog().getByText('计划已暂停。', { exact: true }).waitFor();
  await dialog().getByText('后台跟进 · 版本 2', { exact: true }).waitFor();
  step('stop follow-up maps to the public pause operation and advances the exact revision');

  await restart();
  await page.reload();
  await openPlans();
  await dialog().locator('[aria-label="计划列表"] button').click();
  await dialog().getByText('后台跟进 · 版本 2', { exact: true }).waitFor();
  assert.ok((await dialog().innerText()).includes('已暂停'));
  await dialog().getByRole('button', { name: '恢复', exact: true }).waitFor();
  step('the stopped state remains durable across a full Agent host restart');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await openPlans();
  await dialog().locator('[aria-label="计划列表"] button').click();
  await page.waitForFunction(() => {
    const value = document.querySelector('[role="dialog"]');
    if (!value) return false;
    const bounds = value.getBoundingClientRect();
    return bounds.left >= 0 && bounds.right <= innerWidth && value.scrollWidth <= value.clientWidth + 1;
  });
  report.snapshots.push({ state: 'paused', revision: 2, text: await dialog().innerText() });
  await page.screenshot({ path: join(output, 'follow-up-mobile-paused.png'), fullPage: true });
  step('follow-up management remains usable without horizontal overflow at 390px');

  assert.equal(report.javascriptErrors.length, 0);
  assert.equal(report.consoleErrors.length, 0);
  assert.equal(report.consoleWarnings.length, 0);
  report.complete = true;
} catch (error) {
  report.error = String(error);
  await page.screenshot({ path: join(output, 'failure.png'), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2));
  await browser.close();
}
