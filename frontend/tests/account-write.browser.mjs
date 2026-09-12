import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.AGENT_UI_ORIGIN, output = process.env.AGENT_UI_TEST_OUTPUT;
assert.equal((await (await fetch(origin + '/app/config')).json()).workspace_id, 'accounts-workspace');
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(45000);
const report = { steps: [], javascriptErrors: [], snapshots: [] };
page.on('pageerror', e => report.javascriptErrors.push(String(e)));
const step = s => { report.steps.push(s); console.log('PASS ' + s); };
const control = async name => assert.equal((await fetch(origin + '/__acceptance/' + name, { method: 'POST' })).status, 204, name);
const audit = async () => (await fetch(origin + '/__acceptance/audit', { method: 'POST' })).json();
const api = (path, method = 'GET', body) => page.evaluate(async ({ path, method, body }) => {
  const session = await (await fetch('/app/session')).json();
  const r = await fetch(path, { method, headers: { 'X-Agent-Scope': session.scope, 'Content-Type': 'application/json' }, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
  return { status: r.status, data: await r.json() };
}, { path, method, body });
let runPath;
async function state() {
  const result = await api(runPath);
  assert.equal(result.status, 200);
  return result.data;
}
async function waitStatus(status, call) {
  const deadline = Date.now() + 150000;
  let current;
  while (Date.now() < deadline) {
    current = await state();
    if (current.status === status && (!call || current.interaction?.call_id === call)) return current;
    assert.ok(!['failed', 'cancelled', 'completed'].includes(current.status), JSON.stringify(current));
    await new Promise(r => setTimeout(r, 500));
  }
  throw new Error(`expected ${status}/${call || ''}: ${JSON.stringify(current)}`);
}
async function create(title, message = '创建日程、清空原事件说明地点参与者，并发送和回复完整邮件') {
  await page.getByRole('button', { name: '新建会话', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '新建会话', exact: true });
  await dialog.getByLabel('会话名称', { exact: true }).fill(title);
  await dialog.getByRole('button', { name: '保存', exact: true }).click();
  await dialog.waitFor({ state: 'hidden' });
  await page.getByRole('textbox', { name: '消息', exact: true }).fill(message);
  const submitted = page.waitForResponse(r => r.url().endsWith('/messages') && r.request().method() === 'POST');
  await page.getByRole('button', { name: '发送消息', exact: true }).click();
  const response = await submitted;
  assert.equal(response.status(), 202);
  const run = await response.json();
  runPath = `/agent/conversations/${run.conversation_id}/runs/${run.id}`;
  const waiting = await waitStatus('waiting_confirmation');
  await page.getByRole('region', { name: '操作确认', exact: true }).waitFor();
  return waiting;
}
const confirmation = () => page.getByRole('region', { name: '操作确认', exact: true });
const groupedApprove = () => confirmation().getByRole('button', { name: '授权并执行这 4 项', exact: true });
async function assertPreview() {
  await groupedApprove().waitFor();
  await page.waitForFunction(() => [...document.querySelectorAll('.interaction-card button')].some(b => b.textContent === '授权并执行这 4 项' && !b.disabled));
  const text = await confirmation().innerText();
  for (const value of ['日程与邮件验收账号', 'primary', 'review-event', '2026-11-01', '2026-11-02', 'America/New_York', '清空日程说明', '清空地点', '移除原有全部参与者', 'to@example.test', 'cc@example.test', 'bcc@example.test', 'reply@example.test', 'original-mail', '完整发送主题', '完整回复正文', '<script>这只是正文</script>', '正文末尾确认内容']) assert.ok(text.includes(value), value);
  assert.equal(await confirmation().locator('script').count(), 0);
  assert.equal(await confirmation().getByLabel('外部账号操作内容', { exact: true }).count(), 4);
  const bodies = await confirmation().locator('pre.account-write-text').allTextContents();
  assert.ok(bodies.includes('完整发送正文\n第二行：<script>这只是正文</script>\n' + '正文核对。'.repeat(4000) + '\n正文末尾确认内容'));
}
try {
  await page.goto(origin);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill('Changed-Accounts-Test!3');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await page.getByRole('button', { name: '新建会话', exact: true }).waitFor();
  const keys = ['calendar_write_accounts', 'calendar_event_inspect', 'calendar_event_create', 'calendar_event_update', 'mail_write_accounts', 'mail_send', 'mail_reply'];
  const settings = async () => (await api('/tools/preferences')).data.items.filter(v => keys.includes(v.key));
  const empty = await settings(); assert.equal(empty.length, 7); assert.ok(empty.every(v => !v.available));
  await control('connect'); assert.ok((await settings()).every(v => v.available));
  step('seven opted-in tools require the actual owner OAuth grant');

  const first = await create('完整内容与一次范围授权');
  await assertPreview(); assert.deepEqual((await audit()).effects, {});
  await page.screenshot({ path: join(output, 'four-operation-preview.png'), fullPage: true });
  const cards = confirmation().getByLabel('外部账号操作内容', { exact: true });
  for (const [index, name] of [[0, 'calendar-create-preview'], [1, 'calendar-update-preview'], [2, 'mail-recipients-preview']]) {
    await cards.nth(index).evaluate(el => el.scrollIntoView({ block: 'start' }));
    await page.screenshot({ path: join(output, name + '.png') });
  }
  await control('restart'); await page.reload();
  const restored = await waitStatus('waiting_confirmation');
  assert.deepEqual(restored.interaction, first.interaction); await assertPreview();
  step('four exact targets, all recipient groups, complete bodies and clear semantics survive host restart before approval');

  const submitted = page.waitForRequest(r => r.url().endsWith('/respond') && r.method() === 'POST');
  await groupedApprove().click(); const approved = (await submitted).postDataJSON();
  assert.equal(approved.scope, 'listed_operations');
  const completed = await waitStatus('completed');
  assert.deepEqual((await audit()).effects, { calendar_create: 1, calendar_update: 1, mail_send: 1, mail_reply: 1 });
  assert.equal((await api(runPath + '/respond', 'POST', approved)).status, 200);
  assert.deepEqual((await audit()).effects, { calendar_create: 1, calendar_update: 1, mail_send: 1, mail_reply: 1 });
  report.snapshots.push({ label: 'grouped', initial: first, completed });
  await page.getByRole('button', { name: '查看处理记录', exact: true }).first().click();
  const history = page.getByRole('dialog', { name: '处理记录', exact: true });
  await history.locator('summary').filter({ hasText: /^发送邮件/ }).click();
  await history.getByText('邮件已由服务受理', { exact: true }).first().waitFor();
  assert.ok((await history.innerText()).includes('送达状态尚未确认'));
  await page.screenshot({ path: join(output, 'accepted-results.png') }); await page.keyboard.press('Escape');
  step('one grouped approval causes exactly four native effects; duplicate approval does not resend and result cards state accepted with delivery unknown');

  await create('单项批准和拒绝其余'); await assertPreview();
  await confirmation().getByRole('button', { name: '仅确认第一项', exact: true }).click();
  const single = await waitStatus('waiting_confirmation', 'aw-update');
  assert.deepEqual((await audit()).effects, { calendar_create: 2, calendar_update: 1, mail_send: 1, mail_reply: 1 });
  await confirmation().getByRole('button', { name: '拒绝执行', exact: true }).click();
  await waitStatus('cancelled'); report.snapshots.push({ label: 'single', run: single });
  step('approving only the first item creates one event; rejection leaves update and both mail operations unexecuted');

  await create('确认时撤销写权限'); await assertPreview();
  await control('revoke-write');
  const denied = page.waitForResponse(r => r.url().endsWith('/respond') && r.request().method() === 'POST');
  await groupedApprove().click(); assert.equal((await denied).status(), 403);
  assert.deepEqual((await audit()).effects, { calendar_create: 2, calendar_update: 1, mail_send: 1, mail_reply: 1 });
  await control('restore-write'); await page.reload();
  await confirmation().getByRole('button', { name: '停止本次处理', exact: true }).click(); await waitStatus('cancelled');
  step('current Identity write revocation blocks a displayed approval before any vendor write');

  const unknown = await create('结果未知时只核查原回执', '未知发送：受理后断线');
  await page.setViewportSize({ width: 390, height: 844 });
  const confirm = confirmation().getByRole('button', { name: '确认执行', exact: true });
  await page.waitForFunction(() => [...document.querySelectorAll('.interaction-card button')].some(b => b.textContent === '确认执行' && !b.disabled));
  await confirmation().getByLabel('外部账号操作内容', { exact: true }).evaluate(el => el.scrollIntoView({ block: 'start' }));
  assert.ok((await confirmation().innerText()).includes('受理后断线'));
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
  await page.screenshot({ path: join(output, 'mail-preview-mobile.png'), fullPage: true });
  const body = confirmation().locator('pre.account-write-text');
  await body.evaluate(el => { el.scrollTop = el.scrollHeight; });
  await body.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(output, 'mail-body-end-mobile.png') });
  await confirm.click(); await waitStatus('needs_reconciliation');
  const before = await audit(); await control('restart'); await page.reload();
  await page.getByRole('button', { name: '查询实际结果并继续', exact: true }).click();
  const uncertain = await waitStatus('needs_reconciliation'); const after = await audit();
  assert.equal(uncertain.id, unknown.id); assert.deepEqual(after.effects, before.effects); assert.equal(after.vendor_http_requests, before.vendor_http_requests);
  await page.screenshot({ path: join(output, 'unknown-receipt-mobile.png') });
  report.snapshots.push({ label: 'unknown', initial: unknown, run: uncertain, before, after });
  step('390px preview contains full outgoing content; provider acceptance with lost response remains uncertain and restart reconciliation performs zero vendor I/O');
  assert.equal(report.javascriptErrors.length, 0); report.complete = true;
} catch (error) {
  report.error = String(error); report.lastRun = await state().catch(e => ({ error: String(e) }));
  await page.screenshot({ path: join(output, 'failure.png') }).catch(() => {}); throw error;
} finally {
  await writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2)); await browser.close();
}
