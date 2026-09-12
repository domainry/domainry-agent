// Only the explicit temporary Identity fixture; never the user's browser or deployment.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const qualityPath = process.env.AGENT_UI_QUALITY_CORE;
const inspect = qualityPath ? (await import(pathToFileURL(qualityPath).href)).inspectStaticUiDocument : null;
const origin = "http://127.0.0.1:8092";
const config = await (await fetch(`${origin}/app/config`)).json();
assert.equal(config.workspace_id, "document-workspace", "Refuse a non-fixture deployment");
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-knowledge-datasources-browser");
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, channel: "chrome" });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, acceptDownloads: true });
const page = await context.newPage(); page.setDefaultTimeout(15000);
const errors = [], report = { steps: [], bindings: [], baselines: [], complete: false };
page.on("pageerror", error => errors.push(String(error)));
const step = name => { report.steps.push(name); console.log(`PASS ${name}`); };
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`, { method: "POST" })).status, 204, name);
const waitUntil = async (test, message) => { const deadline = Date.now()+25000; while (Date.now()<deadline) {if(await test()) return;await new Promise(r=>setTimeout(r,100));}throw Error(message); };
const open = async (name = "个人资料") => {
  if (page.viewportSize().width < 650) await page.getByRole("button", { name: "打开工作导航", exact: true }).click();
  await page.getByRole("button", { name: /^资料库/ }).click();
  const dialog = page.getByRole("dialog", { name: "资料库", exact: true });
  await dialog.getByRole("button", { name: new RegExp(`^${name} `) }).click();
  const sources = dialog.getByRole("region", { name: "资料库知识源", exact: true });
  const documents = dialog.getByRole("region", { name: "资料库文档", exact: true });
  await sources.getByRole("button", { name: "刷新知识源", exact: true }).waitFor();
  await waitUntil(() => sources.getByRole("button", { name: "刷新知识源", exact: true }).isEnabled(), "catalog not ready");
  return { dialog, sources, documents };
};
const refresh = async sources => { await sources.getByRole("button", { name: "刷新知识源", exact: true }).click(); await waitUntil(() => sources.getByRole("button", { name: "刷新知识源", exact: true }).isEnabled(), "refresh pending"); };
const baseline = async (dialog, label) => {
  const bounds=await dialog.boundingBox();assert(bounds.x>=0 && bounds.x+bounds.width<=page.viewportSize().width+1,"dialog overflows viewport");
  assert(await dialog.evaluate(e=>e.scrollWidth<=e.clientWidth+1),"dialog horizontal overflow");
  if(inspect) { const result=await page.evaluate(inspect,{});report.baselines.push({label,...result});assert.deepEqual(result.qualityFailures,[],"visual baseline failure");assert(!result.pageOverflow); }
  await page.screenshot({path:join(output,`${label}.png`),animations:"disabled"});
};
try {
  await page.goto(origin);
  await page.getByLabel("账号",{exact:true}).fill("admin@example.com");
  await page.getByLabel("密码",{exact:true}).fill("Changed-Document-Test!3");
  await page.getByRole("button",{name:"登录",exact:true}).click();
  if (process.env.AGENT_UI_STYLE_ONLY === "1") {
    for (const viewport of [{width:1280,height:900},{width:390,height:844}]) {
      await page.setViewportSize(viewport);
      if(viewport.width<650)await page.getByRole("button",{name:"打开工作导航",exact:true}).waitFor();
      const {dialog,sources}=await open(viewport.width<650?"待连接共享资料":"个人资料");
      await sources.getByRole("button",{name:/知识源 third/}).click();
      await baseline(dialog,viewport.width<650?"mobile-typography-fixed":"desktop-typography-fixed");
      await page.keyboard.press("Escape");
    }
    step("targeted desktop/mobile typography and knowledge-source visual baseline");
  } else {
  let {dialog,sources,documents}=await open();
  await sources.getByText(/选择一个知识源/).waitFor();
  assert(!await sources.getByRole("button",{name:/知识源 first/}).isEnabled());
  assert(await sources.getByRole("button",{name:/知识源 second/}).isEnabled());
  for(const name of ["上一页知识源","下一页知识源"]) assert(!await sources.getByRole("button",{name,exact:true}).isEnabled());
  await control("revoke_source_bind");await refresh(sources);
  await sources.getByText(/当前没有连接知识源的权限/).waitFor();
  assert(!await sources.getByRole("button",{name:/知识源 second/}).isEnabled());
  await control("restore_documents");await refresh(sources);
  await waitUntil(()=>sources.getByRole("button",{name:/知识源 second/}).isEnabled(),"grant not reflected");
  await baseline(dialog,"desktop-unbound");step("unbound catalog, occupied source, visible pagination and live Identity denial");
  let lost=true;
  await page.route("**/agent/knowledge-libraries/*/source",async route=>{
    const input=route.request().postDataJSON();report.bindings.push(input);
    assert.deepEqual(Object.keys(input).sort(),["datasource_key","expected_revision"]);
    if(lost){lost=false;const response=await route.fetch();assert.equal(response.status(),200);return route.abort("connectionreset");}
    return route.continue();
  });
  await sources.getByRole("button",{name:/知识源 second/}).click();
  await sources.getByRole("button",{name:"连接知识源",exact:true}).click();
  await sources.getByRole("alert").waitFor();
  const receipts=await page.evaluate(()=>Object.entries(localStorage).filter(([key])=>key.startsWith("agent-library-source:")));
  assert.equal(receipts.length,1);assert.equal(JSON.parse(receipts[0][1]).datasource_key,"second");
  await page.reload();({dialog,sources,documents}=await open());
  await sources.getByText(/已连接知识源/).waitFor();
  assert.equal(report.bindings.length,1,"refresh repeated binding write");
  assert.equal(await page.evaluate(()=>Object.keys(localStorage).filter(key=>key.startsWith("agent-library-source:")).length),0);
  await documents.getByLabel("选择资料文件",{exact:true}).waitFor();step("lost binding response recovered by read after refresh, upload enabled without host restart");
  const filename="知识源绑定验收.txt",original=Buffer.from("知识源连接后，个人资料可持续使用。合成验收内容。\n");
  await documents.getByLabel("选择资料文件",{exact:true}).setInputFiles({name:filename,mimeType:"text/plain",buffer:original});
  await documents.getByRole("button",{name:"保存到个人资料",exact:true}).click();
  await documents.getByRole("button",{name:new RegExp(`^${filename}`)}).click();
  await control("index_documents");
  await waitUntil(async()=>/已索引|可检索/.test(await documents.innerText()),"index not visible");
  await page.keyboard.press("Escape");await control("remove_datasource_config");await page.reload();({dialog,sources,documents}=await open());
  await sources.getByText(/当前连接暂不可用/).waitFor();
  await documents.getByRole("button",{name:new RegExp(`^${filename}`)}).click();
  const pendingDownload=page.waitForEvent("download");await documents.getByRole("button",{name:"下载原文件",exact:true}).click();
  const download=await pendingDownload;const downloaded=join(output,"original.txt");await download.saveAs(downloaded);assert.deepEqual(await readFile(downloaded),original);
  step("host restart without catalog disables knowledge connection and preserves original download");
  await page.keyboard.press("Escape");await control("restore_datasource_config");await page.reload();({dialog,sources,documents}=await open());
  await sources.getByText(/已连接知识源/).waitFor();
  await documents.getByRole("button",{name:new RegExp(`^${filename}`)}).click();
  await documents.getByRole("button",{name:"删除文档",exact:true}).click();
  await documents.getByRole("button",{name:"确认删除文档",exact:true}).click();
  await waitUntil(async()=>await documents.getByRole("button",{name:new RegExp(`^${filename}`)}).count()===0,"cleanup pending");
  await page.keyboard.press("Escape");await page.setViewportSize({width:390,height:844});
  await page.getByRole("button",{name:"打开工作导航",exact:true}).waitFor();
  ({dialog,sources,documents}=await open("待连接共享资料"));
  assert(!await sources.getByRole("button",{name:/知识源 second/}).isEnabled(),"claimed source became reusable after document deletion");
  await sources.getByRole("button",{name:/知识源 third/}).click();
  await baseline(dialog,"mobile-shared-connect");
  let sourceReads=0;page.on("request",req=>{if(new URL(req.url()).pathname.endsWith("/sources"))sourceReads++;});
  await sources.getByRole("button",{name:"连接知识源",exact:true}).click();
  await sources.getByText(/已连接知识源/).waitFor();
  assert.equal(sourceReads,0,"successful binding auto-refetched catalog");
  await documents.getByLabel("选择资料文件",{exact:true}).waitFor();
  assert.equal(report.bindings.length,2);
  step("mobile shared-library binding, one PUT, persisted exclusive KB ownership and original cleanup");
  }
  assert.deepEqual(errors,[]);report.complete=true;
} finally {
  report.errors=errors;await writeFile(join(output,"report.json"),JSON.stringify(report,null,2));
  if(!report.complete)await page.screenshot({path:join(output,"failure.png")}).catch(()=>{});
  await browser.close();await control("finish");
}
