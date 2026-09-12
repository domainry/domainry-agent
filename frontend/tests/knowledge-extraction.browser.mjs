// Real Identity/HTTP/SQLite and binary original storage; deterministic model and
// parsed knowledge-service fixture. This does not claim real PDF parsing.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const inspect = process.env.AGENT_UI_QUALITY_CORE ? (await import(pathToFileURL(process.env.AGENT_UI_QUALITY_CORE).href)).inspectStaticUiDocument : null;
const origin = "http://127.0.0.1:8092";
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id, "document-workspace");
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-knowledge-extraction-browser");
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, acceptDownloads: true });
const page = await context.newPage(); page.setDefaultTimeout(20000);
const report = { steps: [], baselines: [], errors: [], complete: false };
page.on("pageerror", e => report.errors.push(String(e)));
const step = name => { report.steps.push(name); console.log(`PASS ${name}`); };
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`, { method: "POST" })).status, 204, name);
const wait = async (check, message) => { const end = Date.now() + 30000; while (Date.now() < end) { if (await check()) return; await new Promise(r => setTimeout(r, 100)); } throw Error(message); };
const tableOnly = process.env.AGENT_UI_TABLE_ONLY === "1";
const localXLSX = process.env.AGENT_UI_LOCAL_XLSX === "1";
assert(!(tableOnly && localXLSX), "select one acceptance fixture");
const filename = tableOnly ? "synthetic-long-table.txt" : localXLSX ? "synthetic-extraction.xlsx" : "synthetic-extraction.pdf";
const original = tableOnly ? Buffer.from("Customer: Qinghe Fixture\nInvoice Amount: 9007199254740993.25 CNY\nSigned Date: 2026-09-11\nApproval: true\nItem | Quantity | Unit Price\n" + Array.from({ length: 25 }, (_, i) => `Item${String.fromCharCode(65 + i)} | ${i + 1} | 1.20`).join("\n") + "\n") : await readFile(new URL(`../../integration/testdata/knowledge-extraction/${filename}`, import.meta.url));
const openLibrary = async () => {
  if (page.viewportSize().width < 650) await page.getByRole("button", { name: "打开工作导航", exact: true }).click();
  await page.getByRole("button", { name: /^资料库/ }).click();
  const dialog = page.getByRole("dialog", { name: "资料库", exact: true });
  await dialog.getByRole("button", { name: /^共享资料 / }).click();
  const panel = dialog.getByRole("region", { name: "资料库文档", exact: true });
  await panel.getByRole("button", { name: "刷新文档", exact: true }).waitFor();
  await wait(() => panel.getByRole("button", { name: "刷新文档", exact: true }).isEnabled(), "document list loading");
  return { dialog, panel };
};
const baseline = async label => {
  if (label.endsWith("extraction")) await page.getByRole("region", { name: "回复表格", exact: true }).scrollIntoViewIfNeeded();
  if (inspect) {
    const result = await page.evaluate(inspect, {});
    report.baselines.push({ label, ...result });
    assert.deepEqual(result.qualityFailures, [], label);
    assert(!result.pageOverflow, `${label}: page overflow`);
  }
  await page.screenshot({ path: join(output, `${label}.png`), animations: "disabled" });
};
let uploaded = false, deleted = false;
try {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Changed-Document-Test!3");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  let { panel } = await openLibrary();
  await panel.getByLabel("选择资料文件", { exact: true }).setInputFiles({ name: filename, mimeType: tableOnly ? "text/plain" : localXLSX ? "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" : "application/pdf", buffer: original });
  await panel.getByRole("button", { name: "上传到共享资料库", exact: true }).click(); uploaded = true;
  await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).click();
  await control("index_documents");
  await panel.getByText(/已完成知识索引/).waitFor();
  const downloading = page.waitForEvent("download");
  await panel.getByRole("button", { name: "下载原文件", exact: true }).click();
  const download = await downloading;
  await download.saveAs(join(output, filename)); assert.deepEqual(await readFile(join(output, filename)), original);
  step(`${tableOnly ? "text table" : localXLSX ? "actual binary XLSX" : "binary PDF"} uploaded, indexed and original download verified`);
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "新建会话", exact: true }).click();
  const create = page.getByRole("dialog", { name: "新建会话", exact: true });
  await create.getByLabel("会话名称", { exact: true }).fill("文档字段与表格提取验收");
  await create.getByRole("button", { name: "保存", exact: true }).click(); await create.waitFor({ state: "hidden" });
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("请提取共享资料中的客户、金额、日期、确认状态、邮箱与明细表，并提供来源。");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  const log = page.getByRole("log", { name: "聊天记录", exact: true });
  await wait(async () => (await log.innerText()).includes("邮箱：缺失"), "extraction reply missing");
  const answer = await log.innerText();
  for (const expected of ["Qinghe Fixture", "9007199254740993.25", "2026-09-11", "true", ...(tableOnly ? ["ItemA", "ItemT"] : ["Pencil", "Paper"]), "未计算合计"]) assert(answer.includes(expected), expected);
  assert.equal(await log.getByRole("row").count(), tableOnly ? 21 : 3, "visible table row count");
  await page.getByRole("button", { name: "发送消息", exact: true }).waitFor();
  if (tableOnly) {
    const table = page.getByRole("region", { name: "回复表格", exact: true });
    await table.getByRole("button", { name: "下一页", exact: true }).click();
    await table.getByText("第 2 / 2 页 · 共 25 行", { exact: true }).waitFor();
    assert.equal(await table.getByRole("row").count(), 6);
    assert(!(await table.innerText()).includes("ItemA")); assert((await table.innerText()).includes("ItemY"));
    await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin });
    await table.getByRole("button", { name: "复制本页", exact: true }).click();
    await table.getByRole("status").filter({ hasText: "已复制本页表格" }).waitFor();
    const copied = await page.evaluate(() => navigator.clipboard.readText()); assert(copied.includes("ItemY")); assert(!copied.includes("ItemA"));
    const saving = page.waitForEvent("download"); await table.getByRole("button", { name: "下载本页", exact: true }).click();
    const saved = await saving; await saved.saveAs(join(output, "table-page-2.md"));
    const exported = await readFile(join(output, "table-page-2.md"), "utf8"); assert(exported.includes("ItemY")); assert(!exported.includes("ItemA"));
    await table.getByRole("button", { name: "上一页", exact: true }).click();
    assert.equal(await table.getByRole("row").count(), 21);
    step("25 actual parsed rows paginate 20/5, and copy/download contain only the selected page");
  }
  const sourceButton = page.getByRole("button", { name: `查看来源：${filename}`, exact: true }).nth(localXLSX ? 1 : 0);
  await sourceButton.waitFor();
  await page.getByRole("button", { name: "发送消息", exact: true }).waitFor();
  await baseline("desktop-extraction");
  await sourceButton.click();
  const citation = page.getByRole("dialog", { name: filename, exact: true });
  await citation.getByText(/9007199254740993.25/).waitFor();
  assert.match(await citation.innerText(), /文档编号：kdoc_/);
  if (localXLSX) { assert.match(await citation.innerText(), /工作表：Synthetic Invoice/); assert.match(await citation.innerText(), /第 4 行/); step("actual XLSX original supplies exact amount and verified sheet/row in the source dialog"); }
  await baseline("desktop-source"); await page.keyboard.press("Escape");
  step(`tool results display exact values, missing email, ${tableOnly ? "25" : "two"} table rows and verified source`);
  if (!tableOnly) {
  await control("restart_document_host"); await page.reload();
  await wait(async () => (await log.innerText()).includes("9007199254740993.25"), "restart lost extraction");
  await control("revoke_extract"); await page.reload();
  await log.getByRole("status").filter({ hasText: "相关内容已隐藏" }).waitFor();
  assert(!(await log.innerText()).includes("9007199254740993.25"));
  assert.equal(await page.getByRole("button", { name: `查看来源：${filename}`, exact: true }).count(), 0);
  await control("restore_documents"); await page.reload();
  await wait(async () => (await log.innerText()).includes("9007199254740993.25"), "restored permission did not restore verified result");
  step("persisted extraction survives host restart; current Identity revocation hides values and citations; restore revalidates");
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "打开工作导航", exact: true }).waitFor();
  if (tableOnly) await page.getByRole("region", { name: "回复表格", exact: true }).getByRole("button", { name: "下一页", exact: true }).click();
  await baseline("mobile-extraction");
  ({ panel } = await openLibrary());
  await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).click();
  await panel.getByRole("button", { name: "删除文档", exact: true }).click();
  await panel.getByRole("button", { name: "确认删除文档", exact: true }).click();
  await wait(async () => await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).count() === 0, "delete cleanup incomplete"); deleted = true;
  await page.keyboard.press("Escape"); await page.reload();
  await log.getByRole("status").filter({ hasText: "相关内容已隐藏" }).waitFor();
  assert(!(await log.innerText()).includes("Qinghe Fixture"));
  step("mobile result display, document deletion and historical extraction revocation");
  assert.deepEqual(report.errors, []); report.complete = true;
} finally {
  if (!report.complete) {
    await page.screenshot({ path: join(output, "failure.png") }).catch(() => {});
    // Only this owned local protocol fixture. Leave the server running if cleanup
    // fails, so a targeted repair can recover the same fixture without duplication.
    if (uploaded && !deleted) {
      try {
        await control("restore_documents"); await page.reload();
        const { panel } = await openLibrary();
        await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).click();
        await panel.getByRole("button", { name: "删除文档", exact: true }).click();
        await panel.getByRole("button", { name: "确认删除文档", exact: true }).click();
        await wait(async () => await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).count() === 0, "failure cleanup incomplete"); deleted = true;
      } catch (e) { report.cleanupError = String(e); }
    }
  }
  report.ownedDocumentDeleted = deleted;
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
  await browser.close(); if (!uploaded || deleted) await control("finish");
}
