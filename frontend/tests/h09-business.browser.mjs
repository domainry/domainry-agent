import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = process.env.AGENT_UI_ORIGIN || "http://127.0.0.1:8093";
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-h09-business");
const scenario = process.env.AGENT_H09_SCENARIO;
const seededConversationID = process.env.AGENT_UI_CONVERSATION || "";
assert.ok(["relations", "workflows"].includes(scenario), "AGENT_H09_SCENARIO must be relations or workflows");
await mkdir(output, { recursive: true });

const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1360, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(60000);
const report = { scenario, steps: [], javascriptErrors: [], preLoginConsoleErrors: [], expectedTransitionConsoleErrors: [], consoleErrors: [], httpErrors: [], snapshots: [] };
page.on("pageerror", error => report.javascriptErrors.push(String(error)));
page.on("console", message => { if (message.type() === "error") report.consoleErrors.push(message.text()); });
page.on("response", response => { if (response.status() >= 400) report.httpErrors.push({ status: response.status(), path: new URL(response.url()).pathname }); });
const step = value => { report.steps.push(value); console.log(`PASS ${value}`); };
const control = async name => {
  const response = await fetch(`${origin}/__acceptance/${name}`, { method: "POST" });
  assert.ok(response.ok, `${name}: ${response.status}`);
  if ((response.headers.get("content-type") || "").includes("json")) return response.json();
};
const api = async path => page.evaluate(async path => {
  const session = await (await fetch("/app/session")).json();
  const response = await fetch(path, { headers: { "X-Agent-Scope": session.scope } });
  return { status: response.status, data: await response.json() };
}, path);

async function login() {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Business-Browser-Changed!2026");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("button", { name: "新建会话", exact: true }).waitFor();
  await page.waitForTimeout(1000);
  report.preLoginConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];
}
async function createConversation(title) {
  await page.getByRole("button", { name: "新建会话", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "新建会话", exact: true });
  await dialog.getByLabel("会话名称", { exact: true }).fill(title);
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  return new URL(page.url()).hash.slice(1);
}
async function send(message) {
  await page.getByRole("textbox", { name: "消息", exact: true }).fill(message);
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
}
async function waitSaved() {
  await page.locator(".run-status").filter({ hasText: /^已保存$/ }).waitFor({ timeout: 120000 });
}
async function latestRun(conversationID) {
  const messages = await api(`/agent/conversations/${conversationID}/messages`);
  assert.equal(messages.status, 200);
  const runID = [...messages.data.items].reverse().find(message => message.run_id)?.run_id;
  assert.ok(runID);
  const run = await api(`/agent/conversations/${conversationID}/runs/${runID}`);
  assert.equal(run.status, 200);
  return run.data;
}
async function waitForNewTerminalRun(conversationID, previousRunID) {
  const deadline = Date.now() + 120000;
  while (Date.now() < deadline) {
    const run = await latestRun(conversationID);
    if (run.id !== previousRunID && ["completed", "failed", "cancelled"].includes(run.status)) return run;
    await page.waitForTimeout(200);
  }
  throw new Error(`new terminal run did not replace ${previousRunID}`);
}
async function openDetails(locator) {
  await locator.waitFor();
  for (let attempt = 0; attempt < 2; attempt++) {
    if (await locator.evaluate(element => element.open)) return;
    await locator.locator("summary").click();
    await page.waitForTimeout(150);
  }
  assert.equal(await locator.evaluate(element => element.open), true);
}

