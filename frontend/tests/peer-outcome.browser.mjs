import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT;
assert.ok(origin&&output);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const page=await browser.newPage({viewport:{width:1360,height:1000}});page.setDefaultTimeout(15000);
const errors=[];page.on('pageerror',e=>errors.push(String(e)));
const work=()=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
try {
 await page.goto(origin+'#'+process.env.AGENT_UI_CONVERSATION);
 await page.getByLabel('账号',{exact:true}).fill('admin@example.com');await page.getByLabel('密码',{exact:true}).fill(process.env.AGENT_UI_PASSWORD);await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 const section=work().getByRole('region',{name:'原操作结果核查'});
 await section.waitFor();assert.equal(await section.getByRole('button',{name:'核查原操作结果',exact:true}).count(),1);
 await section.scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-outcome-before.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});
 await page.waitForFunction(()=>{const el=document.querySelector('.collaboration-dialog');const r=el?.getBoundingClientRect();return r&&r.x>=0&&r.right<=window.innerWidth&&el.scrollWidth<=el.clientWidth+1;});
 await section.scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-outcome-mobile.png'),fullPage:true});
 await section.getByRole('button',{name:'核查原操作结果',exact:true}).click();await section.waitFor({state:'hidden'});
 await work().getByRole('button',{name:'查看实时执行与工具调用',exact:true}).click();
 const run=page.getByRole('dialog',{name:'处理记录',exact:true});await run.locator('.execution-tool').filter({hasText:'原回执核查'}).locator('summary').click();await run.getByText('原回执核查',{exact:true}).waitFor();
 assert.match(await run.innerText(),/已取得明确回执/);assert.match(await run.innerText(),/已停止/);assert.equal(await run.getByRole('button',{name:'确认执行',exact:true}).count(),0);
 await page.setViewportSize({width:1360,height:1000});await run.getByText('原回执核查',{exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-outcome-receipt.png'),fullPage:true});
 await run.getByRole('button',{name:'关闭',exact:true}).click();
 await page.reload();await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 await work().getByRole('button',{name:'继续执行',exact:true}).waitFor();assert.equal(await work().getByRole('button',{name:'核查原操作结果',exact:true}).count(),0);
 assert.deepEqual(errors,[]);await writeFile(join(output,'report.json'),JSON.stringify({complete:true,errors,checks:['stopped original operation','single unknown write selected','mobile','receipt-only inspection','original receipt visible','no approval or resume','reload leaves explicit continuation']},null,2));
}catch(e){await page.screenshot({path:join(output,'failure.png'),fullPage:true});await writeFile(join(output,'failure.txt'),String(e)+'\n'+errors.join('\n')+'\n'+await page.locator('body').innerText());throw e;}
finally{await browser.close();}
