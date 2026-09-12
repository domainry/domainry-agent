import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = process.env.AGENT_UI_ORIGIN || "http://127.0.0.1:8092";
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-h09-artifacts");
await mkdir(output, { recursive: true });
const config = await (await fetch(`${origin}/app/config`)).json();
assert.equal(config.workspace_id, "artifact-tools-workspace", "Refuse a non-fixture deployment");

const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1360, height: 1000 }, acceptDownloads: true });
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
const nav = async name => page.getByRole("button", { name }).click();

try {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Changed-Artifact-Tool-Test!3");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("button", { name: "新建会话", exact: true }).waitFor();
  await page.waitForTimeout(500);
  report.preLoginConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];

  const artifacts = await api("/agent/artifacts?limit=20");
  assert.equal(artifacts.status, 200);
  const reportArtifact = artifacts.data.items.find(item => item.title === "验收周报");
  assert.ok(reportArtifact);
  assert.equal(reportArtifact.version, 2);
  await nav(/^我的成果/);
  const dialog = page.getByRole("dialog", { name: "我的成果", exact: true });
  await dialog.getByRole("button", { name: /验收周报/ }).click();
  const content = dialog.getByRole("region", { name: "成果内容", exact: true });
  await content.waitFor();
  await dialog.getByText("当前显示版本 2", { exact: true }).waitFor();
  assert.ok((await content.innerText()).includes("已核对各部门费用，统计口径保持一致。"));

  await dialog.getByLabel("成果版本", { exact: true }).selectOption("1");
  await dialog.getByText("当前显示版本 1", { exact: true }).waitFor();
  const versionOne = await content.innerText();
  assert.ok(versionOne.includes("待核对。"));
  assert.ok(!versionOne.includes("已核对各部门费用"));
  const firstDownloadPromise = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "下载此版本 Markdown", exact: true }).click();
  const firstDownload = await firstDownloadPromise;
  const firstPath = join(output, "artifact-v1.md");
  await firstDownload.saveAs(firstPath);
  const firstBytes = await readFile(firstPath, "utf8");
  assert.equal(firstBytes, "# 周报\n\n## 第一节：本周进展\n完成需求访谈，整理项目事项。\n\n## 第二节：费用核对\n待核对。\n\n## 第三节：下周计划\n提交修订稿。\n");
  await page.screenshot({ path: join(output, "artifact-version-1.png"), fullPage: true });
  step("the version selector renders version 1 exactly and its controlled download preserves the selected version bytes");

  await dialog.getByLabel("成果版本", { exact: true }).selectOption("2");
  await dialog.getByText("当前显示版本 2", { exact: true }).waitFor();
  assert.ok((await content.innerText()).includes("已核对各部门费用，统计口径保持一致。"));
  await control("revoke-artifact-export");
  await dialog.getByRole("button", { name: "下载此版本 Markdown", exact: true }).click();
  await dialog.getByRole("alert").waitFor();
  const directDenied = await page.evaluate(async id => {
    const session = await (await fetch("/app/session")).json();
    const response = await fetch(`/agent/artifacts/${id}/exports`, {
      method: "POST", headers: { "Content-Type": "application/json", "X-Agent-Scope": session.scope },
      body: JSON.stringify({ client_id: crypto.randomUUID(), version: 2, format: "markdown" }),
    });
    return response.status;
  }, reportArtifact.id);
  assert.equal(directDenied, 403);
  await page.waitForTimeout(300);
  report.expectedPermissionConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];
  await page.screenshot({ path: join(output, "artifact-download-revoked.png"), fullPage: true });
  step("current Identity export revocation blocks both the download button action and direct export HTTP while read access remains");

  await control("restore-artifacts");
  await dialog.getByRole("button", { name: /验收周报/ }).click();
  await content.waitFor();
  await dialog.getByLabel("成果版本", { exact: true }).selectOption("2");
  await dialog.getByText("当前显示版本 2", { exact: true }).waitFor();
  const secondDownloadPromise = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "下载此版本 Markdown", exact: true }).click();
  const secondDownload = await secondDownloadPromise;
  const secondPath = join(output, "artifact-v2.md");
  await secondDownload.saveAs(secondPath);
  assert.ok((await readFile(secondPath, "utf8")).includes("已核对各部门费用，统计口径保持一致。"));
  step("restoring export permission downloads version 2 without changing either saved version");

  await control("revoke-artifact-read");
  await dialog.getByRole("button", { name: "刷新成果", exact: true }).click();
  await dialog.getByRole("alert").waitFor();
  assert.equal((await api(`/agent/artifacts/${reportArtifact.id}`)).status, 403);
  await page.waitForTimeout(300);
  report.expectedPermissionConsoleErrors.push(...report.consoleErrors);
  report.consoleErrors = [];
  await page.screenshot({ path: join(output, "artifact-read-revoked.png"), fullPage: true });
  step("current Identity read revocation removes the selected content and rejects direct artifact reads");
  report.snapshots.push({ artifact_id: reportArtifact.id, current_version: reportArtifact.version, downloaded_versions: [1, 2], export_denied_status: directDenied });

  await control("restore-artifacts");
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
