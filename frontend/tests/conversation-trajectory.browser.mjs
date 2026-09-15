import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.AGENT_UI_ORIGIN;
const output = process.env.AGENT_UI_TEST_OUTPUT;
const password = process.env.AGENT_UI_PASSWORD;
assert.ok(origin && output && password, 'AGENT_UI_ORIGIN, AGENT_UI_TEST_OUTPUT and AGENT_UI_PASSWORD are required');
await mkdir(output, { recursive: true });
assert.equal((await (await fetch(origin + '/app/config')).json()).workspace_id, 'trajectory-browser-workspace', 'refuse a non-fixture deployment');

const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const context = await browser.newContext({ viewport: { width: 1360, height: 1000 }, acceptDownloads: true });
const page = await context.newPage();
page.setDefaultTimeout(45000);
const report = { steps: [], javascriptErrors: [], consoleErrors: [], consoleWarnings: [], resourceFailures: [] };
let loggedIn = false;
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
page.on('console', message => {
  if (message.type() === 'error' && !message.text().startsWith('Failed to load resource:')) report.consoleErrors.push(message.text());
  if (message.type() === 'warning') report.consoleWarnings.push(message.text());
});
page.on('response', response => {
  if (loggedIn && response.status() >= 400) report.resourceFailures.push({ status: response.status(), path: new URL(response.url()).pathname });
});
const step = value => { report.steps.push(value); console.log('PASS ' + value); };
const hostState = async () => (await (await page.request.get(origin + '/__acceptance/trajectory-state')).json());
const apiState = () => page.evaluate(async () => {
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
  const runID = conversation.active_run_id || [...messages.items].reverse().find(item => item.run_id)?.run_id;
  return { conversationID, conversation, messages, run: runID ? await read(`/agent/conversations/${conversationID}/runs/${runID}`) : null };
});
async function waitCompleted() {
  const deadline = Date.now() + 45000;
  while (Date.now() < deadline) {
    const current = await apiState();
    if (current.run?.status === 'completed') return current;
    if (['failed', 'cancelled'].includes(current.run?.status)) throw new Error(`run ended as ${current.run.status}: ${current.run.error_code || ''}`);
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error('conversation run did not complete');
}

try {
  await page.goto(origin);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  loggedIn = true;
  await page.getByRole('button', { name: '新建会话', exact: true }).click();
  const create = page.getByRole('dialog', { name: '新建会话', exact: true });
  await create.getByLabel('会话名称', { exact: true }).fill('E09 会话分叉与轨迹回放验收');
  await create.getByRole('button', { name: '保存', exact: true }).click();
  await create.waitFor({ state: 'hidden' });
  await page.getByRole('textbox', { name: '消息', exact: true }).fill('记录原方案并形成稳定边界');
  await page.getByRole('button', { name: '发送消息', exact: true }).click();
  const source = await waitCompleted();
  assert.equal((await hostState()).model_calls, 1);
  step('source run reaches a persisted stable completed boundary');

  await page.getByRole('button', { name: '查看处理记录', exact: true }).last().click();
  const dialog = page.getByRole('dialog', { name: '处理记录', exact: true });
  const panel = dialog.getByRole('region', { name: '请求与工具轨迹', exact: true });
  await panel.getByText('稳定边界', { exact: true }).waitFor();
  assert.match(await panel.innerText(), /1 次模型请求 · 1 次录制响应 · 0 个工具调用/);
  await panel.locator('.trajectory-records details').first().locator('summary').click();
  await panel.getByText('记录原方案并形成稳定边界', { exact: true }).waitFor();
  assert.match(await panel.innerText(), /原方案已完成并保存/);
  step('compiled UI exposes the exact saved model-visible request and response at the stable boundary');

  await panel.getByRole('button', { name: '核对录制夹具', exact: true }).click();
  await panel.getByText(/录制夹具可用：1 次请求，未执行外部操作/).waitFor();
  assert.equal((await hostState()).model_calls, 1);
  step('model fixture replay consumes recorded responses without invoking the model or tools');

  const downloadEvent = page.waitForEvent('download');
  await panel.getByRole('button', { name: '导出 JSON', exact: true }).click();
  const download = await downloadEvent;
  const exportPath = join(output, await download.suggestedFilename());
  await download.saveAs(exportPath);
  const exported = JSON.parse(await readFile(exportPath, 'utf8'));
  assert.equal(exported.source.conversation_id, source.conversationID);
  assert.equal(exported.source.run_id, source.run.id);
  assert.equal(exported.requests.length, 1);
  assert.equal(exported.responses.length, 1);
  assert.equal(exported.tools.length, 0);
  assert.ok(exported.sha256 && exported.boundary_event_seq > 0);
  await panel.getByText(/轨迹已导出/).waitFor();
  step('authorized export downloads deterministic structured trajectory data');

  const comparison = panel.locator('form.trajectory-compare');
  await comparison.getByLabel('运行 ID', { exact: true }).fill(source.run.id);
  await comparison.getByRole('button', { name: '开始对照', exact: true }).click();
  await panel.getByText('两条轨迹完全一致', { exact: true }).waitFor();
  assert.equal((await hostState()).model_calls, 1);
  await page.screenshot({ path: join(output, 'conversation-trajectory-source-desktop.png'), fullPage: true });
  step('trajectory comparison uses request, response and tool hashes without replaying execution');

  await panel.getByRole('button', { name: '创建独立分叉', exact: true }).click();
  await dialog.waitFor({ state: 'hidden' });
  await page.getByRole('heading', { name: '分叉探索', exact: true }).waitFor();
  const childID = locationHash(page.url());
  assert.notEqual(childID, source.conversationID);
  assert.equal((await hostState()).model_calls, 1);
  const provenance = page.getByRole('button', { name: new RegExp(`分叉自运行 ${source.run.id.slice(0, 12)}`) });
  await provenance.waitFor();
  step('live rerun creates an independent peer conversation and waits for new input');

  await provenance.click();
  await page.getByRole('dialog', { name: '处理记录', exact: true }).waitFor();
  assert.equal(locationHash(page.url()), source.conversationID);
  await page.keyboard.press('Escape');
  await page.goto(origin + '#' + childID);
  await page.getByRole('heading', { name: '分叉探索', exact: true }).waitFor();
  await page.getByRole('textbox', { name: '消息', exact: true }).fill('沿另一条路线继续');
  await page.getByRole('button', { name: '发送消息', exact: true }).click();
  const child = await waitCompleted();
  const finalHost = await hostState();
  assert.equal(finalHost.model_calls, 2);
  assert.equal(finalHost.fork_context_seen, true);
  assert.match(child.messages.items.at(-1).content, /分叉已基于受权历史上下文/);
  step('the child starts only after user input and receives the authorized historical snapshot as inert context');

  await page.getByRole('button', { name: '查看处理记录', exact: true }).last().click();
  const childDialog = page.getByRole('dialog', { name: '处理记录', exact: true });
  const childPanel = childDialog.getByRole('region', { name: '请求与工具轨迹', exact: true });
  await childPanel.getByText('稳定边界', { exact: true }).waitFor();
  const childComparison = childPanel.locator('form.trajectory-compare');
  await childComparison.getByLabel('会话 ID', { exact: true }).fill(source.conversationID);
  await childComparison.getByLabel('运行 ID', { exact: true }).fill(source.run.id);
  await childComparison.getByRole('button', { name: '开始对照', exact: true }).click();
  await childPanel.getByText(/发现 \d+ 处差异/).waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await childPanel.scrollIntoViewIfNeeded();
  await page.waitForFunction(() => {
    const element = document.querySelector('.run-dialog');
    if (!element) return false;
    const rect = element.getBoundingClientRect();
    return rect.x >= 0 && rect.right <= innerWidth && element.scrollWidth <= element.clientWidth + 1;
  });
  await page.screenshot({ path: join(output, 'conversation-trajectory-mobile.png'), fullPage: true });
  await page.setViewportSize({ width: 1360, height: 1000 });
  await page.screenshot({ path: join(output, 'conversation-trajectory-child-desktop.png'), fullPage: true });
  step('fork provenance remains navigable and source/child trajectories report concrete hash differences on desktop and mobile');

  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  assert.deepEqual(report.resourceFailures, []);
  await writeFile(join(output, 'conversation-trajectory-report.json'), JSON.stringify({ ...report, sourceConversation: source.conversationID, sourceRun: source.run.id, childConversation: childID, childRun: child.run.id, exportedSha256: exported.sha256, modelCalls: finalHost.model_calls, forkContextSeen: finalHost.fork_context_seen }, null, 2));
} catch (error) {
  await page.screenshot({ path: join(output, 'conversation-trajectory-failure.png'), fullPage: true });
  throw error;
} finally {
  await browser.close();
}

function locationHash(url) {
  return new URL(url).hash.slice(1);
}
