// Runs only against the explicit temporary Identity/document fixture. The
// browser uses a fresh, headless profile; it does not use desktop user cookies.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { createHash } from "node:crypto";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = "http://127.0.0.1:8092";
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-library-documents-browser");
await mkdir(output, { recursive: true });
const config = await (await fetch(`${origin}/app/config`)).json();
assert.equal(config.workspace_id, "document-workspace", "Refuse a non-fixture deployment");
const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, acceptDownloads: true });
const page = await context.newPage();
page.setDefaultTimeout(15000);
const javascriptErrors = [], report = { steps: [], downloads: [], uploadAttempts: [] };
page.on("pageerror", error => javascriptErrors.push(String(error)));
let downloadEvents = 0;
page.on("download", () => downloadEvents++);
const step = name => { report.steps.push(name); console.log(`PASS ${name}`); };
const control = async name => { const response = await fetch(`${origin}/__acceptance/${name}`, { method: "POST" }); assert.equal(response.status, 204, name); };
const waitUntil = async (condition, message, timeout = 20000) => {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await condition()) return; await new Promise(resolve => setTimeout(resolve, 100)); }
  throw new Error(message);
};
const openLibrary = async (name = "共享资料") => {
  const open = page.getByRole("button", { name: /^资料库/ });
  if (page.viewportSize().width <= 650) {
    // Wait for React's media-query update before choosing the navigation entry.
    const navigation = page.getByRole("button", { name: "打开工作导航", exact: true });
    await navigation.waitFor(); await navigation.click();
  }
  await open.click();
  const dialog = page.getByRole("dialog", { name: "资料库", exact: true });
  await dialog.getByRole("button", { name: new RegExp(`^${name} `) }).click();
  const panel = dialog.getByRole("region", { name: "资料库文档", exact: true });
  await panel.getByRole("button", { name: "刷新文档", exact: true }).waitFor();
  await waitUntil(() => panel.getByRole("button", { name: "刷新文档", exact: true }).isEnabled(), "document list did not finish loading");
  return { dialog, panel };
};
const filename = "费用规则.txt";
const original = Buffer.from("费用规则：项目单次报销上限 123.45 元。\n资料库网页验收。\n");
const file = { name: filename, mimeType: "text/plain", buffer: original };
const selectFile = async (panel, value = file) => panel.getByLabel("选择资料文件", { exact: true }).setInputFiles(value);
const chooseDocument = async panel => { await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).click(); await panel.getByRole("region", { name: "文档详情", exact: true }).waitFor(); };
const downloadOriginal = async (panel, suffix) => {
  const completed = page.waitForEvent("download");
  await panel.getByRole("button", { name: "下载原文件", exact: true }).click();
  const download = await completed;
  assert.equal(download.suggestedFilename(), filename);
  const path = join(output, `${suffix}.txt`); await download.saveAs(path);
  assert.deepEqual(await readFile(path), original);
  report.downloads.push({ path, sha256: createHash("sha256").update(original).digest("hex") });
};

