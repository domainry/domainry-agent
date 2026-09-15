import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT;
assert.ok(origin&&output);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'}),errors=[],requests=[];
let transferRequest;
async function login(email,password,hash=''){
 const page=await browser.newPage({viewport:{width:1360,height:1000},reducedMotion:'reduce'});page.setDefaultTimeout(15000);
 page.on('pageerror',e=>errors.push(String(e)));page.on('request',r=>{
  requests.push({method:r.method(),url:r.url()});
  if(r.method()==='POST'&&/\/agent\/delegations\/[^/]+\/decisions$/.test(new URL(r.url()).pathname)){
   const body=r.postDataJSON();if(body?.action==='transfer')transferRequest=body;
  }
 });
 await page.goto(origin+(hash?'#'+hash:''));await page.getByLabel('账号',{exact:true}).fill(email);
 await page.getByLabel('密码',{exact:true}).fill(password);await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();return page;
}
const work=page=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
let page;
try{
 const managed=process.env.AGENT_UI_TRANSFER_PARTICIPANT==='1';
 page=await login(managed?'transfer-receiver@example.com':'system_administrator@example.com',managed?'Transfer-Receiver!26':process.env.AGENT_UI_PASSWORD,managed?'':process.env.AGENT_UI_CONVERSATION);
 await work(page).getByRole('button',{name:'转交工作',exact:true}).click();
 await work(page).getByLabel('接收 Agent',{exact:true}).selectOption({label:'接手 Agent'});
 await work(page).getByLabel('剩余工作',{exact:true}).fill('核对原时钟回执并完成剩余工作');
 await work(page).getByLabel('原因与处理意见',{exact:true}).fill('由独立接手人继续处理');
 if(process.env.AGENT_UI_TRANSFER_DEPENDENCY){
  await work(page).getByText('本次采用的上游要求：',{exact:true}).waitFor();
  assert.match(await work(page).locator('.peer-dependencies').last().innerText(),/需求 v1 \/ 约定 1 · 目标/);
 }
 await work(page).getByRole('button',{name:'提交转交工作',exact:true}).scrollIntoViewIfNeeded();
 await page.screenshot({path:join(output,'transfer-form.png'),fullPage:true});
 await work(page).getByRole('button',{name:'提交转交工作',exact:true}).click();
 await work(page).locator('summary').filter({hasText:'接单与转交记录（2 次）'}).click();
 const history=work(page).getByRole('list',{name:'接单历史'});
 await history.getByText('第 2 次 · 接手 Agent · 当前接收方',{exact:true}).waitFor();
 assert.equal(await history.locator('li').count(),2);
 await history.scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'transfer-history.png'),fullPage:true});
 await history.getByRole('button',{name:'查看原执行记录',exact:true}).click();
 const run=page.getByRole('dialog',{name:'处理记录',exact:true});
 await run.getByText('已保存的回复',{exact:true}).waitFor();assert.match(await run.innerText(),/time_now/);
 assert.equal(await run.getByRole('button',{name:'确认执行',exact:true}).count(),0);
 await page.screenshot({path:join(output,'original-shared-execution.png'),fullPage:true});
 await run.getByRole('button',{name:'关闭',exact:true}).click();
 const receiver=await login('transfer-receiver@example.com','Transfer-Receiver!26');
 await work(receiver).locator('summary').filter({hasText:'接单与转交记录（2 次）'}).click();
 const receivedHistory=work(receiver).getByRole('list',{name:'接单历史'});
 assert.equal(await receivedHistory.getByRole('button',{name:'查看第 1 次接单会话',exact:true}).count(),0);
 await receivedHistory.getByRole('button',{name:'查看原执行记录',exact:true}).click();
 await receiver.getByRole('dialog',{name:'处理记录',exact:true}).getByText('已保存的回复',{exact:true}).waitFor();
 await receiver.getByRole('dialog',{name:'处理记录',exact:true}).getByRole('button',{name:'关闭',exact:true}).click();
 const shared=work(receiver).getByRole('region',{name:'共享执行过程',exact:true});
 await shared.getByRole('button',{name:'查看共享执行过程',exact:true}).waitFor();
 await shared.scrollIntoViewIfNeeded();await receiver.screenshot({path:join(output,'receiver-original-publication.png'),fullPage:true});
 const original=await login('admin@example.com',process.env.AGENT_UI_PASSWORD);
 const controls=work(original).getByRole('region',{name:'共享执行过程',exact:true});
 await controls.getByRole('button',{name:'撤回执行共享',exact:true}).waitFor();
 await controls.scrollIntoViewIfNeeded();await original.screenshot({path:join(output,'former-executor-withdrawal.png'),fullPage:true});
 page=receiver;await receiver.setViewportSize({width:390,height:844});
 await receiver.waitForFunction(()=>{const el=document.querySelector('.collaboration-dialog'),r=el?.getBoundingClientRect();return r&&r.x>=0&&r.right<=innerWidth&&el.scrollWidth<=el.clientWidth+1;});
 await shared.scrollIntoViewIfNeeded();await receiver.screenshot({path:join(output,'receiver-mobile.png'),fullPage:true});
 assert.ok(requests.some(r=>r.method==='POST'&&/\/agent\/delegations\/[^/]+\/execution$/.test(new URL(r.url).pathname)));
 assert.deepEqual(errors,[]);
 assert.ok(transferRequest?.client_id);
 if(process.env.AGENT_UI_TRANSFER_DEPENDENCY){
  assert.deepEqual(transferRequest.dependencies,[{delegation_id:process.env.AGENT_UI_TRANSFER_DEPENDENCY,brief_version:1,agreement_revision:1,fields:['goal']}]);
 }
 await writeFile(join(output,'report.json'),JSON.stringify({status:'passed',page_errors:errors,transfer_request:transferRequest,scenarios:[managed?'real three-account participant UI transfer':'real three-account issuer UI transfer','immutable original and current assignment',managed?'manager scoped original execution':'issuer scoped original execution','recipient scoped original execution without private conversation control','recipient index includes original shared run','former executor retains own withdrawal control','390px modal bounds']},null,2));
}catch(e){if(page)await page.screenshot({path:join(output,'failure.png'),fullPage:true});await writeFile(join(output,'failure.txt'),String(e)+'\n'+errors.join('\n')+'\n'+(page?await page.locator('body').innerText():''));throw e;}
finally{await browser.close();}
