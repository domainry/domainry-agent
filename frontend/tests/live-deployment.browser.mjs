import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = "http://127.0.0.1:8092";
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-H08-live-browser");
await mkdir(output, { recursive: true });

const deployment = await (await fetch(`${origin}/app/config`)).json();
assert.equal(deployment.workspace_id, "live-work-workspace", "Refuse a non-live acceptance deployment");

const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, acceptDownloads: true });
const page = await context.newPage();
page.setDefaultTimeout(45000);
const report = { deployment: { model: "gpt-5.6-sol", workspace_id: deployment.workspace_id }, steps: [], javascriptErrors: [], preLoginConsoleErrors: [], consoleErrors: [], snapshots: [] };
page.on("pageerror", error => report.javascriptErrors.push(String(error)));
page.on("console", message => { if (message.type() === "error") report.consoleErrors.push(message.text()); });
const step = value => { report.steps.push(value); console.log(`PASS ${value}`); };
const finish = () => fetch(`${origin}/__acceptance/finish`, { method: "POST" }).catch(() => {});
const api = async path => page.evaluate(async path => {
  const session = await (await fetch("/app/session")).json();
  const response = await fetch(path, { headers: { "X-Agent-Scope": session.scope } });
  return { status: response.status, data: await response.json() };
}, path);

try {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Changed-Live-Work-Test!3");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("button", { name: "新建会话", exact: true }).waitFor();
  await page.locator(".model-name").filter({ hasText: "gpt-5.6-sol" }).waitFor();
  report.preLoginConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];
  step("compiled product is logged into the real Identity host and shows the enabled gpt-5.6-sol model");

  const todosResponse = await api("/agent/todos?limit=20");
  assert.equal(todosResponse.status, 200);
  assert.equal(todosResponse.data.items.length, 3);
  const ordered = [...todosResponse.data.items].sort((a, b) => a.position - b.position);
  assert.deepEqual(ordered.map(item => item.title), ["整理访谈记录", "核对部门费用", "提交发布周报"]);
  assert.equal(ordered[1].status, "completed");
  assert.match(ordered[1].due_date, /^\d{4}-\d{2}-\d{2}$/);
  assert.equal(ordered[0].status, "open");
  assert.equal(ordered[2].status, "open");
  await page.getByRole("button", { name: /^个人待办/ }).click();
  const todoDialog = page.getByRole("dialog", { name: "个人待办", exact: true });
  await todoDialog.getByText("第 2 项 · 核对部门费用", { exact: true }).waitFor();
  assert.equal(await todoDialog.getByLabel("完成事项 核对部门费用", { exact: true }).isChecked(), true);
  await page.screenshot({ path: join(output, "persisted-todos.png"), fullPage: true });
  await page.keyboard.press("Escape");
  step("three ordered todos survive the full host reopen; only the second is completed and its exact due date remains stored");

  await page.getByRole("button", { name: /^个人记忆/ }).click();
  const memoryDialog = page.getByRole("dialog", { name: "个人记忆", exact: true });
  await memoryDialog.getByText("下周计划", { exact: false }).waitFor();
  assert.ok((await memoryDialog.innerText()).includes("本周进展"));
  assert.ok((await memoryDialog.innerText()).includes("风险"));
  await page.keyboard.press("Escape");
  step("the weekly-report preference is rendered from persisted personal memory after restart");

  await page.getByRole("button", { name: /^我的成果/ }).click();
  const artifactDialog = page.getByRole("dialog", { name: "我的成果", exact: true });
  await artifactDialog.getByRole("button", { name: /青禾验收周报/ }).click();
  await artifactDialog.getByRole("region", { name: "成果内容", exact: true }).waitFor();
  await artifactDialog.getByText("当前显示版本 2", { exact: true }).waitFor();
  assert.ok((await artifactDialog.innerText()).includes("费用已核对，暂无新增风险。"));
  await artifactDialog.getByLabel("成果版本", { exact: true }).selectOption("1");
  await artifactDialog.getByText("当前显示版本 1", { exact: true }).waitFor();
  const original = await artifactDialog.getByRole("region", { name: "成果内容", exact: true }).innerText();
  assert.ok(original.includes("核对部门费用"));
  assert.ok(original.includes("未完成"));
  const downloadEvent = page.waitForEvent("download");
  await artifactDialog.getByRole("button", { name: "下载此版本 Markdown", exact: true }).click();
  const download = await downloadEvent;
  const downloadPath = join(output, "qinghe-report-v1.md");
  await download.saveAs(downloadPath);
  const downloaded = await readFile(downloadPath, "utf8");
  assert.ok(downloaded.includes("## 本周进展"));
  assert.ok(downloaded.includes("核对部门费用"));
  assert.ok(downloaded.includes("未完成"));
  await page.screenshot({ path: join(output, "artifact-version-1.png"), fullPage: true });
  await page.keyboard.press("Escape");
  step("the UI opens version 2, switches to immutable version 1 and downloads Markdown containing the stored original facts");

  await page.getByRole("button", { name: "新建会话", exact: true }).click();
  const create = page.getByRole("dialog", { name: "新建会话", exact: true });
  await create.getByLabel("会话名称", { exact: true }).fill("H08 实际部署模型核验");
  await create.getByRole("button", { name: "保存", exact: true }).click();
  await create.waitFor({ state: "hidden" });
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("这是 H08 实际部署验收。只回复标记 H08-LIVE-OK，不要调用工具。 ");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await page.getByText("H08-LIVE-OK", { exact: true }).waitFor({ timeout: 120000 });
  await page.locator(".run-status").filter({ hasText: /^已保存$/ }).waitFor();
  const conversationID = new URL(page.url()).hash.slice(1);
  const beforeReload = await api(`/agent/conversations/${conversationID}/messages`);
  assert.equal(beforeReload.status, 200);
  assert.equal(beforeReload.data.items.filter(item => item.role === "assistant").length, 1);
  await page.reload();
  await page.getByText("H08-LIVE-OK", { exact: true }).waitFor();
  const afterReload = await api(`/agent/conversations/${conversationID}/messages`);
  assert.deepEqual(afterReload.data.items.map(item => item.id), beforeReload.data.items.map(item => item.id));
  report.snapshots.push({ conversation_id: conversationID, message_ids: afterReload.data.items.map(item => item.id), roles: afterReload.data.items.map(item => item.role) });
  await page.screenshot({ path: join(output, "live-model-reloaded.png"), fullPage: true });
  step("a browser-submitted request reaches the enabled real model through Responses and its single reply survives reload");

  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  report.complete = true;
} catch (error) {
  report.error = String(error);
  await page.screenshot({ path: join(output, "failure.png"), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
  await finish();
  await browser.close();
}
