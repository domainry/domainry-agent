// Real Identity/HTTP/SQLite, product UI and file storage; synthetic Connector
// scope fixture only. The script never uses the user's browser or credentials.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.AGENT_PLAYWRIGHT_MODULE || 'playwright');
const inspect = (await import(pathToFileURL(process.env.AGENT_UI_QUALITY_CORE).href)).inspectStaticUiDocument;
const origin='http://127.0.0.1:8092', output=resolve(process.env.AGENT_UI_TEST_OUTPUT || '/tmp/domainry-k07-transfer-browser');
const remaining=process.env.AGENT_UI_TRANSFER_RECOVERY_ONLY==='1';
assert.equal((await (await fetch(`${origin}/app/config`)).json()).workspace_id,'document-workspace');
await mkdir(output,{recursive:true});
const browser=await chromium.launch({channel:'chrome',headless:true});
const context=await browser.newContext({viewport:{width:1280,height:900},acceptDownloads:true});
const page=await context.newPage();page.setDefaultTimeout(20000);
const report={complete:false,scope:remaining?'remaining recovery/index/cleanup':'full transfer journey',steps:[],baselines:[],attempts:[],errors:[],externalRequests:[]};
let scope='';
page.on('pageerror',e=>report.errors.push(String(e)));
page.on('request',r=>{const url=new URL(r.url());if(/^https?:/.test(url.protocol)&&url.origin!==origin)report.externalRequests.push(url.href);if(r.headers()['x-agent-scope'])scope=r.headers()['x-agent-scope'];});
const control=async name=>assert.equal((await fetch(`${origin}/__acceptance/${name}`,{method:'POST'})).status,204,name);
const step=name=>{report.steps.push(name);console.log(`PASS ${name}`);};
const libraryDialog=()=>page.getByRole('dialog',{name:'资料库',exact:true});
const transferDialog=()=>page.getByRole('dialog',{name:'复制或移动文档',exact:true});
const panel=()=>libraryDialog().getByRole('region',{name:'资料库文档',exact:true});
const filename='跨库移动验收.txt', bytes=Buffer.from('K07 browser private copy and move fixture.\n');
const chooseDocument=async()=>{await panel().getByRole('button',{name:new RegExp(`^${filename}`)}).click();await panel().getByRole('region',{name:'文档详情',exact:true}).waitFor();};
async function selectLibrary(name){await libraryDialog().getByRole('button',{name:new RegExp(`^${name} `)}).click();await panel().getByRole('button',{name:'刷新文档',exact:true}).waitFor();}
async function openLibraries(){if(page.viewportSize().width<650){await page.getByRole('button',{name:'打开工作导航',exact:true}).waitFor();await page.getByRole('button',{name:'打开工作导航',exact:true}).click();}await page.getByRole('button',{name:/^资料库/}).click();await libraryDialog().waitFor();}
async function baseline(label){report.baselines.push({label,...await page.evaluate(inspect,{})});await page.screenshot({path:join(output,`${label}.png`),animations:'disabled'});}
async function download(label){const completed=page.waitForEvent('download');await panel().getByRole('button',{name:'下载原文件',exact:true}).click();const file=await completed;const path=join(output,`${label}.txt`);await file.saveAs(path);assert.deepEqual(await readFile(path),bytes);}
async function removeDocument(){await panel().getByRole('button',{name:'删除文档',exact:true}).click();await panel().getByRole('button',{name:'确认删除文档',exact:true}).click();await panel().getByRole('button',{name:new RegExp(`^${filename}`)}).waitFor({state:'hidden'});}
try{
 await page.goto(origin);await page.getByLabel('账号',{exact:true}).fill('admin@example.com');await page.getByLabel('密码',{exact:true}).fill('Changed-Document-Test!3');await page.getByRole('button',{name:'登录',exact:true}).click();
 await control('resume_document_indexing');await openLibraries();await selectLibrary('个人资料');
 const sources=libraryDialog().getByRole('region',{name:'资料库知识源',exact:true});await sources.getByRole('button',{name:/知识源 second/}).click();await sources.getByRole('button',{name:'连接知识源',exact:true}).click();await sources.getByText(/已连接知识源/).waitFor();
 await panel().getByLabel('选择资料文件',{exact:true}).setInputFiles({name:filename,mimeType:'text/plain',buffer:bytes});
 const uploaded=page.waitForResponse(r=>r.request().method()==='POST'&&/\/documents\?/.test(r.url()));await panel().getByRole('button',{name:'保存到个人资料',exact:true}).click();const uploadResponse=await uploaded;assert.equal(uploadResponse.status(),200);const original=await uploadResponse.json();
 await panel().getByRole('button',{name:new RegExp(`^${filename}.*可检索`)}).waitFor();await chooseDocument();
 await panel().getByRole('button',{name:'复制／移动文档',exact:true}).click();await transferDialog().getByRole('button',{name:/^共享资料/}).click();await transferDialog().getByText(/目标资料库的所有阅读成员都能访问/).waitFor();
 if(!remaining){
  await baseline('copy-desktop');await page.setViewportSize({width:390,height:844});await baseline('copy-mobile');await page.setViewportSize({width:1280,height:900});
  await control('revoke_document_transfer');const denied=page.waitForResponse(r=>r.url().endsWith('/documents/from-document'));await transferDialog().getByRole('button',{name:'确认复制文档',exact:true}).click();assert.equal((await denied).status(),403);await transferDialog().getByRole('alert').waitFor();await control('restore_documents');
 }
 const copied=page.waitForResponse(r=>r.url().endsWith('/documents/from-document'));await transferDialog().getByRole('button',{name:remaining?'确认复制文档':'继续核对上次操作',exact:true}).click();const copyResponse=await copied;assert.equal(copyResponse.status(),200);const copy=await copyResponse.json();await transferDialog().getByRole('status').waitFor();
 await transferDialog().getByRole('button',{name:'查看目标资料库',exact:true}).click();await panel().getByRole('button',{name:new RegExp(`^${filename}.*可检索`)}).waitFor();await chooseDocument();if(!remaining){await download('shared-copy');step('private source explicitly copied to shared library; action revocation and recovery, desktop/mobile confirmation');}
 await selectLibrary('个人资料');await chooseDocument();if(!remaining)await download('retained-private-source');await removeDocument();await selectLibrary('共享资料');await chooseDocument();if(!remaining){await download('copy-after-source-delete');step('copy and original are independent; deleting private origin preserves shared copy');}
 await panel().getByRole('button',{name:'复制／移动文档',exact:true}).click();await transferDialog().getByLabel('操作方式',{exact:true}).selectOption('move');await transferDialog().getByRole('button',{name:/^个人资料/}).click();await transferDialog().getByText(/目标文件仅自己可见/).waitFor();if(!remaining)await baseline('move-desktop');
 let lost=false,moved;
 await page.route('**/documents/from-document',async route=>{
  if(route.request().method()!=='POST')return route.continue();
  const input=route.request().postDataJSON();report.attempts.push(input);
  if(!lost){lost=true;const response=await route.fetch();assert.equal(response.status(),200);moved=await response.json();return route.abort('connectionreset');}
  return route.continue();
 });
 await transferDialog().getByRole('button',{name:'确认移动文档',exact:true}).click();await transferDialog().getByRole('alert').waitFor();assert(moved);
 await control('remove_datasource_config');await page.reload();await openLibraries();await libraryDialog().getByRole('button',{name:'核对上次文档操作',exact:true}).click();
 const retry=transferDialog().getByRole('button',{name:'继续核对上次操作',exact:true});await retry.click({trial:true});assert(await retry.isEnabled(),'committed receipt must recover while knowledge source unavailable');
 if(!remaining){await page.setViewportSize({width:390,height:844});await baseline('pending-recovery-mobile');await page.setViewportSize({width:1280,height:900});}
 const recovered=page.waitForResponse(r=>r.url().endsWith('/documents/from-document'));await retry.click();const receipt=await recovered;assert.equal(receipt.status(),200);assert.equal((await receipt.json()).id,moved.id);assert.equal(report.attempts.length,2);assert.deepEqual(report.attempts[0],report.attempts[1]);
 await transferDialog().getByRole('button',{name:'查看目标资料库',exact:true}).click();await chooseDocument();await download('private-move-after-restart');
 const old=await context.request.get(`${origin}/agent/knowledge-libraries/${copy.library_id}/documents/${copy.id}/content`,{headers:{'X-Agent-Scope':scope}});assert.equal(old.status(),404);
 step('lost move response recovered after complete host restart and source configuration removal using the same request; old source denied');
 await control('restore_datasource_config');await page.reload();await openLibraries();await selectLibrary('个人资料');
 // The persisted worker retries unavailable bindings after 30 seconds, then
 // the UI polls every 5 seconds. Observe that schedule without bypassing it.
 await panel().getByRole('button',{name:new RegExp(`^${filename}.*可检索`)}).waitFor({timeout:45000});await chooseDocument();await removeDocument();
 assert.deepEqual(report.errors,[]);assert.deepEqual(report.externalRequests,[]);
 for(const baseline of report.baselines){assert.deepEqual(baseline.qualityFailures,[],baseline.label);assert(!baseline.pageOverflow,baseline.label);}
 const pendingKeys=await page.evaluate(()=>Object.keys(localStorage).filter(k=>k.startsWith('agent-document-transfer:')));assert.deepEqual(pendingKeys,[]);
 report.documents={original:original.id,copy:copy.id,moved:moved.id};report.complete=true;step('source configuration restored, final target indexed and deleted; no pending transfer receipt');
}catch(error){report.failure=String(error);await page.screenshot({path:join(output,'failure.png')}).catch(()=>{});throw error;}
finally{await writeFile(join(output,'report.json'),JSON.stringify(report,null,2));await context.close();await browser.close();await control('finish').catch(()=>{});}
