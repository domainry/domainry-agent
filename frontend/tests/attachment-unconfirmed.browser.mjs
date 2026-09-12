// Built client, actual Identity/Module/SQLite; Connector response loss and
// prolonged invisibility are controlled by the isolated host protocol fixture.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const inspect=(await import(pathToFileURL(process.env.AGENT_UI_QUALITY_CORE).href)).inspectStaticUiDocument;
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT,conversation=process.env.AGENT_UI_CONVERSATION;
const browser=await chromium.launch({channel:'chrome',headless:true});
const context=await browser.newContext({viewport:{width:1280,height:900},acceptDownloads:true});
const page=await context.newPage();page.setDefaultTimeout(15000);
const report={complete:false,scope:'unconfirmed_upload_restart_and_later_observation',steps:[],baselines:[],errors:[],externalRequests:[]};
let attachmentID='',startURL='',checkURL='',scope='';
page.on('pageerror',error=>report.errors.push(String(error)));
page.on('request',r=>{
 const u=new URL(r.url());
 if(/^https?:/.test(u.protocol)&&u.origin!==origin)report.externalRequests.push(u.href);
 if(r.method()==='POST'&&/\/attachments\/att_[a-f0-9]{32}\/index(?:\/check)?$/.test(u.pathname)){
  attachmentID=u.pathname.split('/')[5];scope=r.headers()['x-agent-scope']||'';
  if(u.pathname.endsWith('/check'))checkURL=r.url();else startURL=r.url();
 }
});
const dialog=()=>page.getByRole('dialog',{name:'会话附件',exact:true});
const panel=()=>dialog().getByRole('region',{name:'当前会话附件索引',exact:true});
const step=name=>{report.steps.push(name);console.log(`PASS ${name}`);};
const control=async name=>{const r=await fetch(`${origin}/__acceptance/${name}`,{method:'POST'});assert.equal(r.status,name==='state'?200:204);return r;};
const stats=async()=>await(await control('state')).json();
async function until(read,check){const deadline=Date.now()+15000;let value;while(Date.now()<deadline){value=await read();if(check(value))return value;await new Promise(r=>setTimeout(r,100));}throw new Error(`condition not reached: ${JSON.stringify(value)}`);}
async function detail(){const r=await context.request.get(`${origin}/agent/conversations/${conversation}/attachments/${attachmentID}`,{headers:{'X-Agent-Scope':scope}});assert.equal(r.status(),200);return r.json();}
async function reopen(){await page.reload();await page.getByRole('button',{name:'附件',exact:true}).click();await dialog().getByRole('button',{name:/^browser-private\.pdf/}).click();await panel().waitFor();}
async function check(){const response=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname.endsWith('/index/check'));await panel().getByRole('button',{name:'核对索引状态',exact:true}).click();assert.equal((await response).status(),200);}
async function baseline(label){report.baselines.push({label,...await page.evaluate(inspect,{})});await page.screenshot({path:join(output,`${label}.png`),animations:'disabled'});}
try{
 await page.goto(`${origin}/#${conversation}`);
 await page.getByLabel('账号',{exact:true}).fill('admin@example.com');await page.getByLabel('密码',{exact:true}).fill('Changed-Index-UI!3');await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('navigation',{name:'会话列表',exact:true}).getByRole('button',{name:/^附件索引验收/}).click();await page.getByRole('button',{name:'附件',exact:true}).click();
 await dialog().getByLabel('选择附件',{exact:true}).setInputFiles({name:'browser-private.pdf',mimeType:'application/pdf',buffer:await readFile(join(output,'original.pdf'))});
 await dialog().getByRole('button',{name:'上传并私有保存',exact:true}).click();await panel().getByRole('button',{name:'用于当前会话检索',exact:true}).waitFor();assert.equal((await stats()).puts,0);
 await panel().getByRole('button',{name:'用于当前会话检索',exact:true}).click();await panel().getByRole('button',{name:'确认开始索引',exact:true}).click();
 const pending=await until(detail,a=>a.state==='needs_reconcile');
 assert.equal(pending.indexing.can_start,false);assert.equal(pending.indexing.can_check,true);
 await reopen();await dialog().getByText('仅当前会话可见 · 上传结果待核查',{exact:true}).waitFor();
 await check();await until(stats,s=>s.hidden_fetches>=1);
 await until(detail,a=>a.state==='needs_reconcile'&&a.error_code==='attachment_put_unconfirmed');
 await reopen();await dialog().getByText('仅当前会话可见 · 上传结果待核查',{exact:true}).waitFor();
 assert(await dialog().getByRole('button',{name:'下载原文件',exact:true}).isEnabled());
 await baseline('desktop-unconfirmed-upload');
 step('unknown remote upload is explicitly pending reconciliation; original remains available and no new upload is offered');
 await dialog().getByRole('button',{name:'预览原文件',exact:true}).click();
 const preview=page.getByRole('dialog',{name:'browser-private.pdf',exact:true});await preview.locator('canvas[data-rendered="true"]').waitFor();
 const downloading=page.waitForEvent('download');await preview.getByRole('button',{name:'下载原文件',exact:true}).click();await(await downloading).saveAs(join(output,'download.pdf'));
 assert.deepEqual(await readFile(join(output,'download.pdf')),await readFile(join(output,'original.pdf')));
 await page.keyboard.press('Escape');await preview.waitFor({state:'hidden'});
 step('pending remote result does not block authorized PDF rendering or exact original download');
 await control('restart');await reopen();await dialog().getByText('仅当前会话可见 · 上传结果待核查',{exact:true}).waitFor();
 const resumed=await detail();assert.equal(resumed.id,pending.id);assert.equal(resumed.sha256,pending.sha256);assert.equal(resumed.state,'needs_reconcile');
 const replay=await context.request.post(startURL,{headers:{'X-Agent-Scope':scope,'Origin':origin}});assert.equal(replay.status(),200);assert.equal((await replay.json()).id,pending.id);
 await check();await until(stats,s=>s.hidden_fetches>=2);
 await until(detail,a=>a.state==='needs_reconcile'&&a.error_code==='attachment_put_unconfirmed');assert.equal((await stats()).puts,1);
 step('full host restart and original request replay retain the same attachment; repeated not-found inspections do not repeat upload or fabricate completion');
 await control('revoke-check');await reopen();await panel().getByText('当前没有核对索引任务的权限。',{exact:true}).waitFor();assert(await panel().getByRole('button',{name:'核对索引状态',exact:true}).isDisabled());
 const denied=await context.request.post(checkURL,{headers:{'X-Agent-Scope':scope,'Origin':origin}});assert.equal(denied.status(),403);
 await control('restore');await reopen();await panel().getByRole('button',{name:'核对索引状态',exact:true}).waitFor();await until(()=>panel().getByRole('button',{name:'核对索引状态',exact:true}).isEnabled(),Boolean);
 await page.setViewportSize({width:390,height:844});await baseline('mobile-unconfirmed-upload');await page.setViewportSize({width:1280,height:900});
 step('Identity revocation blocks reconciliation while original uncertain state persists; restoration and mobile display work');
 await control('reveal');await check();await panel().getByText('可以在当前会话中检索。原文件仍可独立预览和下载。',{exact:true}).waitFor();
 const ready=await detail();assert.equal(ready.state,'ready');assert.equal(ready.id,pending.id);assert.equal((await stats()).puts,1);
 step('later positive remote observation resolves the same upload without changing its identity or uploading again');
 await dialog().getByRole('button',{name:'删除附件',exact:true}).click();await dialog().getByRole('button',{name:'确认删除附件',exact:true}).click();await dialog().getByText(/附件已停止访问，原文件正在后台清理。|附件已删除。/).waitFor();
 await until(stats,s=>s.deletes===1);
 const deniedOriginal=await context.request.get(`${origin}/agent/conversations/${conversation}/attachments/${attachmentID}/content`,{headers:{'X-Agent-Scope':scope}});assert.equal(deniedOriginal.status(),404);
 step('deletion revokes original access and clears the resolved remote document exactly once');
 assert.deepEqual(report.errors,[]);assert.deepEqual(report.externalRequests,[]);
 for(const b of report.baselines){assert.deepEqual(b.qualityFailures,[],b.label);assert(!b.pageOverflow,b.label);}
 report.complete=true;
}catch(e){report.failure=String(e);await page.screenshot({path:join(output,'failure.png')}).catch(()=>{});throw e;}
finally{await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));await context.close();await browser.close();}
