import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = process.env.AGENT_UI_ORIGIN || "http://127.0.0.1:8092";
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-h09-task-page-close");
await mkdir(output, { recursive: true });
const config = await (await fetch(`${origin}/app/config`)).json();
assert.equal(config.workspace_id, "task-workspace", "Refuse a non-fixture deployment");

const browser = await chromium.launch({ headless: true, channel: "chrome" });
let context;
let page;
const report = { steps: [], javascriptErrors: [], preLoginConsoleErrors: [], consoleErrors: [], lifecycle: [], snapshots: [] };
async function openPage() {
  context = await browser.newContext({ viewport: { width: 1360, height: 1000 } });
  page = await context.newPage();
  page.setDefaultTimeout(60000);
  page.on("pageerror", error => report.javascriptErrors.push(String(error)));
  page.on("console", message => { if (message.type() === "error") report.consoleErrors.push(message.text()); });
}
const step = value => { report.steps.push(value); console.log(`PASS ${value}`); };
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`, { method: "POST" })).status, 204, name);
const dialog = () => page.getByRole("dialog", { name: "后台任务", exact: true });
const taskNavigation = () => page.getByRole("button", { name: "后台任务 进度、等待与成果", exact: true });
async function login() {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Changed-Task-Start!3");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await taskNavigation().waitFor();
  await page.waitForTimeout(500);
  report.preLoginConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];
}
async function openTasks() {
  await taskNavigation().click();
  await dialog().waitFor();
  await dialog().locator(".task-list").waitFor();
}
async function selectGoal(goal) {
  const row = dialog().locator(".task-list").getByRole("button").filter({ hasText: goal });
  assert.equal(await row.count(), 1, goal);
  await row.click();
  const detail = dialog().getByRole("region", { name: "后台任务详情", exact: true });
  await detail.getByRole("heading", { name: goal, exact: true }).waitFor();
  return detail;
}

try {
  await openPage();
  await login();
  await page.getByRole("button", { name: "新建会话", exact: true }).click();
  const create = page.getByRole("dialog", { name: "新建会话", exact: true });
  await create.getByLabel("会话名称", { exact: true }).fill("H09 页面关闭任务恢复");
  await create.getByRole("button", { name: "保存", exact: true }).click();
  await create.waitFor({ state: "hidden" });
  await page.locator(".scope-settings > summary").click();
  await page.getByRole("checkbox", { name: "允许本次请求创建独立的后台任务", exact: true }).check();
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("请创建一个关闭网页后继续完成的后台任务");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await page.getByText(/^后台任务已受理：task_[a-f0-9]{32}$/).waitFor();

  await openTasks();
  const running = await selectGoal("关闭网页继续任务");
  await running.getByText("正在处理", { exact: true }).waitFor();
  const taskID = (await running.locator(".task-detail-heading small").innerText()).trim();
  assert.match(taskID, /^task_[a-f0-9]{32}$/);
  assert.ok((await running.innerText()).includes("0 次工具调用"));
  await page.screenshot({ path: join(output, "task-running-before-page-close.png"), fullPage: true });
  report.snapshots.push({ phase: "before_close", task_id: taskID, text: await running.innerText() });
  step("the accepted background task is running with a durable task ID before the page closes");

  report.lifecycle.push({ event: "browser_context_closed", task_id: taskID, at: new Date().toISOString() });
  await context.close();
  await control("release-browser-task");
  report.lifecycle.push({ event: "server_worker_released_without_page", task_id: taskID, at: new Date().toISOString() });

  await openPage();
  await login();
  await openTasks();
  let completed;
  for (const deadline = Date.now() + 60000; Date.now() < deadline;) {
    completed = await selectGoal("关闭网页继续任务");
    const value = await completed.innerText();
    if (value.includes("网页关闭期间任务已继续并完成。") && value.includes("已完成")) break;
    await dialog().getByRole("button", { name: "刷新任务", exact: true }).click();
    await page.waitForTimeout(250);
  }
  assert.ok(completed);
  const completedText = await completed.innerText();
  assert.ok(completedText.includes("已完成"));
  assert.ok(completedText.includes("1 次工具调用"));
  assert.ok(completedText.includes("网页关闭期间任务已继续并完成。"));
  assert.equal((await completed.locator(".task-detail-heading small").innerText()).trim(), taskID);
  await page.screenshot({ path: join(output, "task-completed-after-page-reopen.png"), fullPage: true });
  report.snapshots.push({ phase: "after_reopen", task_id: taskID, text: completedText });
  report.lifecycle.push({ event: "new_browser_context_read_completed_task", task_id: taskID, at: new Date().toISOString() });
  step("after the original browser context is closed, the server worker completes and a new login reads the same task ID, progress and result");

  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  report.complete = true;
} catch (error) {
  report.error = String(error);
  if (page && !page.isClosed()) await page.screenshot({ path: join(output, "failure.png"), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
  await fetch(`${origin}/__acceptance/finish`, { method: "POST" }).catch(() => {});
  if (context && typeof context.close === "function") await context.close().catch(() => {});
  await browser.close();
}
