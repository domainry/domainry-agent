import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.AGENT_UI_ORIGIN;
const output = process.env.AGENT_UI_TEST_OUTPUT;
const password = process.env.AGENT_UI_PASSWORD;
assert.ok(origin && output && password, 'AGENT_UI_ORIGIN, AGENT_UI_TEST_OUTPUT and AGENT_UI_PASSWORD are required');
await mkdir(output, { recursive: true });
assert.equal((await (await fetch(origin + '/app/config')).json()).workspace_id, 'parallel-browser-workspace', 'refuse a non-fixture deployment');
const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(45000);
const report = { steps: [], javascriptErrors: [], consoleErrors: [], consoleWarnings: [], resourceFailures: [] };
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
page.on('console', message => {
  if (message.type() === 'error' && !message.text().startsWith('Failed to load resource:')) report.consoleErrors.push(message.text());
  if (message.type() === 'warning') report.consoleWarnings.push(message.text());
});
page.on('response', response => {
  if (response.status() >= 400) report.resourceFailures.push({ status: response.status(), path: new URL(response.url()).pathname });
});
const step = value => { report.steps.push(value); console.log('PASS ' + value); };
const state = () => page.evaluate(async () => {
  const session = await (await fetch('/app/session')).json();
  const conversationID = location.hash.slice(1);
  const headers = { 'X-Agent-Scope': session.scope };
  const read = async path => {
    const response = await fetch(path, { headers });
    if (!response.ok) throw new Error(`${path}: ${response.status}`);
    return response.json();
  };
  const conversation = await read(`/agent/conversations/${conversationID}`);
  const messages = await read(`/agent/conversations/${conversationID}/messages`);
  const runID = conversation.active_run_id || messages.items.at(-1).run_id;
  return { conversationID, run: await read(`/agent/conversations/${conversationID}/runs/${runID}`) };
});
async function waitCompleted() {
  const deadline = Date.now() + 45000;
  while (Date.now() < deadline) {
    const current = await state();
    if (current.run.status === 'completed') return current;
    if (['failed', 'cancelled'].includes(current.run.status)) throw new Error(`run ended as ${current.run.status}: ${current.run.error_code || ''}`);
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error('parallel run did not complete');
}

try {
  await page.goto(origin);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await page.getByRole('button', { name: '新建会话', exact: true }).click();
  const create = page.getByRole('dialog', { name: '新建会话', exact: true });
  await create.getByLabel('会话名称', { exact: true }).fill('E04 受控工具并行验收');
  await create.getByRole('button', { name: '保存', exact: true }).click();
  await create.waitFor({ state: 'hidden' });
  await page.getByRole('textbox', { name: '消息', exact: true }).fill('同时读取两个互不依赖的值');
  await page.getByRole('button', { name: '发送消息', exact: true }).click();
  const completed = await waitCompleted();
  assert.deepEqual(completed.run.steps[0].calls.map(call => call.id), ['read-one', 'read-two']);
  assert.equal(completed.run.steps[0].tool_execution, 'parallel_read');
  assert.equal(completed.run.steps[0].parallel_tool_calls, 2);
  assert.deepEqual(completed.run.metrics, {
    steps: 2,
    model_calls: 2,
    tool_calls: 2,
    tool_attempts: 2,
    parallel_tool_batches: 1,
    parallel_tool_calls: 2,
    peak_parallel_tools: 2,
    authorization_checks: 2,
    confirmation_decisions: 0,
  });
  step('two explicitly independent reads preserve call order and expose actual bounded parallel metrics');

  await page.getByRole('button', { name: '查看处理记录', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: '处理记录', exact: true });
  await dialog.getByText('2 个读取调用 · 1 批 · 峰值 2', { exact: true }).waitFor();
  await dialog.getByText(/2 项独立读取按并行模式调度/).waitFor();
  assert.equal(await dialog.locator('.execution-tool').count(), 2);
  assert.equal(await dialog.locator('.execution-tool[data-tool-status="completed"]').count(), 2);
  await page.screenshot({ path: join(output, 'tool-parallelism-desktop.png'), fullPage: true });
  step('compiled UI distinguishes run-level parallel metrics from the ordered per-step tool receipts');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(300);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  assert.ok(overflow <= 1, `horizontal overflow ${overflow}`);
	const bounds = await dialog.evaluate(element => {
	  const box = element.getBoundingClientRect();
	  const style = getComputedStyle(element);
	  return { left: box.left, right: box.right, width: box.width, viewport: innerWidth, clientWidth: document.documentElement.clientWidth, visualWidth: visualViewport?.width, screenWidth: screen.width, mobileMedia: matchMedia('(max-width: 640px)').matches, cssWidth: style.width, minWidth: style.minWidth, maxWidth: style.maxWidth, boxSizing: style.boxSizing };
	});
	report.mobileDialogBounds = bounds;
	assert.ok(bounds.left >= 0 && bounds.right <= bounds.viewport, `dialog outside viewport ${JSON.stringify(bounds)}`);
  await page.screenshot({ path: join(output, 'tool-parallelism-mobile.png'), fullPage: true });
  step('parallel execution evidence remains readable at 390px');
  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  assert.deepEqual(report.consoleWarnings, []);
  assert.ok(report.resourceFailures.every(item => item.status === 401 || item.status === 404 && item.path === '/favicon.ico'), JSON.stringify(report.resourceFailures));
  report.complete = true;
  report.run = completed.run;
} catch (error) {
  report.error = String(error);
  report.lastState = await state().catch(reason => ({ error: String(reason) }));
  await page.screenshot({ path: join(output, 'failure.png'), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2));
  await context.close();
  await browser.close();
}
