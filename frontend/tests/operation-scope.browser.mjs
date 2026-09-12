import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = "http://127.0.0.1:8092";
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id, "scope-workspace", "Refuse a non-fixture deployment");
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-C04-browser");
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(30000);
const report = { steps: [], javascriptErrors: [], snapshots: [] };
page.on("pageerror", error => report.javascriptErrors.push(String(error)));
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`, { method: "POST" })).status, 204, name);
const step = name => { report.steps.push(name); console.log(`PASS ${name}`); };
const state = () => page.evaluate(async () => {
  const session = await (await fetch("/app/session")).json();
  const id = location.hash.slice(1), base = `/agent/conversations/${id}`;
  const read = async path => { const r = await fetch(path, { headers: { "X-Agent-Scope": session.scope } }); if (!r.ok) throw new Error(`read ${path}: ${r.status}`); return r.json(); };
  const conversation = await read(base), messages = await read(`${base}/messages`);
  const runID = conversation.active_run_id || messages.items.at(-1).run_id;
  return { conversation, messages, run: await read(`${base}/runs/${runID}`), todos: await read(`/agent/todos?source_conversation_id=${id}`) };
});
async function waitStatus(status, callID) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    const current = await state();
    if (current.run.status === status && (!callID || current.run.interaction?.call_id === callID)) return current;
    if (["failed", "cancelled", "completed"].includes(current.run.status) && current.run.status !== status) throw new Error(`unexpected ${current.run.status}/${current.run.error_code}`);
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`run did not reach ${status}/${callID || ""}`);
}
async function create(title, message) {
  await page.getByRole("button", { name: "新建会话", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "新建会话", exact: true });
  await dialog.getByLabel("会话名称", { exact: true }).fill(title);
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  await page.getByRole("textbox", { name: "消息", exact: true }).fill(message);
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await page.getByRole("button", { name: "授权并执行这 2 项", exact: true }).waitFor();
  return waitStatus("waiting_confirmation", "scope-first");
}
try {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Changed-Scope-Test!3");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("button", { name: "新建会话", exact: true }).waitFor();

  const initial = await create("逐项授权及重启", "先创建这两项，再追加一项");
  assert.equal(initial.todos.items.length, 0);
  assert.equal(initial.run.interaction.operations.length, 2);
  await page.reload();
  await control("restart_scope_host");
  await page.reload();
  const persisted = await waitStatus("waiting_confirmation", "scope-first");
  assert.deepEqual(persisted.run.interaction, initial.run.interaction);
  await page.getByLabel("本次操作授权范围", { exact: true }).waitFor();
  await page.screenshot({ path: join(output, "scope-list.png") });
  step("concrete operation list survives page and complete host restart without effects");

  await control("arm_scope_execution");
  let submitted;
  await page.route("**/respond", async route => {
    const result = await route.fetch();
    assert.equal(result.status(), 200);
    submitted = { input: route.request().postDataJSON(), run: await result.json() };
    await route.abort("failed");
  }, { times: 1 });
  await page.getByRole("button", { name: "授权并执行这 2 项", exact: true }).click();
  await page.waitForFunction(() => Object.keys(localStorage).some(key => key.startsWith("agent-interaction:") && localStorage.getItem(key)?.includes('"scope":"listed_operations"')));
  for (let n = 0; !submitted && n < 100; n++) await new Promise(resolve => setTimeout(resolve, 50));
  assert.equal(submitted?.input.scope, "listed_operations");
  assert.equal(submitted.run.interaction.approved_scope, "listed_operations");
  await control("restart_scope_host");
  await page.reload();
  const outside = await waitStatus("waiting_confirmation", "scope-extra");
  assert.equal(outside.run.id, initial.run.id);
  assert.equal(outside.todos.items.length, 2);
  assert.equal(outside.run.interaction.authorization_id, undefined);
  await page.locator(".interaction-card").getByText("追加：发送项目汇总前复核", { exact: false }).waitFor();
  await page.screenshot({ path: join(output, "outside-scope.png") });
  await page.getByRole("button", { name: "拒绝执行", exact: true }).click();
  const stopped = await waitStatus("cancelled");
  assert.equal(stopped.todos.items.length, 2);
  report.snapshots.push({ label: "grouped approval and lost response", submitted, final: stopped });
  step("lost response and restart retain two approvals; the new third call requires separate consent and rejection creates nothing");

  await create("只授权第一项", "只创建这两项待办");
  await page.getByRole("button", { name: "仅确认第一项", exact: true }).click();
  const single = await waitStatus("waiting_confirmation", "scope-second");
  assert.equal(single.todos.items.length, 1);
  await page.getByRole("button", { name: "拒绝执行", exact: true }).click();
  assert.equal((await waitStatus("cancelled")).todos.items.length, 1);
  step("single-operation approval leaves the second operation waiting for its own consent");

  const revokedInitial = await create("撤权后恢复", "只创建这两项待办");
  await control("revoke_scope_tools");
  const deniedResponse = page.waitForResponse(response => response.url().endsWith("/respond") && response.request().method() === "POST");
  await page.getByRole("button", { name: "授权并执行这 2 项", exact: true }).click();
  assert.equal((await deniedResponse).status(), 403);
  await control("restore_scope_tools");
  await page.reload();
  const restored = await waitStatus("waiting_confirmation", "scope-first");
  assert.equal(restored.todos.items.length, 0);
  assert.equal(restored.run.last_event_seq, revokedInitial.run.last_event_seq);
  // The uncommitted response may still be pending locally. Retrying keeps its
  // client id and scope; restoration never auto-submits user approval.
  const retry = page.getByRole("button", { name: "重试授权这 2 项", exact: true });
  if (await retry.count()) await retry.click(); else await page.getByRole("button", { name: "授权并执行这 2 项", exact: true }).click();
  const completed = await waitStatus("completed");
  assert.equal(completed.todos.items.length, 2);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: join(output, "completed-mobile.png") });
  report.snapshots.push({ label: "restored current permission", final: completed });
  step("Identity revocation blocks approval without state change; explicit retry after restoration produces exactly two todos");
  assert.deepEqual(report.javascriptErrors, []);
} catch (error) {
  report.error = String(error);
  await page.screenshot({ path: join(output, "failure.png") }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
  await context.close();
  await browser.close();
  await control("finish");
}
