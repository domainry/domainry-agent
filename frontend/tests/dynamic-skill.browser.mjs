import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const { chromium } = createRequire(import.meta.url)(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.AGENT_UI_ORIGIN;
const output = process.env.AGENT_UI_TEST_OUTPUT;
const password = process.env.AGENT_UI_PASSWORD;
assert.ok(origin && output && password, 'AGENT_UI_ORIGIN, AGENT_UI_TEST_OUTPUT and AGENT_UI_PASSWORD are required');
await mkdir(output, { recursive: true });
assert.equal((await (await fetch(origin + '/app/config')).json()).workspace_id, 'skill-browser-workspace', 'refuse a non-fixture deployment');

const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const context = await browser.newContext({ viewport: { width: 1360, height: 1000 } });
const page = await context.newPage();
page.setDefaultTimeout(45000);
const report = { steps: [], javascriptErrors: [], consoleErrors: [], consoleWarnings: [], resourceFailures: [], mutations: [] };
let loggedIn = false;
page.on('pageerror', error => report.javascriptErrors.push(String(error)));
page.on('console', message => {
  if (message.type() === 'error' && !message.text().startsWith('Failed to load resource:')) report.consoleErrors.push(message.text());
  if (message.type() === 'warning') report.consoleWarnings.push(message.text());
});
page.on('response', response => {
  const path = new URL(response.url()).pathname;
  if (loggedIn && response.status() >= 400) report.resourceFailures.push({ status: response.status(), path });
  if (response.request().method() === 'POST' && (/capability-feedback|improvement-candidates/.test(path))) report.mutations.push({ path, status: response.status() });
});
const step = value => { report.steps.push(value); console.log('PASS ' + value); };

try {
  await page.goto(origin);
  await page.getByLabel('账号', { exact: true }).fill('admin@example.com');
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  loggedIn = true;

  await page.getByRole('button', { name: '后台任务 进度、等待与成果', exact: true }).click();
  const tasks = page.getByRole('dialog', { name: '后台任务', exact: true });
  await tasks.getByRole('button').filter({ hasText: 'K01 记录真实任务反馈' }).click();
  const feedback = tasks.getByRole('region', { name: '任务结果反馈', exact: true });
  await feedback.waitFor();
  assert.match(await feedback.innerText(), /Agent default · agent-revision-/);
  assert.equal(await feedback.getByLabel('report · 1', { exact: true }).isChecked(), true);
  await feedback.getByLabel('任务反馈结果', { exact: true }).selectOption('revised');
  await feedback.getByLabel('反馈原因', { exact: true }).fill('用户修订了报告结构，需要形成可复用改进');
  await feedback.getByRole('button', { name: '保存反馈', exact: true }).click();
  await feedback.getByText('已记录：成果经过修改', { exact: true }).waitFor();
  const feedbackText = await feedback.innerText();
  const feedbackID = feedbackText.match(/反馈 (feedback_[a-f0-9]+)/)?.[1];
  assert.ok(feedbackID, feedbackText);
  assert.match(feedbackText, /指向 report@1/);
  step('completed task feedback records the exact Agent prompt and selected Skill version');

  await page.keyboard.press('Escape');
  await tasks.waitFor({ state: 'hidden' });
  await page.getByRole('button', { name: 'Agent 协作 目录、委派与沟通', exact: true }).click();
  const collaboration = page.getByRole('dialog', { name: 'Agent 协作', exact: true });
  await collaboration.getByRole('button', { name: 'Skill 与改进', exact: true }).click();
  const skills = collaboration.getByRole('region', { name: 'Skill 与能力改进', exact: true });
  await skills.getByRole('heading', { name: 'Report Skill', exact: true }).waitFor();
  assert.doesNotMatch(await skills.innerText(), /K01 FULL BODY ONE|K01 RESOURCE ONE/);
  await skills.getByRole('button', { name: '按需读取正文', exact: true }).click();
  await skills.getByText('K01 FULL BODY ONE', { exact: true }).waitFor();
  assert.doesNotMatch(await skills.innerText(), /K01 RESOURCE ONE/);
  await skills.getByRole('button', { name: 'Report Template', exact: true }).click();
  await skills.getByText('K01 RESOURCE ONE', { exact: true }).waitFor();
  step('compiled UI keeps summary, instructions, workflow and resource bodies separately addressable');

	  await skills.getByRole('button', { name: '以此 Skill 创建改进候选', exact: true }).click();
	  const create = skills.locator('form[aria-label="创建能力改进候选"]');
	  const proposalInput = create.locator('textarea').first();
  await page.waitForFunction(element => element.value.includes('K01 RESOURCE ONE'), await proposalInput.elementHandle());
  await create.getByLabel('新版本', { exact: true }).fill('2');
  await create.getByLabel('关联反馈 ID（逗号或空格分隔）', { exact: true }).fill(feedbackID);
  const proposal = JSON.parse(await proposalInput.inputValue());
  proposal.instructions = 'K01 FULL BODY TWO';
  await proposalInput.fill(JSON.stringify(proposal, null, 2));
  await create.getByLabel('改进原因', { exact: true }).fill('应用真实任务修订反馈');
  await create.getByRole('button', { name: '保存候选', exact: true }).click();
  const candidate = skills.locator('.peer-card').filter({ hasText: 'Skill · report · 2' });
  await candidate.getByText('待评估', { exact: true }).waitFor();
  assert.match(await candidate.innerText(), /基线 1/);
  step('candidate creation retains the separately loaded resource and binds exact feedback and baseline');

  await candidate.getByRole('button', { name: '登记 V01 对照评估', exact: true }).click();
	  const evaluation = skills.locator('form[aria-label="登记 V01 对照评估"]');
  await evaluation.getByLabel('评估套件版本', { exact: true }).fill('v01-k01-browser');
  await evaluation.getByLabel('场景 ID（逗号或空格分隔）', { exact: true }).fill('skill-summary skill-resource feedback-version rollback');
  await evaluation.getByLabel('基线完成数', { exact: true }).fill('4');
  await evaluation.getByLabel('候选完成数', { exact: true }).fill('4');
  await evaluation.getByLabel('基线遗漏数', { exact: true }).fill('1');
  await evaluation.getByLabel('候选遗漏数', { exact: true }).fill('0');
  await evaluation.getByLabel('评估说明', { exact: true }).fill('相同任务、模型与预算的 K01 对照场景');
  await evaluation.getByLabel('对照评估通过', { exact: true }).check();
  await evaluation.getByRole('button', { name: '保存评估结果', exact: true }).click();
  await candidate.getByText('已评估', { exact: true }).waitFor();
  await candidate.getByRole('button', { name: '发布此版本', exact: true }).click();
  await candidate.getByText('已发布', { exact: true }).waitFor();
  await skills.getByText('回退基线', { exact: true }).waitFor();
  await skills.getByText('2', { exact: true }).first().waitFor();
  step('only a passing V01 comparison publishes v2 and archives the original v1 baseline');

  await candidate.getByRole('button', { name: '回退配置', exact: true }).click();
	  const rollback = skills.locator('form[aria-label="回退能力配置"]');
  assert.match(await rollback.innerText(), /已经发生的工具操作和业务效果仍保留/);
  await rollback.getByLabel('恢复到已评估版本', { exact: true }).fill('1');
  await rollback.getByLabel('回退原因', { exact: true }).fill('验收首次托管前版本可恢复');
  await page.screenshot({ path: join(output, 'dynamic-skill-before-rollback.png'), fullPage: true });
  await rollback.getByRole('button', { name: '确认回退', exact: true }).click();
  await skills.locator('.peer-card').filter({ hasText: 'Report Skill' }).getByText('1', { exact: true }).waitFor();
  step('rollback restores the first deployed version while explicitly retaining prior business effects');

  await page.setViewportSize({ width: 390, height: 844 });
  await skills.scrollIntoViewIfNeeded();
  await page.waitForFunction(() => {
    const dialog = document.querySelector('.collaboration-dialog');
    if (!dialog) return false;
    const rect = dialog.getBoundingClientRect();
    return rect.x >= 0 && rect.right <= innerWidth && dialog.scrollWidth <= dialog.clientWidth + 1;
  });
  await page.screenshot({ path: join(output, 'dynamic-skill-mobile.png'), fullPage: true });
  assert.deepEqual(report.javascriptErrors, []);
  assert.deepEqual(report.consoleErrors, []);
  assert.deepEqual(report.consoleWarnings, []);
  assert.deepEqual(report.resourceFailures, []);
  assert.deepEqual(report.mutations.map(item => item.status), [200, 200, 200, 200, 200]);
  report.complete = true;
  report.feedbackID = feedbackID;
} catch (error) {
  report.error = String(error);
  await page.screenshot({ path: join(output, 'dynamic-skill-failure.png'), fullPage: true }).catch(() => {});
  throw error;
} finally {
  await writeFile(join(output, 'dynamic-skill-report.json'), JSON.stringify(report, null, 2));
  await browser.close();
}