try {
  await page.goto(origin);
  await page.getByLabel("账号", { exact: true }).fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill("Changed-Document-Test!3");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  let { dialog, panel } = await openLibrary("个人资料");
  await panel.getByText(/尚未开放文档上传/).waitFor();
  assert.equal(await panel.getByLabel("选择资料文件", { exact: true }).count(), 0);
  await page.keyboard.press("Escape");
  step("unbound personal library explains unavailable upload");

  ({ dialog, panel } = await openLibrary());
  await panel.getByText(/所有阅读成员都能访问文件/).waitFor();
  await selectFile(panel);
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await waitUntil(() => panel.getByRole("button", { name: "上传到共享资料库", exact: true }).isEnabled(), "focus refresh lost selected file");
  step("library visibility is explicit and focus preserves selected file");

  let interrupted = false;
  await page.route("**/agent/knowledge-libraries/*/documents?*", async route => {
    if (route.request().method() !== "POST") return route.continue();
    const url = new URL(route.request().url());
    report.uploadAttempts.push(url.searchParams.get("client_id"));
    if (!interrupted) {
      interrupted = true;
      const response = await route.fetch(); assert.equal(response.status(), 200);
      return route.abort("connectionreset"); // Server accepted it; browser lost the acknowledgement.
    }
    return route.continue();
  });
  await panel.getByRole("button", { name: "上传到共享资料库", exact: true }).click();
  await panel.getByText(/上次上传待确认/).waitFor();
  await panel.getByRole("alert").waitFor();
  const pendingReceipt = await page.evaluate(() => Object.entries(localStorage).filter(([key]) => key.startsWith("agent-library-document-upload:")));
  assert.equal(pendingReceipt.length, 1);
  assert.equal(JSON.parse(pendingReceipt[0][1]).clientID, report.uploadAttempts[0]);
  assert(!pendingReceipt[0][1].includes(original.toString()), "file content stored in browser localStorage");
  await page.reload(); ({ dialog, panel } = await openLibrary());
  await panel.getByText(/上次上传待确认/).waitFor();
  assert(!await panel.getByRole("button", { name: "继续上次上传", exact: true }).isEnabled());
  await selectFile(panel, { ...file, buffer: Buffer.from("wrong file contents") });
  await panel.getByRole("button", { name: "继续上次上传", exact: true }).click();
  await panel.getByRole("alert").filter({ hasText: "请重新选择上次" }).waitFor();
  await new Promise(resolve => setTimeout(resolve, 5500));
  assert(await panel.getByRole("alert").filter({ hasText: "请重新选择上次" }).isVisible(), "automatic refresh hid the upload error");
  assert.equal(report.uploadAttempts.length, 1, "different bytes reused upload receipt");
  await selectFile(panel);
  await panel.getByRole("button", { name: "继续上次上传", exact: true }).click();
  await panel.getByText(/上次上传待确认/).waitFor({ state: "hidden" });
  assert.deepEqual(report.uploadAttempts, [report.uploadAttempts[0], report.uploadAttempts[0]]);
  await chooseDocument(panel);
  await panel.getByText(/正在建立索引/).first().waitFor();
  assert.equal(await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).count(), 1);
  step("lost response, refresh, wrong-file rejection and same-receipt retry without duplication");

  await control("index_documents");
  await panel.getByText(/已完成知识索引/).waitFor({ timeout: 20000 });
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(output, "desktop-ready.png") });
  await downloadOriginal(panel, "download-before-restart");
  step("index completion and downloaded original byte verification");

  await control("revoke_document_download");
  const before = downloadEvents;
  await panel.getByRole("button", { name: "下载原文件", exact: true }).click();
  await panel.getByRole("alert").waitFor();
  assert.equal(downloadEvents, before, "revoked download reached save flow");
  await control("restore_documents");
  await panel.getByRole("button", { name: "刷新文档", exact: true }).click();
  await chooseDocument(panel);
  await control("revoke_document_list");
  await panel.getByRole("button", { name: "刷新文档", exact: true }).click();
  await panel.getByRole("alert").waitFor();
  assert.equal(await panel.getByRole("region", { name: "文档详情", exact: true }).count(), 0);
  assert.equal(await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).count(), 0);
  await control("restore_documents");
  await panel.getByRole("button", { name: "刷新文档", exact: true }).click(); await chooseDocument(panel);
  await control("restart_document_host");
  await page.reload(); ({ dialog, panel } = await openLibrary()); await chooseDocument(panel);
  await downloadOriginal(panel, "download-after-restart");
  step("Identity download revocation, restoration and full host restart with existing browser login");

  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "新建会话", exact: true }).click();
  const newConversation = page.getByRole("dialog", { name: "新建会话", exact: true });
  await newConversation.getByLabel("会话名称", { exact: true }).fill("上传资料查询验收");
  await newConversation.getByRole("button", { name: "保存", exact: true }).click();
  await newConversation.waitFor({ state: "hidden" });
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("查询共享资料库中的费用规则，并给出资料来源。");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await page.getByRole("button", { name: `查看来源：${filename}`, exact: true }).waitFor({ timeout: 30000 });
  await page.getByRole("button", { name: "发送消息", exact: true }).waitFor({ timeout: 30000 });
  await page.getByRole("button", { name: `查看来源：${filename}`, exact: true }).click();
  const citation = page.getByRole("dialog", { name: filename, exact: true });
  await citation.getByText(/项目单次报销上限 123.45 元/).waitFor();
  assert.match(await citation.innerText(), /文档编号：kdoc_/);
  await page.screenshot({ path: join(output, "document-citation.png"), animations: "disabled" });
  await page.keyboard.press("Escape");
  step("uploaded document reaches model tool loop and a verified local document citation");

  await page.setViewportSize({ width: 390, height: 844 });
  ({ dialog, panel } = await openLibrary()); await chooseDocument(panel);
  await dialog.getByRole("button", { name: "归档资料库", exact: true }).click();
  await panel.getByText(/资料库已归档，上传、下载和检索已暂停/).waitFor();
  assert(!await panel.getByRole("button", { name: "下载原文件", exact: true }).isEnabled());
  await dialog.getByRole("button", { name: "恢复资料库", exact: true }).click();
  await panel.getByLabel("选择资料文件", { exact: true }).waitFor();
  step("archive pauses upload/download and restoration updates document controls");
  await panel.getByRole("button", { name: "删除文档", exact: true }).click();
  await panel.getByRole("button", { name: "保留文档", exact: true }).click();
  await panel.getByRole("button", { name: "删除文档", exact: true }).click();
  await panel.getByRole("button", { name: "确认删除文档", exact: true }).scrollIntoViewIfNeeded();
  const bounds = await dialog.boundingBox();
  assert(bounds && bounds.x >= 0 && bounds.x + bounds.width <= 391, "mobile dialog exceeds viewport");
  assert(await dialog.evaluate(element => element.scrollWidth <= element.clientWidth + 1), "mobile dialog has horizontal overflow");
  await page.screenshot({ path: join(output, "mobile-delete-confirmation.png") });
  await panel.getByRole("button", { name: "确认删除文档", exact: true }).click();
  await panel.getByText(/文档已停止检索和下载/).waitFor();
  await waitUntil(async () => await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).count() === 0, "deleted row was not cleared", 25000);
  await page.keyboard.press("Escape");
  await page.reload();
  await page.getByRole("log", { name: "聊天记录" }).getByRole("status").filter({ hasText: "相关内容已隐藏" }).waitFor();
  await waitUntil(async () => !(await page.getByRole("log", { name: "聊天记录" }).innerText()).includes("共享资料记载"), "deleted source retained historical reply");
  assert.equal(await page.getByRole("button", { name: `查看来源：${filename}`, exact: true }).count(), 0);
  await page.screenshot({ path: join(output, "deleted-history.png"), animations: "disabled" });
  step("mobile confirmation/cancel/delete, durable cleanup and historical citation revocation");

  // Save an owned conversation attachment as a separately authorized copy.
  await page.setViewportSize({ width: 1280, height: 900 });
  const openAttachments = async () => {
    await page.getByRole("button", { name: "附件", exact: true }).click();
    return page.getByRole("dialog", { name: "会话附件", exact: true });
  };
  let attachments = await openAttachments();
  let attachmentInterrupted = false;
  report.attachmentAttempts = [];
  await page.route("**/agent/conversations/*/attachments?*", async route => {
    if (route.request().method() !== "POST") return route.continue();
    report.attachmentAttempts.push(new URL(route.request().url()).searchParams.get("client_id"));
    if (!attachmentInterrupted) {
      attachmentInterrupted = true;
      const response = await route.fetch(); assert.equal(response.status(), 200);
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await attachments.getByLabel("选择附件", { exact: true }).setInputFiles(file);
  await attachments.getByRole("button", { name: "上传并私有保存", exact: true }).click();
  await attachments.getByRole("alert").waitFor();
  await page.reload(); attachments = await openAttachments();
  await attachments.getByText(/上次上传待确认/).waitFor();
  await attachments.getByLabel("选择附件", { exact: true }).setInputFiles({ ...file, buffer: Buffer.from("not the original attachment") });
  await attachments.getByRole("button", { name: "重试上传", exact: true }).click();
  await attachments.getByRole("alert").filter({ hasText: /请重新选择上次/ }).waitFor();
  assert.equal(report.attachmentAttempts.length, 1);
  await attachments.getByLabel("选择附件", { exact: true }).setInputFiles(file);
  await attachments.getByRole("button", { name: "重试上传", exact: true }).click();
  await attachments.getByRole("region", { name: "附件详情", exact: true }).waitFor();
  assert.deepEqual(report.attachmentAttempts, [report.attachmentAttempts[0], report.attachmentAttempts[0]]);
  assert.equal(await attachments.getByRole("button", { name: new RegExp(`^${filename}`) }).count(), 1);
  await attachments.getByRole("button", { name: "预览文本", exact: true }).click();
  await attachments.getByLabel("附件文本预览", { exact: true }).filter({ hasText: /123.45/ }).waitFor();
  const attachmentDownload = page.waitForEvent("download");
  await attachments.getByRole("button", { name: "下载原文件", exact: true }).click();
  const downloadedAttachment = await attachmentDownload;
  assert.equal(downloadedAttachment.suggestedFilename(), filename);
  const originalAttachmentPath = join(output, "download-private-attachment.txt");
  await downloadedAttachment.saveAs(originalAttachmentPath);
  assert.deepEqual(await readFile(originalAttachmentPath), original);
  report.downloads.push({ path: originalAttachmentPath, sha256: createHash("sha256").update(original).digest("hex") });
  step("private attachment upload recovers after lost response and refresh, rejects changed bytes, and preview/download match the original");
  await attachments.getByRole("button", { name: "另存到资料库", exact: true }).click();
  let savePanel = attachments.getByRole("region", { name: "附件另存资料库", exact: true });
  await savePanel.getByRole("button", { name: /^个人资料 / }).waitFor();
  assert(!await savePanel.getByRole("button", { name: /^个人资料 / }).isEnabled());
  await savePanel.getByRole("button", { name: /^共享资料 / }).click();
  await savePanel.getByText(/所有阅读成员都能访问这个副本/).waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await savePanel.getByRole("button", { name: "确认另存副本", exact: true }).scrollIntoViewIfNeeded();
  assert(await attachments.evaluate(element => element.scrollWidth <= element.clientWidth + 1), "mobile attachment import has horizontal overflow");
  await page.screenshot({ path: join(output, "mobile-attachment-copy-confirmation.png"), animations: "disabled" });
  await control("revoke_document_import");
  await savePanel.getByRole("button", { name: "确认另存副本", exact: true }).click();
  await savePanel.getByRole("alert").waitFor();
  await control("restore_documents");
  await page.setViewportSize({ width: 1280, height: 900 });
  await control("revoke_attachment_download");
  await savePanel.getByRole("button", { name: "继续确认上次另存", exact: true }).click();
  await savePanel.getByRole("alert").filter({ hasText: /附件操作权限/ }).waitFor();
  await control("restore_documents");
  let copyInterrupted = false;
  report.copyAttempts = [];
  await page.route("**/documents/from-attachment", async route => {
    const body = route.request().postDataJSON();
    report.copyAttempts.push(body);
    assert.deepEqual(Object.keys(body).sort(), ["attachment_id", "client_id", "conversation_id", "expected_revision"]);
    if (!copyInterrupted) {
      copyInterrupted = true;
      const response = await route.fetch(); assert.equal(response.status(), 200);
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await savePanel.getByRole("button", { name: "继续确认上次另存", exact: true }).click();
  await savePanel.getByRole("alert").waitFor();
  await page.reload(); attachments = await openAttachments();
  await attachments.getByRole("button", { name: new RegExp(`^${filename}`) }).click();
  savePanel = attachments.getByRole("region", { name: "附件另存资料库", exact: true });
  await savePanel.getByRole("button", { name: "继续确认上次另存", exact: true }).click();
  await savePanel.getByRole("status").filter({ hasText: /已另存到/ }).waitFor();
  assert.equal(report.copyAttempts.length, 2);
  assert.deepEqual(report.copyAttempts[0], report.copyAttempts[1]);
  await page.screenshot({ path: join(output, "attachment-copy.png"), animations: "disabled" });
  step("attachment import checks both permissions and recovers a lost response after refresh without resending file bytes");
  await savePanel.getByRole("button", { name: "前往资料库查看进度", exact: true }).click();
  await page.getByRole("dialog", { name: "资料库", exact: true }).waitFor();
  await page.keyboard.press("Escape");
  attachments = await openAttachments();
  await attachments.getByRole("button", { name: new RegExp(`^${filename}`) }).click();
  await attachments.getByRole("region", { name: "附件详情", exact: true }).waitFor();
  await attachments.getByRole("button", { name: "删除附件", exact: true }).click();
  await attachments.getByRole("button", { name: "确认删除附件", exact: true }).click();
  await attachments.getByRole("status").filter({ hasText: /附件已停止访问/ }).waitFor();
  await page.keyboard.press("Escape");
  await control("index_documents");
  ({ dialog, panel } = await openLibrary()); await chooseDocument(panel);
  await panel.getByText(/已完成知识索引/).waitFor({ timeout: 20000 });
  await control("restart_document_host");
  await page.reload(); ({ dialog, panel } = await openLibrary()); await chooseDocument(panel);
  await downloadOriginal(panel, "download-import-after-source-deletion-and-restart");
  step("independent shared copy survives source deletion and host restart with identical original bytes");
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "新建会话", exact: true }).click();
  const importConversation = page.getByRole("dialog", { name: "新建会话", exact: true });
  await importConversation.getByLabel("会话名称", { exact: true }).fill("附件另存查询验收");
  await importConversation.getByRole("button", { name: "保存", exact: true }).click();
  await importConversation.waitFor({ state: "hidden" });
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("查询共享资料中的费用规则，并给出来源。");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await page.getByRole("button", { name: `查看来源：${filename}`, exact: true }).waitFor({ timeout: 30000 });
  await page.getByRole("button", { name: "发送消息", exact: true }).waitFor({ timeout: 30000 });
  await page.getByRole("button", { name: `查看来源：${filename}`, exact: true }).click();
  const importedCitation = page.getByRole("dialog", { name: filename, exact: true });
  await importedCitation.getByText(/项目单次报销上限 123.45 元/).waitFor();
  assert(!(await importedCitation.innerText()).includes(report.copyAttempts[0].attachment_id));
  assert(!(await importedCitation.innerText()).includes(report.copyAttempts[0].conversation_id));
  await page.screenshot({ path: join(output, "attachment-copy-citation.png"), animations: "disabled" });
  await page.keyboard.press("Escape");
  ({ dialog, panel } = await openLibrary()); await chooseDocument(panel);
  await panel.getByRole("button", { name: "删除文档", exact: true }).click();
  await panel.getByRole("button", { name: "确认删除文档", exact: true }).click();
  await waitUntil(async () => await panel.getByRole("button", { name: new RegExp(`^${filename}`) }).count() === 0, "imported document cleanup not completed", 25000);
  step("imported file can be queried across conversations without exposing private source identifiers, then is independently deleted");

  assert.deepEqual(javascriptErrors, []);
  report.passed = true;
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
} catch (error) {
  report.passed = false; report.error = String(error); report.javascriptErrors = javascriptErrors;
  await page.screenshot({ path: join(output, "failure.png") }).catch(() => {});
  await writeFile(join(output, "failure.txt"), await page.locator("body").innerText().catch(() => ""));
  await writeFile(join(output, "report.json"), JSON.stringify(report, null, 2));
  console.error(error); process.exitCode = 1;
} finally {
  await browser.close();
  await control("finish");
}
