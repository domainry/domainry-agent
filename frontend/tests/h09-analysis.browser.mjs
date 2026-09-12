import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = process.env.AGENT_UI_ORIGIN || "http://127.0.0.1:8092";
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-h09-analysis");
await mkdir(output, { recursive: true });
const config = await (await fetch(`${origin}/app/config`)).json();
assert.equal(config.workspace_id, "analysis-result-workspace", "Refuse a non-fixture deployment");

const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1360, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(60000);
const report = { steps: [], javascriptErrors: [], preLoginConsoleErrors: [], expectedPermissionConsoleErrors: [], consoleErrors: [], httpErrors: [], snapshots: [] };
page.on("pageerror", error => report.javascriptErrors.push(String(error)));
page.on("console", message => { if (message.type() === "error") report.consoleErrors.push(message.text()); });
page.on("response", response => { if (response.status() >= 400) report.httpErrors.push({ status: response.status(), path: new URL(response.url()).pathname }); });
const step = value => { report.steps.push(value); console.log(`PASS ${value}`); };
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`, { method: "POST" })).status, 204, name);
const api = path => page.evaluate(async path => {
  const session = await (await fetch("/app/session")).json();
  const response = await fetch(path, { headers: { "X-Agent-Scope": session.scope } });
  const text = await response.text();
  return { status: response.status, data: text ? JSON.parse(text) : null };
}, path);

try {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Changed-Analysis-Result-Test!3");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("button", { name: "新建会话", exact: true }).waitFor();
  await page.waitForTimeout(500);
  report.preLoginConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];

  const conversations = await api("/agent/conversations?limit=20");
  assert.equal(conversations.status, 200);
  assert.equal(conversations.data.items.length, 1);
  const conversationID = conversations.data.items[0].id;
  await page.goto(`${origin}/#${conversationID}`);
  await page.getByText("分析图表已保存并生成受控 CSV 下载。", { exact: true }).waitFor();
  await page.getByRole("button", { name: "查看处理记录", exact: true }).last().click();
  const dialog = page.getByRole("dialog", { name: "处理记录", exact: true });
  const analysis = dialog.locator("details.execution-tool").filter({ hasText: "分析数据" });
  await analysis.locator("summary").click();
  await analysis.getByText("执行结果（节选）", { exact: true }).waitFor();
  await analysis.getByRole("button", { name: "查看完整结果", exact: true }).waitFor();

  const messages = await api(`/agent/conversations/${conversationID}/messages`);
  const runID = [...messages.data.items].reverse().find(item => item.run_id)?.run_id;
  const storedRun = await api(`/agent/conversations/${conversationID}/runs/${runID}`);
  const call = storedRun.data.steps.flatMap(value => value.calls || []).find(value => value.name === "analysis_run");
  assert.equal(storedRun.status, 200);
  assert.equal(storedRun.data.status, "completed");
  assert.equal(call.result_truncated, true);
  assert.equal(call.result_reference.call_id, "analysis-e2e");
  step("the persisted analysis call exposes only a bounded preview and a stable full-result reference");

  await analysis.getByRole("button", { name: "查看完整结果", exact: true }).click();
  const complete = analysis.getByRole("region", { name: "结构化分析结果", exact: true });
  await complete.waitFor();
  for (const value of ["完整：是", "截断：否", "80 / 最多 100 行", "80 行 · 2 列 · 数值保留原始精度", "缺失声明：total/null_value × 1；total/source_null_inputs × 1"]) {
    await complete.getByText(value, { exact: true }).waitFor();
  }
  await complete.getByRole("img", { name: "柱状图，精确数据见下表", exact: true }).waitFor();
  await page.screenshot({ path: join(output, "analysis-full-result.png"), fullPage: true });
  step("the browser reassembles all 80 rows and shows complete coverage, no source truncation, chart, precision and missing-value declarations");

  await control("revoke-analysis");
  await analysis.getByRole("button", { name: "收起完整结果", exact: true }).click();
  await analysis.getByRole("button", { name: "查看完整结果", exact: true }).click();
  await analysis.getByRole("alert").waitFor();
  const denied = await page.evaluate(async reference => {
    const session = await (await fetch("/app/session")).json();
    const response = await fetch(`/agent/conversations/${reference.conversation_id}/runs/${reference.run_id}/result`, {
      method: "POST", headers: { "Content-Type": "application/json", "X-Agent-Scope": session.scope },
      body: JSON.stringify({ reference, offset: 0, max_bytes: 8192 }),
    });
    return response.status;
  }, call.result_reference);
  assert.equal(denied, 403);
  await page.waitForTimeout(300);
  report.expectedPermissionConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];
  await page.screenshot({ path: join(output, "analysis-permission-revoked.png"), fullPage: true });
  step("the complete result is reauthorized on every read and current source revocation blocks both the UI and direct HTTP read");
  report.snapshots.push({ conversation_id: conversationID, run_id: runID, call_id: call.id, reference: call.result_reference, returned_rows: 80, requested_max_rows: 100 });

  await control("restore-analysis");
  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  report.complete = true;
} catch (error) {
  report.error = String(error);
  await page.screenshot({ path: join(output, "failure.png"), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
  await fetch(`${origin}/__acceptance/finish`, { method: "POST" }).catch(() => {});
  await browser.close();
}
