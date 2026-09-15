import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT,id=process.env.AGENT_UI_DELEGATION;
assert.ok(origin&&output&&id);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const page=await browser.newPage({viewport:{width:1360,height:1000}});page.setDefaultTimeout(20000);
const errors=[],submissions=[],previews=[];let scope;
page.on('pageerror',e=>errors.push(String(e)));
page.on('request',r=>{if(r.headers()['x-agent-scope'])scope=r.headers()['x-agent-scope'];if(r.method()==='POST'&&r.url().endsWith(`/agent/delegations/${id}/decisions`))submissions.push(r.postDataJSON());if(r.method()==='POST'&&r.url().endsWith('/delivery-publication'))previews.push(r.postDataJSON());});
const work=()=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
const section=()=>work().getByRole('region',{name:'重新共享原交付',exact:true});
const form=()=>section().getByRole('form',{name:'原交付共享预览',exact:true});
async function history(){const r=await page.request.get(`${origin}/agent/delegations/${id}/deliveries`,{headers:{'X-Agent-Scope':scope}});const body=await r.text();assert.equal(r.status(),200,body);return JSON.parse(body);}
async function share(allowed){const r=await page.request.post(`${origin}/fixture/delivery-publication-share?allowed=${allowed}`);assert.equal(r.status(),204);}
try {
 await page.goto(origin+'#'+process.env.AGENT_UI_CONVERSATION);
 await page.getByLabel('账号',{exact:true}).fill('admin@example.com');await page.getByLabel('密码',{exact:true}).fill(process.env.AGENT_UI_PASSWORD);await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 await section().getByRole('button',{name:'选择要共享的原交付',exact:true}).click();
 await section().getByLabel('原交付版本',{exact:true}).waitFor();
 const options=await section().getByLabel('原交付版本',{exact:true}).locator('option').evaluateAll(items=>items.map(x=>({value:x.value,label:x.textContent})));
 const accepted=options.find(x=>x.label.includes('验收通过'));assert.ok(accepted,JSON.stringify(options));
 await section().getByLabel('原交付版本',{exact:true}).selectOption(accepted.value);
 const before=await history();await section().getByRole('button',{name:'预览原交付',exact:true}).click();await form().waitFor();
 assert.equal(previews[0].delivery_revision,Number(accepted.value));assert.equal((await history()).items.length,before.items.length);
 await form().getByLabel('原交付共享说明',{exact:true}).fill('明确重新共享已验收的原时钟回执');
 await form().scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'delivery-publication-desktop.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});await form().scrollIntoViewIfNeeded();
 await page.waitForFunction(()=>{const el=document.querySelector('.collaboration-dialog');const r=el?.getBoundingClientRect();return r&&r.x>=0&&r.right<=innerWidth&&el.scrollWidth<=el.clientWidth+1;});
 await page.screenshot({path:join(output,'delivery-publication-mobile.png'),fullPage:true});await page.setViewportSize({width:1360,height:1000});
 // Commit really succeeds, but its first response is lost. Retry must keep
 // the original CAS revision, exact selected record and client ID.
 let lost=false;
 await page.route(`**/agent/delegations/${id}/decisions`,async route=>{if(!lost){lost=true;const response=await route.fetch();assert.equal(response.status(),200);await route.abort('failed');}else await route.continue();});
 await form().getByRole('button',{name:'确认共享原交付',exact:true}).click();
 await section().getByRole('button',{name:'重试上次共享',exact:true}).waitFor();
 await page.reload();await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 await section().getByRole('button',{name:'重试上次共享',exact:true}).click();
 await section().getByRole('button',{name:'重试上次共享',exact:true}).waitFor({state:'hidden'});
 assert.equal(submissions.length,2);assert.deepEqual(submissions[1],submissions[0]);
 const after=await history();assert.equal(after.items.length,before.items.length+1);assert.equal(after.items[0].kind,'republish_delivery');assert.equal(after.items[0].publication.delivery_revision,Number(accepted.value));
 for(const original of before.items)assert.deepEqual(after.items.find(x=>x.revision===original.revision),original);
 // Older unverified publication receipts serialize assessment arrays as null.
 // Replay that DTO shape after the real authorized history response.
 await page.route(`**/agent/delegations/${id}/deliveries`,async route=>{const response=await route.fetch();const data=await response.json();const publication=data.items.find(x=>x.publication);if(publication){publication.verification.checks=null;publication.verification.blockers=null;}await route.fulfill({response,json:data});});
 await work().getByRole('button',{name:'查看交付与验收历史',exact:true}).click();
 const renderedHistory=work().getByRole('list',{name:'交付与验收历史',exact:true});await renderedHistory.waitFor();await renderedHistory.locator('summary').first().click();
 assert.match(await renderedHistory.innerText(),new RegExp(`发布用户 admin · 角色 admin · 原记录 ${accepted.value}`));
 const d=await (await page.request.get(`${origin}/agent/delegations/${id}`,{headers:{'X-Agent-Scope':scope}})).json();assert.equal(d.status,'accepted_delivery');
 await section().getByRole('button',{name:'选择要共享的原交付',exact:true}).click();await section().getByRole('button',{name:'预览原交付',exact:true}).click();await form().waitFor();
 await share(false);await section().waitFor({state:'hidden'});
 const denied=await page.request.post(`${origin}/agent/delegations/${id}/delivery-publication`,{headers:{'X-Agent-Scope':scope,Origin:origin},data:{delivery_revision:Number(accepted.value)}});assert.equal(denied.status(),403);
 await share(true);await section().waitFor();assert.equal(await form().count(),0);
 await section().getByRole('button',{name:'选择要共享的原交付',exact:true}).click();await section().getByRole('button',{name:'预览原交付',exact:true}).click();await form().waitFor();
 await section().getByRole('button',{name:'收起共享预览',exact:true}).click();assert.equal(await form().count(),0);
 assert.deepEqual(errors,[]);
 await writeFile(join(output,'delivery-publication-report.json'),JSON.stringify({acceptedHistory:true,previewWithoutGrant:true,explicitPublication:true,lostResponse:true,reloadRetry:true,exactRetryIdentity:true,singleAudit:true,originalRecordsUnchanged:true,actualPublisherAuditVisible:true,legacyEmptyAssessmentArrays:true,currentShareRevocation:true,restoration:true,closeClears:true,narrowViewport:true,errors},null,2));
} finally {await browser.close();}
