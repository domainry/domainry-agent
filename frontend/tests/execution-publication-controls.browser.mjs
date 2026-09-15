import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const {AGENT_UI_ORIGIN:origin,AGENT_UI_TEST_OUTPUT:output,AGENT_UI_READER_EMAIL:email,AGENT_UI_READER_PASSWORD:password}=process.env;
assert.ok(origin&&output&&email&&password);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const pages=await Promise.all(Array.from({length:2},async()=>{const context=await browser.newContext({viewport:{width:1360,height:1000}});return context.newPage();}));
const [executor,reader]=pages,errors=[];
for(const page of pages){page.setDefaultTimeout(20000);page.on('pageerror',e=>errors.push(String(e)));}
const work=page=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
const shared=page=>work(page).getByRole('region',{name:'共享执行过程',exact:true});
const inspection=()=>reader.getByRole('dialog',{name:'处理记录',exact:true});
async function open(page){await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await work(page).getByRole('button',{name:/^Read the actual current time/}).click();}
async function login(page,account,secret){await page.goto(origin);await page.getByLabel('账号',{exact:true}).fill(account);await page.getByLabel('密码',{exact:true}).fill(secret);await page.getByRole('button',{name:'登录',exact:true}).click();await open(page);}
async function permission(mode){const response=await executor.request.post(origin+'/fixture/execution-publication-permissions?mode='+mode);assert.equal(response.status(),204);}
async function refresh(page){await page.reload();await open(page);}
async function withdraw(reason){await shared(executor).getByLabel('执行共享说明',{exact:true}).fill(reason);await shared(executor).getByRole('button',{name:'撤回执行共享',exact:true}).click();await shared(executor).getByRole('button',{name:'撤回执行共享',exact:true}).waitFor({state:'hidden'});}
async function republish(){await permission('restore');await refresh(executor);await shared(executor).getByLabel('执行共享说明',{exact:true}).fill('恢复权限后明确重新共享原始运行');await shared(executor).getByRole('button',{name:'共享这次执行过程',exact:true}).click();await shared(executor).getByRole('button',{name:'撤回执行共享',exact:true}).waitFor();await shared(reader).getByRole('button',{name:'查看共享执行过程',exact:true}).waitFor();}
try{
 await login(executor,'admin@example.com','Source-Changed!26');await login(reader,email,password);
 await shared(reader).getByRole('button',{name:'查看共享执行过程',exact:true}).click();await inspection().getByLabel('工具执行记录',{exact:true}).waitFor();
 await permission('execution-denied');await shared(executor).getByText('当前只能撤回本人已发布的共享，执行内容暂不可查看。',{exact:true}).waitFor();
 assert.equal(await shared(executor).getByRole('button',{name:'查看共享执行过程',exact:true}).count(),0);assert.equal(await shared(executor).getByRole('button',{name:'共享这次执行过程',exact:true}).count(),0);
 await inspection().getByLabel('工具执行记录',{exact:true}).waitFor();
 await shared(executor).evaluate(section=>section.scrollIntoView({block:'center'}));await executor.screenshot({path:join(output,'withdraw-without-execution.png'),fullPage:true});
 await executor.setViewportSize({width:390,height:844});await executor.waitForFunction(()=>{const box=document.querySelector('[role="dialog"]')?.getBoundingClientRect();return box&&box.left>=0&&box.right<=innerWidth;});
 await shared(executor).evaluate(section=>section.scrollIntoView({block:'center'}));await executor.screenshot({path:join(output,'withdraw-without-execution-mobile.png'),fullPage:true,animations:'disabled'});
 await withdraw('已失去执行阅读，仍撤回本人发布的共享');await inspection().waitFor({state:'hidden'});
 await executor.setViewportSize({width:1360,height:1000});await republish();
 await permission('share-denied');await shared(reader).getByRole('button',{name:'查看共享执行过程',exact:true}).waitFor({state:'hidden'});await shared(executor).getByRole('button',{name:'共享这次执行过程',exact:true}).waitFor({state:'hidden'});
 await withdraw('已失去共享权限，仍撤回本人已有发布');await republish();
 await permission('view-denied');await shared(executor).waitFor({state:'hidden'});
 await permission('restore');await refresh(executor);await shared(executor).getByRole('button',{name:'撤回执行共享',exact:true}).waitFor();
 assert.deepEqual(errors,[]);await writeFile(join(output,'report.json'),JSON.stringify({status:'passed',page_errors:errors,scenarios:['owner metadata without execution reading','owner withdrawal without execution reading clears open third-user run','execution authority and independent result reading stay separate','owner withdrawal without share authority','current view withdrawal hides owner controls','restored permissions preserve explicit publication','desktop and mobile controls']},null,2));
}catch(error){await writeFile(join(output,'failure.json'),JSON.stringify({error:String(error),page_errors:errors,pages:await Promise.all(pages.map(async p=>(await p.locator('body').innerText()).slice(-12000)))},null,2));throw error;}
finally{await browser.close();}
