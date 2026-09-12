import {createRequire} from 'node:module';
import {writeFile, mkdir} from 'node:fs/promises';
import assert from 'node:assert/strict';
const require = createRequire(import.meta.url);
const {chromium} = require('/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright');
const origin = 'http://127.0.0.1:8093';
const directory = new URL(process.env.BROWSER_EVIDENCE || './browser/', import.meta.url);
await mkdir(directory, {recursive:true});
const evidence = {model:'in-process deterministic SDK model; real Report analysis/Identity/Runtime/Tools/Agent/SQLite', phases:[], runs:[], pageErrors:[]};
let browser, page;
const save = async name => { await page.screenshot({path:new URL(name+'.png',directory).pathname,fullPage:true}); };
async function api(path, method='GET', data={}) {
  return page.evaluate(async ({path,method,data})=>{
    const session = await (await fetch('/app/session')).json();
    const response = await fetch(path,{method,headers:{'X-Agent-Scope':session.scope,'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},...(method==='POST'?{body:JSON.stringify(data)}:{})});
    const body = response.status === 204 ? null : await response.json();
    return {status:response.status,body};
  },{path,method,data});
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
const answer = '分析已完成：3 条授权记录，总计 60，均值 20.000000（非空 3 条）。';
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
  assert.equal(calls.length,expected===answer?6:1);
  assert(calls.every(c=>c.name==='analysis_run' && (c.status==='completed'||c.status==='failed' && c.error_code==='backend.report.analysis.result_limit_exceeded')));
  evidence.runs.push({conversation:run.conversation_id,id:run.id,status:final.body.status,calls:calls.map(c=>({name:c.name,status:c.status,error_code:c.error_code,arguments:c.arguments,result_preview:c.result_preview,result_truncated:c.result_truncated,result_reference:c.result_reference}))});
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
  await createConversation('N02 浏览器数据分析');
  await send('读取当前获准数据集，执行汇总、对比、趋势和表格计算，保留方法与来源。');
  await save('01-desktop-answer');
  const queries=page.locator('details.execution-tool');
  assert.equal(await queries.count(),6);
  const latest=evidence.runs.at(-1);
  const operations=latest.calls.map(c=>JSON.parse(c.arguments));
  assert.deepEqual(operations.map(c=>c.operation),['catalog','run','run','run','run','run']);
  assert.deepEqual(operations.slice(1).map(c=>c.spec.mode),['aggregate','aggregate','compare','trend','table']);
  for(let index=2;index<latest.calls.length;index++){
    const row=queries.nth(index), call=latest.calls[index];
    await row.locator('summary').click();
    if(call.result_truncated){
      await row.getByRole('button',{name:'查看完整结果',exact:true}).click();
      const full=row.getByLabel('完整工具结果',{exact:true});
      await full.waitFor({timeout:120000});
      call.complete_result=JSON.parse(await full.innerText());
    } else call.complete_result={content:JSON.parse(call.result_preview)};
    const result=call.complete_result.content.result;
    assert.equal(result.source.complete,true);
    assert(result.source.proof && result.source.definition_version && result.source.data_version);
    assert(result.methods.length>0);
  }
  assert.equal(latest.calls[1].error_code,'backend.report.analysis.result_limit_exceeded');
  assert.equal(latest.calls[1].status,'failed');
  assert(!('result' in JSON.parse(latest.calls[1].result_preview)));
  const whole=latest.calls[2].complete_result.content.result;
  assert.equal(whole.source.input_counts.dataset,'3');
  assert.equal(whole.rows[0].values.total,'60');
  assert.equal(whole.rows[0].non_null_counts.mean,'3');
  const table=latest.calls.at(-1).complete_result.content.result;
  assert.deepEqual(table.rows.map(r=>r.values.doubled),['20.00','40.00','60.00']);
  assert.equal(table.rows.flatMap(r=>r.anomalies).length,2);
  await queries.last().getByText('"dataset_key": "customer"',{exact:false}).first().waitFor();
  await queries.nth(4).getByLabel('完整工具结果',{exact:true}).evaluate(el=>{
    const text=el.firstChild;
    const index=text.textContent.indexOf('"source"');
    if(index<0)throw new Error('analysis source absent from rendered result');
    const range=document.createRange(); range.setStart(text,index); range.setEnd(text,index+8);
    for(let parent=el.parentElement;parent;parent=parent.parentElement){
      if(['auto','scroll'].includes(getComputedStyle(parent).overflowY) && parent.scrollHeight>parent.clientHeight){
        parent.scrollTop+=range.getBoundingClientRect().top-parent.getBoundingClientRect().top-80;
        break;
      }
    }
  });
  await save('02-analysis-source');
  await queries.nth(4).locator('summary').click();
  await queries.nth(4).getByLabel('完整工具结果',{exact:true}).waitFor({state:'hidden'});
  await queries.nth(4).locator('summary').click();
  await queries.nth(4).getByRole('button',{name:'查看完整结果',exact:true}).waitFor();
  evidence.phases.push('closing result details clears complete data; reopening requires current authorization');
  evidence.phases.push('UI complete-result reads and SHA256 verification; login/create/send; catalog + explicit output-limit failure + corrected aggregate/compare/trend/table; actual numbers, counts, methods, rules and source versions');
  await page.reload();
  await page.getByText(answer,{exact:false}).first().waitFor();
  await control('restart');
  await page.reload();
  await page.getByText(answer,{exact:false}).first().waitFor();
  await page.getByText(answer,{exact:false}).first().scrollIntoViewIfNeeded();
  await save('03-after-restart');
  evidence.phases.push('full owner/module/Identity disk restart restores actual saved analysis answer');
  await control('restrict-fields');
  await refresh();
  await page.getByText(hidden,{exact:false}).first().waitFor();
  assert.equal(await page.getByText(answer,{exact:false}).count(),0);
  assert.equal(await page.getByLabel('完整工具结果',{exact:true}).count(),0);
  const old=latest.calls.at(-1).result_reference;
  const revoked=await api(`/agent/conversations/${old.conversation_id}/runs/${old.run_id}/result`,'POST',{reference:old,max_bytes:8192});
  assert.notEqual(revoked.status,200,'direct full-result read must reauthorize current source');
  evidence.revokedFullResult={status:revoked.status,body:revoked.body};
  await save('04-fields-revoked');
  evidence.phases.push('field permission revoked hides prior answer and tool content');
  await control('restore');
  await refresh();
  await page.getByText(hidden,{exact:false}).first().waitFor();
  assert.equal(await page.getByText(answer,{exact:false}).count(),0);
  await createConversation('N02 恢复权限后重新查询');
  await send('重新分析客户余额，保留统计口径、单位与来源。');
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
  await createConversation('N02 无分析读取权限');
  await send('分析客户余额。','当前没有可用客户数据集，未执行分析。');
  await save('06-read-revoked');
  evidence.phases.push('customer read revoked removes its dataset; one catalog request and no analysis execution');
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
