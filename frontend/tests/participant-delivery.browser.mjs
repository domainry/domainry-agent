import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const {AGENT_UI_ORIGIN:origin,AGENT_UI_TEST_OUTPUT:output,AGENT_UI_CONVERSATION:source,AGENT_UI_READER:readerID,AGENT_UI_READER_EMAIL:email,AGENT_UI_READER_PASSWORD:password}=process.env;
assert.ok(origin&&output&&source&&readerID&&email&&password);
await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const ownerContext=await browser.newContext({viewport:{width:1360,height:1000}}),readerContext=await browser.newContext({viewport:{width:1360,height:1000}});
const owner=await ownerContext.newPage(),reader=await readerContext.newPage();
const errors=[];
for(const page of [owner,reader]){page.setDefaultTimeout(20000);page.on('pageerror',e=>errors.push(String(e)));}
const work=page=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
const members=()=>work(owner).getByRole('region',{name:'委派参与人',exact:true});
async function login(page,account,secret,hash=''){
 await page.goto(origin+hash);
 await page.getByLabel('账号',{exact:true}).fill(account);
 await page.getByLabel('密码',{exact:true}).fill(secret);
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 await work(page).getByRole('button',{name:/^Read the actual current time/}).click();
}
async function save(reason){await members().getByLabel('参与范围调整说明',{exact:true}).fill(reason);await members().getByRole('button',{name:'保存参与范围',exact:true}).click();await members().getByLabel('参与范围调整说明',{exact:true}).waitFor({state:'hidden'});}
async function edit(){await members().getByRole('button',{name:'管理参与人',exact:true}).click();await members().getByLabel('参与人 1 阅读交付',{exact:true}).waitFor();}
try{
 await login(owner,'system_administrator@example.com','Source-Changed!26','#'+source);
 await login(reader,email,password);
 await work(reader).getByRole('region',{name:'交付完成条件核对',exact:true}).getByRole('button',{name:'查看第 1 项原回执 1',exact:true}).click();
 await work(reader).getByText('已核对来源权限与内容完整性。关闭或切换窗口后需重新读取。',{exact:true}).waitFor();
 await work(reader).getByText('查看原始结构化结果',{exact:true}).click();
 assert.equal(JSON.parse(await work(reader).getByLabel('完整工具结果',{exact:true}).innerText()).content.timezone_source,'host_default');
 assert.equal(await work(reader).getByRole('button',{name:'查看该回执的执行过程',exact:true}).count(),0);
 assert.equal(await work(reader).getByRole('button',{name:'查看执行详情',exact:true}).count(),0);
 await reader.screenshot({path:join(output,'participant-delivery.png'),fullPage:true});
 await edit();
 assert.equal(await members().getByLabel('参与人 1 用户 ID',{exact:true}).inputValue(),readerID);
 await members().getByLabel('参与人 1 管理委派',{exact:true}).check();
 await members().getByLabel('参与人 1 范围',{exact:true}).selectOption('communicate');
 assert.equal(await members().getByLabel('参与人 1 阅读交付',{exact:true}).isChecked(),true);
 await members().getByLabel('参与人 1 范围',{exact:true}).selectOption('view');
 await members().getByLabel('参与人 1 管理委派',{exact:true}).uncheck();
 await members().getByLabel('参与人 1 阅读交付',{exact:true}).uncheck();
 await save('撤回交付阅读，保留查看约定');
 await work(reader).getByRole('region',{name:'交付完成条件核对',exact:true}).waitFor({state:'hidden'});
 assert.equal(await work(reader).getByLabel('完整工具结果',{exact:true}).count(),0);
 await edit();await members().getByLabel('参与人 1 阅读交付',{exact:true}).check();await save('重新授予交付阅读');
 await work(reader).getByRole('region',{name:'交付完成条件核对',exact:true}).waitFor();
 assert.equal(await work(reader).getByLabel('完整工具结果',{exact:true}).count(),0);
 await work(reader).getByRole('region',{name:'交付完成条件核对',exact:true}).getByRole('button',{name:'查看交付与验收历史',exact:true}).click();
 await work(reader).getByRole('list',{name:'交付与验收历史',exact:true}).waitFor();
 await reader.setViewportSize({width:390,height:844});
 await reader.waitForFunction(()=>{const box=document.querySelector('[role="dialog"]')?.getBoundingClientRect();return box&&box.left>=0&&box.right<=window.innerWidth;});
 await work(reader).evaluate(dialog=>{dialog.scrollTop=0;});
 await reader.screenshot({path:join(output,'participant-delivery-mobile.png'),fullPage:true,animations:'disabled'});
 assert.ok(await reader.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1));
 assert.deepEqual(errors,[]);
 await writeFile(join(output,'report.json'),JSON.stringify({status:'passed',page_errors:errors,scenarios:['actual third-user result reading','delivery history','execution detail withheld','independent grants survive communication changes','delivery withdrawal clears old result','regrant requires fresh reading','mobile width']},null,2));
}catch(error){await writeFile(join(output,'failure.json'),JSON.stringify({error:String(error),page_errors:errors,owner:(await owner.locator('body').innerText()).slice(-16000),reader:(await reader.locator('body').innerText()).slice(-16000)},null,2));throw error;}
finally{await browser.close();}
