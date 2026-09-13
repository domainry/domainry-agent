import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const {AGENT_UI_ORIGIN:origin,AGENT_UI_TEST_OUTPUT:output,AGENT_UI_RECIPIENT:recipient,AGENT_UI_CONVERSATION:source}=process.env;
assert.ok(origin&&output&&recipient&&source);
await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const ownerContext=await browser.newContext({viewport:{width:1360,height:1000}}),callerContext=await browser.newContext({viewport:{width:1360,height:1000}});
const owner=await ownerContext.newPage(),caller=await callerContext.newPage();
const errors=[];
for(const page of [owner,caller]){page.setDefaultTimeout(20000);page.on('pageerror',e=>errors.push(String(e)));}
const work=page=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
const members=()=>work(owner).getByRole('region',{name:'委派参与人',exact:true});
const login=async(page,email,hash='')=>{
 await page.goto(origin+hash);
 await page.getByLabel('账号',{exact:true}).fill(email);
 await page.getByLabel('密码',{exact:true}).fill('Participant-Changed!26');
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
};
const select=async page=>{await work(page).getByRole('button',{name:/^核对待办/}).click();};
const save=async reason=>{
 await members().getByLabel('参与范围调整说明',{exact:true}).fill(reason);
 await members().getByRole('button',{name:'保存参与范围',exact:true}).click();
 await members().getByLabel('参与范围调整说明',{exact:true}).waitFor({state:'hidden'});
};
try{
 await login(owner,'admin@example.com','#'+source);
 await select(owner);
 await members().getByRole('button',{name:'管理参与人',exact:true}).click();
 await members().getByRole('button',{name:'添加参与人',exact:true}).click();
 await members().getByLabel('参与人 1 用户 ID',{exact:true}).fill(recipient);
 await save('先邀请查看当前约定');
 await login(caller,'system_administrator@example.com');
 await select(caller);
 await work(caller).getByRole('region',{name:'委派参与人',exact:true}).waitFor();
 for(const name of ['管理参与人','查看要求历史','查看实时执行与工具调用','发起方会话','接收方会话'])assert.equal(await work(caller).getByRole('button',{name,exact:true}).count(),0,name);
 assert.equal(await work(caller).getByRole('region',{name:'委派通信',exact:true}).count(),0);
 assert.doesNotMatch(await work(caller).innerText(),/EXECUTION-OWNER-PRIVATE-TODO/);
 await members().getByRole('button',{name:'管理参与人',exact:true}).click();
 await members().getByLabel('参与人 1 范围',{exact:true}).selectOption('communicate');
 await save('允许补充核对意见');
 await work(caller).getByLabel('补充信息或讨论分歧',{exact:true}).fill('浏览器参与者补充意见');
 await work(caller).getByRole('button',{name:'发送消息',exact:true}).click();
 const ownMessage=work(owner).getByRole('region',{name:'委派通信',exact:true}).locator('li').filter({hasText:'浏览器参与者补充意见'});
 await ownMessage.waitFor();
 assert.match(await ownMessage.innerText(),new RegExp('用户 '+recipient));
 assert.doesNotMatch((await ownMessage.innerText()).split('\n')[0],/^你/);
 await ownMessage.getByText(/已进入执行上下文/).waitFor();
 await ownMessage.scrollIntoViewIfNeeded();
 await owner.screenshot({path:join(output,'participant-sender-identity.png'),fullPage:true});
 await caller.setViewportSize({width:390,height:844});
 await work(caller).getByRole('region',{name:'委派参与人',exact:true}).scrollIntoViewIfNeeded();
 await caller.waitForFunction(()=>{const e=document.querySelector('.collaboration-dialog');const r=e?.getBoundingClientRect();return r&&r.x>=0&&r.right<=innerWidth&&[e,...e.querySelectorAll('.peer-messages, .peer-messages li')].every(v=>v.scrollWidth<=v.clientWidth+1);});
 await caller.screenshot({path:join(output,'participant-mobile.png'),fullPage:true});
 await members().getByRole('button',{name:'管理参与人',exact:true}).click();
 await members().getByRole('button',{name:'移除参与人 1',exact:true}).click();
 await save('结束本次参与');
 await work(caller).getByRole('region',{name:'委派详情',exact:true}).waitFor({state:'hidden'});
 await caller.setViewportSize({width:1360,height:1000});
 await caller.reload();
 await caller.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 await work(caller).getByText('还没有委派。可以在 Agent 目录中选择接收方，或在对话中让 Agent 发起委派。',{exact:true}).waitFor();
 assert.deepEqual(errors,[]);
 await writeFile(join(output,'participants-report.json'),JSON.stringify({passed:true,realChrome:true,realIdentityUsers:true,scenarios:['invite observer to one delegation','no private work or owner controls','upgrade to communication','actual sender label','message consumed by original worker','390px layout','remove participant clears live detail and survives reload'],errors},null,2));
}catch(error){await owner.screenshot({path:join(output,'owner-failure.png'),fullPage:true});await caller.screenshot({path:join(output,'caller-failure.png'),fullPage:true});throw error;}
finally{await browser.close();}
