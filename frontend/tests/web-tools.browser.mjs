import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile,readFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT;
await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const context=await browser.newContext({viewport:{width:1280,height:1000}}),page=await context.newPage();
page.setDefaultTimeout(30000);
const report={steps:[],javascriptErrors:[],snapshots:[]};
page.on('pageerror',error=>report.javascriptErrors.push(String(error)));
const step=name=>{report.steps.push(name);console.log('PASS '+name)};
const control=async name=>assert.equal((await fetch(origin+'/__acceptance/'+name,{method:'POST'})).status,204,name);
const api=(p,path)=>p.evaluate(async path=>{const session=await(await fetch('/app/session')).json();const r=await fetch(path,{headers:{'X-Agent-Scope':session.scope}});const text=await r.text();return {status:r.status,data:JSON.parse(text)};},path);
const login=async(p,eweb='admin@example.com')=>{await p.goto(origin);await p.getByLabel('账号',{exact:true}).fill(eweb);await p.getByLabel('密码',{exact:true}).fill('Changed-Accounts-Test!3');await p.getByRole('button',{name:'登录',exact:true}).click();await p.getByRole('button',{name:'新建会话',exact:true}).waitFor();};
const nav=async(p,name)=>{if(await p.evaluate(()=>matchMedia('(max-width: 650px)').matches))await p.getByRole('button',{name:'打开工作导航',exact:true}).click();await p.getByRole('button',{name}).click();};
const settings=p=>p.getByRole('dialog',{name:'工具设置',exact:true});
const openSettings=async p=>{await nav(p,/^工具设置/);await settings(p).getByLabel('搜索工具',{exact:true}).fill('web_');await settings(p).getByText('正在读取工具设置…').waitFor({state:'hidden'});};
const refreshSettings=async p=>{await settings(p).getByRole('button',{name:'刷新设置',exact:true}).click();await settings(p).getByText('正在读取工具设置…').waitFor({state:'hidden'});};
const toggle=p=>settings(p).getByRole('switch',{name:'web_fetch 工具开关',exact:true});
const list=async p=>{const r=await api(p,'/tools/preferences');assert.equal(r.status,200);return r.data.items.filter(v=>v.key.startsWith('web_'));};
const state=async()=>{
 const id=new URL(page.url()).hash.slice(1),base='/agent/conversations/'+id;
 const c=await api(page,base),m=await api(page,base+'/messages');assert.equal(c.status,200);assert.equal(m.status,200);
 const runID=c.data.active_run_id||m.data.items.at(-1)?.run_id;
 return {conversation:c.data,messages:m.data,run:runID?(await api(page,base+'/runs/'+runID)).data:null};
};
async function create(title,message,write=true){
 await page.getByRole('button',{name:'新建会话',exact:true}).click();const d=page.getByRole('dialog',{name:'新建会话',exact:true});await d.getByLabel('会话名称',{exact:true}).fill(title);await d.getByRole('button',{name:'保存',exact:true}).click();await d.waitFor({state:'hidden'});
 if(write){await page.locator('.scope-settings > summary').click();await page.getByRole('checkbox',{name:'允许本次请求创建、修改或导出我的成果',exact:true}).check();}
 await page.getByRole('textbox',{name:'消息',exact:true}).fill(message);await page.getByRole('button',{name:'发送消息',exact:true}).click();
 const deadline=Date.now()+120000;
 while(Date.now()<deadline){const s=await state();if(s.run?.status==='completed'){await page.locator('.run-status').filter({hasText:/^已保存$/}).waitFor();report.snapshots.push({title,...s});return s;}assert.ok(!['failed','needs_reconciliation','waiting_interaction'].includes(s.run?.status),JSON.stringify(s.run));await new Promise(resolve=>setTimeout(resolve,400));}
 throw new Error('web conversation timeout');
}
async function expectHidden(id,refresh=true){
 const r=await api(page,'/agent/conversations/'+id+'/messages');assert.equal(r.status,200);const answer=r.data.items.find(v=>v.role==='assistant');assert.ok(answer.access_error);assert.ok(!answer.content.includes('WEB-BODY'));
 if(refresh){await page.goto(origin+'/#'+id);await page.reload();}
 await page.waitForFunction(()=>{const main=document.querySelector('main');return main&&!main.innerText.includes('WEB-BODY')});
}
const artifacts=()=>page.getByRole('dialog',{name:'我的成果',exact:true});
const openDraft=async()=>{await nav(page,/^我的成果/);await artifacts().getByRole('button',{name:/网页资料报告/}).click();await artifacts().getByRole('region',{name:'成果内容',exact:true}).waitFor();};
try{
 await login(page);await openSettings(page);assert.equal((await list(page)).length,2);assert.ok((await list(page)).every(v=>!v.available));
 await control('connect');await refreshSettings(page);assert.ok((await list(page)).every(v=>v.available));await page.screenshot({path:join(output,'web-settings.png')});await page.keyboard.press('Escape');
 step('two selected Web tools become available with workspace service registration and no OAuth');
 const full=await create('公开网页与资料报告','查询最新导出功能，保留来源，并保存、修改和导出资料报告。');
 const answer=full.messages.items.find(v=>v.role==='assistant').content;
 for(const value of ['WEB-BODY','https://example.com/releases','读取时间','页面完整性未知','版本 2'])assert.ok(answer.includes(value),value);
 const calls=full.run.steps.flatMap(v=>v.calls||[]);assert.deepEqual(calls.map(v=>v.name),['web_search','web_fetch','artifact_create','artifact_read','artifact_edit','artifact_export']);assert.ok(calls.every(v=>v.status==='completed'));
 await page.getByText(/资料摘要：/).first().waitFor();await page.screenshot({path:join(output,'web-conversation.png')});
 await page.getByRole('button',{name:'查看处理记录',exact:true}).first().click();const runDialog=page.getByRole('dialog',{name:'处理记录',exact:true});
 await runDialog.locator('details.execution-tool').filter({hasText:'搜索公开网页'}).locator('summary').click();
 await runDialog.locator('details.execution-tool').filter({hasText:'读取网页正文'}).locator('summary').click();
 await runDialog.getByText('页面完整性未知。',{exact:true}).waitFor();assert.ok((await runDialog.innerText()).includes('并非网页发布时间'));
 const source=runDialog.locator('.web-result a').first();assert.equal(await source.getAttribute('href'),'https://example.com/releases');assert.equal(await source.getAttribute('rel'),'noopener noreferrer');
 await page.screenshot({path:join(output,'web-run-sources.png')});await page.keyboard.press('Escape');
 step('six persisted tool calls drive source-bound report; result cards retain query, URLs, read time and unknown completeness');
 const stored=(await api(page,'/agent/artifacts')).data.items;assert.equal(stored.length,1);const draft=stored[0];assert.equal(draft.version,2);assert.equal(draft.source_conversation_id,full.conversation.id);assert.equal(draft.source_run_id,full.run.id);
 await openDraft();for(const s of ['WEB-BODY','https://example.com/releases','最新导出功能发布说明','unknown','读取时间并非发布时间'])assert.ok((await artifacts().innerText()).includes(s),s);
 await artifacts().getByRole('button',{name:'修改内容',exact:true}).click();const text=await artifacts().getByRole('textbox',{name:'成果正文',exact:true}).inputValue();await artifacts().getByRole('textbox',{name:'成果正文',exact:true}).fill(text+'\n人工补充：采用前再次核对来源。\n');await artifacts().getByRole('button',{name:'保存新版本',exact:true}).click();await artifacts().getByText('当前显示版本 3',{exact:true}).waitFor();
 const downloadPromise=page.waitForEvent('download');await artifacts().getByRole('button',{name:'下载此版本 Markdown',exact:true}).click();const download=await downloadPromise;const downloadPath=join(output,'web-report-v3.md');await download.saveAs(downloadPath);const exported=await readFile(downloadPath,'utf8');assert.ok(exported.includes('人工补充'));assert.ok(exported.includes('https://example.com/releases'));assert.ok(exported.includes('unknown'));await page.screenshot({path:join(output,'web-report.png')});await page.keyboard.press('Escape');
 step('real Knowledge artifact UI previews, edits v3 and downloads source-preserving Markdown');
 await openSettings(page);await toggle(page).click();await page.waitForFunction(()=>document.querySelector('[aria-label="web_fetch 工具开关"]')?.getAttribute('aria-checked')==='false');await page.keyboard.press('Escape');await expectHidden(full.conversation.id,false);assert.equal((await api(page,'/agent/artifacts/'+draft.id)).status,503);
 await control('restart');await page.reload();await openSettings(page);assert.equal(await toggle(page).getAttribute('aria-checked'),'false');await toggle(page).click();await page.waitForFunction(()=>document.querySelector('[aria-label="web_fetch 工具开关"]')?.getAttribute('aria-checked')==='true');await page.keyboard.press('Escape');await page.reload();await page.getByText(/资料摘要：/).first().waitFor();assert.equal((await api(page,'/agent/artifacts/'+draft.id)).data.artifact.version,3);
 step('tool revocation hides saved answer and report; full host restart preserves preference and authorized v3');
 await openDraft();await control('revoke-read');await artifacts().getByRole('button',{name:'刷新成果',exact:true}).click();await page.waitForFunction(()=>{const d=document.querySelector('[role="dialog"]');return d&&!d.innerText.includes('WEB-BODY')&&!d.querySelector('[aria-label="成果内容"]')});assert.equal((await api(page,'/agent/artifacts/'+draft.id)).status,503);await page.screenshot({path:join(output,'report-source-revoked.png')});await page.keyboard.press('Escape');await expectHidden(full.conversation.id);await control('restore-read');await page.reload();
 step('current Identity read revocation removes displayed report and blocks derived content');
 const second=await browser.newContext(),other=await second.newPage();await login(other,'system_administrator@example.com');await openSettings(other);assert.ok((await list(other)).every(v=>v.available));assert.equal((await api(other,'/agent/conversations/'+full.conversation.id+'/messages')).status,404);assert.equal((await api(other,'/agent/artifacts/'+draft.id)).status,404);await second.close();step('authorized workspace member can use shared Web service while private conversation and report remain isolated');
 await control('partial');const partial=await create('裁剪的网页正文','查询网页并保存报告');const partialAnswer=partial.messages.items.find(v=>v.role==='assistant').content;assert.ok(partialAnswer.includes('正文已裁剪'));assert.ok(!partialAnswer.includes('已保存'));assert.equal((await api(page,'/agent/artifacts')).data.items.length,1);await page.setViewportSize({width:390,height:844});await page.getByText(/网页正文已裁剪/).first().scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'partial-mobile.png')});step('bounded page explicitly reports truncation and creates no report at 390px');
 await control('revoke-account');await expectHidden(full.conversation.id);assert.equal((await api(page,'/agent/artifacts/'+draft.id)).status,503);await openSettings(page);assert.ok((await list(page)).every(v=>!v.available));await page.waitForFunction(()=>{const d=document.querySelector('[role="dialog"]');const b=d?.getBoundingClientRect();return b&&b.left>=0&&b.right<=innerWidth&&d.scrollWidth<=d.clientWidth+1});await page.screenshot({path:join(output,'revoked-mobile-settings.png')});await page.keyboard.press('Escape');
 step('revoked workspace connection makes both tools unavailable and denies historical sources');
 assert.equal(report.javascriptErrors.length,0);report.complete=true;
}catch(error){report.error=String(error);report.lastState=await state().catch(e=>({error:String(e)}));await page.screenshot({path:join(output,'failure.png')}).catch(()=>{});throw error;}
finally{await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));await browser.close();}
