// Product browser + real Identity/HTTP/original stores. Connector/model responses
// are synthetic fixtures; rendering always consumes the uploaded binary file.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const ExcelJS = require('exceljs');
const inspect = (await import(pathToFileURL(process.env.AGENT_UI_QUALITY_CORE).href)).inspectStaticUiDocument;
const origin = 'http://127.0.0.1:8092';
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id, 'document-workspace');
const output = process.env.AGENT_UI_TEST_OUTPUT || '/tmp/domainry-file-preview-browser'; await mkdir(output,{recursive:true});
const report = { complete:false, steps:[], baselines:[], errors:[], fileRequests:[], externalRequests:[] };
const browser = await chromium.launch({channel:'chrome',headless:true});
const context = await browser.newContext({viewport:{width:1280,height:900},acceptDownloads:true});
const page = await context.newPage(); page.setDefaultTimeout(20000);
page.on('pageerror', e => report.errors.push(String(e)));
page.on('request', r => { if (r.url().endsWith('/content')) report.fileRequests.push(r.url()); if (!r.url().startsWith(origin) && /^https?:/.test(r.url())) report.externalRequests.push(r.url()); });
const control = async name => assert.equal((await fetch(`${origin}/__acceptance/${name}`,{method:'POST'})).status,204,name);
const step = name => { report.steps.push(name); console.log(`PASS ${name}`); };
const wait = async (check,name) => { const end=Date.now()+20000; while(Date.now()<end) { if(await check())return; await new Promise(r=>setTimeout(r,100)); } throw Error(name); };
async function openLibrary() {
 if(page.viewportSize().width<650) await page.getByRole('button',{name:'打开工作导航',exact:true}).click();
 await page.getByRole('button',{name:/^资料库/}).click();
 const dialog=page.getByRole('dialog',{name:'资料库',exact:true}); await dialog.getByRole('button',{name:/^共享资料 /}).click();
 const panel=dialog.getByRole('region',{name:'资料库文档',exact:true}); await wait(()=>panel.getByRole('button',{name:'刷新文档',exact:true}).isEnabled(),'library loading'); return {dialog,panel};
}
async function baseline(label) {
 const result=await page.evaluate(inspect,{}); report.baselines.push({label,...result});
 await page.screenshot({path:join(output,`${label}.png`),animations:'disabled'});
 // Collect the complete baseline; diagnose together after this bounded round.
}
const files=[]; const previewDialog=()=>page.getByRole('dialog').filter({has:page.getByText('原文件在线预览',{exact:true})});
async function closePreview() { await previewDialog().getByRole('button',{name:'关闭',exact:true}).click(); }
let finished=false;
try {
 await page.goto(origin); await page.getByLabel('账号',{exact:true}).fill('admin@example.com'); await page.getByLabel('密码',{exact:true}).fill('Changed-Document-Test!3'); await page.getByRole('button',{name:'登录',exact:true}).click();
 for(const format of ['pdf','docx','xlsx']) {
  const name=`synthetic-extraction.${format}`;
  let bytes=await readFile(resolve(`integration/testdata/knowledge-extraction/${name}`));
  if(format==='xlsx') {
   const workbook=new ExcelJS.Workbook(); await workbook.xlsx.load(bytes);
   const sheet=workbook.addWorksheet('第二张工作表'); sheet.mergeCells('A1:C1'); sheet.getCell('A1').value='合并表头'; sheet.getCell('A1').font={bold:true,color:{argb:'FFFFFFFF'}}; sheet.getCell('A1').fill={type:'pattern',pattern:'solid',fgColor:{argb:'FF285C4D'}};
   sheet.getCell('B2').value={formula:'1+2',result:3}; sheet.getCell('B2').numFmt='0.00'; sheet.getCell('B62').value='最后一页数据'; sheet.getCell('T62').value='最后一组列'; bytes=Buffer.from(await workbook.xlsx.writeBuffer());
  }
  const {dialog,panel}=await openLibrary();
  await panel.getByLabel('选择资料文件',{exact:true}).setInputFiles({name,mimeType:'application/octet-stream',buffer:bytes}); await panel.getByRole('button',{name:'上传到共享资料库',exact:true}).click(); files.push(name);
  await panel.getByRole('button',{name:new RegExp(`^${name}`)}).click(); await control('index_documents'); await panel.getByText(/已完成知识索引/).waitFor();
  if(!(format === 'pdf' && process.env.AGENT_UI_PREVIEW_REMAINING === '1')) {
  const before=report.fileRequests.length;
  await panel.getByRole('button',{name:'预览原文件',exact:true}).click();
  const viewer=previewDialog(); await viewer.waitFor();
  if(format==='pdf') {
   await viewer.locator('canvas[data-rendered="true"]').waitFor(); await viewer.getByLabel('缩放',{exact:true}).selectOption('1.5'); await viewer.locator('canvas[data-rendered="true"]').waitFor();
  } else if(format==='docx') {
   await page.frameLocator('iframe[title="Word 原文件预览"]').getByText('Customer: Qinghe Fixture',{exact:true}).waitFor();
   await page.frameLocator('iframe[title="Word 原文件预览"]').getByText('Pencil',{exact:true}).waitFor();
   assert.equal(await viewer.locator('iframe').getAttribute('sandbox'),'allow-same-origin');
  } else {
   await viewer.getByRole('table').getByText('9007199254740993.25 CNY',{exact:true}).waitFor();
   await viewer.getByLabel('工作表',{exact:true}).selectOption({label:'第二张工作表'});
   assert.equal(await viewer.getByRole('cell',{name:'合并表头',exact:true}).getAttribute('colspan'),'3');
   await viewer.getByRole('cell',{name:'3.00',exact:true}).click(); await viewer.getByText(/=1\+2 → 3.00/).waitFor();
   await viewer.getByRole('button',{name:'下一页行',exact:true}).click(); await viewer.getByRole('cell',{name:'最后一页数据',exact:true}).waitFor();
   await viewer.getByLabel('定位单元格',{exact:true}).fill('T62'); await viewer.getByRole('button',{name:'定位',exact:true}).click(); await viewer.getByRole('cell',{name:'最后一组列',exact:true}).waitFor();
  }
  assert.equal(report.fileRequests.length-before,1,'opening uses one original content operation');
  await baseline(`desktop-${format}`);
  await page.setViewportSize({width:390,height:844}); await baseline(`mobile-${format}`); await page.setViewportSize({width:1280,height:900});
  const pending=page.waitForEvent('download'); await viewer.getByRole('button',{name:'下载原文件',exact:true}).click(); const downloaded=await pending; const path=join(output,name); await downloaded.saveAs(path); assert.deepEqual(await readFile(path),bytes);
  await closePreview(); await dialog.getByRole('button',{name:'关闭',exact:true}).click();
  step(`${format}: real original preview, desktop/mobile, matching download`);
  } else { await dialog.getByRole('button',{name:'关闭',exact:true}).click(); }
  if(format==='pdf') {
   await page.getByRole('button',{name:'新建会话',exact:true}).click(); const create=page.getByRole('dialog',{name:'新建会话',exact:true}); await create.getByLabel('会话名称',{exact:true}).fill('原文件在线预览验收'); await create.getByRole('button',{name:'保存',exact:true}).click();
   await page.getByRole('textbox',{name:'消息',exact:true}).fill('查询共享资料中的费用规则'); await page.getByRole('button',{name:'发送消息',exact:true}).click();
   const log=page.getByRole('log'); await log.getByRole('button',{name:/^查看来源：/}).last().waitFor();
   await page.getByRole('button',{name:'发送消息',exact:true}).waitFor(); await log.getByRole('button',{name:/^查看来源：/}).last().click(); await page.getByRole('button',{name:'预览原文件',exact:true}).click(); await previewDialog().locator('canvas[data-rendered="true"]').waitFor();
   await baseline('retrieval-original'); await closePreview(); await page.getByRole('dialog').getByRole('button',{name:'关闭',exact:true}).click(); step('Connector search citation opens mapped original');
  }
 }
 await control('restart_document_host'); await page.reload();
 let {dialog,panel}=await openLibrary(); await panel.getByRole('button',{name:/^synthetic-extraction.pdf/}).click(); await panel.getByRole('button',{name:'预览原文件',exact:true}).click(); await previewDialog().locator('canvas[data-rendered="true"]').waitFor();
 await control('revoke_document_download'); await previewDialog().getByRole('button',{name:'重新打开',exact:true}).click(); await previewDialog().getByRole('alert').waitFor(); assert.equal(await previewDialog().locator('canvas').count(),0); await baseline('revoked-original');
 await control('restore_documents'); await previewDialog().getByRole('button',{name:'重新打开',exact:true}).click(); await previewDialog().locator('canvas[data-rendered="true"]').waitFor(); await closePreview(); await dialog.getByRole('button',{name:'关闭',exact:true}).click(); step('restart and permission revoke/restore');
 for(const name of files) {
  const {dialog,panel}=await openLibrary(); await panel.getByRole('button',{name:new RegExp(`^${name}`)}).click(); await panel.getByRole('button',{name:'删除文档',exact:true}).click(); await panel.getByRole('button',{name:'确认删除文档',exact:true}).click(); await panel.getByText('文档已停止检索和下载，后台正在核对并清理原文件与索引。',{exact:true}).waitFor(); await dialog.getByRole('button',{name:'关闭',exact:true}).click();
 }
 step('synthetic originals deleted');
 assert.deepEqual(report.errors,[]); assert.deepEqual(report.externalRequests,[],'original bytes stay in this application');
 for(const baseline of report.baselines) { assert.deepEqual(baseline.qualityFailures,[],baseline.label); assert(!baseline.pageOverflow,baseline.label); }
 report.complete=true; finished=true;
} catch(error) { report.failure=String(error); await page.screenshot({path:join(output,'failure.png')}).catch(()=>{}); throw error; }
finally { await writeFile(join(output,'report.json'),JSON.stringify(report,null,2)); await context.close(); await browser.close(); await control('finish').catch(()=>{}); }
