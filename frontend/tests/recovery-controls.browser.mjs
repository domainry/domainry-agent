import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT,id=process.env.AGENT_UI_DELEGATION;
assert.ok(origin&&output&&id);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'}),page=await browser.newPage({viewport:{width:1360,height:1000}});page.setDefaultTimeout(20000);
const errors=[],submissions=[];let scope;
page.on('pageerror',e=>errors.push(String(e)));
page.on('request',r=>{if(r.headers()['x-agent-scope'])scope=r.headers()['x-agent-scope'];if(r.method()==='POST'&&r.url().endsWith(`/agent/delegations/${id}/decisions`))submissions.push(r.postDataJSON());});
const work=()=>page.getByRole('dialog',{name:'Agent 协作',exact:true}),detail=()=>work().getByRole('region',{name:'委派详情',exact:true}),management=()=>detail().getByRole('region',{name:'委派管理',exact:true});
async function permissions(profile){const r=await page.request.post(`${origin}/fixture/recovery-permissions?profile=${profile}`);assert.equal(r.status(),204);}
async function get(){const r=await page.request.get(`${origin}/agent/delegations/${id}`,{headers:{'X-Agent-Scope':scope}});assert.equal(r.status(),200,await r.text());return r.json();}
async function decide(body,status){const r=await page.request.post(`${origin}/agent/delegations/${id}/decisions`,{headers:{'X-Agent-Scope':scope,Origin:origin},data:body});assert.equal(r.status(),status,await r.text());return r.json();}
async function submit(action,reason){await management().getByRole('button',{name:action,exact:true}).click();await management().getByLabel('管理原因',{exact:true}).fill(reason);const response=page.waitForResponse(r=>r.url().endsWith(`/agent/delegations/${id}/decisions`)&&r.request().method()==='POST');await management().getByRole('button',{name:`提交${action}`,exact:true}).click();const r=await response;assert.equal(r.status(),200,await r.text());return r.json();}
try {
 await page.goto(origin+'#'+process.env.AGENT_UI_CONVERSATION);await page.getByLabel('账号',{exact:true}).fill('system_administrator@example.com');await page.getByLabel('密码',{exact:true}).fill(process.env.AGENT_UI_PASSWORD);await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await detail().getByRole('heading',{name:'Protected recovery control goal',exact:true}).waitFor();
 // A form prepared while readable must be discarded at the denied boundary.
 await detail().getByRole('button',{name:'更新需求',exact:true}).click();await detail().getByLabel('原因与处理意见',{exact:true}).fill('stale readable form');
 await permissions('source-denied');await management().getByRole('button',{name:'暂停',exact:true}).waitFor();
 assert.doesNotMatch(await work().innerText(),/Protected recovery control goal|stale readable form|需求 v0/);
 assert.equal(await detail().getByLabel('原因与处理意见',{exact:true}).count(),0);
 for(const name of ['更新需求','更新输入资料','调整依赖','继续执行','查看实时执行与工具调用'])assert.equal(await detail().getByRole('button',{name,exact:true}).count(),0);
 const before=await get();assert.equal(before.contract_omitted,true);assert.equal(before.access.manage,true);assert.equal(before.task,undefined);assert.equal(before.delivery,undefined);
 const paused=await submit('暂停','来源失权后暂停原委派');assert.equal(paused.status,'paused');assert.equal(paused.contract_omitted,true);
 await management().getByRole('button',{name:'暂停',exact:true}).waitFor({state:'hidden'});
 assert.equal(await detail().getByRole('button',{name:'继续执行',exact:true}).count(),0);
 await decide({client_id:'stale-stop',expected_revision:before.revision,action:'cancel',reason:'stale revision must not change work'},409);
 await permissions('all');await detail().getByRole('heading',{name:'Protected recovery control goal',exact:true}).waitFor();await detail().getByRole('button',{name:'继续执行',exact:true}).waitFor();
 assert.equal(await detail().getByLabel('管理原因',{exact:true}).count(),0);
 await permissions('source-denied');await management().getByRole('button',{name:'取消委派',exact:true}).waitFor();
 await management().getByRole('button',{name:'取消委派',exact:true}).click();await management().getByLabel('管理原因',{exact:true}).fill('withdrawn manager form');
 await permissions('manage-denied');await management().getByLabel('管理原因',{exact:true}).waitFor({state:'hidden'});await management().getByRole('button',{name:'取消委派',exact:true}).waitFor({state:'hidden'});
 const denied=await get();assert.equal(denied.access.manage,false);await decide({client_id:'withdrawn-stop',expected_revision:denied.revision,action:'cancel',reason:'withdrawn manager must not change work'},403);
 assert.equal((await get()).revision,denied.revision);
 await permissions('source-denied');await management().getByRole('button',{name:'取消委派',exact:true}).waitFor();assert.equal(await management().getByLabel('管理原因',{exact:true}).count(),0);
 await management().getByRole('button',{name:'取消委派',exact:true}).click();await management().getByLabel('管理原因',{exact:true}).fill('资料不可读时取消原委派');
 await page.screenshot({path:join(output,'recovery-management-desktop.png'),fullPage:true});await page.setViewportSize({width:390,height:844});await management().scrollIntoViewIfNeeded();
 await page.waitForFunction(()=>{const el=document.querySelector('.collaboration-dialog'),r=el?.getBoundingClientRect();return r&&r.x>=0&&r.right<=innerWidth&&el.scrollWidth<=el.clientWidth+1;});await page.screenshot({path:join(output,'recovery-management-mobile.png'),fullPage:true});
 const completed=page.waitForResponse(r=>r.url().endsWith(`/agent/delegations/${id}/decisions`)&&r.request().method()==='POST');await management().getByRole('button',{name:'提交取消委派',exact:true}).click();const final=await completed;assert.equal(final.status(),200,await final.text());assert.equal((await final.json()).status,'cancelled');
 await management().getByLabel('管理原因',{exact:true}).waitFor({state:'hidden'});await management().getByRole('button',{name:'暂停',exact:true}).waitFor({state:'hidden'});await management().getByRole('button',{name:'取消委派',exact:true}).waitFor({state:'hidden'});
 assert.deepEqual(errors,[]);assert.equal(submissions.length,2);assert.deepEqual(submissions.map(s=>s.action),['pause','cancel']);
 await writeFile(join(output,'recovery-controls-report.json'),JSON.stringify({realIdentityAndHTTP:true,sourceWithdrawalHidesContent:true,readableFormCleared:true,pauseWithoutSourceRead:true,hiddenRequirementCannotResume:true,staleRevisionRejected:true,sourceRestoration:true,managerWithdrawalClosesForm:true,withdrawnManagerMutationRejected:true,restorationClearsForm:true,cancelWithoutSourceRead:true,narrowViewport:true,submissions,errors},null,2));
} catch(error){await page.screenshot({path:join(output,'recovery-controls-failure.png'),fullPage:true});throw error;} finally {await browser.close();}
