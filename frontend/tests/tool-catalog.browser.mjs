// Isolated real product UI + Identity/SQLite; the model and knowledge server
// are protocol fixtures. Never uses a user's desktop profile or credentials.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = "http://127.0.0.1:8092";
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-tool-catalog-browser");
await mkdir(output, { recursive: true });
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id, "catalog-workspace", "Refuse a non-fixture deployment");
const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
page.setDefaultTimeout(30000);
const report = { steps: [], javascriptErrors: [], runs: [], completedRuns: [] };
page.on("response", async response => {
  if (!/\/agent\/conversations\/[^/]+\/runs\/[^/?]+$/.test(response.url())) return;
  try {
    const run = await response.json();
    report.runs.push({ id: run.id, status: run.status, error: run.error_code, seq: run.last_event_seq, at: new Date().toISOString() });
    if (report.runs.length > 100) report.runs.shift();
  } catch { /* Requests cancelled on a page switch have no body to inspect. */ }
});
page.on("pageerror", error => report.javascriptErrors.push(String(error)));
const step = name => { report.steps.push(name); console.log(`PASS ${name}`); };
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`, { method: "POST" })).status, 204, name);
const readServerState = () => page.evaluate(async () => {
  const session = await (await fetch("/app/session")).json();
  const base = `/agent/conversations/${location.hash.slice(1)}`;
  const get = path => fetch(path, { headers: { "X-Agent-Scope": session.scope } }).then(r => r.json());
  const conversation = await get(base), messages = await get(`${base}/messages`);
  const id = conversation.active_run_id || messages.items?.at(-1)?.run_id;
  return { conversation, messages, run: id ? await get(`${base}/runs/${id}`) : null };
});
async function completed() {
  // A tool result or text delta is not a committed final answer. Verify both
  // the actual UI terminal label and the stored run/message through HTTP.
  await page.locator(".run-status").filter({ hasText: /^已保存$/ }).waitFor({ timeout: 60000 });
  const stored = await readServerState();
  assert.equal(stored.run.status, "completed");
  assert.equal(stored.messages.items.at(-1).role, "assistant");
  assert.equal(stored.messages.items.at(-1).run_id, stored.run.id);
  report.completedRuns.push({ id: stored.run.id, attempt: stored.run.attempt, assistantMessageID: stored.messages.items.at(-1).id, calls: stored.run.steps.flatMap(step => step.calls).map(call => ({ id: call.id, name: call.name, status: call.status })) });
  return stored.run;
}
async function sendFresh(title, message) {
  await page.getByRole("button", { name: "新建会话", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "新建会话", exact: true });
  await dialog.getByLabel("会话名称", { exact: true }).fill(title);
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  await page.getByRole("textbox", { name: "消息", exact: true }).fill(message);
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
}
async function catalog() {
  const answer = page.getByText(/^当前可用工具：/).last();
  await answer.waitFor();
  await completed();
  return (await answer.innerText()).replace("当前可用工具：", "").split("、");
}
try {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Changed-Catalog-Test!3");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await sendFresh("工具目录初始", "目录");
  let keys = await catalog();
  for (const key of ["calculate", "time_now", "memory_save", "knowledge_search", "knowledge_read"]) assert(keys.includes(key), key);
  assert(!keys.some(key => key.startsWith("artifact_") || key.startsWith("business_")));
  await page.screenshot({ path: join(output, "ready-catalog.png") });
  step("model receives only mounted and Identity-authorized tools");

  await control("disconnect_knowledge");
  await page.reload();
  await sendFresh("连接断开后的目录", "目录");
  keys = await catalog();
  assert(!keys.some(key => key.startsWith("knowledge_")));
  assert(keys.includes("time_now") && keys.includes("calculate"));
  await sendFresh("连接断开仍可计算", "计算");
  await page.getByText("计算已完成：0.30 元。", { exact: true }).waitFor({ timeout: 60000 });
  await completed();
  await page.locator('[data-tool-status="completed"]').filter({ hasText: "计算" }).waitFor();
  step("disconnected knowledge is omitted while a local tool still executes");

  await control("disable_calculate");
  await sendFresh("工具开关停用", "目录");
  keys = await catalog(); assert(!keys.includes("calculate")); assert(keys.includes("time_now"));
  await control("restore_connections");
  await control("revoke_calculate");
  await sendFresh("权限优先于连接", "目录");
  keys = await catalog(); assert(!keys.includes("calculate")); assert(keys.includes("knowledge_search"));
  await control("restore_catalog_permissions");
  step("tool switch and live Identity denial each filter the current catalog");

  await sendFresh("执行前连接变化", "执行前停用计算");
  await page.getByRole("status").filter({ hasText: /当前工具不可用：权限可能已被撤销/ }).first().waitFor();
  const paused = (await readServerState()).run;
  assert.equal(paused.status, "failed");
  assert.equal(await page.locator('[data-tool-status="completed"]').count(), 0);
  await page.screenshot({ path: join(output, "disabled-before-execution.png") });
  await control("restart_catalog_host");
  await page.reload();
  await page.getByRole("status").filter({ hasText: /当前工具不可用：权限可能已被撤销/ }).first().waitFor();
  await control("restore_connections");
  await page.getByRole("button", { name: "继续处理", exact: true }).click();
  await page.getByText("计算已完成：0.30 元。", { exact: true }).waitFor();
  const resumed = await completed();
  assert.equal(resumed.id, paused.id);
  assert.equal(resumed.attempt, paused.attempt + 1);
  assert.equal(await page.locator('[data-tool-status="completed"]').filter({ hasText: "计算" }).count(), 1);
  await page.locator('[data-tool-status="completed"] summary').click();
  await page.getByText("0.1+0.2 = 0.30 CNY", { exact: true }).waitFor();
  await page.screenshot({ path: join(output, "restart-and-resume.png") });
  step("disable after model selection prevents execution; restart and restore resume the original call");

  await control("availability_error");
  await sendFresh("连接检查故障恢复", "目录");
  await page.getByRole("status").filter({ hasText: "暂时无法检查工具连接状态，请稍后重试。" }).first().waitFor();
  assert(!(await page.locator("body").innerText()).includes("secret-catalog-fixture-credential"));
  await page.screenshot({ path: join(output, "connection-check-error.png") });
  await control("restore_connections");
  await page.getByRole("button", { name: "重新生成", exact: true }).click();
  keys = await catalog(); assert(keys.includes("knowledge_search") && keys.includes("calculate"));
  step("connection lookup errors are sanitized and recover through the existing UI");

  await sendFresh("恢复连接后真实调用", "检索");
  await page.getByText("已通过当前连接取得验收资料。", { exact: true }).waitFor();
  await completed();
  await page.locator('[data-tool-status="completed"]').filter({ hasText: "搜索知识库" }).waitFor();
  await control("restart_catalog_host");
  await page.reload();
  await page.getByText("已通过当前连接取得验收资料。", { exact: true }).waitFor();
  await page.screenshot({ path: join(output, "knowledge-restored.png") });
  step("restored connection executes the HTTP knowledge Connector and remains readable after host restart");

  await control("disconnect_knowledge");
  await page.reload();
  await page.getByRole("status").filter({ hasText: "相关内容已隐藏" }).first().waitFor();
  await page.screenshot({ path: join(output, "disconnected-history.png") });
  assert.equal(await page.getByText("已通过当前连接取得验收资料。", { exact: true }).count(), 0);
  await control("restore_connections");
  await page.reload();
  await page.getByText("已通过当前连接取得验收资料。", { exact: true }).waitFor();
  step("disconnect also prevents historical source revalidation and hides its reply until restored");

  assert.deepEqual(report.javascriptErrors, []);
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
} catch (error) {
  report.failure = String(error);
  report.lastServerState = await readServerState().catch(error => ({ diagnosticError: String(error) }));
  await page.screenshot({ path: join(output, "failure.png") }).catch(() => {});
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
  throw error;
} finally {
  await browser.close();
  await control("finish");
}