try {
  await login();
  if (scenario === "relations") {
    const conversationID = await createConversation("H09 目录与关联权限截图");
    await send("请发现业务对象和关系，查找 Acme 客户。通过关联查询工具按金额升序、每页一条查完它的订单，再从第一条订单查对应项目，最后从项目查回客户。列出订单名称和金额、项目名称、返回的客户名称。所有关系键和记录 ID 都必须从工具实际结果取得。");
    await page.getByText("Order Alpha", { exact: false }).last().waitFor({ timeout: 120000 });
    await waitSaved();
    const run = await latestRun(conversationID);
    const calls = run.steps.flatMap(value => value.calls || []);
    assert.ok(calls.filter(call => call.name === "business_catalog" && call.status === "completed").length >= 2);
    assert.ok(calls.filter(call => call.name === "query_related_records" && call.status === "completed").length >= 4);
    const related = page.locator("details.execution-tool").filter({ hasText: "查询关联记录" });
    assert.ok(await related.count() >= 4);
    await related.nth(1).locator("summary").click();
    await related.nth(1).getByText("Order Beta", { exact: false }).waitFor();
    assert.ok((await page.locator("main").innerText()).includes("Delivery Project"));
    assert.ok(!(await page.locator("main").innerText()).includes("PRIVATE-ORDER"));
    await page.screenshot({ path: join(output, "relations-authorized.png"), fullPage: true });
    step("business catalog publishes real relation keys and the authorized traversal shows two orders, project and customer without the other owner's row");

    await control("restrict-fields");
    await page.reload();
    await page.getByRole("status").filter({ hasText: "相关内容已隐藏" }).first().waitFor();
    assert.equal(await page.getByText("Order Alpha", { exact: false }).count(), 0);
    await page.screenshot({ path: join(output, "relations-field-revoked.png"), fullPage: true });
    step("revoking the relation field hides the saved answer and tool evidence after a full page reload");

    await control("restore");
    await control("restart");
    await page.reload();
    await page.getByText("Order Alpha", { exact: false }).last().waitFor();
    await page.waitForTimeout(1000);
    report.expectedTransitionConsoleErrors.push(...report.consoleErrors);
    report.consoleErrors = [];
    await page.screenshot({ path: join(output, "relations-restored-after-host-restart.png"), fullPage: true });
    step("restoring the role and reopening Runtime, Identity and Agent restores the same authorized relation result");
    report.snapshots.push({ conversation_id: conversationID, run_id: run.id, relation_calls: calls.filter(call => call.name === "query_related_records").length });
  } else {
    assert.match(seededConversationID, /^conv_[a-f0-9]{32}$/);
    const conversationID = seededConversationID;
    await page.goto(`${origin}/#${conversationID}`);
    await page.getByRole("textbox", { name: "消息", exact: true }).waitFor();
    await send("验收：启动流程");
    const confirmation = page.getByRole("region", { name: "操作确认", exact: true });
    await confirmation.getByText("启动流程：agent_review", { exact: true }).waitFor({ timeout: 120000 });
    await confirmation.getByRole("button", { name: "确认执行", exact: true }).click();
    await waitSaved();
    const startDetails = page.locator("details.execution-tool").filter({ hasText: "启动业务流程" }).last();
    const progressDetails = page.locator("details.execution-tool").filter({ hasText: "查询流程进度" }).last();
    await openDetails(startDetails);
    await openDetails(progressDetails);
    await startDetails.getByText("流程启动已受理", { exact: true }).waitFor();
    await progressDetails.getByText("流程尚未结束", { exact: false }).waitFor();
    const accepted = await latestRun(conversationID);
    const calls = accepted.steps.flatMap(value => value.calls || []);
    const start = calls.find(call => call.name === "workflow_start" && call.status === "completed");
    const waiting = [...calls].reverse().find(call => call.name === "workflow_get" && call.status === "completed");
    assert.ok(start && waiting);
    const startEvidence = JSON.parse(start.result_preview);
    const receipt = typeof startEvidence.data === "string" ? JSON.parse(startEvidence.data) : startEvidence.data;
    assert.equal(start.completion, "accepted");
    assert.equal(receipt.status, "accepted");
    await page.screenshot({ path: join(output, "workflow-accepted-waiting.png"), fullPage: true });
    step("the confirmed start is displayed as accepted and a separate progress read shows the actual workflow is still waiting");

    const approved = await control("approve-workflow");
    assert.equal(approved.process_id, receipt.process_id);
    await send(`查询流程 ${receipt.process_id}`);
    const completed = await waitForNewTerminalRun(conversationID, accepted.id);
    const completedDetails = page.locator("details.execution-tool").filter({ hasText: "查询流程进度" }).last();
    await completedDetails.getByText(/流程已结束 · 审批通过/).waitFor({ state: "attached" });
    await openDetails(completedDetails);
    await completedDetails.getByText(/流程已结束 · 审批通过/).waitFor();
    assert.ok(completed.steps.flatMap(value => value.calls || []).some(call => call.name === "workflow_get" && call.status === "completed"));
    assert.ok(!completed.steps.flatMap(value => value.calls || []).some(call => call.name === "workflow_start"));
    await page.screenshot({ path: join(output, "workflow-completed-approved.png"), fullPage: true });
    step("after the Runtime approval, a progress-only request shows the same process as terminal and approved without starting another workflow");

    await send("验收：启动流程");
    const rejected = page.getByRole("region", { name: "操作确认", exact: true });
    await rejected.getByText("启动流程：agent_review", { exact: true }).waitFor();
    await rejected.getByRole("button", { name: "拒绝执行", exact: true }).click();
    await page.locator(".run-status").filter({ hasText: /^已停止$/ }).waitFor();
    step("rejecting a later confirmation persists a stopped run and creates no additional process");
    report.snapshots.push({ conversation_id: conversationID, accepted_run_id: accepted.id, completed_run_id: completed.id, process_id: receipt.process_id });
  }
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
