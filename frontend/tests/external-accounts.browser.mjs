// Built shared client + actual Identity/Integration/Google Provider. Only vendor
// authorization and token issuer are isolated fixtures; no live account is used.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT;
assert.equal((await(await fetch(origin+'/app/config')).json()).workspace_id,'accounts-workspace');
await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const context=await browser.newContext({viewport:{width:1280,height:960}}),page=await context.newPage();
page.setDefaultTimeout(15000);
const report={steps:[],javascriptErrors:[],sensitiveReferrers:0};
page.on('pageerror',e=>report.javascriptErrors.push(String(e)));
page.on('request',r=>{if(/(?:[?&])(code|state)=/.test(r.headers().referer||''))report.sensitiveReferrers++;});
const step=name=>{report.steps.push(name);console.log('PASS '+name)};
const control=async(name,body)=>{
 const r=await fetch(`${origin}/__acceptance/${name}`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body||{})});
 assert.ok(r.ok,`fixture ${name}: ${r.status}`);return r.status===204?null:r.json();
};
const login=async(p,email='admin@example.com')=>{
 await p.goto(origin);await p.getByLabel('账号',{exact:true}).fill(email);await p.getByLabel('密码',{exact:true}).fill('Changed-Accounts-Test!3');
 await p.getByRole('button',{name:'登录',exact:true}).click();await p.getByRole('button',{name:/^外部账号/}).or(p.getByRole('dialog',{name:'外部账号',exact:true})).first().waitFor();
};
const dialog=p=>p.getByRole('dialog',{name:'外部账号',exact:true});
const accounts=()=>page.evaluate(async()=>{
 const s=await(await fetch('/app/session')).json();const r=await fetch('/integration/connection-accounts',{headers:{'X-Agent-Scope':s.scope}});return {status:r.status,data:await r.json()};
});
let outcome='connect', switchIdentity=false;
let callbackPosts=0;page.on('request',r=>{if(r.method()==='POST'&&r.url()===origin+'/integration/oauth-authorizations/callback')callbackPosts++});
await page.route('https://accounts.google.com/**',async route=>{
 const {callback}=await control('authorize',{url:route.request().url(),outcome});
 if(switchIdentity){switchIdentity=false;assert.equal((await page.request.post(origin+'/auth/logout',{headers:{Origin:origin},data:{}})).status(),204);assert.equal((await page.request.post(origin+'/auth/login',{headers:{Origin:origin},data:{login:'system_administrator@example.com',password:'Changed-Accounts-Test!3'}})).status(),200);}
 await route.fulfill({status:302,headers:{location:callback,'cache-control':'no-store'},body:''});
});
async function start(name, calendarOnly=false){
 await dialog(page).getByLabel('授权应用',{exact:true}).selectOption('work-google');
 await dialog(page).getByLabel('账号备注',{exact:true}).fill(name);
 if(calendarOnly) await dialog(page).getByRole('checkbox',{name:'openid',exact:true}).uncheck();
 await dialog(page).getByRole('button',{name:'前往授权',exact:true}).click();
}
async function clearPrompt(){
 await dialog(page).getByRole('button',{name:'关闭授权提示',exact:true}).click();
}
async function snapshot(name){await page.screenshot({path:join(output,name+'.png')});}
try{
 await login(page);await page.getByRole('button',{name:/^外部账号/}).click();
 await dialog(page).getByRole('button',{name:'启用管理员账号管理',exact:true}).click();
 await dialog(page).getByText('还没有可见的外部账号。',{exact:true}).waitFor();
 await dialog(page).getByRole('button',{name:'应用配置',exact:true}).click();
 const app=dialog(page).getByRole('region',{name:'OAuth 应用配置'});
 await app.getByLabel('配置标识',{exact:true}).fill('work-google');await app.getByLabel('显示名称',{exact:true}).fill('Google 工作账号');
 await app.getByLabel('Client ID',{exact:true}).fill('fixture-client');await app.getByLabel('Client Secret',{exact:true}).fill('fixture-client-secret');
 await app.getByLabel('允许请求的 Scopes（每行一个）',{exact:true}).fill('https://www.googleapis.com/auth/calendar.readonly\nopenid');
 await app.getByRole('button',{name:'保存应用配置',exact:true}).click();await app.getByText('应用配置已保存。',{exact:true}).waitFor();
 assert.equal(await app.getByLabel('Client Secret',{exact:true}).inputValue(),'');
 await app.getByRole('button',{name:'保存应用配置',exact:true}).click();await app.getByText('应用配置已保存。',{exact:true}).waitFor();
 const safe=await page.evaluate(async()=>{const s=await(await fetch('/app/session')).json();return(await fetch('/integration/oauth-applications',{headers:{'X-Agent-Scope':s.scope}})).text()});
 assert.ok(!safe.includes('fixture-client-secret'));await snapshot('application-configured');
 await dialog(page).getByRole('button',{name:'应用配置',exact:true}).click();
 step('administrator explicitly enables actions, configures owner application, and edits without reading or replacing its secret');
 await start('我的工作账号');await dialog(page).getByText('账号已连接',{exact:true}).waitFor();
 assert.equal(new URL(page.url()).search,'');
 const stored=await page.evaluate(()=>JSON.parse(sessionStorage.getItem('domainry:oauth:pending')));
 assert.deepEqual(Object.keys(stored).sort(),['id','returnHash','scope']);
 let list=await accounts();assert.equal(list.status,200);assert.equal(list.data.accounts.length,1);const first=list.data.accounts[0];assert.equal(first.scope,'personal');
 await page.reload();await dialog(page).getByText('账号已连接',{exact:true}).waitFor();
 await dialog(page).getByRole('button',{name:'测试连接',exact:true}).click();await dialog(page).getByText('「我的工作账号」连接测试通过。',{exact:true}).waitFor();
 await dialog(page).getByRole('button',{name:'撤销连接',exact:true}).click();await dialog(page).getByRole('button',{name:'保留连接',exact:true}).click();
 assert.equal((await accounts()).data.accounts[0].status,'active');await snapshot('personal-connected');
 step('real redirect callback binds current Identity, strips URL secrets, persists one personal account, survives reload and tests connection');
 const other=await browser.newContext(),otherPage=await other.newPage();await login(otherPage,'system_administrator@example.com');await otherPage.getByRole('button',{name:/^外部账号/}).click();
 await dialog(otherPage).getByText('还没有可见的外部账号。',{exact:true}).waitFor();
 const denied=await otherPage.evaluate(async key=>{const s=await(await fetch('/app/session')).json();const h={'X-Agent-Scope':s.scope};return [(await fetch('/integration/connection-accounts/'+key,{headers:h})).status,(await fetch('/integration/oauth-applications',{headers:h})).status];},first.key);
 assert.deepEqual(denied,[400,403]);await other.close();step('second real Identity user cannot see first personal account or administrator application config');
 await clearPrompt();outcome='reject';await start('拒绝的账号');await dialog(page).getByText('授权已拒绝',{exact:true}).waitFor();assert.equal((await accounts()).data.accounts.length,1);
 assert.equal((await control('state')).exchanges,1);await snapshot('authorization-rejected');await clearPrompt();
 step('provider denial is a durable rejected receipt and creates no account or token exchange');
 outcome='connect';switchIdentity=true;const postsBeforeSwitch=callbackPosts;
 await start('身份变化测试');await page.getByText('暂时无法完成授权。请返回工作空间，使用发起授权的账号核对状态；需要时重新授权。',{exact:true}).waitFor();
 assert.equal(callbackPosts,postsBeforeSwitch);assert.equal((await control('state')).exchanges,1);assert.equal(new URL(page.url()).search,'');
 await page.request.post(origin+'/auth/logout',{headers:{Origin:origin},data:{}});await login(page);await dialog(page).getByText('等待完成授权',{exact:true}).waitFor();await clearPrompt();
 step('changing Identity during vendor navigation prevents callback submission and token exchange; original owner can inspect the pending session');
 let dropped=0;
 await page.route(origin+'/integration/oauth-authorizations/callback',async route=>{dropped++;await route.fetch();await route.abort('failed');},{times:1});
 await start('失响应账号');await page.getByText('暂时无法完成授权。请返回工作空间，使用发起授权的账号核对状态；需要时重新授权。',{exact:true}).waitFor();
 assert.equal(new URL(page.url()).search,'');await page.getByRole('link',{name:'返回工作空间',exact:true}).click();await dialog(page).getByText('账号已连接',{exact:true}).waitFor();
 assert.equal(dropped,1);assert.equal((await accounts()).data.accounts.length,2);assert.equal((await control('state')).exchanges,2);await clearPrompt();
 await control('restart');await page.reload();await page.getByRole('button',{name:/^外部账号/}).click();await dialog(page).getByText('失响应账号',{exact:true}).waitFor();
 assert.equal((await control('state')).exchanges,2);await snapshot('lost-response-restarted');
 step('lost callback response recovers original receipt; reopening both persistent modules does not exchange code again');
 await control('revoke-list');await dialog(page).getByRole('button',{name:'刷新账号',exact:true}).click();await dialog(page).getByText('你当前没有这项账号操作的权限，请联系工作空间管理员。',{exact:true}).waitFor();
 assert.equal((await accounts()).status,403);await control('restart');assert.equal((await accounts()).status,403);await control('restore');await dialog(page).getByRole('button',{name:'刷新账号',exact:true}).click();await dialog(page).getByText('我的工作账号',{exact:true}).waitFor();
 step('live Identity revocation applies immediately and remains revoked across restart; explicit restoration restores list');
 const row=dialog(page).getByRole('listitem').filter({hasText:'我的工作账号'});
 await row.getByRole('button',{name:'撤销连接',exact:true}).click();await row.getByRole('button',{name:'确认撤销此连接',exact:true}).click();await row.getByText('个人 · 已撤销',{exact:true}).waitFor();
 await page.setViewportSize({width:390,height:844});await dialog(page).evaluate(el=>{el.scrollTop=0;el.scrollLeft=0});
 await page.waitForFunction(()=>{const el=document.querySelector('.external-accounts-dialog');if(!el)return false;const r=el.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth&&el.scrollWidth<=el.clientWidth+1});
 const layout=await dialog(page).evaluate(el=>{const r=el.getBoundingClientRect();return {left:r.left,right:r.right,viewport:innerWidth,content:el.scrollWidth,width:el.clientWidth}});
 assert.ok(layout.left>=0&&layout.right<=layout.viewport&&layout.content<=layout.width+1,JSON.stringify(layout));report.mobileLayout=layout;await snapshot('mobile-revoked');
 const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth);assert.equal(overflow,false);
 await control('restart');await page.setViewportSize({width:1280,height:960});await page.reload();await page.getByRole('button',{name:/^外部账号/}).click();await dialog(page).getByRole('listitem').filter({hasText:'我的工作账号'}).getByText('个人 · 已撤销',{exact:true}).waitFor();
 const final=await accounts();assert.equal(final.data.accounts.filter(a=>a.status==='revoked').length,1);assert.equal((await control('state')).exchanges,2);
 step('explicit revoke survives full reopen, remains scoped to selected account, and fits mobile viewport');
 await start('仅日历授权',true);await dialog(page).getByText('账号已连接',{exact:true}).waitFor();
 const calendarRow=dialog(page).getByRole('listitem').filter({hasText:'仅日历授权'});
 await calendarRow.getByText('当前授权不包含连接测试所需的账号资料范围。其他功能按各自授权范围使用。',{exact:false}).waitFor();
 assert.equal(await calendarRow.getByRole('button',{name:'测试连接',exact:true}).isDisabled(),true);
 const calendar=(await accounts()).data.accounts.find(a=>a.name==='仅日历授权');
 assert.equal(calendar.readiness.available,true);assert.equal(calendar.readiness.test.allowed,false);
 assert.deepEqual(calendar.readiness.granted_scopes,['https://www.googleapis.com/auth/calendar.readonly']);
 const probeBefore=(await control('state')).probes;
 const deniedProbe=await page.evaluate(async key=>{const s=await(await fetch('/app/session')).json();return (await fetch('/integration/connection-accounts/'+key+'/test',{method:'POST',headers:{'Content-Type':'application/json','X-Agent-Scope':s.scope,'Idempotency-Key':crypto.randomUUID()},body:'{}'})).status},calendar.key);
 assert.equal(deniedProbe,400);assert.equal((await control('state')).probes,probeBefore);
 await control('restart');await page.reload();await dialog(page).getByRole('listitem').filter({hasText:'仅日历授权'}).waitFor();
 assert.equal((await accounts()).data.accounts.find(a=>a.key===calendar.key).readiness.test.allowed,false);
 await page.setViewportSize({width:390,height:844});await calendarRow.scrollIntoViewIfNeeded();
 await page.waitForFunction(()=>{const el=document.querySelector('.external-accounts-dialog');return el && el.scrollWidth<=el.clientWidth+1});
 await snapshot('scope-limited-mobile');
 step('calendar-only consent stays available, probe scope explanation persists across restart, direct test is denied without provider I/O, no scopes are added');
 assert.deepEqual(report.javascriptErrors,[]);assert.equal(report.sensitiveReferrers,0);
 await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));
}catch(error){report.error=String(error);await snapshot('failure').catch(()=>{});await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));throw error}
finally{await browser.close()}
