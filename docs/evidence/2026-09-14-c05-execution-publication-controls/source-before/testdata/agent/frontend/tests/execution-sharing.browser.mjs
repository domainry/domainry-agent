import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const {AGENT_UI_ORIGIN:origin,AGENT_UI_TEST_OUTPUT:output,AGENT_UI_CONVERSATION:source,AGENT_UI_READER_EMAIL:email,AGENT_UI_READER_PASSWORD:password}=process.env;
assert.ok(origin&&output&&source&&email&&password);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const pages=await Promise.all(Array.from({length:3},async()=>{const context=await browser.newContext({viewport:{width:1360,height:1000}});return context.newPage();}));
const [executor,issuer,reader]=pages,errors=[];
for(const page of pages){page.setDefaultTimeout(20000);page.on('pageerror',e=>errors.push(String(e)));}
const work=page=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
const shared=page=>work(page).getByRole('region',{name:'共享执行过程',exact:true});
const inspection=()=>reader.getByRole('dialog',{name:'处理记录',exact:true});
async function login(page,account,secret,hash=''){
 await page.goto(origin+hash);await page.getByLabel('账号',{exact:true}).fill(account);await page.getByLabel('密码',{exact:true}).fill(secret);await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await work(page).getByRole('button',{name:/^Read the actual current time/}).click();
}
async function inspect(){await shared(reader).getByRole('button',{name:'查看共享执行过程',exact:true}).click();await inspection().getByLabel('工具执行记录',{exact:true}).waitFor();await inspection().locator('details.execution-tool').first().locator('summary').click();}
async function scope(checked,reason){await issuer.reload();await issuer.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await work(issuer).getByRole('button',{name:/^Read the actual current time/}).click();const members=work(issuer).getByRole('region',{name:'委派参与人',exact:true});await members.getByRole('button',{name:'管理参与人',exact:true}).click();await members.getByLabel('参与人 1 查看执行',{exact:true}).setChecked(checked);assert.equal(await members.getByLabel('参与人 1 阅读交付',{exact:true}).isChecked(),true);assert.equal(await members.getByLabel('参与人 1 范围',{exact:true}).inputValue(),'communicate');await members.getByLabel('参与范围调整说明',{exact:true}).fill(reason);await members.getByRole('button',{name:'保存参与范围',exact:true}).click();await members.getByLabel('参与范围调整说明',{exact:true}).waitFor({state:'hidden'});}
try{
 await login(executor,'admin@example.com','Source-Changed!26');await login(issuer,'system_administrator@example.com','Source-Changed!26','#'+source);await login(reader,email,password);
 await inspect();assert.match(await inspection().innerText(),/timezone_source|当前时间/);assert.equal(await inspection().getByRole('button',{name:/继续处理|准备修复|确认操作/}).count(),0);
 await reader.screenshot({path:join(output,'shared-execution.png'),fullPage:true});
 await shared(executor).getByLabel('执行共享说明',{exact:true}).fill('执行人从页面撤回这次运行共享');await shared(executor).getByRole('button',{name:'撤回执行共享',exact:true}).click();await inspection().waitFor({state:'hidden'});assert.equal(await shared(reader).getByRole('button',{name:'查看共享执行过程',exact:true}).count(),0);
 await shared(executor).getByLabel('执行共享说明',{exact:true}).fill('执行人重新共享原始运行');await shared(executor).getByRole('button',{name:'共享这次执行过程',exact:true}).click();await shared(reader).getByRole('button',{name:'查看共享执行过程',exact:true}).waitFor();await inspect();
 await scope(false,'撤回查看执行，保留原沟通范围');await inspection().waitFor({state:'hidden'});await shared(reader).waitFor({state:'hidden'});
 await scope(true,'恢复执行阅读，保留原交付阅读');await shared(reader).waitFor();await inspect();
 await reader.setViewportSize({width:390,height:844});await reader.waitForFunction(()=>{const dialogs=[...document.querySelectorAll('[role="dialog"]')];const box=dialogs.at(-1)?.getBoundingClientRect();return box&&box.left>=0&&box.right<=window.innerWidth;});await inspection().evaluate(dialog=>{dialog.scrollTop=0;});await reader.screenshot({path:join(output,'shared-execution-mobile.png'),fullPage:true,animations:'disabled'});assert.deepEqual(errors,[]);
 await writeFile(join(output,'report.json'),JSON.stringify({status:'passed',page_errors:errors,scenarios:['actual third-user shared tool parameters and results','inspection cannot confirm or resume','executor withdraw clears open run','executor explicitly reshares original run','participant grant withdraw clears open run','independent communication/delivery scopes','restored grant fresh reading','mobile modal bounds']},null,2));
}catch(error){await writeFile(join(output,'failure.json'),JSON.stringify({error:String(error),page_errors:errors,pages:await Promise.all(pages.map(async p=>(await p.locator('body').innerText()).slice(-12000)))},null,2));throw error;}
finally{await browser.close();}
