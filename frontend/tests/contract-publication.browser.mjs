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
const work=()=>page.getByRole('dialog',{name:'Agent 协作',exact:true}),section=()=>work().getByRole('region',{name:'重新共享原约定',exact:true}),form=()=>section().getByRole('form',{name:'原约定共享预览',exact:true});
async function get(path){const r=await page.request.get(`${origin}/agent/delegations/${id}${path}`,{headers:{'X-Agent-Scope':scope}});assert.equal(r.status(),200,await r.text());return r.json();}
async function share(allowed){const r=await page.request.post(`${origin}/fixture/contract-publication-share?allowed=${allowed}`);assert.equal(r.status(),204);}
async function prepare(){await section().getByRole('button',{name:'选择要共享的原约定',exact:true}).click();const pending=page.waitForResponse(r=>r.url().endsWith('/contract-publication')&&r.request().method()==='POST');await section().getByRole('button',{name:'核对原约定内容',exact:true}).click();const response=await pending;assert.equal(response.status(),200,await response.text());const preview=await response.json();await form().waitFor();return preview;}
try {
 await page.goto(origin+'#'+process.env.AGENT_UI_CONVERSATION);await page.getByLabel('账号',{exact:true}).fill('system_administrator@example.com');await page.getByLabel('密码',{exact:true}).fill(process.env.AGENT_UI_PASSWORD);await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await section().waitFor();
 const before=await get(''),agreements=await get('/requirements'),history=await get('/contract-publications');assert.equal(before.status,'accepted_delivery');
 const preview=await prepare();assert.match(await form().innerText(),/Read the actual current time/);assert.deepEqual(await get('/contract-publications'),history);
 await form().getByLabel('约定共享说明',{exact:true}).fill('明确共享原时钟委派约定与来源');await form().scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'contract-publication-desktop.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});await form().scrollIntoViewIfNeeded();await page.waitForFunction(()=>{const el=document.querySelector('.collaboration-dialog'),r=el?.getBoundingClientRect();return r&&r.x>=0&&r.right<=innerWidth&&el.scrollWidth<=el.clientWidth+1;});await page.screenshot({path:join(output,'contract-publication-mobile.png'),fullPage:true});await page.setViewportSize({width:1360,height:1000});
 let lost=false;await page.route(`**/agent/delegations/${id}/decisions`,async route=>{if(!lost){lost=true;const response=await route.fetch();assert.equal(response.status(),200,await response.text());await route.abort('failed');}else await route.continue();});
 await form().getByRole('button',{name:'确认共享原约定',exact:true}).click();await section().getByRole('button',{name:'按原请求重试约定共享',exact:true}).waitFor();
 await page.reload();await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await section().getByRole('button',{name:'按原请求重试约定共享',exact:true}).click();await section().getByRole('button',{name:'按原请求重试约定共享',exact:true}).waitFor({state:'hidden'});
 assert.equal(submissions.length,2);assert.deepEqual(submissions[0],submissions[1]);
 const after=await get(''),audit=await get('/contract-publications');assert.equal(audit.items.length,history.items.length+1);assert.deepEqual(audit.items[0].publisher,preview.publisher);assert.equal(audit.items[0].publisher.user_id,before.owner_user_id);assert.equal(audit.items[0].recipient_user_id,before.execution_subject.user_id);
 for(const key of ['status','agreement_revision','brief','delivery','verification','decision','task'])assert.deepEqual(after[key],before[key],key);assert.deepEqual(await get('/requirements'),agreements);
 await work().getByRole('region',{name:'约定共享记录',exact:true}).getByRole('button',{name:'查看约定共享记录',exact:true}).click();await work().getByText('明确共享原时钟委派约定与来源',{exact:true}).waitFor();
 await prepare();await share(false);await section().waitFor({state:'hidden'});
 const denied=await page.request.post(`${origin}/agent/delegations/${id}/contract-publication`,{headers:{'X-Agent-Scope':scope,Origin:origin},data:{agreement_revision:1}});assert.equal(denied.status(),403);
 await share(true);await section().waitFor();assert.equal(await form().count(),0);await prepare();await section().getByRole('button',{name:'收起约定预览',exact:true}).click();assert.equal(await form().count(),0);assert.deepEqual(errors,[]);
 await writeFile(join(output,'contract-publication-report.json'),JSON.stringify({realIdentityAndHTTP:true,originalAgreementPreview:true,previewWithoutGrant:true,acceptedOriginalUnchanged:true,explicitPublication:true,actualPublisherAudit:true,lostResponse:true,exactReloadRetry:true,singleAudit:true,shareWithdrawal:true,restorationClearsPreview:true,closeClearsPreview:true,narrowViewport:true,errors},null,2));
} finally {await browser.close();}
