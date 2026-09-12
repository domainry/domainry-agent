// Exact built client against Identity + Module + SQLite + official Connector.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const inspect = (await import(pathToFileURL(process.env.AGENT_UI_QUALITY_CORE).href)).inspectStaticUiDocument;
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT,conversation=process.env.AGENT_UI_CONVERSATION;
const remainingOnly=process.env.AGENT_UI_INDEX_CASE==='permissions';
const browser=await chromium.launch({channel:'chrome',headless:true});
const context=await browser.newContext({viewport:{width:1280,height:900},acceptDownloads:true});
const page=await context.newPage();page.setDefaultTimeout(15000);
const report={complete:false,scope:remainingOnly?'permissions_preview_cleanup':'full_index_recovery',steps:[],baselines:[],requests:[],errors:[],externalRequests:[],lostResponses:[]};
let dropStart=false,dropCheck=false,lastCheckURL='',scope='';
page.on('pageerror',error=>report.errors.push(String(error)));
page.on('console',message=>{if(message.type()==='error'&&!/Failed to load resource/.test(message.text()))report.errors.push(message.text());});
page.on('request',r=>{const url=new URL(r.url());if(/^https?:/.test(url.protocol)&&url.origin!==origin)report.externalRequests.push(url.href);if(url.pathname.startsWith('/agent/'))report.requests.push({method:r.method(),path:url.pathname,query:url.search});});
await page.route('**/agent/conversations/*/attachments/*/index**',async route=>{
 const req=route.request(),url=new URL(req.url());
 if(req.method()==='POST'&&url.pathname.endsWith('/index/check')){lastCheckURL=req.url();scope=req.headers()['x-agent-scope']||'';}
 if(req.method()==='POST'&&(url.pathname.endsWith('/index')&&dropStart||url.pathname.endsWith('/index/check')&&dropCheck)){
  dropStart=false;dropCheck=false;
  const result=await route.fetch();assert.equal(result.status(),200);
  report.lostResponses.push(url.pathname);await route.abort('failed');return;
 }
 await route.continue();
});
const step=name=>{report.steps.push(name);console.log(`PASS ${name}`);};
const dialog=()=>page.getByRole('dialog',{name:'会话附件',exact:true});
const panel=()=>dialog().getByRole('region',{name:'当前会话附件索引',exact:true});
const control=async name=>{const response=await fetch(`${origin}/__acceptance/${name}`,{method:'POST'});assert.equal(response.status,name==='state'?200:204);return response;};
const stats=async()=>await(await control('state')).json();
const baseline=async label=>{report.baselines.push({label,...await page.evaluate(inspect,{})});await page.screenshot({path:join(output,`${label}.png`),animations:'disabled'});};
const select=async()=>{const loaded=page.waitForResponse(r=>r.status()===200&&r.request().method()==='GET'&&/\/attachments\/att_[a-f0-9]{32}$/.test(new URL(r.url()).pathname));await dialog().getByRole('button',{name:/^browser-private\.pdf/}).click();await loaded;await panel().waitFor();};
async function reopen(){
 await page.reload();await page.getByRole('button',{name:'附件',exact:true}).click();await select();
}
try {
 await page.goto(`${origin}/#${conversation}`);
 await page.getByLabel('账号',{exact:true}).fill('admin@example.com');await page.getByLabel('密码',{exact:true}).fill('Changed-Index-UI!3');await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('navigation',{name:'会话列表',exact:true}).getByRole('button',{name:/^附件索引验收/}).click();await page.getByRole('button',{name:'附件',exact:true}).click();
 await dialog().getByLabel('选择附件',{exact:true}).setInputFiles({name:'browser-private.pdf',mimeType:'application/pdf',buffer:await readFile(join(output,'original.pdf'))});
 let before=report.requests.length;
 await dialog().getByRole('button',{name:'上传并私有保存',exact:true}).click();await panel().getByRole('button',{name:'用于当前会话检索',exact:true}).waitFor();
 assert.deepEqual(report.requests.slice(before).map(r=>r.method),['POST']);assert.equal((await stats()).puts,0);
 if(!remainingOnly)await baseline('desktop-private-original');step('browser upload saves private original with one request; no automatic remote index');
 await panel().getByRole('button',{name:'用于当前会话检索',exact:true}).click();assert.equal((await stats()).puts,0);
 if(!remainingOnly){
 dropStart=true;before=report.requests.length;
 await panel().getByRole('button',{name:'确认开始索引',exact:true}).click();await panel().getByRole('button',{name:'核对上次请求',exact:true}).waitFor();await panel().getByRole('alert').waitFor();
 assert.deepEqual(report.requests.slice(before).map(r=>r.method),['POST']);
 await reopen();await panel().getByRole('button',{name:'核对上次请求',exact:true}).waitFor();
 await control('source-off');before=report.requests.length;
 await panel().getByRole('button',{name:'核对上次请求',exact:true}).click();await panel().getByText('已找回原请求的服务端记录，当前状态如下。',{exact:true}).waitFor();
 assert.deepEqual(report.requests.slice(before).map(r=>r.method),['GET']);await panel().getByText('尚未配置可用的附件知识源，请联系管理员。',{exact:true}).waitFor();
 assert(await panel().getByRole('button',{name:'核对索引状态',exact:true}).isDisabled());assert.equal((await stats()).puts,1);
 await baseline('unavailable-index-receipt');step('lost start response survives reload and host restart; original receipt lookup works without source configuration');
 await control('source-on');await select();await panel().getByRole('button',{name:'核对索引状态',exact:true}).waitFor();
 dropCheck=true;before=report.requests.length;
 await panel().getByRole('button',{name:'核对索引状态',exact:true}).click();await panel().getByRole('alert').waitFor();assert.deepEqual(report.requests.slice(before).map(r=>r.method),['POST']);
 await control('restart');await reopen();await panel().getByRole('button',{name:'核对上次请求',exact:true}).click();await panel().getByText('已找回原请求的服务端记录，当前状态如下。',{exact:true}).waitFor();
 await panel().getByText('可以在当前会话中检索。原文件仍可独立预览和下载。',{exact:true}).waitFor();assert.equal((await stats()).puts,1);
 step('durable check acknowledgement recovers after restart; positive remote status resolves unknown PUT without another upload');
 }else{
 await panel().getByRole('button',{name:'确认开始索引',exact:true}).click();
 await panel().getByText('上传请求的结果尚未确认，正在核对同一份远端文档，不会自动重复上传。',{exact:true}).waitFor();
 await panel().getByRole('button',{name:'核对索引状态',exact:true}).click();
 await panel().getByText('可以在当前会话中检索。原文件仍可独立预览和下载。',{exact:true}).waitFor();
 step('remaining-case setup: one indexed private original, unknown PUT resolved by observation');
 }
 await control('revoke-check');await select();await panel().getByText('当前没有核对索引任务的权限。',{exact:true}).waitFor();assert(await panel().getByRole('button',{name:'核对索引状态',exact:true}).isDisabled());
 const denied=await context.request.post(lastCheckURL,{headers:{'X-Agent-Scope':scope,'Origin':origin}});assert.equal(denied.status(),403);
 await control('restore');await select();await panel().locator('button:enabled').filter({hasText:'核对索引状态'}).waitFor();assert(await panel().getByRole('button',{name:'核对索引状态',exact:true}).isEnabled());
 step('Identity check revocation changes visible capability and rejects direct POST; restoration enables the same task');
 if(remainingOnly)await baseline('desktop-private-index');
 await page.setViewportSize({width:390,height:844});await baseline('mobile-private-index');await page.setViewportSize({width:1280,height:900});
 before=report.requests.filter(r=>r.path.endsWith('/content')).length;
 await dialog().getByRole('button',{name:'预览原文件',exact:true}).click();
 const preview=page.getByRole('dialog',{name:'browser-private.pdf',exact:true});await preview.locator('canvas[data-rendered="true"]').waitFor();assert.equal(report.requests.filter(r=>r.path.endsWith('/content')).length-before,1);
 const downloading=page.waitForEvent('download');await preview.getByRole('button',{name:'下载原文件',exact:true}).click();const download=await downloading;await download.saveAs(join(output,'download.pdf'));assert.deepEqual(await readFile(join(output,'download.pdf')),await readFile(join(output,'original.pdf')));
 await page.keyboard.press('Escape');await preview.waitFor({state:'hidden'});await dialog().getByRole('button',{name:'删除附件',exact:true}).click();before=report.requests.length;await dialog().getByRole('button',{name:'确认删除附件',exact:true}).click();
 await dialog().getByText(/附件已停止访问，原文件正在后台清理。|附件已删除。/).waitFor();assert.deepEqual(report.requests.slice(before).map(r=>r.method),['DELETE']);
 step('indexed original previews and downloads exact bytes; one delete request starts private cleanup');
 assert.deepEqual(report.errors,[]);assert.deepEqual(report.externalRequests,[]);assert.equal(report.lostResponses.length,remainingOnly?0:2);
 for(const result of report.baselines){assert.deepEqual(result.qualityFailures,[],result.label);assert(!result.pageOverflow,result.label);}
 report.complete=true;
} catch(error){report.failure=String(error);await page.screenshot({path:join(output,'failure.png')}).catch(()=>{});throw error;}
finally{await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));await context.close();await browser.close();}
