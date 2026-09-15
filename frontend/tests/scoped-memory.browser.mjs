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
assert.equal((await (await fetch(origin + '/app/config')).json()).workspace_id, 'memory-browser-workspace');
const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const context = await browser.newContext({ viewport: { width: 1280, height: 960 } });
const page = await context.newPage();
page.setDefaultTimeout(45000);
const report = { steps: [], javascriptErrors: [], consoleErrors: [], consoleWarnings: [], resourceFailures: [], mutations: [] };
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
page.on('console', message => { if (message.type() === 'error' && !message.text().startsWith('Failed to load resource:')) report.consoleErrors.push(message.text()); if (message.type() === 'warning') report.consoleWarnings.push(message.text()); });
page.on('response', response => { const path = new URL(response.url()).pathname; if (response.status() >= 400) report.resourceFailures.push({ status: response.status(), path }); if (/\/agent\/conversations\/memories\//.test(path) && ['PUT', 'DELETE'].includes(response.request().method())) report.mutations.push({ method: response.request().method(), status: response.status(), path }); });
const step = value => { report.steps.push(value); console.log('PASS ' + value); };

async function createConversation(title) {
  await page.getByRole('button', { name: '新建会话', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '新建会话', exact: true });
  await dialog.getByLabel('会话名称', { exact: true }).fill(title);
  await dialog.getByRole('button', { name: '保存', exact: true }).click();
  await dialog.waitFor({ state: 'hidden' });
  const memorySwitch = page.getByRole('switch', { name: '使用个人记忆', exact: true });
  if (await memorySwitch.getAttribute('aria-checked') !== 'true') await memorySwitch.click();
  await page.waitForFunction(() => document.querySelector('[role="switch"][aria-label="使用个人记忆"]')?.getAttribute('aria-checked') === 'true');
}
async function send(message, expected) {
  await page.getByRole('textbox', { name: '消息', exact: true }).fill(message);
  await page.getByRole('button', { name: '发送消息', exact: true }).click();
  await page.getByText(expected, { exact: true }).waitFor();
}
async function api(path) {
  return page.evaluate(async path => {
    const session = await (await fetch('/app/session')).json();
    const response = await fetch(path, { headers: { 'X-Agent-Scope': session.scope } });
    return { status: response.status, data: await response.json() };
  }, path);
}

try {
  await page.goto(origin);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await createConversation('K02 Alpha 记忆');
  await send('项目 Alpha 使用 SQLite，目前只核对开发环境。', '已记录当前消息。');
  const sourceMessage = page.locator('[data-message-id]').filter({ hasText: '项目 Alpha 使用 SQLite，目前只核对开发环境。' });
  await sourceMessage.getByRole('button', { name: '将这条消息保存为记忆', exact: true }).click();
  let dialog = page.getByRole('dialog', { name: '个人记忆', exact: true });
  await dialog.getByLabel('记忆类型', { exact: true }).selectOption('project_fact');
  assert.equal(await dialog.getByLabel('记忆范围', { exact: true }).inputValue(), 'conversation');
  await dialog.getByLabel('名称', { exact: true }).fill('Alpha 存储架构');
  await dialog.getByLabel('适用主题', { exact: true }).fill('Alpha，存储架构');
  await dialog.getByLabel('适用限制或未确认信息', { exact: true }).fill('旧内容只覆盖开发环境');
  await dialog.getByRole('button', { name: '保存记忆', exact: true }).click();
  await dialog.getByText(/项目事实 · 当前会话/).waitFor();
  let memories = await api('/agent/conversations/memories');
  assert.equal(memories.status, 200);
  assert.equal(memories.data.length, 1);
  assert.equal(memories.data[0].source.message_id.length > 0, true);
  assert.equal(memories.data[0].scope.conversation_id, locationHash(page));
  step('saving from an original message records project-fact type, conversation scope, topics, uncertainty and exact source');

  await dialog.getByRole('button', { name: '编辑记忆 Alpha 存储架构', exact: true }).click();
  await dialog.getByLabel('内容', { exact: true }).fill('项目 Alpha 生产环境使用 PostgreSQL');
  await dialog.getByLabel('本次修正原因', { exact: true }).fill('生产环境已经完成核对');
  await dialog.getByRole('button', { name: '保存修改', exact: true }).click();
  await dialog.getByText(/修正自 v1：生产环境已经完成核对/).waitFor();
  await page.keyboard.press('Escape');
  await send('继续 Alpha 存储架构', '已使用当前会话的 Alpha 项目事实。');
  step('an explicit correction keeps its previous revision and is recalled for a relevant request in the same conversation');

  await createConversation('K02 Beta 隔离');
  await send('请说明完全无关的 Beta 事项', 'Beta 会话没有收到 Alpha 的私有任务资料。');
  step('a conversation-scoped project fact is excluded from another conversation automatically');

  await page.getByRole('navigation', { name: '会话列表' }).getByRole('button', { name: /K02 Alpha 记忆/ }).click();
  await page.getByRole('button', { name: /^个人记忆/ }).click();
  dialog = page.getByRole('dialog', { name: '个人记忆', exact: true });
  await dialog.getByText(/项目事实 · 当前会话/).waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await dialog.getByText(/修正自 v1/).scrollIntoViewIfNeeded();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  assert.ok(overflow <= 1, `horizontal overflow ${overflow}`);
  await page.screenshot({ path: join(output, 'scoped-memory-mobile.png'), fullPage: true });
  await dialog.getByRole('button', { name: '删除记忆 Alpha 存储架构', exact: true }).click();
  await dialog.getByText('还没有已保存记忆。会话历史仍单独保留。', { exact: true }).waitFor();
  step('compiled mobile UI shows provenance and correction metadata and deletion removes the active memory');

  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  assert.deepEqual(report.consoleWarnings, []);
  assert.ok(report.resourceFailures.every(item => item.status === 404 && item.path === '/favicon.ico' || item.status === 401 && ['/app/session', '/auth/refresh'].includes(item.path)), JSON.stringify(report.resourceFailures));
  assert.deepEqual(report.mutations.map(item => item.status), [200, 200, 200]);
  report.complete = true;
} catch (error) {
  report.error = String(error);
  await page.screenshot({ path: join(output, 'scoped-memory-failure.png'), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, 'scoped-memory-report.json'), JSON.stringify(report, null, 2));
  await context.close();
  await browser.close();
}

function locationHash(page) {
  return new URL(page.url()).hash.slice(1);
}
