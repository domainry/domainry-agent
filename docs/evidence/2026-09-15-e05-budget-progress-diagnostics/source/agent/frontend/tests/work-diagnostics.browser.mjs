import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.AGENT_UI_ORIGIN;
const output = process.env.AGENT_UI_TEST_OUTPUT;
const conversation = process.env.AGENT_UI_CONVERSATION;
assert.ok(origin && output && conversation, 'AGENT_UI_ORIGIN, AGENT_UI_TEST_OUTPUT and AGENT_UI_CONVERSATION are required');
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(60000);
const report = { steps: [], javascriptErrors: [], preLoginConsoleErrors: [], consoleErrors: [], consoleWarnings: [] };
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
page.on('console', message => {
  if (message.type() === 'error') report.consoleErrors.push(message.text());
  if (message.type() === 'warning') report.consoleWarnings.push(message.text());
});
const step = value => { report.steps.push(value); console.log('PASS ' + value); };

try {
  await page.goto(origin + '#' + conversation);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill('Peer-Changed!33');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await page.getByRole('button', { name: 'Agent 协作 目录、委派与沟通', exact: true }).waitFor();
  report.preLoginConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];
  await page.goto(origin + '#' + conversation);
  await page.getByText('正在同步协作状态…', { exact: true }).waitFor({ state: 'hidden' });
  await page.getByRole('button', { name: 'Agent 协作 目录、委派与沟通', exact: true }).click();
  const collaboration = page.getByRole('dialog', { name: 'Agent 协作', exact: true });
  await collaboration.getByText('正在读取协作记录…', { exact: true }).waitFor({ state: 'hidden' });
  const task = collaboration.locator('.task-list button').filter({ hasText: '核查发布说明' });
  await task.waitFor();
  await task.click();

  const diagnostic = collaboration.getByRole('region', { name: '预算与进度诊断', exact: true });
  await diagnostic.getByText('无需后续动作', { exact: true }).waitFor();
  const content = await diagnostic.innerText();
  assert.match(content, /执行活动/);
  assert.match(content, /业务进度/);
  assert.match(content, /7 \/ 12 次模型步骤/);
  assert.match(content, /6 \/ 12 次工具调用/);
  assert.match(content, /700 输入 token/);
  assert.match(content, /140 输出 token/);
  assert.match(content, /3 项完成条件已核实 · 0 项剩余/);
  assert.match(content, /2 项阶段成果 · 0 个计划阶段未结束/);
  assert.match(content, /本项委派累计/);
  assert.match(content, /整个工作累计/);
  assert.match(content, /整个工作上限/);
  assert.match(content, /0\.001960 CNY 模型费用/);
  step('desktop separates execution activity, business progress, delegation allocation and whole-work budget');
  await diagnostic.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(output, 'work-diagnostics.png'), fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForFunction(() => {
    const element = document.querySelector('.task-diagnostics');
    const rect = element?.getBoundingClientRect();
    return rect && rect.x >= 0 && rect.right <= window.innerWidth && element.scrollWidth <= element.clientWidth + 1;
  });
  await diagnostic.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(output, 'work-diagnostics-mobile.png'), fullPage: true });
  step('390px layout keeps diagnostics inside the viewport');

  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  assert.deepEqual(report.consoleWarnings, []);
  report.complete = true;
} catch (error) {
  report.error = String(error);
  await page.screenshot({ path: join(output, 'failure.png'), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2));
  await browser.close();
}
