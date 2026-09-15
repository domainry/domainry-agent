import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.AGENT_UI_ORIGIN, output = process.env.AGENT_UI_TEST_OUTPUT;
assert.ok(origin && output); await mkdir(output,{recursive:true});
const browser = await chromium.launch({headless:true,channel:'chrome'});
const page = await browser.newPage({viewport:{width:1360,height:1000}}); page.setDefaultTimeout(60000);
const errors=[]; page.on('pageerror',e=>errors.push(String(e)));
const work = () => page.getByRole('dialog',{name:'Agent 协作',exact:true});
async function reopenTask(title){await page.reload();await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();const task=work().locator('.task-list button').filter({hasText:title});await task.waitFor();await task.click();}
async function reopenBrowserTask(){await reopenTask('浏览器协作验收');}
try {
 await page.goto(origin+'#'+process.env.AGENT_UI_CONVERSATION);
 await page.getByLabel('账号',{exact:true}).fill('admin@example.com'); await page.getByLabel('密码',{exact:true}).fill('Peer-Changed!33');await page.getByRole('button',{name:'登录',exact:true}).click();
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).waitFor(); await page.goto(origin+'#'+process.env.AGENT_UI_CONVERSATION);await page.getByText('正在同步协作状态…',{exact:true}).waitFor({state:'hidden',timeout:60000});
 await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();
 await work().getByText('正在读取协作记录…',{exact:true}).waitFor({state:'hidden',timeout:60000});
 await work().getByRole('button',{name:'Agent 目录',exact:true}).click(); await work().getByRole('button',{name:'创建 Agent',exact:true}).click();
 const editor = page.getByRole('dialog',{name:'创建 Agent',exact:true});
 await editor.getByLabel('名称',{exact:true}).fill('浏览器核查员');await editor.getByLabel('擅长的工作',{exact:true}).fill('核查交付条件');await editor.getByLabel('工作要求',{exact:true}).fill('独立核查并说明证据');
 await editor.getByLabel('模型',{exact:true}).selectOption('review',{timeout:60000});
 for(const key of ['time_now','delegation_get','agent_message','delegation_update']) await editor.locator('.peer-tool-options label').filter({hasText:new RegExp('^'+key)}).locator('input').check();
 await editor.getByRole('button',{name:'保存 Agent',exact:true}).click();await editor.waitFor({state:'hidden'});
 const peer = work().locator('.peer-card').filter({hasText:'浏览器核查员'});await peer.getByText('可以接单',{exact:true}).waitFor();
 await work().getByRole('button',{name:'按工作要求选择 Agent',exact:true}).click();
 const matching=page.getByRole('dialog',{name:'按工作要求选择 Agent',exact:true});
 await matching.getByLabel('任务类型（用于匹配历史记录）',{exact:true}).fill('publish_review');
 await matching.getByLabel('calculate',{exact:true}).check();await matching.getByRole('button',{name:'检查匹配',exact:true}).click();
 await matching.getByText('没有满足当前要求的 Agent',{exact:true}).waitFor();
 await matching.getByLabel('calculate',{exact:true}).uncheck();await matching.getByLabel('time_now',{exact:true}).check();
 await matching.locator('summary').filter({hasText:'模型费用筛选与资料引用'}).click();
 await matching.getByLabel('预计总输入 token',{exact:true}).fill('1000');await matching.getByLabel('预计总输出 token',{exact:true}).fill('200');
 await matching.getByLabel('预估模型费用上限',{exact:true}).fill('1');await matching.getByLabel('币种',{exact:true}).fill('CNY');
 await matching.getByRole('button',{name:'检查匹配',exact:true}).click();await matching.getByRole('status').filter({hasText:'建议选择'}).waitFor();
 await matching.getByRole('status').scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-matching.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>{const els=[...document.querySelectorAll('.peer-editor')];const el=els.at(-1);const r=el?.getBoundingClientRect();return r && r.x>=0 && r.right<=window.innerWidth && el.scrollWidth<=el.clientWidth+1;});
 await matching.getByRole('status').scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-matching-mobile.png'),fullPage:true});await page.setViewportSize({width:1360,height:1000});
 await matching.getByRole('button',{name:'选择 浏览器核查员',exact:true}).click();
 const form=page.getByRole('dialog',{name:'委派给 浏览器核查员',exact:true});
 await form.getByLabel('委派原因',{exact:true}).fill('需要独立核查');await form.getByLabel('目标',{exact:true}).fill('浏览器协作验收');await form.getByLabel('交付物',{exact:true}).fill('核查结论');await form.getByLabel('完成条件（每行一项）',{exact:true}).fill('给出可追溯证据\n验证结果为 true\n读取实际时间');
 await form.locator('summary').filter({hasText:'完成条件的检查方式'}).click();
 await form.getByLabel('第 2 项检查方式',{exact:true}).selectOption('data');
 await form.getByLabel('第 2 项数据 JSON Schema',{exact:true}).fill('{"type":"object","properties":{"verified":{"const":true}},"required":["verified"]}');
 await form.getByLabel('第 3 项检查方式',{exact:true}).selectOption('receipt');await form.getByLabel('第 3 项工具名称',{exact:true}).fill('time_now');
 await form.locator('summary').filter({hasText:'结构化输入与交付格式'}).click();await form.getByLabel('结构化输入 JSON',{exact:true}).fill('{"currency":"EUR"}');await form.getByLabel('输入 JSON Schema（可选）',{exact:true}).fill('{"type":"object","properties":{"currency":{"type":"string"}},"required":["currency"]}');await form.getByRole('button',{name:'发起委派',exact:true}).click();await Promise.race([form.waitFor({state:'hidden'}),form.getByRole('alert').waitFor().then(async()=>{throw new Error(await form.getByRole('alert').innerText());})]);
 await page.keyboard.press('Escape');await work().waitFor({state:'hidden'});
 const inline=page.getByRole('region',{name:'当前会话的 Agent 协作',exact:true});
 await inline.getByText('浏览器协作验收',{exact:true}).waitFor();assert.match(await inline.innerText(),/完成条件：/);assert.doesNotMatch(await inline.innerText(),/\d+%/);
 await inline.scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-inline-progress.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>{const el=document.querySelector('.collaboration-inline');return el&&el.scrollWidth<=el.clientWidth+1;});await page.screenshot({path:join(output,'peer-inline-progress-mobile.png'),fullPage:true});await page.setViewportSize({width:1360,height:1000});
 await inline.locator('.collaboration-inline-main').filter({hasText:'浏览器协作验收'}).click();await work().getByText('正在读取协作记录…',{exact:true}).waitFor({state:'hidden',timeout:90000});const browserTask=work().locator('.task-list button').filter({hasText:'浏览器协作验收'});await browserTask.waitFor({timeout:90000});await browserTask.click();await work().getByRole('heading',{name:'浏览器协作验收',exact:true}).waitFor();
 await work().getByLabel('委派查看范围',{exact:true}).selectOption('sent');assert.equal(await work().locator('.task-list button').filter({hasText:'浏览器协作验收'}).count(),1);await work().getByLabel('委派查看范围',{exact:true}).selectOption('all');
 const plan=work().getByRole('region',{name:'委派执行计划',exact:true});await plan.getByText('计划 v2 · 要求 v1',{exact:true}).waitFor();const planText=await plan.innerText();assert.match(planText,/读取并核对当前时间/);assert.match(planText,/提交核查结论/);assert.match(planText,/当前时间回执已经核对/);assert.match(planText,/核查结论已经提交/);assert.equal(await plan.getByRole('button',{name:'执行证据 peer-time',exact:true}).count(),1);assert.equal(await plan.getByRole('button',{name:'执行证据 peer-deliver',exact:true}).count(),1);await plan.scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-plan.png'),fullPage:true});
 await work().getByRole('button',{name:'查看实时执行与工具调用',exact:true}).click();
 const run=page.getByRole('dialog',{name:'处理记录',exact:true});
 assert.equal(await run.getByRole('button',{name:'确认执行',exact:true}).count(),0);
 await run.getByText('已保存的回复',{exact:true}).waitFor();await page.screenshot({path:join(output,'peer-execution.png'),fullPage:true});
 await Promise.all([page.waitForResponse(response=>new URL(response.url()).pathname==='/agent/delegations'&&response.request().method()==='GET'&&response.status()===200),run.getByRole('button',{name:'关闭',exact:true}).click()]);
 const verification=work().getByRole('region',{name:'交付完成条件核对',exact:true});
 await verification.getByText('原操作回执及参数／结果符合该项约定',{exact:false}).waitFor();
 async function submitDeliveryReview(){await work().getByRole('button',{name:'核对交付',exact:true}).click();assert.ok(['unknown','met'].includes(await work().getByLabel('第 1 项核对结果',{exact:true}).inputValue()));assert.equal(await work().getByLabel('第 2 项核对结果',{exact:true}).count(),0);await work().getByLabel('第 1 项核对结果',{exact:true}).selectOption('met');await work().getByLabel('第 1 项核对依据',{exact:true}).fill('人工检查原回执与交付结论一致');await work().getByLabel('原因与处理意见',{exact:true}).fill('保存逐项核对，稍后验收');await work().getByRole('button',{name:'提交核对交付',exact:true}).click();return Promise.race([verification.getByText('条件已核对通过，交付仍需明确验收',{exact:true}).waitFor().then(()=>true),work().getByRole('alert').filter({hasText:'内容已在其他页面更新'}).waitFor().then(()=>false)]);}
 let reviewSaved=false;
 for(let attempt=0;attempt<5&&!reviewSaved;attempt++){
  reviewSaved=await submitDeliveryReview();
  if(!reviewSaved)await reopenBrowserTask();
 }
 assert.equal(reviewSaved,true);
 assert.doesNotMatch(await work().locator('.task-detail-heading').innerText(),/已验收/);
 await verification.scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-verification.png'),fullPage:true});
 async function submitAcceptance(reason,conditionBasis=''){
  await work().getByRole('button',{name:'验收通过',exact:true}).click();
  if(conditionBasis&&await work().getByLabel('第 1 项核对结果',{exact:true}).count()){await work().getByLabel('第 1 项核对结果',{exact:true}).selectOption('met');await work().getByLabel('第 1 项核对依据',{exact:true}).fill(conditionBasis);}
  await work().getByLabel('验收依据与完成条件核对',{exact:true}).fill(reason);await work().getByRole('button',{name:'提交验收通过',exact:true}).click();
  return Promise.race([work().locator('.task-detail-heading').filter({hasText:'已验收'}).waitFor().then(()=>true),work().getByRole('alert').filter({hasText:'内容已在其他页面更新'}).waitFor().then(()=>false)]);
 }
 async function retryAcceptance(reason,conditionBasis=''){
  for(let attempt=0;attempt<5;attempt++){
   if(await submitAcceptance(reason,conditionBasis))return;
   await reopenBrowserTask();
  }
  assert.fail('acceptance kept changing after five fresh revisions');
 }
 await retryAcceptance('已核对实际工具记录与交付要求','已核对实际工具记录与交付内容');
 await work().evaluate(el=>el.scrollTop=0);await page.screenshot({path:join(output,'peer-accepted.png'),fullPage:true});
 await page.keyboard.press('Escape');await work().waitFor({state:'hidden'});const acceptedInline=inline.locator('.collaboration-inline-main').filter({hasText:'浏览器协作验收'});await acceptedInline.getByText(/交付已验收/).waitFor();assert.match(await acceptedInline.innerText(),/3\/3 项完成条件有核对结论/);await acceptedInline.click();
 await page.reload();await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await work().locator('.task-list button').filter({hasText:'浏览器协作验收'}).click();assert.match(await work().innerText(),/已验收/);
 await work().getByRole('button',{name:'查看交付与验收历史',exact:true}).click();
 const deliveries=work().getByRole('list',{name:'交付与验收历史',exact:true});await deliveries.locator('li').filter({hasText:'验收通过'}).first().waitFor();assert.equal(await deliveries.locator(':scope > li').count(),3);
 await deliveries.locator('summary').filter({hasText:'核对交付'}).click();await deliveries.getByText('人工检查原回执与交付结论一致',{exact:false}).waitFor();
 await deliveries.scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-verification-history.png'),fullPage:true});
 await page.setViewportSize({width:390,height:844});
 await page.waitForFunction(()=>{const el=document.querySelector('.collaboration-dialog');const r=el?.getBoundingClientRect();return r && r.x>=0 && r.right<=window.innerWidth && el.scrollWidth<=el.clientWidth+1;});
 await deliveries.scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-verification-mobile.png'),fullPage:true});await page.setViewportSize({width:1360,height:1000});
 // Disagreement records reopen acceptance and preserve the evidence comparison.
 await work().getByRole('button',{name:'记录结论分歧',exact:true}).click();
 const disputeForm=()=>work().getByRole('form',{name:'结构化分歧表单',exact:true});
 await disputeForm().getByLabel('分歧主题',{exact:true}).fill('时间回执的归属存在分歧');await disputeForm().getByLabel('关联完成条件',{exact:true}).selectOption('2');
 for(let i=1;i<=2;i++){
  await disputeForm().getByLabel(`结论 ${i} 内容`,{exact:true}).fill(i===1?'回执属于本次核查执行':'回执可能属于上次核查执行');
  await disputeForm().getByLabel(`结论 ${i} 数据范围`,{exact:true}).fill('当前核查任务的时钟读取');await disputeForm().getByLabel(`结论 ${i} 时间范围`,{exact:true}).fill(i===1?'本次执行开始至结束':'上次执行，尚未核实');
  await disputeForm().getByLabel(`结论 ${i} 来源及版本`,{exact:true}).fill('已保存的 time_now 原回执');await disputeForm().getByLabel(`结论 ${i} 计算或推导过程`,{exact:true}).fill('对照运行标识与工具步骤号');
 }
 await disputeForm().locator('fieldset').first().locator('summary').click();await disputeForm().locator('fieldset').first().getByLabel('完成条件 3 · peer-time',{exact:true}).check();
 await disputeForm().getByLabel('分歧记录说明',{exact:true}).fill('先核对原记录归属');await disputeForm().getByRole('button',{name:'保存分歧记录',exact:true}).click();
 const disagreements=work().getByRole('region',{name:'结构化结论分歧',exact:true});
 await disagreements.getByText('时间回执的归属存在分歧 · 待核对',{exact:true}).waitFor();assert.equal(await work().getByRole('button',{name:'验收通过',exact:true}).count(),0);
 await disagreements.getByRole('button',{name:'查看分歧证据与历史',exact:true}).click();await disagreements.getByRole('button',{name:'对照证据并决定',exact:true}).click();
 async function fillComparison(){for(const label of ['数据范围','时间范围','来源及版本','计算或推导过程'])await disputeForm().getByLabel(`${label}对照`,{exact:true}).fill(label+'已与原回执及运行标识逐项核对');await disputeForm().getByLabel('分歧处理依据',{exact:true}).fill('原回执链接指向本次执行的第一个工具步骤');await disputeForm().getByLabel('分歧记录说明',{exact:true}).fill('保存证据对照和处理责任');}
 await disputeForm().getByLabel('分歧处理方式',{exact:true}).selectOption('ask_user');await fillComparison();await disputeForm().getByLabel('分歧后续工作',{exact:true}).fill('请确定报告需要本次执行时间还是上次时间');await disputeForm().getByRole('button',{name:'保存分歧记录',exact:true}).click();
 await disagreements.getByText('时间回执的归属存在分歧 · 等待用户判断',{exact:true}).waitFor();await disagreements.getByText('时间回执的归属存在分歧 · 修订 2',{exact:true}).waitFor();
 await disagreements.getByRole('button',{name:'对照证据并决定',exact:true}).click();await disputeForm().getByLabel('分歧处理方式',{exact:true}).selectOption('adopt');await fillComparison();await disputeForm().getByLabel('采用的结论',{exact:true}).selectOption({index:1});await disputeForm().getByRole('button',{name:'保存分歧记录',exact:true}).click();
 await disagreements.getByText('时间回执的归属存在分歧 · 已采用结论',{exact:true}).waitFor();await disagreements.getByText('时间回执的归属存在分歧 · 修订 3',{exact:true}).waitFor();
 await work().getByRole('button',{name:'验收通过',exact:true}).waitFor();assert.doesNotMatch(await work().locator('.task-detail-heading').innerText(),/已验收/);
 await disagreements.getByRole('region',{name:'分歧处理决定',exact:true}).first().scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-disagreement-decision.png'),fullPage:true});
 await page.reload();await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await work().locator('.task-list button').filter({hasText:'浏览器协作验收'}).click();
 await disagreements.getByRole('button',{name:'查看分歧证据与历史',exact:true}).click();await disagreements.getByRole('button',{name:'更早的分歧记录',exact:true}).click();await disagreements.locator('summary').filter({hasText:'历史修订 1'}).waitFor();
 await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>{const el=document.querySelector('.collaboration-dialog');const r=el?.getBoundingClientRect();return r && r.x>=0 && r.right<=window.innerWidth && el.scrollWidth<=el.clientWidth+1;});await disagreements.getByRole('region',{name:'分歧处理决定',exact:true}).first().scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-disagreement-mobile.png'),fullPage:true});await page.setViewportSize({width:1360,height:1000});
 await retryAcceptance('分歧已按原回执核对，重新验收');
 // A second peer task tracks only the first task's constraints.
 await work().getByRole('button',{name:'Agent 目录',exact:true}).click();
 await work().locator('.peer-card').filter({hasText:'浏览器核查员'}).getByRole('button',{name:'委派当前工作',exact:true}).click();
 const follow=page.getByRole('dialog',{name:'委派给 浏览器核查员',exact:true});
 await follow.getByLabel('委派原因',{exact:true}).fill('依赖已核对的约束');await follow.getByLabel('目标',{exact:true}).fill('依赖约束的复核');await follow.getByLabel('交付物',{exact:true}).fill('按约束核查的结论');await follow.getByLabel('完成条件（每行一项）',{exact:true}).fill('核对币种要求');
 const dependency=follow.locator('.peer-dependencies section').filter({hasText:'浏览器协作验收'});
 await dependency.locator('input[type=checkbox]').first().check();await dependency.getByLabel('约束',{exact:true}).check();
 await follow.getByRole('button',{name:'发起委派',exact:true}).click();await follow.waitFor({state:'hidden'});
 await page.waitForTimeout(10000);await reopenTask('依赖约束的复核');
 await work().getByRole('button',{name:'验收通过',exact:true}).waitFor();
 await work().locator('.task-list button').filter({hasText:'浏览器协作验收'}).click();
 await work().getByRole('button',{name:'更新需求',exact:true}).click();await work().getByLabel('约束（每行一项）',{exact:true}).fill('所有金额统一使用 EUR');await work().getByLabel('原因与处理意见',{exact:true}).fill('币种要求有变化');await work().getByRole('button',{name:'提交更新需求',exact:true}).click();
 await work().getByRole('button',{name:'查看要求历史',exact:true}).click();
 const history=work().getByRole('list',{name:'要求历史'});await history.getByText(/约定 2 · 需求 v2/).waitFor();assert.equal(await history.locator('li').count(),2);
 await work().locator('.task-list button').filter({hasText:'依赖约束的复核'}).click();
 await work().getByText('这是旧要求下的交付，需更新后重新验收。',{exact:true}).waitFor();
 assert.equal(await work().getByRole('button',{name:'验收通过',exact:true}).count(),0);
 await work().getByLabel('消息类型',{exact:true}).selectOption('question');await work().getByLabel('消息接收时机',{exact:true}).selectOption('next_run');await work().getByLabel('补充信息或讨论分歧',{exact:true}).fill('币种改为 EUR 后需要重新核对哪些数据？');await work().getByRole('button',{name:'发送问题',exact:true}).click();
 await work().getByRole('button',{name:'回复这条问题',exact:true}).click();await work().getByLabel('补充信息或讨论分歧',{exact:true}).fill('请重新核对全部金额与币种标注');await work().getByRole('button',{name:'发送回复',exact:true}).click();
 await work().locator('.peer-messages li').filter({hasText:'币种改为 EUR 后需要重新核对哪些数据？'}).filter({hasText:'已回复'}).waitFor();
 await work().getByRole('button',{name:'继续执行',exact:true}).click();
 const resume=work().getByRole('button',{name:'提交继续执行',exact:true});assert.equal(await resume.isDisabled(),true);
 await work().getByLabel('已核对上方新要求和依赖，按显示版本继续',{exact:true}).check();await work().getByLabel('原因与处理意见',{exact:true}).fill('已核对 EUR 约束，继续本任务');await resume.click();
 let resumeOutcome=null;
 for(let attempt=0;attempt<4&&resumeOutcome===null;attempt++){
  await page.waitForTimeout(10000);await reopenTask('依赖约束的复核');
  if(await work().getByRole('button',{name:'验收通过',exact:true}).count())resumeOutcome=true;
  else if(/执行失败/.test(await work().locator('.task-detail-heading').innerText()))resumeOutcome=false;
 }
 assert.notEqual(resumeOutcome,null,'resumed execution did not reach a persisted terminal state');
 if(!resumeOutcome){await work().getByRole('button',{name:'查看实时执行与工具调用',exact:true}).click();const failedRun=page.getByRole('dialog',{name:'处理记录',exact:true});await failedRun.getByText('正在读取记录…',{exact:true}).waitFor({state:'hidden'});throw new Error('resumed execution failed:\n'+await failedRun.innerText());}
 const resumedPlan=work().getByRole('region',{name:'委派执行计划',exact:true});await resumedPlan.getByText('计划 v5 · 要求 v2',{exact:true}).waitFor();const resumedPlanText=await resumedPlan.innerText();assert.match(resumedPlanText,/读取并核对当前时间/);assert.match(resumedPlanText,/复核变更后的委派要求/);assert.match(resumedPlanText,/最新依赖要求已经复核并重新提交/);assert.equal(await resumedPlan.getByRole('button',{name:'执行证据 peer-time',exact:true}).count(),1);assert.equal(await resumedPlan.getByRole('button',{name:'执行证据 peer-deliver',exact:true}).count(),2);await resumedPlan.scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-plan-resumed.png'),fullPage:true});
 await work().getByText(/执行已采用约定 2/).waitFor();
 await work().getByRole('button',{name:'查看要求历史',exact:true}).click();await work().getByRole('list',{name:'要求历史'}).getByText(/约定 2 · 需求 v1/).waitFor();
 await work().getByRole('region',{name:'要求版本与依赖'}).scrollIntoViewIfNeeded();await page.screenshot({path:join(output,'peer-requirements.png'),fullPage:true});
 await page.reload();await page.getByRole('button',{name:'Agent 协作 目录、委派与沟通',exact:true}).click();await work().locator('.task-list button').filter({hasText:'依赖约束的复核'}).click();await work().getByText(/执行已采用约定 2/).waitFor();
 // Input changes preserve the independent requirement version and unrelated dependents.
 await work().locator('.task-list button').filter({hasText:'浏览器协作验收'}).click();await work().getByRole('button',{name:'更新输入资料',exact:true}).click();
 await work().getByLabel('结构化输入 JSON',{exact:true}).fill('{"currency":"CNY"}');await work().getByLabel('原因与处理意见',{exact:true}).fill('换用下一批数据，约束另行核对');await work().getByRole('button',{name:'提交更新输入资料',exact:true}).click();
 await work().getByText(/当前约定 3/).waitFor();await work().getByRole('button',{name:'查看要求历史',exact:true}).click();await work().getByRole('list',{name:'要求历史'}).getByText(/约定 3 · 需求 v2/).waitFor();
 await work().locator('summary').filter({hasText:'当前结构化输入'}).click();await work().getByText('"currency": "CNY"',{exact:false}).first().waitFor();
 await page.screenshot({path:join(output,'peer-structured-input.png'),fullPage:true});
 await work().locator('.task-list button').filter({hasText:'依赖约束的复核'}).click();await work().getByRole('button',{name:'验收通过',exact:true}).waitFor();
 await page.setViewportSize({width:390,height:844});
 await page.waitForFunction(()=>{const el=document.querySelector('.collaboration-dialog');const r=el?.getBoundingClientRect();return r && r.x>=0 && r.right<=window.innerWidth && el.scrollWidth<=el.clientWidth+1;});
 await work().evaluate(el=>el.scrollTop=0);
 await page.screenshot({path:join(output,'peer-mobile.png'),fullPage:true});
 assert.deepEqual(errors,[]);await writeFile(join(output,'report.json'),JSON.stringify({complete:true,errors,checks:['create Agent','capability mismatch','priced Agent matching','delegate','main conversation collaboration summary','business condition progress without percentage','inline current state','global sent scope','inline detail targeting','delegated Agent plan with exact step evidence','inline desktop and mobile layout','automatic scoped peer communication','live execution','per-condition data and real receipt rules','recipient does not self-accept','independent review without acceptance','immutable delivery and review history after reload','delivery acceptance','accepted evidence summary','structured competing claims','dispute blocks acceptance','user decision responsibility','evidence comparison and adopted conclusion','immutable disagreement history after reload','resolved dispute requires explicit reacceptance','scoped dependency','change propagation','stale delivery blocked','version history','question and reply','explicit dependency review','resumed plan preserves completed evidence','resumed plan adopts current agreement','new execution adoption','structured input admission','input revision history','input change does not invalidate unrelated fields','reload','mobile']},null,2));
} catch(e) {await page.screenshot({path:join(output,'failure.png'),fullPage:true});await writeFile(join(output,'failure.txt'),String(e)+'\n'+errors.join('\n')+'\n'+await page.locator('body').innerText());throw e;}
finally {await browser.close();}
