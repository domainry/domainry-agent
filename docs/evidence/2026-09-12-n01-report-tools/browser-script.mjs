import {createRequire} from 'node:module';
import {writeFile, mkdir} from 'node:fs/promises';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const {chromium} = require('/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright');
const origin = 'http://127.0.0.1:8093';
const directory = new URL(process.env.BROWSER_EVIDENCE || './browser/', import.meta.url);
await mkdir(directory, {recursive:true});
const evidence = {model:'in-process deterministic SDK model; real Report/Identity/Runtime/Agent/SQLite', phases:[], runs:[], pageErrors:[]};
let browser, page;
const save = async name => { await page.screenshot({path:new URL(name+'.png',directory).pathname,fullPage:true}); };
async function api(path, method='GET') {
  return page.evaluate(async ({path,method})=>{
    const session = await (await fetch('/app/session')).json();
    const response = await fetch(path,{method,headers:{'X-Agent-Scope':session.scope,'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},...(method==='POST'?{body:'{}'}:{})});
    const body = response.status === 204 ? null : await response.json();
    return {status:response.status,body};
  },{path,method});
}
async function control(name) {
  const r = await fetch(origin+'/__acceptance/'+name,{method:'POST'});
  assert.equal(r.status,204,name);
}
async function refresh() {
  const response = await api('/auth/refresh','POST');
  assert.equal(response.status,200,'refresh token after permission mutation');
  await page.reload();
  await page.getByRole('button',{name:'新建会话',exact:true}).waitFor();
}
async function createConversation(title) {
  await page.getByRole('button',{name:'新建会话',exact:true}).click();
  const dialog=page.getByRole('dialog');
  await dialog.getByLabel('会话名称',{exact:true}).fill(title);
  await dialog.getByRole('button',{name:'保存',exact:true}).click();
  await dialog.waitFor({state:'hidden'});
  await page.getByLabel('消息',{exact:true}).waitFor();
}
const answer = '已逐页查询客户名称报表：Beta、Gamma、Updated Acme。';
const hidden = '这次处理使用的资料当前无权读取或暂时无法验证，相关内容已隐藏。';
async function send(message, expected=answer) {
  await page.getByLabel('消息',{exact:true}).fill(message);
  const sent=page.waitForResponse(r=>r.request().method()==='POST' && /\/agent\/conversations\/[^/]+\/messages$/.test(new URL(r.url()).pathname));
  await page.getByRole('button',{name:'发送消息',exact:true}).click();
  const response=await sent;
  assert.equal(response.status(),202);
  const run=await response.json();
  await page.getByText(expected,{exact:false}).first().waitFor({timeout:180000});
  const final=await api(`/agent/conversations/${run.conversation_id}/runs/${run.id}`);
  assert.equal(final.status,200);
  assert.equal(final.body.status,'completed');
  const calls=(final.body.steps||[]).flatMap(s=>s.calls||[]);
  assert.equal(calls.length,expected===answer?5:0);
  assert(calls.every(c=>c.name==='report_query' && c.status==='completed'));
  evidence.runs.push({conversation:run.conversation_id,id:run.id,status:final.body.status,calls:calls.map(c=>({name:c.name,status:c.status,arguments:c.arguments,result_preview:c.result_preview}))});
  return run;
}
try {
  const deadline=Date.now()+600000;
  for (;;) {
    try {const r=await fetch(origin+'/app/session'); if(r.status===401||r.ok)break;}catch{}
    if(Date.now()>deadline)throw new Error('browser fixture not ready');
    await new Promise(r=>setTimeout(r,1000));
  }
  browser=await chromium.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true});
  page=await browser.newPage({viewport:{width:1440,height:1000}});
  page.on('pageerror',e=>evidence.pageErrors.push(e.message));
  await page.goto(origin);
  await page.getByLabel('账号',{exact:true}).fill('admin@example.com');
  await page.getByLabel('密码',{exact:true}).fill('Business-Browser-Changed!2026');
  await page.getByRole('button',{name:'登录',exact:true}).click();
  await createConversation('N01 浏览器报表查询');
  await send('发现当前报表，逐页读取客户名称并注明来源、固定行数上限与完整性。');
  await save('01-desktop-answer');
  const queries=page.locator('details.execution-tool');
  assert.equal(await queries.count(),5);
  await queries.last().locator('summary').click();
  await queries.last().getByText('"report_key": "customer_names"',{exact:false}).first().waitFor();
  await queries.last().locator('pre').last().evaluate(el=>{
    const text=el.firstChild;
    const index=text.textContent.indexOf('"source"');
    if(index<0)throw new Error('report source absent from rendered result');
    const range=document.createRange(); range.setStart(text,index); range.setEnd(text,index+8);
    for(let parent=el.parentElement;parent;parent=parent.parentElement){
      if(['auto','scroll'].includes(getComputedStyle(parent).overflowY) && parent.scrollHeight>parent.clientHeight){
        parent.scrollTop+=range.getBoundingClientRect().top-parent.getBoundingClientRect().top-80;
        break;
      }
    }
  });
  await save('02-report-source');
  evidence.phases.push('UI login/create/send; 2 catalog + 3 query pages; actual updated rows, source and bounded completeness');
  await page.reload();
  await page.getByText(answer,{exact:false}).first().waitFor();
  await control('restart');
  await page.reload();
  await page.getByText(answer,{exact:false}).first().waitFor();
  await page.getByText(answer,{exact:false}).first().scrollIntoViewIfNeeded();
  await save('03-after-restart');
  evidence.phases.push('full owner/module/Identity disk restart restores actual saved report answer');
  await control('restrict-fields');
  await refresh();
  await page.getByText(hidden,{exact:false}).first().waitFor();
  assert.equal(await page.getByText(answer,{exact:false}).count(),0);
  await save('04-fields-revoked');
  evidence.phases.push('field permission revoked hides prior answer and tool content');
  await control('restore');
  await refresh();
  await page.getByText(hidden,{exact:false}).first().waitFor();
  assert.equal(await page.getByText(answer,{exact:false}).count(),0);
  await createConversation('N01 恢复权限后重新查询');
  await send('重新查询当前客户名称报表并标明来源及范围。');
  await page.setViewportSize({width:390,height:844});
  await page.getByText(answer,{exact:false}).first().scrollIntoViewIfNeeded();
  await save('05-mobile-fresh-query');
  const geometry=await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth}));
  assert(geometry.scroll<=geometry.width,JSON.stringify(geometry));
  evidence.mobile=geometry;
  evidence.phases.push('restored permission requires fresh query; mobile 390px no document overflow');
  await page.setViewportSize({width:1440,height:1000});
  await control('revoke');
  await refresh();
  await createConversation('N01 无报表读取权限');
  await send('查询客户名称报表。','当前没有可用报表工具，未执行查询。');
  await save('06-read-revoked');
  evidence.phases.push('business read revoked removes report tool and does not claim successful execution');
  assert.deepEqual(evidence.pageErrors,[]);
  evidence.passed=true;
} catch(error) {
  evidence.passed=false;
  evidence.error=String(error.stack||error);
  if(page){await save('failure').catch(()=>{}); evidence.failureText=await page.locator('body').innerText().catch(()=>null);}
  process.exitCode=1;
} finally {
  await writeFile(new URL('result.json',directory),JSON.stringify(evidence,null,2)+'\n');
  await browser?.close();
  await fetch(origin+'/__acceptance/finish',{method:'POST'}).catch(()=>{});
}
console.log(JSON.stringify({passed:evidence.passed,phases:evidence.phases,error:evidence.error},null,2));
