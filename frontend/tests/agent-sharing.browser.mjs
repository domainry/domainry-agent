import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
const {chromium}=createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE||'playwright');
const {AGENT_UI_ORIGIN:origin,AGENT_UI_TEST_OUTPUT:output,AGENT_UI_RECIPIENT:recipient}=process.env;
assert.ok(origin&&output&&recipient);
await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,channel:'chrome'});
const ownerContext=await browser.newContext({viewport:{width:1360,height:1000}});
const callerContext=await browser.newContext({viewport:{width:1360,height:1000}});
const owner=await ownerContext.newPage(),caller=await callerContext.newPage();
const errors=[];
for(const page of [owner,caller]){page.setDefaultTimeout(20000);page.on('pageerror',e=>errors.push(String(e)));}
const work=page=>page.getByRole('dialog',{name:'Agent 协作',exact:true});
const editor=()=>owner.getByRole('dialog',{name:'配置 Agent',exact:true});
const card=page=>work(page).locator('.peer-card').filter({has:page.getByRole('heading',{name:'共享核对 Agent',exact:true})});
const login=async(page,email)=>{
 await page.goto(origin);
 await page.getByLabel('账号',{exact:true}).fill(email);
 await page.getByLabel('密码',{exact:true}).fill('Shared-Agent-Changed!26');
 await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 await work(page).getByRole('button',{name:'Agent 目录',exact:true}).click();
};
const save=async()=>{
 await editor().getByRole('button',{name:'保存 Agent',exact:true}).click();
 await editor().waitFor({state:'hidden'});
};
try {
 await login(owner,'admin@example.com');
 await card(owner).getByRole('button',{name:'配置',exact:true}).click();
 await editor().getByLabel('可使用此 Agent 的用户 ID（每行一项）',{exact:true}).fill(recipient);
 await save();
 await card(owner).getByText('已共享给 1 位用户',{exact:true}).waitFor();
 await login(caller,'system_administrator@example.com');
 await card(caller).getByText('共享 Agent · 配置所有者 admin · 按你的当前权限执行',{exact:true}).waitFor();
 assert.equal(await card(caller).getByRole('button',{name:'配置',exact:true}).count(),0);
 await caller.screenshot({path:join(output,'shared-agent-recipient.png'),fullPage:true});
 // Editing a real response must strip read-only owner fields and timestamps.
 await card(owner).getByRole('button',{name:'配置',exact:true}).click();
 assert.equal(await editor().getByLabel('可使用此 Agent 的用户 ID（每行一项）',{exact:true}).inputValue(),recipient);
 await editor().getByLabel('擅长的工作',{exact:true}).fill('共享配置，执行使用实际调用者权限');
 await save();
 await card(caller).getByText('共享配置，执行使用实际调用者权限',{exact:true}).waitFor();
 await caller.setViewportSize({width:390,height:844});
 await card(caller).scrollIntoViewIfNeeded();
 await caller.waitForFunction(()=>{const e=document.querySelector('.collaboration-dialog');const r=e?.getBoundingClientRect();return r&&r.x>=0&&r.right<=innerWidth&&e.scrollWidth<=e.clientWidth+1;});
 await caller.screenshot({path:join(output,'shared-agent-mobile.png'),fullPage:true});
 await card(owner).getByRole('button',{name:'配置',exact:true}).click();
 await editor().getByLabel('可使用此 Agent 的用户 ID（每行一项）',{exact:true}).fill('');
 await save();
 await card(caller).waitFor({state:'hidden'});
 await caller.setViewportSize({width:1360,height:1000});
 await caller.reload();
 await caller.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 await work(caller).getByRole('button',{name:'Agent 目录',exact:true}).click();
 await work(caller).getByRole('heading',{name:'默认 Agent',exact:true}).waitFor();
 assert.equal(await card(caller).count(),0);
 assert.deepEqual(errors,[]);
 await writeFile(join(output,'agent-sharing-report.json'),JSON.stringify({passed:true,realChrome:true,realIdentityUsers:true,scenarios:['owner shares existing configuration','recipient directory hides editing','configuration update strips server-owned fields','recipient receives current configuration','390px layout','revoke sharing removes live directory entry and survives reload'],errors},null,2));
}catch(error){await owner.screenshot({path:join(output,'owner-failure.png'),fullPage:true});await caller.screenshot({path:join(output,'caller-failure.png'),fullPage:true});throw error;}
finally{await browser.close();}
