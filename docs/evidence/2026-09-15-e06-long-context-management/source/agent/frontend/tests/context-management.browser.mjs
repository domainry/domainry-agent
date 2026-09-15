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
assert.equal((await (await fetch(origin + '/app/config')).json()).workspace_id, 'context-browser-workspace', 'refuse a non-fixture deployment');
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
  return { conversationID, messages, run: await read(`/agent/conversations/${conversationID}/runs/${runID}`) };
});
async function waitCompleted() {
  const deadline = Date.now() + 45000;
  while (Date.now() < deadline) {
    const current = await state();
    if (current.run.status === 'completed') return current;
    if (['failed', 'cancelled'].includes(current.run.status)) throw new Error(`run ended as ${current.run.status}: ${current.run.error_code || ''}`);
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error('context run did not complete');
}

try {
  await page.goto(origin);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await page.getByRole('button', { name: '新建会话', exact: true }).click();
  const create = page.getByRole('dialog', { name: '新建会话', exact: true });
  await create.getByLabel('会话名称', { exact: true }).fill('E06 长上下文管理验收');
  await create.getByRole('button', { name: '保存', exact: true }).click();
  await create.waitFor({ state: 'hidden' });
  await page.getByRole('textbox', { name: '消息', exact: true }).fill('保留这条用户修正，连续读取两次当前记录。');
  await page.getByRole('button', { name: '发送消息', exact: true }).click();
  const first = await waitCompleted();
  assert.equal(first.run.steps.length, 3);
  assert.equal(first.run.metrics.context_compactions, 1);
  assert.equal(first.run.metrics.compacted_results, 2);
  assert.equal(first.run.metrics.compacted_intervals, 1);
  assert.equal(first.run.metrics.context_limit_bytes, 20 * 1024);
  assert.ok(first.run.metrics.peak_context_bytes > 0 && first.run.metrics.peak_context_bytes <= first.run.metrics.context_limit_bytes);
  assert.equal(first.run.metrics.cache_read_input_tokens, 180);
  assert.equal(first.run.metrics.cache_creation_input_tokens, 100);
  for (const [index, version] of ['record-v1', 'record-v2', 'record-v3'].entries()) {
    const contextView = first.run.steps[index].context;
    assert.ok(contextView?.window?.provider_serialized);
    assert.equal(contextView.sources.find(source => source.key === 'business.current')?.version, version);
    assert.equal(contextView.sources.find(source => source.key === 'project.rules')?.stable_prefix, true);
    assert.equal(contextView.sources.find(source => source.key === 'file.current')?.kind, 'file_reference');
  }
  assert.deepEqual(first.run.steps[2].context.changes.map(change => [change.previous_version, change.current_version]), [['record-v1', 'record-v2'], ['record-v2', 'record-v3']]);
  step('registered project, business and file context stays ordered, refreshes by version and reports exact provider pressure and cache use');

  await page.getByRole('button', { name: '查看处理记录', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: '处理记录', exact: true });
  await dialog.getByText(/1 步压缩 · 2 个结果 · 1 个执行区间/).waitFor();
  const diagnostic = dialog.locator('details.context-diagnostic').last();
  await diagnostic.locator('summary').click();
  await diagnostic.getByText(/business.current.*版本 record-v3.*每步刷新/).waitFor();
  await diagnostic.getByText(/project.rules.*稳定前缀/).waitFor();
  await diagnostic.getByText(/record-v1 → record-v2.*record-v2 → record-v3/).waitFor();
  await diagnostic.getByText(/模型报告缓存：命中 100 token/).waitFor();
  await page.screenshot({ path: join(output, 'context-management-desktop.png'), fullPage: true });
  step('compiled UI exposes bounded source versions, change history, compaction and provider cache diagnostics without source content or hashes');

  await page.keyboard.press('Escape');
  assert.equal((await fetch(origin + '/__acceptance/revoke-old-context', { method: 'POST' })).status, 204);
  await page.getByRole('textbox', { name: '消息', exact: true }).fill('只使用现在仍获准的上下文继续。');
  await page.getByRole('button', { name: '发送消息', exact: true }).click();
  const second = await waitCompleted();
  assert.equal(second.run.steps[0].text, '旧来源已撤回，已使用当前 record-v4 上下文。');
  assert.equal(second.run.steps[0].context.sources.find(source => source.key === 'business.current')?.version, 'record-v4');
  assert.doesNotMatch(JSON.stringify(second.run), /quarter=Q[123]|first context run completed|content_hash|message_hash/);
  step('revoking the old source removes the derived historical reply while a newly authorized source version can continue');

  await page.getByRole('button', { name: '查看处理记录', exact: true }).last().click();
  const secondDialog = page.getByRole('dialog', { name: '处理记录', exact: true });
  const secondDiagnostic = secondDialog.locator('details.context-diagnostic').first();
  await secondDiagnostic.locator('summary').click();
  await secondDiagnostic.getByText(/business.current.*版本 record-v4/).waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await secondDiagnostic.scrollIntoViewIfNeeded();
  await page.waitForTimeout(300);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  assert.ok(overflow <= 1, `horizontal overflow ${overflow}`);
  await page.screenshot({ path: join(output, 'context-management-mobile.png'), fullPage: true });
  step('context diagnostics remain readable at 390px after source refresh and revocation');
  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  assert.deepEqual(report.consoleWarnings, []);
  assert.ok(report.resourceFailures.every(item => item.status === 401 || item.status === 404 && item.path === '/favicon.ico'), JSON.stringify(report.resourceFailures));
  report.complete = true;
  report.firstRun = first.run;
  report.secondRun = second.run;
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
