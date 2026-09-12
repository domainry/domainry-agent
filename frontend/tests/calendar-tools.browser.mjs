import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT;
assert.equal((await(await fetch(origin+'/app/config')).json()).workspace_id,'accounts-workspace');
await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const context=await browser.newContext({viewport:{width:1280,height:1000}}),page=await context.newPage();
page.setDefaultTimeout(30000);
const report={steps:[],javascriptErrors:[],snapshots:[]};
page.on('pageerror',error=>report.javascriptErrors.push(String(error)));
const step=name=>{report.steps.push(name);console.log('PASS '+name)};
const control=async name=>assert.equal((await fetch(origin+'/__acceptance/'+name,{method:'POST'})).status,204,name);
const api=(p,path)=>p.evaluate(async path=>{const session=await(await fetch('/app/session')).json();const r=await fetch(path,{headers:{'X-Agent-Scope':session.scope}});return {status:r.status,data:await r.json()};},path);
const login=async(p,email='admin@example.com')=>{await p.goto(origin);await p.getByLabel('账号',{exact:true}).fill(email);await p.getByLabel('密码',{exact:true}).fill('Changed-Accounts-Test!3');await p.getByRole('button',{name:'登录',exact:true}).click();await p.getByRole('button',{name:'新建会话',exact:true}).waitFor();};
const settings=p=>p.getByRole('dialog',{name:'工具设置',exact:true});
const openSettings=async p=>{if(await p.evaluate(()=>matchMedia('(max-width: 650px)').matches))await p.getByRole('button',{name:'打开工作导航',exact:true}).click();await p.getByRole('button',{name:/^工具设置/}).click();await settings(p).getByLabel('搜索工具',{exact:true}).fill('calendar_');await settings(p).getByText('正在读取工具设置…').waitFor({state:'hidden'});};
const refreshSettings=async p=>{await settings(p).getByRole('button',{name:'刷新设置',exact:true}).click();await settings(p).getByText('正在读取工具设置…').waitFor({state:'hidden'});};
const eventToggle=p=>settings(p).getByRole('switch',{name:'calendar_event 工具开关',exact:true});
const list=async p=>{const r=await api(p,'/tools/preferences');assert.equal(r.status,200);return r.data.items.filter(v=>v.key.startsWith('calendar_'));};
const state=async()=>{
 const id=new URL(page.url()).hash.slice(1),base='/agent/conversations/'+id;
 const c=await api(page,base),m=await api(page,base+'/messages');assert.equal(c.status,200);assert.equal(m.status,200);
 const runID=c.data.active_run_id||m.data.items.at(-1)?.run_id;
 const run=runID?(await api(page,base+'/runs/'+runID)).data:null;
 return {conversation:c.data,messages:m.data,run};
};
async function create(title,message){
 await page.getByRole('button',{name:'新建会话',exact:true}).click();const d=page.getByRole('dialog',{name:'新建会话',exact:true});await d.getByLabel('会话名称',{exact:true}).fill(title);await d.getByRole('button',{name:'保存',exact:true}).click();await d.waitFor({state:'hidden'});
 await page.getByRole('textbox',{name:'消息',exact:true}).fill(message);await page.getByRole('button',{name:'发送消息',exact:true}).click();
 const deadline=Date.now()+120000;
 while(Date.now()<deadline){const s=await state();if(s.run?.status==='completed'){await page.locator('.run-status').filter({hasText:/^已保存$/}).waitFor();report.snapshots.push({title,...s});return s;}assert.ok(!['failed','needs_reconciliation'].includes(s.run?.status),JSON.stringify(s.run));await new Promise(resolve=>setTimeout(resolve,400));}
 throw new Error('calendar conversation timeout');
}
async function expectHidden(id,refresh=true){
 const r=await api(page,'/agent/conversations/'+id+'/messages');assert.equal(r.status,200);const answer=r.data.items.find(v=>v.role==='assistant');assert.ok(answer.access_error);assert.ok(!answer.content.includes('CALENDAR-BODY'));
 if(refresh){await page.goto(origin+'/#'+id);await page.reload();}
 await page.waitForFunction(()=>{const main=document.querySelector('main');return main&&!main.innerText.includes('CALENDAR-BODY')});
 assert.ok(!(await page.locator('main').innerText()).includes('CALENDAR-BODY'));
}
try{
 await login(page);await openSettings(page);assert.equal((await list(page)).length,5);assert.ok((await list(page)).every(v=>!v.available));
 await control('connect');await refreshSettings(page);assert.ok((await list(page)).every(v=>v.available));await page.screenshot({path:join(output,'calendar-settings.png')});await page.keyboard.press('Escape');
 step('five product-selected calendar tools show unavailable before actual OAuth grant and available after owner authorization');

 const full=await create('日历安排与空闲','读取 2026-11-01 America/New_York 的完整日历安排、事件详情与共同空闲。');
 const answer=full.messages.items.find(v=>v.role==='assistant').content;
 for(const value of ['已读取 2 项安排','2026-11-01','2026-11-02','CALENDAR-BODY','2026-11-02T05:00:00Z'])assert.ok(answer.includes(value),value);
 const calls=full.run.steps.flatMap(v=>v.calls||[]);assert.deepEqual(calls.map(v=>v.name),['calendar_accounts','calendar_list','calendar_events','calendar_events','calendar_event','calendar_availability']);assert.ok(calls.every(v=>v.status==='completed'));
 assert.ok(calls[2].result_preview.includes('"date":"2026-11-01"'));assert.ok(calls[3].result_preview.includes('01:30:00-04:00'));assert.ok(calls[3].result_preview.includes('01:30:00-05:00'));
 await page.getByText(/事件详情：CALENDAR-BODY/).first().waitFor();await page.screenshot({path:join(output,'calendar-conversation.png')});
 await page.getByRole('button',{name:'查看处理记录',exact:true}).first().click();const history=page.getByRole('dialog',{name:'处理记录',exact:true});await history.getByText('读取事件详情',{exact:true}).waitFor();await history.getByRole('button',{name:'刷新记录',exact:true}).click();await history.getByText('查询共同空闲',{exact:true}).waitFor();await page.screenshot({path:join(output,'calendar-run.png')});await page.keyboard.press('Escape');
 step('actual browser message completes six persisted tool calls through Identity, Tools, Integration, Google protocol HTTP and model protocol HTTP; all-day dates, DST offsets and complete paging survive');

 await openSettings(page);await eventToggle(page).click();await page.waitForFunction(()=>document.querySelector('[aria-label="calendar_event 工具开关"]')?.getAttribute('aria-checked')==='false');await page.keyboard.press('Escape');
 await expectHidden(full.conversation.id,false);await control('restart');await page.reload();await openSettings(page);assert.equal(await eventToggle(page).getAttribute('aria-checked'),'false');await eventToggle(page).click();await page.waitForFunction(()=>document.querySelector('[aria-label="calendar_event 工具开关"]')?.getAttribute('aria-checked')==='true');await page.keyboard.press('Escape');await page.reload();await page.getByText(/事件详情：CALENDAR-BODY/).first().waitFor();
 step('disabling event details hides the retained reply; full host restart preserves choice; explicit re-enable reauthorizes the original account source');

 await control('revoke-read');await expectHidden(full.conversation.id);await control('restore-read');await page.reload();await page.getByText(/事件详情：CALENDAR-BODY/).first().waitFor();
 step('current Integration read permission independently controls previously saved calendar results');

 const secondContext=await browser.newContext(),other=await secondContext.newPage();await login(other,'system_administrator@example.com');await openSettings(other);assert.equal((await list(other)).length,5);assert.ok((await list(other)).every(v=>!v.available));assert.equal((await api(other,'/agent/conversations/'+full.conversation.id+'/messages')).status,404);await secondContext.close();
 step('another real Identity user cannot discover the personal calendar or read its conversation');

 await control('partial');const partial=await create('不完整的忙闲来源','读取当前窗口的日历，明确说明不完整的忙闲来源。');const partialAnswer=partial.messages.items.find(v=>v.role==='assistant').content;assert.ok(partialAnswer.includes('无法确认共同空闲'));assert.ok(!partialAnswer.includes('共同空闲（绝对时刻）'));const availability=JSON.parse(partial.run.steps.flatMap(v=>v.calls||[]).find(v=>v.name==='calendar_availability').result_preview).data;assert.equal(availability.complete,false);assert.deepEqual(availability.free,[]);
 await page.setViewportSize({width:390,height:844});await page.getByText(/忙闲来源不完整，无法确认共同空闲/).first().scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'partial-mobile.png')});
 step('a real partial provider response yields no free intervals; the mobile reply states that availability cannot be confirmed');

 await control('revoke-account');await expectHidden(full.conversation.id);await openSettings(page);assert.ok((await list(page)).every(v=>!v.available));await page.waitForFunction(()=>{const d=document.querySelector('[role="dialog"]');if(!d)return false;const b=d.getBoundingClientRect();return b.left>=0&&b.right<=innerWidth&&d.scrollWidth<=d.clientWidth+1});await page.screenshot({path:join(output,'revoked-mobile-settings.png')});
 step('owner account revocation hides retained calendar text and removes availability across all five tools; settings remain usable at 390px');
 assert.equal(report.javascriptErrors.length,0);report.complete=true;
}catch(error){report.error=String(error);report.lastState=await state().catch(e=>({error:String(e)}));await page.screenshot({path:join(output,'failure.png')}).catch(()=>{});throw error;}
finally{await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));await browser.close();}
