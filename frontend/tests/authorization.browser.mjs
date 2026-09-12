// Real product UI, Identity account disable/enable HTTP and durable Run storage.
// Only the model and the worker scheduling gate belong to the local fixture.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || "playwright");
const origin = "http://127.0.0.1:8092";
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id, "admission-workspace", "Refuse a non-fixture deployment");
const output = resolve(process.env.AGENT_UI_TEST_OUTPUT || "/tmp/domainry-C03-browser");
await mkdir(output, {recursive:true});
const browser = await chromium.launch({headless:true,channel:"chrome"});
const context = await browser.newContext({viewport:{width:1280,height:900}});
const page = await context.newPage();
page.setDefaultTimeout(30000);
const report = {steps:[],javascriptErrors:[],snapshots:[]};
page.on("pageerror",error=>report.javascriptErrors.push(String(error)));
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`,{method:"POST"})).status,204,name);
const step = name => {report.steps.push(name);console.log(`PASS ${name}`);};
const login = async () => {
  await page.getByLabel("账号",{exact:true}).fill("system_administrator@example.com");
  await page.getByLabel("密码",{exact:true}).fill("Changed-Admission-Test!3");
  await page.getByRole("button",{name:"登录",exact:true}).click();
  await page.getByRole("button",{name:"新建会话",exact:true}).waitFor();
};
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
  await login();
  await page.getByRole("button",{name:"新建会话",exact:true}).click();
  const create=page.getByRole("dialog",{name:"新建会话",exact:true});
  await create.getByLabel("会话名称",{exact:true}).fill("后台执行当前身份核验");
  await create.getByRole("button",{name:"保存",exact:true}).click();
  await create.waitFor({state:"hidden"});
  await control("arm_admission");
  await page.getByRole("textbox",{name:"消息",exact:true}).fill("整理资料，账号停用时应停止后台执行");
  await page.getByRole("button",{name:"发送消息",exact:true}).click();
  await page.getByRole("button",{name:"停止生成",exact:true}).waitFor();
  const before=await snapshot("before account disable");
  const conversationID=before.conversation.id, runID=before.run.id;
  assert.equal(before.run.status,"running");
  step("authenticated user enqueues a real durable Run; worker waits at execution admission");

  await control("disable_and_release_owner");
  // Account disable also revokes browser sessions. Read access must fail, too.
  const revoked=await page.evaluate(async()=>({session:(await fetch("/app/session")).status,conversation:(await fetch(`/agent/conversations/${location.hash.slice(1)}`)).status}));
  assert.equal(revoked.session,401);
  assert.equal(revoked.conversation,401);
  await page.reload();
  await page.getByLabel("账号",{exact:true}).waitFor();
  await page.screenshot({path:join(output,"disabled-session.png")});
  step("Identity disable revokes the browser session and stops queued model execution");

  await control("restore_owner");
  await login();
  await page.goto(`${origin}/#${conversationID}`);
  await page.getByRole("button",{name:"重新生成",exact:true}).waitFor();
  const failed=await snapshot("after account restoration");
  assert.equal(failed.run.id,runID);
  assert.equal(failed.run.status,"failed");
  assert.equal(failed.run.error_code,"execution_access_denied");
  assert.equal(failed.messages.items.length,1);
  await page.getByText(/当前身份已失效或无权继续处理/).first().waitFor();
  await page.screenshot({path:join(output,"denied-run.png")});
  step("restored owner sees the stored denial; no assistant reply was produced");

  await control("restart_admission_host");
  await page.reload();
  await page.getByRole("button",{name:"重新生成",exact:true}).waitFor();
  const reopened=await snapshot("after host restart");
  assert.equal(reopened.run.status,"failed");
  assert.equal(reopened.run.last_event_seq,failed.run.last_event_seq);
  await page.getByRole("button",{name:"重新生成",exact:true}).click();
  await page.locator(".run-status").filter({hasText:/^已保存$/}).waitFor();
  const final=await snapshot("after explicit resume");
  assert.equal(final.run.id,runID);
  assert.equal(final.run.attempt,2);
  assert.equal(final.run.status,"completed");
  assert.equal(final.messages.items.length,2);
  assert.equal(final.messages.items[1].run_id,runID);
  await page.screenshot({path:join(output,"authorized-resume.png")});
  step("restart keeps the denied Run stopped; explicit resume checks current identity and saves one reply");
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
