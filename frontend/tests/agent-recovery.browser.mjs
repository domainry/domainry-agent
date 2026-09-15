import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT,id=process.env.AGENT_UI_DELEGATION,agent=process.env.AGENT_UI_AGENT;
assert.ok(origin&&output&&id&&agent);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'}),page=await browser.newPage({viewport:{width:1360,height:1000}});page.setDefaultTimeout(20000);
const errors=[],submissions=[];let scope;
page.on('pageerror',error=>errors.push(String(error)));
page.on('request',request=>{if(request.headers()['x-agent-scope'])scope=request.headers()['x-agent-scope'];if(request.method()==='POST'&&request.url().endsWith(`/agent/delegations/${id}/decisions`))submissions.push(request.postDataJSON());});
const dialog=()=>page.getByRole('dialog',{name:'Agent 协作',exact:true}),detail=()=>dialog().getByRole('region',{name:'委派详情',exact:true});
try {
 await page.goto(origin+'#'+process.env.AGENT_UI_CONVERSATION);await page.getByLabel('账号',{exact:true}).fill('admin@example.com');await page.getByLabel('密码',{exact:true}).fill(process.env.AGENT_UI_PASSWORD);await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await detail().getByRole('heading',{name:'Recover this changed Agent assignment',exact:true}).waitFor();
 assert.match(await detail().innerText(),/整个工作用量/);const currentResponse=await page.request.get(`${origin}/agent/delegations/${id}`,{headers:{'X-Agent-Scope':scope}}),current=await currentResponse.json(),actionNames=await detail().getByRole('button').allTextContents();assert.equal(current.task?.error_code,'agent_changed');assert.ok(actionNames.includes('按当前配置重启'));await page.screenshot({path:join(output,'agent-recovery-budget-desktop.png'),fullPage:true});await detail().getByRole('button',{name:'按当前配置重启',exact:true}).click();
 assert.match(await detail().innerText(),/新的接单记录.*保留原执行、效果、来源和整个工作剩余预算/s);
 await detail().getByLabel('剩余工作',{exact:true}).fill('Continue the preserved assignment with the repaired configuration.');await detail().getByLabel('原因与处理意见',{exact:true}).fill('The Agent configuration is repaired and ready.');await detail().getByRole('button',{name:'提交按当前配置重启',exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'agent-recovery-form-desktop.png'),fullPage:true});
 const response=page.waitForResponse(r=>r.url().endsWith(`/agent/delegations/${id}/decisions`)&&r.request().method()==='POST');await detail().getByRole('button',{name:'提交按当前配置重启',exact:true}).click();const recovered=await response;assert.equal(recovered.status(),200,await recovered.text());
 const receipt=await recovered.json();assert.equal(receipt.to_agent_id,agent);assert.notEqual(receipt.conversation_id,process.env.AGENT_UI_OLD_CONVERSATION);assert.notEqual(receipt.task?.id,process.env.AGENT_UI_OLD_TASK);assert.equal(receipt.assignment_number,2);
 assert.equal(submissions.length,1);assert.equal(submissions[0].action,'transfer');assert.equal(submissions[0].transfer.agent_id,agent);
 await page.setViewportSize({width:390,height:844});await detail().scrollIntoViewIfNeeded();await page.waitForFunction(()=>{const el=document.querySelector('.collaboration-dialog'),r=el?.getBoundingClientRect();return r&&r.x>=0&&r.right<=innerWidth&&el.scrollWidth<=el.clientWidth+1;});await page.screenshot({path:join(output,'agent-recovery-mobile.png'),fullPage:true});
 await page.setViewportSize({width:1360,height:1000});await page.screenshot({path:join(output,'agent-recovery-desktop.png'),fullPage:true});
 assert.deepEqual(errors,[]);await writeFile(join(output,'agent-recovery-report.json'),JSON.stringify({realIdentityHTTPAndChrome:true,sameAgentImmutableRecovery:true,wholeWorkBudgetVisible:true,oldConversation:process.env.AGENT_UI_OLD_CONVERSATION,newConversation:receipt.conversation_id,oldTask:process.env.AGENT_UI_OLD_TASK,newTask:receipt.task?.id,submission:submissions[0],errors},null,2));
} catch(error){await page.screenshot({path:join(output,'agent-recovery-failure.png'),fullPage:true});throw error;} finally {await browser.close();}
