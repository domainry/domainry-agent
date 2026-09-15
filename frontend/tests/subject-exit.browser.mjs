import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const {AGENT_UI_ORIGIN:origin,AGENT_UI_TEST_OUTPUT:output}=process.env;
assert.ok(origin&&output);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const page=await browser.newPage({viewport:{width:1360,height:1000}}),errors=[];
page.setDefaultTimeout(20000);page.on('pageerror',e=>errors.push(String(e)));
const work=()=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
const detail=()=>work().getByRole('region',{name:'委派详情',exact:true});
try{
 await page.goto(origin);await page.getByLabel('账号',{exact:true}).fill('system_administrator@example.com');await page.getByLabel('密码',{exact:true}).fill('Execution-Changed!26');await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 await work().getByRole('button',{name:/^执行主体已退出/}).first().click();await detail().getByText('执行主体已退出，相关工作已停止。原委派记录保留，执行内容和交付来源已不可读取。',{exact:true}).waitFor();
 assert.doesNotMatch(await work().innerText(),/RECEIVER-PRIVATE-PROFESSIONAL-TODO/);
 assert.equal(await detail().getByRole('button').count(),0);
 await detail().evaluate(section=>section.scrollIntoView({block:'center'}));await page.screenshot({path:join(output,'subject-exited.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});await detail().evaluate(section=>section.scrollIntoView({block:'center'}));await page.waitForFunction(()=>{const box=document.querySelector('[role="dialog"]')?.getBoundingClientRect();return box&&box.left>=0&&box.right<=innerWidth;});await page.screenshot({path:join(output,'subject-exited-mobile.png'),fullPage:true,animations:'disabled'});
 await page.setViewportSize({width:1360,height:1000});await page.reload();await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await work().getByRole('button',{name:/^执行主体已退出/}).first().click();await detail().getByRole('heading',{name:'执行主体已退出',exact:true}).waitFor();assert.equal(await detail().getByRole('button').count(),0);assert.deepEqual(errors,[]);
 await writeFile(join(output,'report.json'),JSON.stringify({status:'passed',page_errors:errors,scenarios:['real subject erasure persisted over host restart','retained delegated work has explicit exit state','deleted private tool content absent','no resume, execution inspection or republication controls','desktop and mobile modal bounds','browser reload retains exit metadata']},null,2));
}catch(error){await writeFile(join(output,'failure.json'),JSON.stringify({error:String(error),page_errors:errors,body:(await page.locator('body').innerText()).slice(-12000)},null,2));throw error;}
finally{await browser.close();}
