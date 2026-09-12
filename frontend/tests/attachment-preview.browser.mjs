// Binary attachment previews against real Identity/HTTP/SQLite and original
// storage. No knowledge service or model operation is configured/needed.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const ExcelJS = require('exceljs');
const inspect = (await import(pathToFileURL(process.env.AGENT_UI_QUALITY_CORE).href)).inspectStaticUiDocument;
const origin='http://127.0.0.1:8092', output=resolve(process.env.AGENT_UI_TEST_OUTPUT || '/tmp/domainry-attachment-preview-browser');
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id,'attachment-workspace');
await mkdir(output,{recursive:true});
const browser=await chromium.launch({channel:'chrome',headless:true});
const context=await browser.newContext({viewport:{width:1280,height:900},acceptDownloads:true});
const page=await context.newPage(); page.setDefaultTimeout(20000);
const report={complete:false,steps:[],baselines:[],errors:[],externalRequests:[],contentRequests:[],unexpectedWork:[]};
let scope='';
page.on('pageerror',e=>report.errors.push(String(e)));
page.on('request',r=>{
 const url=new URL(r.url());
 if(/^https?:/.test(url.protocol)&&url.origin!==origin) report.externalRequests.push(url.href);
 if(url.pathname.endsWith('/content')) { scope=r.headers()['x-agent-scope']||scope; report.contentRequests.push(url.pathname); }
 if(/\/runs|\/turns|\/knowledge-libraries/.test(url.pathname)&&r.method()==='POST') report.unexpectedWork.push(url.pathname);
});
const control=async name=>assert.equal((await fetch(`${origin}/__acceptance/${name}`,{method:'POST'})).status,204,name);
const step=name=>{report.steps.push(name);console.log(`PASS ${name}`);};
const attachmentDialog=()=>page.getByRole('dialog',{name:'会话附件',exact:true});
const previewDialog=()=>page.getByRole('dialog').filter({has:page.getByText('原文件在线预览',{exact:true})});
async function openAttachments(){await page.getByRole('button',{name:'附件',exact:true}).click();await attachmentDialog().waitFor();}
async function closePreview(){await previewDialog().getByRole('button',{name:'关闭',exact:true}).click();}
async function createConversation(name){
 await page.getByRole('button',{name:'新建会话',exact:true}).click();const dialog=page.getByRole('dialog',{name:'新建会话',exact:true});await dialog.getByLabel('会话名称',{exact:true}).fill(name);await dialog.getByRole('button',{name:'保存',exact:true}).click();await dialog.waitFor({state:'hidden'});
 const id=new URL(page.url()).hash.slice(1); assert.match(id,/^conv_[a-f0-9]{32}$/);return id;
}
async function baseline(label){const result=await page.evaluate(inspect,{});report.baselines.push({label,...result});await page.screenshot({path:join(output,`${label}.png`),animations:'disabled'});}
const files=[];
try {
 await page.goto(origin);await page.getByLabel('账号',{exact:true}).fill('admin@example.com');await page.getByLabel('密码',{exact:true}).fill('Changed-Attachment-Test!3');await page.getByRole('button',{name:'登录',exact:true}).click();
 const conversationID=await createConversation('原文件附件预览验收');report.conversationID=conversationID;
 for(const format of ['pdf','docx','xlsx']){
  const name=`preview-original.${format}`;
  let bytes=await readFile(format==='pdf'?resolve('integration/testdata/file-preview/two-page.pdf'):resolve(`integration/testdata/knowledge-extraction/synthetic-extraction.${format}`));
  if(format==='xlsx'){
   const workbook=new ExcelJS.Workbook();await workbook.xlsx.load(bytes);
   const sheet=workbook.addWorksheet('第二张工作表');sheet.mergeCells('A1:C1');sheet.getCell('A1').value='合并表头';sheet.getCell('A1').font={bold:true,color:{argb:'FFFFFFFF'}};sheet.getCell('A1').fill={type:'pattern',pattern:'solid',fgColor:{argb:'FF285C4D'}};
   sheet.getCell('B2').value={formula:'1+2',result:3};sheet.getCell('B2').numFmt='0.00';sheet.getCell('B62').value='最后一页数据';sheet.getCell('T62').value='最后一组列';bytes=Buffer.from(await workbook.xlsx.writeBuffer());
  }
  await openAttachments();const dialog=attachmentDialog();await dialog.getByLabel('选择附件',{exact:true}).setInputFiles({name,mimeType:'application/octet-stream',buffer:bytes});
  const uploaded=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname.endsWith('/attachments'));
  await dialog.getByRole('button',{name:'上传并私有保存',exact:true}).click();const response=await uploaded;assert.equal(response.status(),200);const file=await response.json();assert.equal(file.state,'stored');assert(!Object.hasOwn(file,'parse_supported'));files.push({...file,bytes});
  await dialog.getByRole('button',{name:new RegExp(`^${name}`)}).click();
  const start=report.contentRequests.length;await dialog.getByRole('button',{name:'预览原文件',exact:true}).click();const viewer=previewDialog();
  if(format==='pdf'){
   const canvas=viewer.locator('canvas[data-rendered="true"]');await canvas.waitFor();await viewer.getByText('第 1 / 2 页',{exact:true}).waitFor();const first=await canvas.evaluate(c=>({width:c.width,height:c.height}));assert(first.height>first.width);
   await viewer.getByRole('button',{name:'下一页',exact:true}).click();await viewer.getByText('第 2 / 2 页',{exact:true}).waitFor();await canvas.waitFor();const second=await canvas.evaluate(c=>({width:c.width,height:c.height}));assert(second.width>second.height,'second PDF page has its actual rotated dimensions');
   await viewer.getByRole('button',{name:'上一页',exact:true}).click();await canvas.waitFor();await viewer.getByLabel('缩放',{exact:true}).selectOption('1.5');await canvas.waitFor();
  }else if(format==='docx'){
   await page.frameLocator('iframe[title="Word 原文件预览"]').getByText('Customer: Qinghe Fixture',{exact:true}).waitFor();await page.frameLocator('iframe[title="Word 原文件预览"]').getByText('Pencil',{exact:true}).waitFor();
  }else{
   await viewer.getByRole('table').getByText('9007199254740993.25 CNY',{exact:true}).waitFor();await viewer.getByLabel('工作表',{exact:true}).selectOption({label:'第二张工作表'});
   assert.equal(await viewer.getByRole('cell',{name:'合并表头',exact:true}).getAttribute('colspan'),'3');await viewer.getByRole('cell',{name:'3.00',exact:true}).click();await viewer.getByText(/=1\+2 → 3.00/).waitFor();
   await viewer.getByRole('button',{name:'下一页行',exact:true}).click();await viewer.getByRole('cell',{name:'最后一页数据',exact:true}).waitFor();await viewer.getByLabel('定位单元格',{exact:true}).fill('T62');await viewer.getByRole('button',{name:'定位',exact:true}).click();await viewer.getByRole('cell',{name:'最后一组列',exact:true}).waitFor();
  }
  assert.equal(report.contentRequests.length-start,1,'one original request; changing pages or sheets makes no backend call');
  await baseline(`desktop-${format}`);await page.setViewportSize({width:390,height:844});await baseline(`mobile-${format}`);await page.setViewportSize({width:1280,height:900});
  const download=page.waitForEvent('download');await viewer.getByRole('button',{name:'下载原文件',exact:true}).click();const fileDownload=await download;const downloadPath=join(output,name);await fileDownload.saveAs(downloadPath);assert.deepEqual(await readFile(downloadPath),bytes);
  await closePreview();await dialog.getByRole('button',{name:'关闭',exact:true}).click();step(`${format}: private original rendered, desktop/mobile and exact download`);
 }
 const other=await createConversation('其他会话隔离验收');
 const path=`/agent/conversations/${conversationID}/attachments/${files[0].id}/content`;
 const mismatched=await context.request.get(`${origin}${path.replace(conversationID,other)}`,{headers:{'X-Agent-Scope':scope}});assert.equal(mismatched.status(),404);assert(!(await mismatched.text()).includes('%PDF'));step('cross-conversation original request denied');
 await page.getByRole('navigation',{name:'会话列表',exact:true}).getByRole('button',{name:/^原文件附件预览验收/}).click();
 await control('restart_attachment_host');await page.reload();
 await openAttachments();await attachmentDialog().getByRole('button',{name:/^preview-original.pdf/}).click();await attachmentDialog().getByRole('button',{name:'预览原文件',exact:true}).click();await previewDialog().locator('canvas[data-rendered="true"]').waitFor();
 await control('revoke_attachment_download');await previewDialog().getByRole('button',{name:'重新打开',exact:true}).click();await previewDialog().getByRole('alert').waitFor();assert.equal(await previewDialog().locator('canvas').count(),0);await baseline('revoked-attachment');
 await control('restore_attachments');await previewDialog().getByRole('button',{name:'重新打开',exact:true}).click();await previewDialog().locator('canvas[data-rendered="true"]').waitFor();await closePreview();step('host restart, permission denial and recovery');
 const dialog=attachmentDialog();
 for(const file of files){
  await dialog.getByRole('button',{name:new RegExp(`^${file.filename}`)}).click();const deleted=page.waitForResponse(r=>r.request().method()==='DELETE'&&new URL(r.url()).pathname.endsWith(`/attachments/${file.id}`));await dialog.getByRole('button',{name:'删除附件',exact:true}).click();await dialog.getByRole('button',{name:'确认删除附件',exact:true}).click();assert.equal((await deleted).status(),200);await dialog.getByRole('button',{name:new RegExp(`^${file.filename}`)}).waitFor({state:'hidden'});
  const missing=await context.request.get(`${origin}/agent/conversations/${conversationID}/attachments/${file.id}/content`,{headers:{'X-Agent-Scope':scope}});assert.equal(missing.status(),404);
 }
 step('deleted attachments stop serving original bytes');
 assert.deepEqual(report.errors,[]);assert.deepEqual(report.externalRequests,[]);assert.deepEqual(report.unexpectedWork,[]);
 for(const baseline of report.baselines){assert.deepEqual(baseline.qualityFailures,[],baseline.label);assert(!baseline.pageOverflow,baseline.label);}
 report.complete=true;
}catch(error){report.failure=String(error);await page.screenshot({path:join(output,'failure.png')}).catch(()=>{});throw error;}
finally{await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));await context.close();await browser.close();await control('finish').catch(()=>{});}
