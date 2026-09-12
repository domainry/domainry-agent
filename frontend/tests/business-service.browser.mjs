import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir, writeFile} from 'node:fs/promises';
import {join} from 'node:path';

const {chromium} = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.AGENT_UI_ORIGIN, output = process.env.AGENT_UI_TEST_OUTPUT;
const queryID = process.env.AGENT_BUSINESS_QUERY_CONVERSATION;
await mkdir(output, {recursive: true});
const browser = await chromium.launch({headless: true, channel: 'chrome'});
const context = await browser.newContext({viewport: {width: 1280, height: 1000}});
const page = await context.newPage();
page.setDefaultTimeout(30000);
const report = {steps: [], javascriptErrors: [], snapshots: [], actualIdentityProcess: true, actualBusinessHTTP: true, realLanguageModel: false};
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
const step = name => {report.steps.push(name); console.log('PASS ' + name);};
const control = async name => {
  const response = await fetch(origin + '/__acceptance/' + name, {method: 'POST'});
  assert.equal(response.status, name === 'records' ? 200 : 204, name);
  return name === 'records' ? response.json() : null;
};
const api = path => page.evaluate(async path => {
  const session = await (await fetch('/app/session')).json();
  const response = await fetch(path, {headers: {'X-Agent-Scope': session.scope}});
  const text = await response.text();
  let data;
  try {data = JSON.parse(text);} catch {throw new Error(path + ' returned HTTP ' + response.status + ' instead of JSON');}
  return {status: response.status, data};
}, path);
async function state() {
  const id = new URL(page.url()).hash.slice(1), base = '/agent/conversations/' + id;
  const conversation = await api(base), messages = await api(base + '/messages');
  assert.equal(conversation.status, 200); assert.equal(messages.status, 200);
  const runID = conversation.data.active_run_id || messages.data.items.at(-1)?.run_id;
  return {conversation: conversation.data, messages: messages.data, run: runID ? (await api(base + '/runs/' + runID)).data : null};
}
async function waitRun(status) {
  const deadline = Date.now() + 120000;
  while (Date.now() < deadline) {
    const snapshot = await state();
    if (snapshot.run?.status === status) return snapshot;
    assert.ok(!['failed', 'cancelled', 'needs_reconciliation'].includes(snapshot.run?.status), JSON.stringify(snapshot.run));
    await new Promise(resolve => setTimeout(resolve, 1000));
  }
  throw new Error('business run did not reach ' + status);
}
async function openQuery(hidden = false) {
  await page.goto(origin + '/#' + queryID);
  await page.reload();
  await page.getByRole('button', {name: '新建会话', exact: true}).waitFor();
  if (hidden) {
    // Absence during the loading screen is not evidence of a hidden answer.
    await page.getByText(/相关内容已隐藏/).first().waitFor();
    await page.waitForFunction(() => {
      const main = document.querySelector('main');
      return main && !main.innerText.includes('Acme');
    });
    const messages = await api('/agent/conversations/' + queryID + '/messages');
    assert.equal(messages.status, 200);
    const answer = messages.data.items.find(message => message.role === 'assistant');
    assert.ok(answer.access_error); assert.ok(!answer.content.includes('Acme'));
  } else {
    await page.getByText(/已逐页读取可访问的客户/).first().waitFor();
    const messages = await api('/agent/conversations/' + queryID + '/messages');
    const answer = messages.data.items.find(message => message.role === 'assistant');
    assert.equal(answer.access_error || '', '');
    for (const name of ['Acme', 'Beta', 'Gamma']) assert.ok(answer.content.includes(name), name);
  }
}
const records = async () => (await control('records')).map(record => ({id: record.id, name: record.data.name}));
try {
  await page.goto(origin);
  await page.getByLabel('账号', {exact: true}).fill('admin@example.com');
  await page.getByLabel('密码', {exact: true}).fill('Business-Browser-Changed!2026');
  await page.getByRole('button', {name: '登录', exact: true}).click();
  await page.getByRole('button', {name: '新建会话', exact: true}).waitFor();
  const settings = await api('/tools/preferences'); assert.equal(settings.status, 200);
  const keys = ['business_catalog', 'query_records', 'get_record', 'query_related_records', 'invoke_action', 'workflow_start', 'workflow_get'];
  assert.deepEqual(settings.data.items.filter(tool => keys.includes(tool.key) && tool.available).map(tool => tool.key).sort(), keys.sort());
  step('real Chrome login reaches actual shared Identity and seven optional business tools');

  await openQuery();
  const original = await state();
  assert.deepEqual(original.run.steps.flatMap(step => step.calls || []).map(call => call.name), ['business_catalog', 'business_catalog', 'query_records', 'query_records', 'query_records', 'get_record']);
  report.snapshots.push(original);
  await page.screenshot({path: join(output, 'business-query.png')});
  step('persisted catalog, three cursor pages and record detail render through independent product');

  await page.getByRole('button', {name: '新建会话', exact: true}).click();
  const dialog = page.getByRole('dialog', {name: '新建会话', exact: true});
  await dialog.getByLabel('会话名称', {exact: true}).fill('共享服务客户改名');
  await dialog.getByRole('button', {name: '保存', exact: true}).click();
  await dialog.waitFor({state: 'hidden'});
  await page.getByRole('textbox', {name: '消息', exact: true}).fill('验收：改名');
  await page.getByRole('button', {name: '发送消息', exact: true}).click();
  const pending = await waitRun('waiting_confirmation');
  const renameID = pending.conversation.id;
  const confirmation = page.getByRole('region', {name: '操作确认', exact: true});
  await confirmation.waitFor();
  for (const value of ['customer.rename', 'Agent Renamed', '目标记录']) assert.ok((await confirmation.innerText()).includes(value), value);
  const before = await records(); assert.equal(before.length, 4); assert.equal(before.filter(record => record.name === 'Agent Created').length, 1);
  assert.equal(before.filter(record => record.name === 'Agent Renamed').length, 0);
  await page.screenshot({path: join(output, 'business-confirmation.png')});
  report.snapshots.push(pending);
  step('browser requests actual rename and shows frozen target and payload before any mutation');

  await control('restart'); await page.reload();
  const restored = await waitRun('waiting_confirmation');
  assert.equal(restored.run.interaction.id, pending.run.interaction.id);
  assert.equal(restored.run.interaction.arguments, pending.run.interaction.arguments);
  await confirmation.getByRole('button', {name: '确认执行', exact: true}).click();
  const completed = await waitRun('completed');
  await page.locator('.run-status').filter({hasText: /^已保存$/}).waitFor();
  const changed = await records();
  assert.equal(changed.length, before.length);
  const target = before.find(record => record.name === 'Agent Created');
  assert.equal(changed.find(record => record.id === target.id).name, 'Agent Renamed');
  assert.ok(completed.messages.items.find(message => message.role === 'assistant').content.includes('Agent Renamed'));
  report.snapshots.push(completed);
  await page.screenshot({path: join(output, 'business-completed.png')});
  step('actual Identity process plus all product and Runtime bindings restart with pending confirmation; browser approval changes the exact record');

  await control('revoke-fields'); await openQuery(true);
  await page.screenshot({path: join(output, 'business-field-revoked.png')});
  await control('restore'); await openQuery();
  await control('revoke-read'); await openQuery(true);
  await control('restore'); await openQuery();
  step('current field and read revocations hide saved source-derived answer; restoring the original authority reveals it');

  await control('unavailable'); await openQuery(true);
  await control('available'); await openQuery();
  step('unavailable business owner hides old answer without fallback; restored owner revalidates the original source');

  await control('restart'); await page.goto(origin + '/#' + renameID); await page.reload();
  await page.getByText(/已读取业务实际结果/).first().waitFor();
  assert.equal((await records()).find(record => record.id === target.id).name, 'Agent Renamed');
  await page.setViewportSize({width: 390, height: 844});
  await page.getByText(/已读取业务实际结果/).first().scrollIntoViewIfNeeded();
  await page.waitForFunction(() => document.documentElement.scrollWidth <= innerWidth + 1);
  await page.screenshot({path: join(output, 'business-mobile.png')});
  step('completed reply and exact business effect survive another full restart and fit a 390px viewport');
  assert.equal(report.javascriptErrors.length, 0); report.complete = true;
} catch (error) {
  report.error = String(error); report.lastState = await state().catch(error => ({error: String(error)}));
  await page.screenshot({path: join(output, 'failure.png')}).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2));
  await browser.close();
}
