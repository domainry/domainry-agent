import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT;
await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const context=await browser.newContext({viewport:{width:1280,height:960}}),page=await context.newPage();
page.setDefaultTimeout(15000);
const report={steps:[],javascriptErrors:[]};
page.on('pageerror',error=>report.javascriptErrors.push(String(error)));
const step=name=>{report.steps.push(name);console.log('PASS '+name)};
const control=async name=>assert.equal((await fetch(origin+'/__acceptance/'+name,{method:'POST'})).status,204);
const dialog=p=>p.getByRole('dialog',{name:'工具设置',exact:true});
const toggle=(p,key='calculate')=>dialog(p).getByRole('switch',{name:key+' 工具开关',exact:true});
const login=async(p,email='admin@example.com')=>{await p.goto(origin);await p.getByLabel('账号',{exact:true}).fill(email);await p.getByLabel('密码',{exact:true}).fill('Changed-Accounts-Test!3');await p.getByRole('button',{name:'登录',exact:true}).click();await p.getByRole('button',{name:/^工具设置/}).waitFor();};
const open=async p=>{await p.getByRole('button',{name:/^工具设置/}).click();await dialog(p).waitFor();};
const refresh=async p=>{await dialog(p).getByRole('button',{name:'刷新设置',exact:true}).click();await dialog(p).getByText('正在读取工具设置…').waitFor({state:'hidden'});};
const api=(p,path,method='GET',body)=>p.evaluate(async({path,method,body})=>{const session=await(await fetch('/app/session')).json();const r=await fetch(path,{method,headers:{'Content-Type':'application/json','X-Agent-Scope':session.scope},body:body===undefined?undefined:JSON.stringify(body)});return {status:r.status,data:await r.json()};},{path,method,body});
const list=async p=>{const r=await api(p,'/tools/preferences');assert.equal(r.status,200);return r.data.items;};
async function expectToggle(p,checked){await p.waitForFunction(({checked})=>document.querySelector('[role="switch"][aria-label="calculate 工具开关"]')?.getAttribute('aria-checked')===String(checked),{checked});}
async function runCatalog(){
 const id=crypto.randomUUID();const {data:c}=await api(page,'/agent/conversations','POST',{client_id:id,title:id});const {data:r}=await api(page,`/agent/conversations/${c.id}/messages`,'POST',{client_message_id:id,message:'目录'});
 for(let i=0;i<100;i++){const {data:current}=await api(page,`/agent/conversations/${c.id}/runs/${r.id}`);if(current.status==='completed'){const {data:messages}=await api(page,`/agent/conversations/${c.id}/messages`);return messages.items.at(-1).content;}assert.notEqual(current.status,'failed');await new Promise(resolve=>setTimeout(resolve,50));}
 throw new Error('catalog run timeout');
}
try{
 await login(page);await open(page);await dialog(page).getByRole('button',{name:'启用管理员工具设置',exact:true}).click();await toggle(page).waitFor();
 assert.deepEqual((await list(page)).map(i=>i.key),['calculate','time_now']);
 assert.equal(await toggle(page).getAttribute('aria-checked'),'true');await dialog(page).getByText('连接或授权范围不可用',{exact:true}).waitFor();
 await toggle(page).click();await expectToggle(page,false);assert.ok(!(await runCatalog()).includes('calculate'));
 await page.waitForFunction(()=>{const s=document.querySelector('[role="switch"][aria-label="calculate 工具开关"]');const thumb=s?.querySelector('[data-slot="switch-thumb"]');return thumb&&thumb.getBoundingClientRect().left<=s.getBoundingClientRect().left+s.getBoundingClientRect().width/4;});
 await page.screenshot({path:join(output,'disabled-desktop.png')});
 step('administrator explicitly enables preference permission; selected catalog excludes other granted tools; disabling removes calculation from actual model input');

 await control('restart');await page.reload();await open(page);await expectToggle(page,false);
 const secondContext=await browser.newContext(),second=await secondContext.newPage();await login(second,'system_administrator@example.com');await open(second);await expectToggle(second,true);
 assert.equal((await list(second)).find(i=>i.key==='time_now').available,false);await secondContext.close();
 step('Agent and Integration reopen preserves disabled preference; another actual Identity user retains independent defaults');

 let dropped=false;
 await page.route('**/tools/preferences/calculate',async route=>{if(!dropped&&route.request().method()==='PUT'){dropped=true;const response=await route.fetch();assert.equal(response.status(),200);await route.abort('failed');}else await route.continue();});
 await toggle(page).click();await dialog(page).getByRole('alert').waitFor();assert.equal(await toggle(page).isDisabled(),true);
 await page.unroute('**/tools/preferences/calculate');await refresh(page);await expectToggle(page,true);
 const afterLost=(await list(page)).find(i=>i.key==='calculate');assert.equal(afterLost.revision,2);
 assert.ok((await runCatalog()).includes('calculate'));
 step('lost write response blocks further clicks until explicit refresh reads the committed revision; no automatic write replay');

 const input={enabled:false,expected_revision:afterLost.revision,tool_version:afterLost.version};
 const concurrent=await Promise.all([api(page,'/tools/preferences/calculate','PUT',input),api(page,'/tools/preferences/calculate','PUT',input)]);
 assert.deepEqual(concurrent.map(r=>r.status).sort(),[200,409]);await refresh(page);await expectToggle(page,false);
 await control('revoke-tool');await refresh(page);assert.equal(await toggle(page).count(),0);
 assert.equal((await api(page,'/tools/preferences/calculate','PUT',{...input,enabled:true,expected_revision:3})).status,403);
 await control('restore-tool');await refresh(page);await expectToggle(page,false);
 step('two concurrent writes produce one revision; current permission removal hides the row and rejects enable; restoring permission preserves the choice');

 await control('connect');await refresh(page);assert.equal((await list(page)).find(i=>i.key==='time_now').available,true);assert.ok((await runCatalog()).includes('time_now'));
 await control('revoke-account');await refresh(page);assert.equal((await list(page)).find(i=>i.key==='time_now').available,false);assert.ok(!(await runCatalog()).includes('time_now'));
 step('actual owner OAuth grant enables the configured account requirement; revocation removes the tool on the next model request');

 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>{const d=document.querySelector('[role="dialog"]');if(!d)return false;const b=d.getBoundingClientRect();return b.left>=0&&b.right<=innerWidth&&d.scrollWidth<=d.clientWidth+1;});
 await dialog(page).getByLabel('搜索工具',{exact:true}).fill('calculate');assert.equal(await dialog(page).locator('[data-tool-key]').count(),1);await toggle(page).click();await expectToggle(page,true);
 await page.waitForFunction(()=>{const s=document.querySelector('[role="switch"][aria-label="calculate 工具开关"]');const thumb=s?.querySelector('[data-slot="switch-thumb"]');return thumb&&thumb.getBoundingClientRect().left>s.getBoundingClientRect().left+s.getBoundingClientRect().width/4;});
 await page.screenshot({path:join(output,'mobile-settings.png')});
 step('390px viewport keeps dialog and switch in bounds; search and re-enable operate on the persisted current revision');
 assert.equal(report.javascriptErrors.length,0);
 report.complete=true;
}catch(error){report.error=String(error);await page.screenshot({path:join(output,'failure.png')});throw error;}
finally{await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));await browser.close();}
