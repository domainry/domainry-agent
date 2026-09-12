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
const report = { steps: [], javascriptErrors: [], preLoginConsoleErrors: [], consoleErrors: [], consoleWarnings: [], snapshots: [], controlRequests: [] };
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
page.on('console', message => {
  if (message.type() === 'error') report.consoleErrors.push(message.text());
  if (message.type() === 'warning') report.consoleWarnings.push(message.text());
});
page.on('response', response => {
  const url = new URL(response.url());
  if (response.request().method() === 'POST' && /^\/agent\/conversation-tasks\/[^/]+\/(cancel|resume)$/.test(url.pathname)) {
    report.controlRequests.push({ path: url.pathname, status: response.status() });
  }
});
const step = value => { report.steps.push(value); console.log('PASS ' + value); };
const dialog = () => page.getByRole('dialog', { name: '后台任务', exact: true });
const taskNavigation = () => page.getByRole('button', { name: '后台任务 进度、等待与成果', exact: true });
async function login() {
  await page.goto(origin);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill('Changed-Task-Start!3');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await taskNavigation().waitFor();
  report.preLoginConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];
}
async function openTasks() {
  await taskNavigation().click();
  await dialog().waitFor();
  await dialog().locator('.task-list').waitFor();
}
async function selectGoal(goal) {
  const row = dialog().locator('.task-list').getByRole('button').filter({ hasText: goal });
  assert.equal(await row.count(), 1, goal);
  await row.click();
  await dialog().getByRole('heading', { name: goal, exact: true }).waitFor();
}
async function waitForCompleted(goal) {
  const deadline = Date.now() + 60000;
  while (Date.now() < deadline) {
    const detail = dialog().getByRole('region', { name: '后台任务详情', exact: true });
    if ((await detail.innerText()).includes('任务结果') && (await detail.innerText()).includes('失败任务已恢复完成。')) return;
    await dialog().getByRole('button', { name: '刷新任务', exact: true }).click();
    await selectGoal(goal);
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  throw new Error('resumed task did not complete');
}

try {
  await login();
  await openTasks();
  await selectGoal('浏览器失败恢复任务');
  const failed = dialog().getByRole('region', { name: '后台任务详情', exact: true });
  assert.ok((await failed.innerText()).includes('处理失败'));
  await failed.getByRole('button', { name: '继续任务', exact: true }).waitFor();
  await page.screenshot({ path: join(output, 'failed-task.png'), fullPage: true });
  const resumeResponse = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/resume'));
  await failed.getByRole('button', { name: '继续任务', exact: true }).click();
  assert.equal((await resumeResponse).status(), 200);
  await waitForCompleted('浏览器失败恢复任务');
  assert.equal(await failed.getByRole('button', { name: '继续任务', exact: true }).count(), 0);
  report.snapshots.push({ goal: '浏览器失败恢复任务', state: 'completed', text: await failed.innerText() });
  step('failed task resumes through its exact POST endpoint, re-enters the durable Run and completes at attempt 2');

  await page.reload();
  await taskNavigation().waitFor();
  await openTasks();
  await selectGoal('浏览器失败恢复任务');
  assert.ok((await dialog().innerText()).includes('失败任务已恢复完成。'));
  step('full page reload reads the completed resumed task from the server');

  await selectGoal('浏览器等待取消任务');
  const waiting = dialog().getByRole('region', { name: '后台任务详情', exact: true });
  const waitingText = await waiting.innerText();
  assert.ok(waitingText.includes('等待补充信息'));
  assert.ok(waitingText.includes('请选择发布渠道'));
  assert.ok(waitingText.includes('请在来源会话完成当前等待事项。'));
  assert.equal(await waiting.getByRole('button', { name: '继续任务', exact: true }).count(), 0);
  await waiting.getByRole('button', { name: '停止任务', exact: true }).click();
  await waiting.getByText('停止这个后台任务？', { exact: true }).waitFor();
  assert.ok((await waiting.innerText()).includes('正在写入外部系统的结果可能需要后续核查'));
  const cancelResponse = page.waitForResponse(response => response.request().method() === 'POST' && response.url().endsWith('/cancel'));
  await waiting.getByRole('button', { name: '确认停止任务', exact: true }).click();
  assert.equal((await cancelResponse).status(), 200);
  await waiting.getByText('已停止', { exact: true }).waitFor();
  await waiting.getByText('原等待事项已关闭，无法直接继续；请重新创建任务。', { exact: true }).waitFor();
  assert.equal(await waiting.getByRole('button', { name: '继续任务', exact: true }).count(), 0);
  report.snapshots.push({ goal: '浏览器等待取消任务', state: 'cancelled', text: await waiting.innerText() });
  step('waiting-user task requires explicit inline confirmation, cancels the exact Run and exposes the closed-interaction blocker');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await page.getByRole('button', { name: '打开工作导航', exact: true }).click();
  await openTasks();
  await selectGoal('浏览器等待取消任务');
  await page.waitForFunction(() => {
    const value = document.querySelector('[role="dialog"]');
    if (!value) return false;
    const bounds = value.getBoundingClientRect();
    return bounds.left >= 0 && bounds.right <= innerWidth && value.scrollWidth <= value.clientWidth + 1;
  });
  const blocker = dialog().getByText('原等待事项已关闭，无法直接继续；请重新创建任务。', { exact: true });
  await blocker.waitFor();
  await blocker.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(output, 'cancelled-task-mobile.png'), fullPage: true });
  step('cancelled state and blocker survive reload and remain usable at 390px');

  assert.deepEqual(report.controlRequests.map(value => value.status), [200, 200]);
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
