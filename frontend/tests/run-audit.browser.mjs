import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = process.env.AGENT_UI_ORIGIN || "http://127.0.0.1:8092";
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id, "accounts-workspace", "Refuse a non-fixture deployment");
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-H01-browser");
await mkdir(output, {recursive:true});
const browser = await chromium.launch({headless:true, channel:"chrome"});
const context = await browser.newContext({viewport:{width:1280,height:1000}});
const page = await context.newPage();
page.setDefaultTimeout(30000);
const report = {steps:[], javascriptErrors:[], consoleWarnings:[], resourceFailures:[], snapshots:[]};
page.on("pageerror", error => report.javascriptErrors.push(String(error)));
page.on("console", message => {
  if (message.type() === "warning") report.consoleWarnings.push(message.text());
  if (message.type() === "error" && !message.text().startsWith("Failed to load resource:")) report.javascriptErrors.push(message.text());
});
page.on("response", response => {
  if (response.status() >= 400) report.resourceFailures.push({status:response.status(), url:new URL(response.url()).pathname});
});
const step = name => { report.steps.push(name); console.log(`PASS ${name}`); };
const state = () => page.evaluate(async () => {
  const session = await (await fetch("/app/session")).json();
  const id = location.hash.slice(1), base = `/agent/conversations/${id}`;
  const read = async path => { const response = await fetch(path, {headers:{"X-Agent-Scope":session.scope}}); if (!response.ok) throw new Error(`${path}: ${response.status}`); return response.json(); };
  const conversation = await read(base), messages = await read(`${base}/messages`);
  const runID = conversation.active_run_id || messages.items.at(-1).run_id;
  return {conversation, messages, run:await read(`${base}/runs/${runID}`)};
});
async function waitStatus(status) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    const current = await state();
    if (current.run.status === status) return current;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`run did not reach ${status}`);
}
try {
  await page.goto(origin);
  await page.getByLabel("账号", {exact:true}).fill("admin@example.com");
  await page.getByLabel("密码", {exact:true}).fill("Changed-Accounts-Test!3");
  await page.getByRole("button", {name:"登录", exact:true}).click();
  await page.getByRole("button", {name:"新建会话", exact:true}).click();
  const create = page.getByRole("dialog", {name:"新建会话", exact:true});
  await create.getByLabel("会话名称", {exact:true}).fill("H01 运行审计验收");
  await create.getByRole("button", {name:"保存", exact:true}).click();
  await create.waitFor({state:"hidden"});
  await page.getByRole("textbox", {name:"消息", exact:true}).fill("创建审计验收事项");
  await page.getByRole("button", {name:"发送消息", exact:true}).click();
  await waitStatus("waiting_confirmation");
  await page.getByRole("button", {name:"确认执行", exact:true}).click();
  await waitStatus("needs_reconciliation");
  await page.getByRole("button", {name:"查询实际结果并继续", exact:true}).click();
  const completed = await waitStatus("completed");
  assert.equal(completed.run.correlation_id, completed.run.id);
  assert.deepEqual(completed.run.metrics, {steps:2, model_calls:2, tool_calls:1, tool_attempts:2, authorization_checks:3, confirmation_decisions:1});
  assert.equal(completed.run.usage.total_tokens, 20);
  assert.ok(completed.run.duration_ms >= 0);
  assert.ok(completed.run.audit_complete);
  assert.ok(completed.run.audit.some(event => event.type === "confirmation" && event.status === "approved" && event.actor_id));
  assert.ok(completed.run.audit.some(event => event.type === "authorization" && event.status === "granted" && event.authorization_revision));
  assert.doesNotMatch(JSON.stringify(completed.run.audit), /周报验收事项|fixture-record/);
  report.snapshots.push({label:"completed", run:completed.run});
  step("real model protocol, authorization, confirmation, uncertain result and reconciliation produce one safe correlated audit");

  assert.equal((await fetch(`${origin}/__acceptance/restart`, {method:"POST"})).status, 204);
  await page.reload();
  await page.getByRole("button", {name:"查看处理记录", exact:true}).first().click();
  const dialog = page.getByRole("dialog", {name:"处理记录", exact:true});
  await dialog.getByText(completed.run.id, {exact:true}).waitFor();
  await dialog.getByText(/2 个步骤 · 2 次模型调用 · 1 个工具调用/).waitFor();
  await dialog.getByText(/输入 tokens 14 · 输出 tokens 6 · 总 tokens 20/).waitFor();
  await dialog.getByRole("region", {name:"运行审计", exact:true}).getByText(/用户确认 · 已批准/).waitFor();
  const tool = dialog.locator(".execution-tool").filter({hasText:"create_fixture_record"});
  await tool.locator("summary").click();
  await tool.getByText(/授权结果/).waitFor();
  await tool.getByText(/已授权 · 检查 3 次/).waitFor();
  await tool.getByText(/确认结果/).waitFor();
  await tool.getByText(/已批准/).waitFor();
  await page.screenshot({path:join(output, "run-audit-desktop.png")});
  step("full host restart preserves usage, timing, authorization and confirmation details in the compiled product UI");

  await page.setViewportSize({width:390,height:844});
  await dialog.getByRole("region", {name:"运行审计", exact:true}).scrollIntoViewIfNeeded();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  assert.ok(overflow <= 1, `horizontal overflow ${overflow}`);
  await page.screenshot({path:join(output, "run-audit-mobile.png")});
  step("390px run details remain readable without horizontal page overflow");
  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleWarnings, []);
  assert.ok(report.resourceFailures.every(item => item.url === "/favicon.ico" && item.status === 404 || item.status === 401), JSON.stringify(report.resourceFailures));
} catch (error) {
  report.error = String(error);
  report.lastState = await state().catch(reason => ({error:String(reason)}));
  await page.screenshot({path:join(output, "failure.png")}).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
  await context.close();
  await browser.close();
}
