import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.AGENT_UI_ORIGIN,output=process.env.AGENT_UI_TEST_OUTPUT;
assert.ok(origin&&output);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const page=await browser.newPage({viewport:{width:1360,height:1000}});page.setDefaultTimeout(20000);
const errors=[],reads=[];page.on('pageerror',e=>errors.push(String(e)));page.on('request',r=>{if(r.method()==='POST'&&/\/(delivery-result|result)$/.test(new URL(r.url()).pathname))reads.push({path:new URL(r.url()).pathname,body:r.postDataJSON()});});
const work=()=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
const change=async allowed=>{const r=await page.request.post(origin+'/fixture/knowledge-result-read?allowed='+allowed);assert.equal(r.status(),204);};
async function openReceipt(scope,index){await scope.getByRole('button',{name:`查看第 1 项原回执 ${index}`,exact:true}).click();const result=scope.locator('.stored-result').filter({has:page.getByText('已核对来源权限与内容完整性。关闭或切换窗口后需重新读取。',{exact:true})});await result.locator('summary').click();await result.getByLabel('完整工具结果',{exact:true}).waitFor();return result;}
try{
 await page.goto(origin+'#'+process.env.AGENT_UI_CONVERSATION);await page.getByLabel('账号',{exact:true}).fill('admin@example.com');await page.getByLabel('密码',{exact:true}).fill('Knowledge-Receipt-Changed!26');await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 assert.equal(await work().getByRole('button',{name:/查看该回执的执行过程|查看实时执行/}).count(),0);
 for(const [index,operation] of [[1,'libraries'],[2,'search'],[3,'fetch'],[4,'extract']]){
  const shown=await openReceipt(work(),index);const result=JSON.parse(await shown.getByLabel('完整工具结果',{exact:true}).innerText());assert.equal(result.status,'completed');assert.equal(result.content.operation,operation);
  if(index===4){assert.equal(result.content.data.fields[0].value,'123.45');assert.ok(result.content.data.fields[0].evidence.length>0);await page.screenshot({path:join(output,'knowledge-delivery-desktop.png'),fullPage:true});}
  await shown.getByRole('button',{name:'收起完整结果',exact:true}).click();
 }
 await openReceipt(work(),4);await change(false);await work().locator('.stored-result pre').waitFor({state:'hidden'});assert.doesNotMatch(await work().innerText(),/已核对资料中的金额 123.45/);
 await change(true);await work().getByRole('button',{name:'查看交付与验收历史',exact:true}).click();const history=work().getByRole('list',{name:'交付与验收历史',exact:true});await history.locator('summary').first().click();
 const shown=await openReceipt(history,4);assert.equal(JSON.parse(await shown.getByLabel('完整工具结果',{exact:true}).innerText()).content.data.fields[0].value,'123.45');
 await page.setViewportSize({width:390,height:844});await shown.scrollIntoViewIfNeeded();await page.waitForFunction(()=>{const e=document.querySelector('.collaboration-dialog'),r=e?.getBoundingClientRect();return r&&r.x>=0&&r.right<=innerWidth&&e.scrollWidth<=e.clientWidth+1;});await page.screenshot({path:join(output,'knowledge-delivery-mobile.png'),fullPage:true});
 await history.locator('summary').first().click();await shown.getByLabel('完整工具结果',{exact:true}).waitFor({state:'hidden'});
 await page.setViewportSize({width:1360,height:1000});await page.reload();await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await work().getByRole('button',{name:'查看第 1 项原回执 4',exact:true}).waitFor();assert.equal(await work().locator('.stored-result pre').count(),0);
 assert.ok(reads.some(r=>r.body.delivery_revision>0));assert.ok(reads.some(r=>r.body.delivery_revision===0));assert.ok(reads.every(r=>r.path.endsWith('/delivery-result')));assert.deepEqual(errors,[]);
 await writeFile(join(output,'knowledge-delivery-report.json'),JSON.stringify({passed:true,realChrome:true,current:['libraries','search','fetch','extract'],extractionValueAndEvidence:true,immutableHistory:true,sourceRevocation:true,restoration:true,refresh:true,closeClears:true,narrowViewport:true,rawExecutionRequests:0,pages:reads.length,errors},null,2));
}catch(e){await page.screenshot({path:join(output,'knowledge-delivery-failure.png'),fullPage:true});throw e;}
finally{await browser.close();}
