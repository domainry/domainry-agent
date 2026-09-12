// Product UI + real Identity, HTTP, SQLite and official knowledge Connector.
// All accounts, documents and model responses belong to the isolated fixture.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = "http://127.0.0.1:8092";
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id, "catalog-workspace", "Refuse a non-fixture deployment");
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-B06-browser");
await mkdir(output, {recursive:true});
const browser = await chromium.launch({headless:true,channel:"chrome"});
const context = await browser.newContext({viewport:{width:1280,height:900}});
const page = await context.newPage();
page.setDefaultTimeout(30000);
const report = {steps:[],javascriptErrors:[],snapshots:[]};
page.on("pageerror",error=>report.javascriptErrors.push(String(error)));
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`,{method:"POST"})).status,204,name);
const step = name => {report.steps.push(name);console.log(`PASS ${name}`);};
const state = () => page.evaluate(async()=>{
  const session = await (await fetch("/app/session")).json();
  const base = `/agent/conversations/${location.hash.slice(1)}`;
  const read = path => fetch(path,{headers:{"X-Agent-Scope":session.scope}}).then(r=>r.json());
  const conversation=await read(base), messages=await read(`${base}/messages`);
  const id=conversation.active_run_id || messages.items.at(-1).run_id;
  return {conversation,messages,run:await read(`${base}/runs/${id}`)};
});
async function snapshot(label) {
  const stored=await state();
  report.snapshots.push({label,run:stored.run,messages:stored.messages.items.map(m=>({id:m.id,role:m.role,run_id:m.run_id}))});
  return stored;
}
try {
  await page.goto(origin);
  await page.getByLabel("账号",{exact:true}).fill("admin@example.com");
  await page.getByLabel("密码",{exact:true}).fill("Changed-Catalog-Test!3");
  await page.getByRole("button",{name:"登录",exact:true}).click();
  await page.getByRole("button",{name:"新建会话",exact:true}).click();
  const create=page.getByRole("dialog",{name:"新建会话",exact:true});
  await create.getByLabel("会话名称",{exact:true}).fill("取消保留已完成结果");
  await create.getByRole("button",{name:"保存",exact:true}).click();
  await create.waitFor({state:"hidden"});
  await page.getByRole("textbox",{name:"消息",exact:true}).fill("B06取消验收");
  await page.getByRole("button",{name:"发送消息",exact:true}).click();
  await page.locator('[data-tool-status="running"]').filter({hasText:"搜索知识库"}).waitFor();
  await control("wait_cancellation_read");
  const started=await snapshot("before cancellation");
  const runID=started.run.id;
  assert.deepEqual(started.run.steps[0].calls.map(c=>c.status),["completed","running","queued"]);
  await page.screenshot({path:join(output,"before-cancel.png")});
  step("first calculation committed, official knowledge HTTP request in flight, third call unstarted");

  await page.getByRole("button",{name:"停止生成",exact:true}).click();
  await page.getByText(/已停止后续处理。已完成或已受理的业务操作会保留/).waitFor();
  await control("assert_cancellation_observed");
  // The stop request and the interrupted read's final receipt are distinct
  // transactions. Wait for the actual stored receipt, not just a stopped label.
  const deadline=Date.now()+10000;
  let stopped;
  do {
    stopped=await state();
    if (stopped.run.steps[0].calls[1].status==="failed") break;
    await new Promise(resolve=>setTimeout(resolve,100));
  } while(Date.now()<deadline);
  assert.equal(stopped.run.id,runID);
  assert.equal(stopped.run.status,"cancelled");
  assert.deepEqual(stopped.run.steps[0].calls.map(c=>c.status),["completed","failed","not_started"]);
  assert.equal(stopped.messages.items.length,1);
  assert.equal(stopped.messages.items[0].role,"user");
  await page.getByRole("button",{name:"查看处理记录",exact:true}).first().click();
  const details=page.getByRole("dialog",{name:"处理记录",exact:true});
  await details.getByText("未执行",{exact:true}).waitFor();
  assert.equal(await details.locator('[data-tool-status="completed"]').count(),1);
  await details.locator('[data-tool-status="completed"] summary').click();
  await details.getByText("0.1+0.2 = 0.30 CNY",{exact:true}).waitFor();
  await page.screenshot({path:join(output,"cancelled-receipts.png")});
  await page.keyboard.press("Escape");
  await details.waitFor({state:"hidden"});
  step("stop closes Connector HTTP, preserves calculation, leaves third call unexecuted and saves no assistant reply");

  await page.reload();
  await page.getByText(/已停止后续处理。已完成或已受理的业务操作会保留/).waitFor();
  const refreshed=await snapshot("after refresh");
  assert.equal(refreshed.run.id,runID);
  assert.deepEqual(refreshed.run.steps[0].calls.map(c=>c.status),["completed","failed","not_started"]);
  const repeated=await page.evaluate(async({runID})=>{
    const session=await(await fetch("/app/session")).json();
    const response=await fetch(`/agent/conversations/${location.hash.slice(1)}/runs/${runID}/cancel`,{method:"POST",headers:{"X-Agent-Scope":session.scope,"Content-Type":"application/json"},body:"{}"});
    return {status:response.status,run:await response.json()};
  },{runID});
  assert.equal(repeated.status,200);
  assert.equal(repeated.run.last_event_seq,refreshed.run.last_event_seq);
  step("refresh retains real outcomes and duplicate cancellation is idempotent");

  await control("restart_catalog_host");
  await page.reload();
  await page.getByRole("button",{name:"继续处理",exact:true}).waitFor();
  const reopened=await snapshot("after host restart");
  assert.equal(reopened.run.id,runID);
  assert.deepEqual(reopened.run.steps[0].calls.map(c=>c.status),["completed","failed","not_started"]);
  await page.screenshot({path:join(output,"restart-cancelled.png")});
  await page.getByRole("button",{name:"继续处理",exact:true}).click();
  await page.locator(".run-status").filter({hasText:/^已保存$/}).waitFor({timeout:60000});
  const final=await snapshot("after explicit resume");
  assert.equal(final.run.id,runID);
  assert.equal(final.run.status,"completed");
  assert.equal(final.run.attempt,2);
  assert.deepEqual(final.run.steps[0].calls.map(c=>c.status),["completed","failed","completed"]);
  assert.equal(final.messages.items.length,2);
  assert.equal(final.messages.items[1].role,"assistant");
  assert.equal(final.messages.items[1].run_id,runID);
  await page.screenshot({path:join(output,"explicit-resume.png")});
  step("host restart retains cancellation; explicit resume completes only remaining work on the same run");
  assert.deepEqual(report.javascriptErrors,[]);
  await writeFile(join(output,"report.json"),JSON.stringify(report,null,2));
} catch(error) {
  report.error=String(error);
  report.lastState=await state().catch(reason=>({error:String(reason)}));
  await page.screenshot({path:join(output,"failure.png")}).catch(()=>{});
  await writeFile(join(output,"report.json"),JSON.stringify(report,null,2));
  throw error;
} finally {
  await browser.close();
  await control("finish");
}
