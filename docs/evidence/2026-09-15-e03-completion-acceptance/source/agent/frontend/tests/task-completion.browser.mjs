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
const report = { steps: [], javascriptErrors: [], preLoginConsoleErrors: [], consoleErrors: [], consoleWarnings: [], reviewRequests: [] };
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
page.on('console', message => {
  if (message.type() === 'error') report.consoleErrors.push(message.text());
  if (message.type() === 'warning') report.consoleWarnings.push(message.text());
});
page.on('response', response => {
  const url = new URL(response.url());
  if (response.request().method() === 'POST' && /\/agent\/conversation-tasks\/[^/]+\/completion-review$/.test(url.pathname)) {
    report.reviewRequests.push({ path: url.pathname, status: response.status() });
  }
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
  const row = tasks.locator('.task-list').getByRole('button').filter({ hasText: '浏览器等待用户逐项复核' });
	await row.waitFor();
  assert.equal(await row.count(), 1);
  await row.click();
  await tasks.getByRole('heading', { name: '浏览器等待用户逐项复核', exact: true }).waitFor();
  const completion = tasks.getByRole('region', { name: '任务完成验收', exact: true });
  const pending = await completion.innerText();
  assert.ok(pending.includes('记录 v1 · Agent 提交'));
  assert.ok(pending.includes('需要处理'));
  assert.ok(pending.includes('尚未确定'));
  assert.ok(pending.includes('仍有完成条件未通过'));
  assert.ok((await tasks.getByRole('region', { name: '后台任务详情', exact: true }).innerText()).includes('等待验收'));
  await page.screenshot({ path: join(output, 'task-completion-awaiting-review.png'), fullPage: true });
  step('task execution end remains awaiting review and renders the Agent submission, each condition, basis and blocker');

  await completion.getByRole('button', { name: '逐项复核', exact: true }).click();
  await completion.getByLabel('任务第 1 项核对结果', { exact: true }).selectOption('met');
  await completion.getByLabel('任务第 1 项核对依据', { exact: true }).fill('用户已核对交付内容与当前约定一致');
  await completion.getByLabel('任务复核说明', { exact: true }).fill('页面逐项验收通过');
  const response = page.waitForResponse(value => value.request().method() === 'POST' && value.url().endsWith('/completion-review'));
  await completion.getByRole('button', { name: '保存复核结果', exact: true }).click();
  assert.equal((await response).status(), 200);
  await completion.getByText('记录 v2 · 用户复核', { exact: true }).waitFor();
  await completion.getByText('条件已通过', { exact: true }).waitFor();
  assert.ok((await tasks.getByRole('region', { name: '后台任务详情', exact: true }).innerText()).includes('已完成'));
  step('user review binds the current digest and revision, evaluates every non-program condition and commits completion');

  await completion.getByRole('button', { name: '查看验收历史', exact: true }).click();
  const history = completion.getByRole('list', { name: '任务验收历史', exact: true });
  await history.waitFor();
  assert.equal(await history.locator(':scope > li').count(), 2);
  await history.getByText(/v1 · Agent 提交/).click();
  await history.getByText('仍有完成条件未通过', { exact: true }).waitFor();
  await page.screenshot({ path: join(output, 'task-completion-history.png'), fullPage: true });
  step('completion history preserves both the original unresolved assessment and the later user acceptance');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: join(output, 'task-completion-mobile.png'), fullPage: true });
  assert.deepEqual(report.reviewRequests.map(value => value.status), [200]);
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
