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
const report = { steps: [], javascriptErrors: [], consoleErrors: [], expectedConsoleErrors: [], consoleWarnings: [], scheduleRequests: [], snapshots: [] };
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
async function selectPlan(name) {
  const row = dialog().locator('[aria-label="计划列表"]').getByRole('button').filter({ hasText: name });
  assert.equal(await row.count(), 1, name);
  await row.click();
  await dialog().getByRole('heading', { name, exact: true }).waitFor();
}
async function api(path, options = {}) {
  return page.evaluate(async ({ path, options }) => {
    const session = await (await fetch('/app/session')).json();
    const response = await fetch(path, { ...options, headers: { 'Content-Type': 'application/json', 'X-Agent-Scope': session.scope, 'Idempotency-Key': crypto.randomUUID(), ...(options.headers || {}) } });
    return { status: response.status, body: await response.text() };
  }, { path, options });
}
async function control(name) {
  assert.equal((await fetch(origin + '/__acceptance/' + name, { method: 'POST' })).status, 204, name);
}

try {
  await login();
  await openPlans();
  assert.equal(await dialog().locator('[aria-label="计划列表"] button').count(), 2);
  await selectPlan('每周一整理待办');
  assert.ok((await dialog().innerText()).includes('每周一 09:00'));
  await page.screenshot({ path: join(output, 'plans-initial.png'), fullPage: true });
  step('compiled product page lists the task and reminder returned by the current-owner plan API');

  await dialog().getByLabel('名称', { exact: true }).fill('每周二整理待办');
  await dialog().getByLabel('执行时间', { exact: true }).fill('10:30');
  await dialog().locator('.schedule-form select').selectOption('tuesday');
  const update = page.waitForResponse(response => response.request().method() === 'PUT' && response.url().includes('/app/product/plans/'));
  await dialog().getByRole('button', { name: '保存修改', exact: true }).click();
  assert.equal((await update).status(), 200);
  await dialog().getByText('计划已保存。除非再次修改，否则会按新版本执行。', { exact: true }).waitFor();
  await dialog().getByText('后台任务 · 版本 2', { exact: true }).waitFor();
  const stale = await api('/app/product/plans/plan-1', { method: 'PUT', body: JSON.stringify({ expected_revision: 1, name: '过期修改', timezone: 'Asia/Shanghai', trigger: { type: 'recurring', schedule: { type: 'weekly_at', time_of_day: '11:00', day_of_week: 'wednesday' } }, details: { goal: '不应保存', allowed_tools: ['todo_list'] } }) });
  assert.equal(stale.status, 409, stale.body);
  const expectedConflict = report.consoleErrors.findIndex(value => value === 'Failed to load resource: the server responded with a status of 409 (Conflict)');
  assert.ok(expectedConflict >= 0, 'browser did not observe the deliberate stale-write conflict');
  report.expectedConsoleErrors.push(report.consoleErrors.splice(expectedConflict, 1)[0]);
  step('edit sends the exact revision and stale browser writes are rejected by Scheduler CAS');

  const pause = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/pause'));
  await dialog().getByRole('button', { name: '暂停', exact: true }).click();
  assert.equal((await pause).status(), 200);
  await dialog().getByText('计划已暂停。', { exact: true }).waitFor();
  await dialog().getByText('后台任务 · 版本 3', { exact: true }).waitFor();
  await page.reload();
  await openPlans();
  await selectPlan('每周二整理待办');
  assert.ok((await dialog().innerText()).includes('已暂停'));
  step('pause persists across full page reload');

  await control('restart');
  await page.reload();
  await openPlans();
  await selectPlan('每周二整理待办');
  const resume = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/resume'));
  await dialog().getByRole('button', { name: '恢复', exact: true }).click();
  assert.equal((await resume).status(), 200);
  await dialog().getByText('计划已恢复。', { exact: true }).waitFor();
  await dialog().getByText('后台任务 · 版本 4', { exact: true }).waitFor();
  step('Agent host restart reuses the external Scheduler SDK state and resume advances the version');

  await selectPlan('周五提醒我提交周报');
  await dialog().getByRole('button', { name: '删除', exact: true }).click();
  await dialog().getByText('确定删除这个计划？', { exact: true }).waitFor();
  const remove = page.waitForResponse(response => response.request().method() === 'DELETE' && response.url().includes('/app/product/plans/'));
  await dialog().getByRole('button', { name: '确认删除', exact: true }).click();
  assert.equal((await remove).status(), 200);
  await dialog().getByText('计划已删除，重启后也不会恢复执行。', { exact: true }).waitFor();
  assert.equal(await dialog().getByText('周五提醒我提交周报', { exact: true }).count(), 0);
  step('destructive delete requires an explicit second click and removes the reminder from the public list');

  await selectPlan('每周二整理待办');
  await dialog().getByRole('button', { name: '交给 Agent 修改', exact: true }).click();
  const composer = page.getByRole('textbox', { name: '消息', exact: true });
  await composer.waitFor();
  assert.ok((await composer.inputValue()).includes('先读取最新版本再修改'));
  assert.ok((await composer.inputValue()).includes('当前版本 4'));
  step('management entry hands an exact plan id and revision to the natural-language conversation path');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await openPlans();
  await selectPlan('每周二整理待办');
  await page.waitForFunction(() => {
    const value = document.querySelector('[role="dialog"]');
    if (!value) return false;
    const bounds = value.getBoundingClientRect();
    return bounds.left >= 0 && bounds.right <= innerWidth && value.scrollWidth <= value.clientWidth + 1;
  });
  await page.screenshot({ path: join(output, 'plans-mobile.png'), fullPage: true });
  report.snapshots.push({ state: 'enabled', revision: 4, text: await dialog().innerText() });
  step('the surviving plan remains usable without horizontal overflow at 390px');

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
