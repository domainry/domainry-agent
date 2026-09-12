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
const login=async(p,email='admin@example.com')=>{await p.goto(origin);await p.getByLabel('账号',{exact:true}).fill(email);await p.getByLabel('密码',{exact:true}).fill('Changed-Accounts-Test!3');await p.getByRole('button',{name:'登录',exact:true}).click();await p.getByRole('button',{name:'新建会话',exact:true}).waitFor();};
const nav=async(p,name)=>{if(await p.evaluate(()=>matchMedia('(max-width: 650px)').matches))await p.getByRole('button',{name:'打开工作导航',exact:true}).click();await p.getByRole('button',{name}).click();};
const settings=p=>p.getByRole('dialog',{name:'工具设置',exact:true});
const openSettings=async p=>{await nav(p,/^工具设置/);await settings(p).getByLabel('搜索工具',{exact:true}).fill('mail_');await settings(p).getByText('正在读取工具设置…').waitFor({state:'hidden'});};
const refreshSettings=async p=>{await settings(p).getByRole('button',{name:'刷新设置',exact:true}).click();await settings(p).getByText('正在读取工具设置…').waitFor({state:'hidden'});};
const toggle=p=>settings(p).getByRole('switch',{name:'mail_read 工具开关',exact:true});
const list=async p=>{const r=await api(p,'/tools/preferences');assert.equal(r.status,200);return r.data.items.filter(v=>v.key.startsWith('mail_'));};
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
 throw new Error('mail conversation timeout');
}
async function expectHidden(id,refresh=true){
 const r=await api(page,'/agent/conversations/'+id+'/messages');assert.equal(r.status,200);const answer=r.data.items.find(v=>v.role==='assistant');assert.ok(answer.access_error);assert.ok(!answer.content.includes('MAIL-BODY'));
 if(refresh){await page.goto(origin+'/#'+id);await page.reload();}
 await page.waitForFunction(()=>{const main=document.querySelector('main');return main&&!main.innerText.includes('MAIL-BODY')});
}
const artifacts=()=>page.getByRole('dialog',{name:'我的成果',exact:true});
const openDraft=async()=>{await nav(page,/^我的成果/);await artifacts().getByRole('button',{name:/预算回复草稿（未发送）/}).click();await artifacts().getByRole('region',{name:'成果内容',exact:true}).waitFor();};
try{
 await login(page);await openSettings(page);assert.equal((await list(page)).length,4);assert.ok((await list(page)).every(v=>!v.available));
 await control('connect');await refreshSettings(page);assert.ok((await list(page)).every(v=>v.available));await page.screenshot({path:join(output,'mail-settings.png')});await page.keyboard.press('Escape');
 step('four product-selected mail tools become available after actual owner OAuth grant');
 const full=await create('预算邮件与回复草稿','搜索预算邮件，提取待处理事项，并保存、修改和导出回复草稿。');
 const answer=full.messages.items.find(v=>v.role==='assistant').content;
 for(const value of ['已搜索 2 封邮件','MAIL-BODY','3200','2026-09-14','负责人和承诺待确认','版本 2','未发送','budget-mail-1@example.com'])assert.ok(answer.includes(value),value);
 const calls=full.run.steps.flatMap(v=>v.calls||[]);assert.deepEqual(calls.map(v=>v.name),['mail_accounts','mail_list','mail_search','mail_search','mail_read','artifact_create','artifact_read','artifact_edit','artifact_export']);assert.ok(calls.every(v=>v.status==='completed'));
 await page.getByText(/已搜索 2 封邮件/).first().waitFor();await page.screenshot({path:join(output,'mail-conversation.png')});
 await page.getByRole('button',{name:'查看处理记录',exact:true}).first().click();await page.getByRole('dialog',{name:'处理记录',exact:true}).getByText('读取邮件正文',{exact:true}).waitFor();await page.screenshot({path:join(output,'mail-run.png')});await page.keyboard.press('Escape');
 step('browser submits authorized request; two search pages and body drive summary, extracted action and nine persisted tool calls including local draft create/read/edit/export');
 const stored=(await api(page,'/agent/artifacts')).data.items;assert.equal(stored.length,1);const draft=stored[0];assert.equal(draft.version,2);assert.equal(draft.source_conversation_id,full.conversation.id);assert.equal(draft.source_run_id,full.run.id);
 await openDraft();for(const s of ['MAIL-BODY','thread-budget','budget@example.com','budget-mail-1@example.com','未发送'])assert.ok((await artifacts().innerText()).includes(s),s);
 await artifacts().getByRole('button',{name:'修改内容',exact:true}).click();const text=await artifacts().getByRole('textbox',{name:'成果正文',exact:true}).inputValue();await artifacts().getByRole('textbox',{name:'成果正文',exact:true}).fill(text+'\n人工补充：回复前再次确认预算。\n');await artifacts().getByRole('button',{name:'保存新版本',exact:true}).click();await artifacts().getByText('当前显示版本 3',{exact:true}).waitFor();
 const downloadPromise=page.waitForEvent('download');await artifacts().getByRole('button',{name:'下载此版本 Markdown',exact:true}).click();const download=await downloadPromise;const downloadPath=join(output,'reply-draft-v3.md');await download.saveAs(downloadPath);const exported=await readFile(downloadPath,'utf8');assert.ok(exported.includes('人工补充'));assert.ok(exported.includes('budget-mail-1@example.com'));assert.ok(exported.includes('未发送'));await page.screenshot({path:join(output,'mail-draft.png')});await page.keyboard.press('Escape');
 step('Knowledge draft retains source run and original mail references; real UI previews, edits v3 and downloads exact Markdown');
 await openSettings(page);await toggle(page).click();await page.waitForFunction(()=>document.querySelector('[aria-label="mail_read 工具开关"]')?.getAttribute('aria-checked')==='false');await page.keyboard.press('Escape');await expectHidden(full.conversation.id,false);assert.equal((await api(page,'/agent/artifacts/'+draft.id)).status,503);
 await control('restart');await page.reload();await openSettings(page);assert.equal(await toggle(page).getAttribute('aria-checked'),'false');await toggle(page).click();await page.waitForFunction(()=>document.querySelector('[aria-label="mail_read 工具开关"]')?.getAttribute('aria-checked')==='true');await page.keyboard.press('Escape');await page.reload();await page.getByText(/已搜索 2 封邮件/).first().waitFor();assert.equal((await api(page,'/agent/artifacts/'+draft.id)).data.artifact.version,3);
 step('disabling mail body hides saved answer and derived draft; host restart preserves choice and explicit re-enable restores authorized v3');
 await openDraft();await control('revoke-read');await artifacts().getByRole('button',{name:'刷新成果',exact:true}).click();await page.waitForFunction(()=>{const d=document.querySelector('[role="dialog"]');return d&&!d.innerText.includes('MAIL-BODY')&&!d.querySelector('[aria-label="成果内容"]')});assert.equal((await api(page,'/agent/artifacts/'+draft.id)).status,503);await page.screenshot({path:join(output,'draft-source-revoked.png')});await page.keyboard.press('Escape');await expectHidden(full.conversation.id);await control('restore-read');await page.reload();
 step('current Identity read revocation removes a displayed draft on refresh and prevents source-dependent reads');
 const second=await browser.newContext(),other=await second.newPage();await login(other,'system_administrator@example.com');await openSettings(other);assert.ok((await list(other)).every(v=>!v.available));assert.equal((await api(other,'/agent/conversations/'+full.conversation.id+'/messages')).status,404);assert.equal((await api(other,'/agent/artifacts/'+draft.id)).status,404);await second.close();step('another Identity user cannot discover the personal mailbox or read the conversation and draft');
 await control('partial');const partial=await create('缺少正文的邮件','整理不完整邮件并保存草稿');const partialAnswer=partial.messages.items.find(v=>v.role==='assistant').content;assert.ok(partialAnswer.includes('正文不完整'));assert.ok(!partialAnswer.includes('已保存'));assert.equal((await api(page,'/agent/artifacts')).data.items.length,1);await page.setViewportSize({width:390,height:844});await page.getByText(/邮件正文不完整/).first().scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'partial-mobile.png')});step('missing provider body yields explicit incomplete answer and creates no additional draft at 390px');
 await control('revoke-account');await expectHidden(full.conversation.id);assert.equal((await api(page,'/agent/artifacts/'+draft.id)).status,503);await openSettings(page);assert.ok((await list(page)).every(v=>!v.available));await control('basic');await refreshSettings(page);const basic=await list(page);for(const v of basic)assert.equal(v.available,['mail_accounts','mail_list'].includes(v.key),v.key);await page.waitForFunction(()=>{const d=document.querySelector('[role="dialog"]');const b=d?.getBoundingClientRect();return b&&b.left>=0&&b.right<=innerWidth&&d.scrollWidth<=d.clientWidth+1});await page.screenshot({path:join(output,'basic-mobile-settings.png')});await page.keyboard.press('Escape');
 // The mobile navigation collapses after choosing a dialog, so use desktop for another composer submission.
 await page.setViewportSize({width:1280,height:1000});const headers=await create('仅邮件头授权','总结邮件并创建回复草稿');assert.ok(headers.messages.items.find(v=>v.role==='assistant').content.includes('仅有邮件头权限'));assert.deepEqual(headers.run.steps.flatMap(v=>v.calls||[]).map(v=>v.name),['mail_accounts','mail_list']);
 step('revoked account no longer exposes derived drafts; fresh metadata-only OAuth enables headers and excludes search/body/drafting');
 assert.equal(report.javascriptErrors.length,0);report.complete=true;
}catch(error){report.error=String(error);report.lastState=await state().catch(e=>({error:String(e)}));await page.screenshot({path:join(output,'failure.png')}).catch(()=>{});throw error;}
finally{await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));await browser.close();}
