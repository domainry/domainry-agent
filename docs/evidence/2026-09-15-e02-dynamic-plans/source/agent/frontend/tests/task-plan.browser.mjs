import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.AGENT_UI_ORIGIN;
const output = process.env.AGENT_UI_TEST_OUTPUT;
assert.ok(origin && output, 'AGENT_UI_ORIGIN and AGENT_UI_TEST_OUTPUT are required');
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(45000);
const report = { steps: [], javascriptErrors: [], preLoginConsoleErrors: [], consoleErrors: [], consoleWarnings: [] };
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
page.on('console', message => {
  if (message.type() === 'error') report.consoleErrors.push(message.text());
  if (message.type() === 'warning') report.consoleWarnings.push(message.text());
});
const step = value => { report.steps.push(value); console.log('PASS ' + value); };

try {
  await page.goto(origin);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill('Changed-Planning-Task!3');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  const taskNavigation = page.getByRole('button', { name: '后台任务 进度、等待与成果', exact: true });
  await taskNavigation.waitFor();
  report.preLoginConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];
  await taskNavigation.click();
  const tasks = page.getByRole('dialog', { name: '后台任务', exact: true });
  await tasks.getByRole('heading', { name: '核对当前时间并发布结果', exact: true }).waitFor();
  const plan = tasks.getByRole('region', { name: '执行计划', exact: true });
  await plan.getByText('计划 v2 · 要求 v1', { exact: true }).waitFor();
  const current = await plan.innerText();
  assert.ok(current.includes('The tool receipt completed both remaining steps'));
  assert.ok(current.includes('Inspect current time'));
  assert.ok(current.includes('Publish verified answer'));
  assert.ok(current.includes('Current time receipt verified'));
  assert.ok(current.includes('Verified answer prepared'));
  assert.equal(await plan.getByRole('button', { name: '执行证据 inspect-time', exact: true }).count(), 2);
  step('task detail renders the current durable plan with executor, requirement fields, outcomes and exact evidence links');

  await plan.getByLabel('计划历史版本', { exact: true }).selectOption('1');
  await plan.getByText('计划 v1 · 要求 v1', { exact: true }).waitFor();
  const historical = await plan.innerText();
  assert.ok(historical.includes('执行中'));
  assert.ok(historical.includes('待执行'));
  assert.equal(await plan.getByRole('button', { name: '执行证据 inspect-time', exact: true }).count(), 0);
  step('version selector reads the immutable first plan instead of rewriting it with current status');

  await plan.getByLabel('计划历史版本', { exact: true }).selectOption('2');
  await page.screenshot({ path: join(output, 'task-plan.png'), fullPage: true });
  await plan.getByRole('button', { name: '执行证据 inspect-time', exact: true }).first().click();
  const execution = page.getByRole('dialog', { name: '处理记录', exact: true });
  await execution.waitFor();
  await execution.getByText('查询时间', { exact: true }).waitFor();
  step('evidence link opens the exact persisted execution run and tool call');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: join(output, 'task-plan-evidence-mobile.png'), fullPage: true });
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
  await fetch(origin + '/__acceptance/finish', { method: 'POST' }).catch(() => {});
  await browser.close();
}
