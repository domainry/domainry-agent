import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = process.env.AGENT_UI_ORIGIN;
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-H08-web-command");
const phase = process.env.AGENT_UI_PHASE || "seed";
assert.ok(origin, "AGENT_UI_ORIGIN is required");
assert.ok(["seed", "verify"].includes(phase), "AGENT_UI_PHASE must be seed or verify");
await mkdir(output, { recursive: true });
const statePath = join(output, "state.json");
const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
page.setDefaultTimeout(45000);
const report = { phase, steps: [], javascriptErrors: [], consoleErrors: [] };
page.on("pageerror", error => report.javascriptErrors.push(String(error)));
page.on("console", message => { if (message.type() === "error") report.consoleErrors.push(message.text()); });
const step = value => { report.steps.push(value); console.log(`PASS ${value}`); };

async function login(password) {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}
async function api(path) {
  return page.evaluate(async path => {
    const session = await (await fetch("/app/session")).json();
    const response = await fetch(path, { headers: { "X-Agent-Scope": session.scope } });
    return { status: response.status, data: await response.json() };
  }, path);
}

try {
  const config = await (await fetch(`${origin}/app/config`)).json();
  assert.equal(config.runtime_id, "h08-command-runtime");
  assert.equal(config.workspace_id, "h08-command-workspace");
  if (phase === "seed") {
    await login("H08-Initial-Password!2");
    await page.getByRole("heading", { name: "设置你的新密码", exact: true }).waitFor();
    await page.getByLabel("初始密码", { exact: true }).fill("H08-Initial-Password!2");
    await page.getByLabel("新密码", { exact: true }).fill("H08-Changed-Password!3");
    await page.getByLabel("再次输入新密码", { exact: true }).fill("H08-Changed-Password!3");
    await page.getByRole("button", { name: "保存密码并进入", exact: true }).click();
    await page.getByRole("button", { name: "新建会话", exact: true }).waitFor();
    await page.locator(".model-name").filter({ hasText: "gpt-5.6-sol" }).waitFor();
    report.consoleErrors = [];
    step("the built domainry-agent-web executable serves the compiled UI, managed Identity and enabled real model");

    await page.getByRole("button", { name: "新建会话", exact: true }).click();
    const create = page.getByRole("dialog", { name: "新建会话", exact: true });
    await create.getByLabel("会话名称", { exact: true }).fill("H08 命令部署重启验收");
    await create.getByRole("button", { name: "保存", exact: true }).click();
    await create.waitFor({ state: "hidden" });
    await page.getByRole("textbox", { name: "消息", exact: true }).fill("只回复 H08-WEB-COMMAND-OK，不要调用工具。");
    await page.getByRole("button", { name: "发送消息", exact: true }).click();
    await page.getByText("H08-WEB-COMMAND-OK", { exact: true }).waitFor({ timeout: 120000 });
    await page.locator(".run-status").filter({ hasText: /^已保存$/ }).waitFor();
    const conversationID = new URL(page.url()).hash.slice(1);
    const messages = await api(`/agent/conversations/${conversationID}/messages`);
    assert.equal(messages.status, 200);
    assert.deepEqual(messages.data.items.map(item => item.role), ["user", "assistant"]);
    const state = { conversation_id: conversationID, message_ids: messages.data.items.map(item => item.id) };
    await writeFile(statePath, JSON.stringify(state, null, 2));
    report.state = state;
    await page.screenshot({ path: join(output, "before-process-restart.png"), fullPage: true });
    step("a browser request reaches the real Responses model and persists one user/assistant turn in SQLite");
  } else {
    const state = JSON.parse(await readFile(statePath, "utf8"));
    await login("H08-Changed-Password!3");
    await page.getByRole("button", { name: "新建会话", exact: true }).waitFor();
    report.consoleErrors = [];
    await page.goto(`${origin}/#${state.conversation_id}`);
    await page.getByText("H08-WEB-COMMAND-OK", { exact: true }).waitFor();
    const messages = await api(`/agent/conversations/${state.conversation_id}/messages`);
    assert.equal(messages.status, 200);
    assert.deepEqual(messages.data.items.map(item => item.id), state.message_ids);
    assert.deepEqual(messages.data.items.map(item => item.role), ["user", "assistant"]);
    await page.screenshot({ path: join(output, "after-process-restart.png"), fullPage: true });
    report.state = state;
    step("after terminating and restarting the executable, the same Identity account and exact message IDs remain visible");
  }
  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  report.complete = true;
} catch (error) {
  report.error = String(error);
  await page.screenshot({ path: join(output, `${phase}-failure.png`), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, `${phase}-report.json`), JSON.stringify(report, null, 2));
  await browser.close();
}
