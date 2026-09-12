import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = "http://127.0.0.1:8092";
const config = await (await fetch(`${origin}/app/config`)).json();
assert.ok(["outcome-workspace", "external-outcome-workspace"].includes(config.workspace_id), "Refuse non-fixture deployment");
const external = config.workspace_id === "external-outcome-workspace";
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || `/tmp/domainry-E05-browser-${external ? "external" : "local"}`);
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(30000);
const report = { steps: [], javascriptErrors: [], snapshots: [] };
page.on("pageerror", error => report.javascriptErrors.push(String(error)));
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`, { method: "POST" })).status, 204, name);
const step = name => { report.steps.push(name); console.log(`PASS ${name}`); };
const state = () => page.evaluate(async ({ external }) => {
  const session = await (await fetch("/app/session")).json();
  const id = location.hash.slice(1), base = `/agent/conversations/${id}`;
  const read = async path => { const r = await fetch(path, { headers: { "X-Agent-Scope": session.scope } }); if (!r.ok) throw new Error(`read ${path}: ${r.status}`); return r.json(); };
  const conversation = await read(base), messages = await read(`${base}/messages`);
  const runID = conversation.active_run_id || messages.items.at(-1).run_id;
  return { conversation, messages, run: await read(`${base}/runs/${runID}`), todos: external ? null : await read(`/agent/todos?source_conversation_id=${id}`) };
}, { external });
async function waitStatus(status) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    const value = await state();
    if (value.run.status === status) return value;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`did not reach ${status}: ${JSON.stringify(await state())}`);
}
async function create(title, message) {
  await page.getByRole("button", { name: "新建会话", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "新建会话", exact: true });
  await dialog.getByLabel("会话名称", { exact: true }).fill(title);
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  await page.getByRole("textbox", { name: "消息", exact: true }).fill(message);
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await page.locator(".interaction-card").waitFor();
  return waitStatus("waiting_confirmation");
}
const outcome = () => page.getByRole("region", { name: "本次实际结果", exact: true });
try {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Changed-Outcome-Test!3");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("button", { name: "新建会话", exact: true }).waitFor();
  if (!external) {
    await create("一项成功一项失败", "创建这两项待办");
    await outcome().getByText("等待操作确认", { exact: true }).waitFor();
    await page.getByRole("button", { name: "授权并执行这 2 项", exact: true }).click();
    const partial = await waitStatus("completed");
    assert.equal(partial.todos.items.length, 1);
    await outcome().getByText("部分完成", { exact: true }).waitFor();
    assert.match(await outcome().innerText(), /已完成 1 项.*失败 1 项/);
    await page.reload();
    await outcome().getByText("部分完成", { exact: true }).waitFor();
    await page.screenshot({ path: join(output, "partial-failure.png") });
    step("saved reply and page reload show one real success and one definite failure");

    const scope = page.getByLabel("允许本次请求创建、修改或删除我的个人待办", { exact: true });
    await page.locator(".scope-settings > summary").click();
    await scope.check();
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "准备修复这项失败", exact: true }).click();
    const input = page.getByRole("textbox", { name: "消息", exact: true });
    assert.match(await input.inputValue(), new RegExp(partial.run.id));
    assert.match(await input.inputValue(), /调用：outcome-bad/);
    assert.equal(await scope.isChecked(), false, "repair request cannot retain broad write consent");
    await page.getByRole("button", { name: "发送消息", exact: true }).click();
    const repairing = await waitStatus("waiting_confirmation");
    assert.notEqual(repairing.run.id, partial.run.id);
    assert.equal(repairing.run.interaction.call_id, "repair-bad");
    assert.equal(repairing.run.steps[0].calls[0].name, "execution_read");
    assert.equal(repairing.run.steps[0].calls[0].status, "completed");
    assert.equal(repairing.todos.items.length, 1);
    await page.getByRole("button", { name: "确认执行", exact: true }).click();
    const fixed = await waitStatus("completed");
    assert.equal(fixed.todos.items.length, 2);
    assert.equal(fixed.todos.items.filter(item => item.title === "核对合同付款条件").length, 1);
    await input.fill("保留我尚未发送的草稿");
    await page.getByRole("button", { name: "查看处理记录", exact: true }).first().click();
    const history = page.getByRole("dialog", { name: "处理记录", exact: true });
    await history.getByRole("region", { name: "本次实际结果", exact: true }).getByText("部分完成", { exact: true }).waitFor();
    assert.equal(await history.getByRole("button", { name: "准备修复这项失败", exact: true }).isDisabled(), true);
    await history.getByRole("button", { name: "刷新记录", exact: true }).click();
    await history.getByRole("region", { name: "本次实际结果", exact: true }).getByText("部分完成", { exact: true }).waitFor();
    await page.keyboard.press("Escape");
    assert.equal(await input.inputValue(), "保留我尚未发送的草稿");
    await input.fill("");
    report.snapshots.push({ label: "partial and explicit repair", partial, fixed });
    step("repair reads the original failure, asks for new consent and creates only the missing todo; old history and user draft remain intact");

    await create("后续回复失败并恢复", "创建待办后回复中断");
    await page.getByRole("button", { name: "确认执行", exact: true }).click();
    const failed = await waitStatus("failed");
    assert.equal(failed.todos.items.length, 1);
    await outcome().getByText("部分完成，后续处理失败", { exact: true }).waitFor();
    await control("restart_outcome_host");
    await page.reload();
    await page.getByRole("button", { name: "从第 2 步继续", exact: true }).waitFor();
    await page.getByRole("button", { name: "从第 2 步继续", exact: true }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: join(output, "resume-specific-step.png") });
    await page.getByRole("button", { name: "查看处理记录", exact: true }).first().click();
    const detail = page.getByRole("dialog", { name: "处理记录", exact: true });
    await detail.getByRole("button", { name: "从第 2 步继续", exact: true }).click();
    await detail.waitFor({ state: "hidden" });
    const resumed = await waitStatus("completed");
    assert.equal(resumed.run.id, failed.run.id);
    assert.equal(resumed.run.attempt, failed.run.attempt + 1);
    assert.equal(resumed.todos.items.length, 1);
    assert.equal(resumed.todos.items[0].id, failed.todos.items[0].id);
    await outcome().getByText("工具调用已完成", { exact: true }).waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: join(output, "resumed-mobile.png") });
    report.snapshots.push({ label: "specific model step recovery", failed, resumed });
    step("after full host restart the history action resumes the exact unfinished model step in the same run without repeating its write");
  } else {
    await create("外部结果丢失后核查", "创建两个外部记录");
    await page.getByRole("button", { name: "授权并执行这 2 项", exact: true }).click();
    const unknown = await waitStatus("needs_reconciliation");
    await outcome().getByText("部分完成，仍有结果待核查", { exact: true }).waitFor();
    assert.match(await outcome().innerText(), /已完成 1 项.*待核查 1 项/);
    assert.equal(await page.getByRole("button", { name: "准备修复这项失败", exact: true }).count(), 0);
    await control("restart_outcome_host");
    await page.reload();
    await outcome().getByText("部分完成，仍有结果待核查", { exact: true }).waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: join(output, "external-unknown-mobile.png") });
    await page.locator('.execution-tool button[data-recovery-kind="reconcile"]').click();
    const done = await waitStatus("completed");
    assert.equal(done.run.id, unknown.run.id);
    assert.equal(done.run.attempt, unknown.run.attempt + 1);
    assert.equal(done.run.steps[0].calls[0].resource_id, unknown.run.steps[0].calls[0].resource_id);
    assert.equal(done.run.steps[0].calls[1].status, "completed");
    assert.ok(done.run.steps[0].calls[1].resource_id);
    await outcome().getByText("工具调用已完成", { exact: true }).waitFor();
    await page.reload();
    await outcome().getByText("工具调用已完成", { exact: true }).waitFor();
    report.snapshots.push({ label: "external transport lost response", unknown, done });
    step("two external writes survive host restart; unknown result is reconciled from the specific call with the original key and no new POST");
  }
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
