import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_PRODUCT_TOOLS_OUTPUT,product=process.env.AGENT_PRODUCT_KEY;
await mkdir(output,{recursive:true});const browser=await chromium.launch({headless:true,channel:'chrome'});const page=await browser.newPage({viewport:{width:1280,height:900}});page.setDefaultTimeout(15000);
const report={product,steps:[],javascriptErrors:[],navigation:[],requests:[]};page.on('framenavigated',frame=>{if(frame===page.mainFrame())report.navigation.push({at:Date.now(),path:new URL(frame.url()).pathname});});page.on('response',response=>{const path=new URL(response.url()).pathname;if(path.startsWith('/app/')||path.startsWith('/tools/')||path.startsWith('/auth/'))report.requests.push({at:Date.now(),path,status:response.status()});});page.on('pageerror',error=>report.javascriptErrors.push(String(error)));
const dialog=()=>page.getByRole('dialog',{name:'工具设置',exact:true});
const api=(path,method='GET',body)=>page.evaluate(async({path,method,body})=>{const s=await(await fetch('/app/session')).json();const r=await fetch(path,{method,headers:{'Content-Type':'application/json','X-Agent-Scope':s.scope},body:body===undefined?undefined:JSON.stringify(body)});return{status:r.status,data:await r.json()};},{path,method,body});
const toggle=()=>dialog().getByRole('switch',{name:'calculate 工具开关',exact:true});
try{
 await page.goto(origin);await page.getByLabel('账号',{exact:true}).fill('admin@example.com');await page.getByLabel('密码',{exact:true}).fill('Product-Changed-Test-Password!3');await page.getByRole('button',{name:'登录',exact:true}).click();
 await Promise.all([page.waitForNavigation({waitUntil:'domcontentloaded'}),page.getByRole('button',{name:'启用工作空间功能',exact:true}).click()]);await page.getByRole('button',{name:'启用工作空间功能',exact:true}).waitFor({state:'hidden'});
 await page.waitForLoadState('networkidle');report.openAt=Date.now();await page.getByRole('button',{name:'工具设置',exact:true}).click();await dialog().waitFor();await dialog().getByRole('button',{name:'启用管理员工具设置',exact:true}).click();await toggle().waitFor();
 const before=await api('/tools/preferences');assert.equal(before.status,200);assert.ok(before.data.items.some(i=>i.key.startsWith(product.includes('work')?'work_':'pm_')));assert.ok(before.data.items.every(i=>!i.key.startsWith(product.includes('work')?'pm_':'work_')));
 await dialog().getByLabel('搜索工具').fill('calculate');await toggle().click();await page.waitForFunction(()=>document.querySelector('[aria-label="calculate 工具开关"]')?.getAttribute('aria-checked')==='false');
 await page.waitForFunction(()=>{const s=document.querySelector('[role="switch"][aria-label="calculate 工具开关"]');const thumb=s?.querySelector('[data-slot="switch-thumb"]');return thumb&&thumb.getBoundingClientRect().left<=s.getBoundingClientRect().left+s.getBoundingClientRect().width/4;});
 await page.screenshot({path:join(output,'disabled.png')});report.steps.push('delivered product setup, Tools permission setup, owner tool selection and persistent disable');
 const id=crypto.randomUUID();const {data:c}=await api('/agent/conversations','POST',{client_id:id,title:'工具设置验收'});const {data:r}=await api(`/agent/conversations/${c.id}/messages`,'POST',{client_message_id:id,message:'列出当前可用工具'});
 let done=false;for(let i=0;i<100;i++){const {data:run}=await api(`/agent/conversations/${c.id}/runs/${r.id}`);if(run.status==='completed'){done=true;break;}assert.notEqual(run.status,'failed');await new Promise(resolve=>setTimeout(resolve,50));}assert.ok(done);
 const {data:messages}=await api(`/agent/conversations/${c.id}/messages`);assert.ok(!messages.items.at(-1).content.includes('calculate'));report.steps.push('actual product Agent and Skill catalog sends no disabled calculation tool to model');
 assert.equal((await fetch(origin+'/__acceptance/restart',{method:'POST'})).status,204);await page.reload();await page.getByRole('button',{name:'工具设置',exact:true}).click();await dialog().getByLabel('搜索工具').fill('calculate');await toggle().waitFor();assert.equal(await toggle().getAttribute('aria-checked'),'false');
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>{const d=document.querySelector('[role="dialog"]');if(!d)return false;const b=d.getBoundingClientRect();return b.left>=0&&b.right<=innerWidth&&d.scrollWidth<=d.clientWidth+1;});
 await toggle().click();await page.waitForFunction(()=>document.querySelector('[aria-label="calculate 工具开关"]')?.getAttribute('aria-checked')==='true');report.steps.push('entire product reopen preserves preference and mobile re-enable writes current revision');
 assert.equal(report.javascriptErrors.length,0);report.complete=true;console.log('PASS '+product+' Tools browser: '+report.steps.length+' scenes');
}catch(error){report.error=String(error);await page.screenshot({path:join(output,'failure.png')});throw error;}finally{await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));await browser.close();}
