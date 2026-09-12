import { useEffect, useRef, useState } from "react";
import { ArrowLeft, ArrowUpRight, BookOpen, CheckSquare, ChevronRight, FileText, History, Loader2, LogOut, Plus, Search, Sparkles, X } from "lucide-react";
import App from "./App";
import { ToolSettingsDialog } from "./ToolSettingsDialog";
import { ExternalAccountsDialog } from "./ExternalAccountsDialog";
import { loadAuthorization } from "./external-account-state.ts";
import { request, type ConversationRecord } from "./api";
import { ApiError } from "./errors";
import { DraftStore } from "./drafts";
import type { AppSession } from "./session";
import { TodoDialog } from "./TodoDialog";
import { KnowledgeLibraryDialog } from "./KnowledgeLibraryDialog";
import { ArtifactDialog } from "./ArtifactDialog";
import "./product.css";

type Field = { key: string; label: string; type: string; options?: string[]; option_labels?:Record<string,string> };
type Resource = {kind:string; name:string; singular:string; description:string; initial_status:string; state_order?:string[]; states:Record<string,string>; fields:Field[]; defaults:Record<string,string|number>};
type Config = {eyebrow?:string; key:string; name:string; tagline:string; agent_name:string; resources:Resource[]; starters:string[]};
type Item = {id:string; kind:string; title:string; status:string; revision:number; data:Record<string,string|number>; updated_at:string};
type Page = {items:Item[]; next_cursor?:string; complete:boolean};
type Write = {client_id:string; id?:string; expected_revision:number; title:string; status:string; data:Item["data"]};
const url = (kind:string) => `/app/product/records/${encodeURIComponent(kind)}`;
function message(error:unknown) {
  if (error instanceof ApiError) {
    const labels:Record<string,string>={
      "knowledge.records.revision_conflict":"记录已有新版本。请重新读取，再合并你的修改。",
      "knowledge.records.idempotency_conflict":"这次请求的内容与原请求不同，请读取最新记录后再提交。",
      "pm.prd_and_acceptance_required":"进入规划前，请补齐用户问题、目标用户、目标、PRD 和验收标准。",
      "pm.acceptance_evidence_required":"验收通过需要选择「passed」并填写实际验证证据。",
      "pm.reopen_before_edit":"已验收的需求需先返回待验收，再修改内容。",
      "pm.transition_invalid":"当前状态不能直接进入目标状态，请按需求流程推进。",
      "work.transition_invalid":"当前状态不能直接进入目标状态，请按文档流程推进。",
      "work.meeting_notes_required":"请补齐会议日期与会议记录。",
      "work.meeting_outcome_required":"确认会议前，请填写已达成的决策或行动项。",
      "work.document_content_required":"请补齐面向对象与文档内容。",
      "pm.initial_status_required":"新需求需从需求池开始。",
      "work.initial_status_required":"新记录需从草稿开始。",
    };
    if(labels[error.code])return labels[error.code];
    if(error.status===400)return "内容未通过校验，请检查字段格式、长度和必填项。";
    if(error.status===403)return "当前账号没有执行此操作的权限。";
  }
  return "服务暂时不可用。请重试；尚未确认的保存会沿用原请求。";
}
export default function ProductWorkspace({session,onLogout}:{session:AppSession;onLogout:()=>void}){
  const [config,setConfig]=useState<Config|null>(null);
  const [tab,setTab]=useState("");
  const [error,setError]=useState("");
  const [dialog,setDialog]=useState<"todo"|"knowledge"|"artifact"|"accounts"|"tools"|null>(() => loadAuthorization(session.scope) ? "accounts" : null);
  const [creating,setCreating]=useState(false);
  const [agentKey,setAgentKey]=useState(0);
 const [setup,setSetup]=useState(true);const [settingUp,setSettingUp]=useState(false);
 useEffect(()=>{request<{ready:boolean}>("/app/product/setup").then(s=>setSetup(s.ready)).catch(e=>setError(message(e)))},[]);
  useEffect(()=>{let gone=false;request<Config>("/app/product/config").then(c=>{if(!gone){setConfig(c);setTab(c.resources[0].kind);document.title=c.name}}).catch(e=>{if(!gone)setError(message(e))});return()=>{gone=true}},[]);
  async function ask(prompt:string){if(creating)return;setCreating(true);setError("");try{
    const conversation=await request<ConversationRecord>("/agent/conversations","POST",{client_id:crypto.randomUUID(),title:prompt.slice(0,65),memory_enabled:true});
    const drafts=new DraftStore(()=>localStorage,session.scope);drafts.write(conversation.id,prompt);location.hash=conversation.id;setAgentKey(n=>n+1);setTab("agent");
  }catch(e){setError(message(e))}finally{setCreating(false)}}
  if(!config)return <main className="product-loading"><Sparkles size={24}/><p>{error||"正在打开工作空间…"}</p>{error&&<button onClick={()=>location.reload()}>重新连接</button>}</main>;
  return <div className="product-shell" data-product={config.key}>
    <header className="product-top"><a className="product-brand" href="#" onClick={e=>{e.preventDefault();setTab(config.resources[0].kind)}}><span>d.</span><strong>{config.name}</strong></a><div className="product-account"><span>{session.name || "我的工作空间"}</span><button aria-label="退出登录" onClick={onLogout}><LogOut size={17}/></button></div></header>
    <nav className="product-nav" aria-label="产品导航">{config.resources.map(r=><button key={r.kind} data-active={tab===r.kind} onClick={()=>setTab(r.kind)}><FileText size={16}/>{r.name}</button>)}<button data-active={tab==="agent"} onClick={()=>setTab("agent")}><Sparkles size={16}/>{config.agent_name}</button><span/><button onClick={()=>setDialog("todo")}><CheckSquare size={16}/>待办</button><button onClick={()=>setDialog("knowledge")}><BookOpen size={16}/>知识库</button><button onClick={()=>setDialog("artifact")}><FileText size={16}/>成果文件</button><button onClick={()=>setDialog("accounts")}>外部账号</button><button onClick={()=>setDialog("tools")}>工具设置</button></nav>
    {!setup&&<div className="product-alert"><span>首次使用需要管理员启用产品功能。此操作为管理员角色添加当前工作空间的业务权限。</span><button disabled={settingUp} onClick={async()=>{setSettingUp(true);try{await request("/app/product/setup","POST",{});setSetup(true);setTab(config.resources[0].kind);location.reload()}catch(e){setError(message(e))}finally{setSettingUp(false)}}}>{settingUp?"正在启用…":"启用工作空间功能"}</button></div>}
    {error&&<div className="product-alert" role="alert">{error}<button onClick={()=>setError("")} aria-label="关闭提示"><X size={16}/></button></div>}
    {tab==="agent"?<div className="product-agent"><App key={agentKey} session={session}/></div>:<main className="product-main">
      <div className="product-intro"><div><span className="product-eyebrow">{config.eyebrow || config.name}</span><h1>{config.resources.find(r=>r.kind===tab)?.name}</h1><p>{config.tagline}</p></div><button className="product-assist" disabled={creating||!session.ready} onClick={()=>void ask(config.starters[0])}><Sparkles size={18}/>{creating?"正在打开…":"和 Agent 一起梳理"}<ArrowUpRight size={18}/></button></div>
      {!session.ready&&<p className="product-model-note">Agent 模型尚未配置。你可以先管理记录、待办与资料。</p>}
      {config.resources.filter(r=>r.kind===tab).map(r=><ResourceBoard key={r.kind} resource={r} scope={session.scope} onAsk={ask} agentReady={!!session.ready}/>) }
      <section className="product-starters"><div><Sparkles size={17}/><h2>从这里开始</h2></div>{config.starters.map(prompt=><button key={prompt} disabled={!session.ready||creating} onClick={()=>void ask(prompt)}>{prompt}<ArrowUpRight size={16}/></button>)}</section>
    </main>}
    {dialog==="todo"&&<TodoDialog conversationID="" onClose={()=>setDialog(null)} onSource={id=>{setDialog(null);location.hash=id;setAgentKey(n=>n+1);setTab("agent")}}/>}
    {dialog==="knowledge"&&<KnowledgeLibraryDialog onClose={()=>setDialog(null)}/>}
    {dialog==="tools"&&<ToolSettingsDialog key={session.scope} session={session} onClose={()=>setDialog(null)}/>}
    {dialog==="accounts"&&<ExternalAccountsDialog session={session} onClose={()=>setDialog(null)}/>}
    {dialog==="artifact"&&<ArtifactDialog onClose={()=>setDialog(null)}/>}
  </div>
}
function ResourceBoard({resource,scope,onAsk,agentReady}:{resource:Resource;scope:string;onAsk:(s:string)=>Promise<void>;agentReady:boolean}){
  const [page,setPage]=useState<Page>({items:[],complete:true});const [loading,setLoading]=useState(true);const [error,setError]=useState("");
  const [query,setQuery]=useState("");const [search,setSearch]=useState("");const [cursors,setCursors]=useState([""]);const [refresh,setRefresh]=useState(0);
  const [editing,setEditing]=useState<Item|"new"|null>(null);const [inspecting,setInspecting]=useState<Item|null>(null);const [busy,setBusy]=useState(false);
  const [title,setTitle]=useState("");const [status,setStatus]=useState(resource.initial_status);const [data,setData]=useState<Item["data"]>({...resource.defaults});
  const pending=useRef<Write|null>(null);const [retry,setRetry]=useState(false);const [notice,setNotice]=useState("");
  const editorRef=useRef<HTMLElement>(null);const modalBusy=useRef(false);modalBusy.current=busy;
 useEffect(()=>{if(!editing)return;const previous=document.activeElement as HTMLElement|null;const node=editorRef.current;if(!node)return;node.querySelector<HTMLInputElement>("input")?.focus();
  const keydown=(event:KeyboardEvent)=>{if(event.key==="Escape"){event.preventDefault();if(!modalBusy.current)setEditing(null)};if(event.key!=="Tab")return;const elements=Array.from(node.querySelectorAll<HTMLElement>('button:not([disabled]),input:not([disabled]),textarea:not([disabled]),select:not([disabled]),a[href]')).filter(e=>e.offsetParent!==null);if(elements.length===0)return;const first=elements[0],last=elements[elements.length-1];if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus()}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus()}};node.addEventListener("keydown",keydown);return()=>{node.removeEventListener("keydown",keydown);if(previous?.isConnected)previous.focus()}
 },[editing!==null]);
 const dirty=editing!==null && (editing==="new" || title!==editing.title || status!==editing.status || JSON.stringify(data)!==JSON.stringify(editing.data));
 const pendingKey=`domainry-product:${scope}:${resource.kind}:pending`;
  useEffect(()=>{const controller=new AbortController();setLoading(true);setError("");const q=new URLSearchParams({query:search,cursor:cursors.at(-1)!,limit:"5"});request<Page>(url(resource.kind)+"?"+q,"GET",undefined,controller.signal).then(setPage).catch(e=>{if(!controller.signal.aborted)setError(message(e))}).finally(()=>{if(!controller.signal.aborted)setLoading(false)});return()=>controller.abort()},[resource.kind,search,cursors,refresh]);
  useEffect(()=>{try{const raw=localStorage.getItem(pendingKey);if(raw){pending.current=JSON.parse(raw);setRetry(true);setNotice("有一次保存尚未确认，请先重试原请求。")}}catch{setNotice("浏览器暂时无法保存恢复信息，请在当前页面确认保存结果。")}},[pendingKey]);
  function edit(item:Item|"new"){setEditing(item);setInspecting(null);setTitle(item==="new"?"":item.title);setStatus(item==="new"?resource.initial_status:item.status);setData(item==="new"?{...resource.defaults}:{...item.data});setError("")}
  async function save(replay=false){if(busy)return;setBusy(true);setError("");let write=pending.current;
    if(!replay){write={client_id:crypto.randomUUID(),...(editing&&editing!=="new"?{id:editing.id}:{}),expected_revision:editing&&editing!=="new"?editing.revision:0,title,status,data};pending.current=write;try{localStorage.setItem(pendingKey,JSON.stringify(write))}catch{setNotice("恢复信息未能保存，请在当前页面完成确认。")}}
    if(!write){setBusy(false);return}
    try{const saved=await request<Item>(url(resource.kind),"POST",write);pending.current=null;try{localStorage.removeItem(pendingKey)}catch{/* Keep server success authoritative. */}setRetry(false);edit(saved);setNotice(`已保存「${saved.title}」· 版本 ${saved.revision}`);setRefresh(n=>n+1)}catch(e){setError(message(e));if(e instanceof ApiError&&e.status&&e.status>=400&&e.status<500){pending.current=null;setRetry(false);try{localStorage.removeItem(pendingKey)}catch{}}else setRetry(true)}finally{setBusy(false)}}
  async function version(item:Item,revision:number){setBusy(true);setError("");try{setInspecting(await request<Item>(url(resource.kind)+`/${item.id}?revision=${revision}`))}catch(e){setError(message(e))}finally{setBusy(false)}}
  return <section className="product-board">
    <div className="product-toolbar"><form onSubmit={e=>{e.preventDefault();setSearch(query);setCursors([""])}}><Search size={17}/><input aria-label={`搜索${resource.singular}`} value={query} onChange={e=>setQuery(e.target.value)} placeholder={`搜索${resource.singular}标题与内容`}/><button type="submit">搜索</button></form><button className="product-primary" disabled={retry} onClick={()=>edit("new")}><Plus size={17}/>新建{resource.singular}</button></div>
    <p className="product-board-description">{resource.description}</p>
    {error&&<p className="product-alert" role="alert">{error}</p>}{notice&&<p className="product-notice" role="status">{notice}</p>}
    {retry&&<div className="product-retry">保存结果尚未确认。<button disabled={busy} onClick={()=>void save(true)}>重试原保存</button></div>}
    <div className="product-records" aria-label={resource.name}>{loading?<div className="product-empty"><Loader2 className="product-spin"/>正在读取…</div>:page.items.length===0?<div className="product-empty"><FileText size={30}/><h3>{search?"没有匹配的记录":`还没有${resource.singular}`}</h3><p>{search?"试试其他关键词。":`创建第一条${resource.singular}，或让 Agent 协助整理。`}</p></div>:page.items.map(item=><button key={item.id} className="product-record" onClick={()=>edit(item)} disabled={retry}><span className="product-record-icon"><FileText size={20}/></span><span className="product-record-title"><strong>{item.title}</strong><small>{item.data.priority?`${item.data.priority} · RICE ${Number(item.data.rice_score||0).toFixed(1)} · `:""}版本 {item.revision} · {new Date(item.updated_at).toLocaleString()}</small></span><span className="product-badge" data-status={item.status}>{resource.states[item.status]||item.status}</span><ChevronRight size={18}/></button>)}</div>
    <footer className="product-pagination"><span>第 {cursors.length} 页 · {page.items.length} 条记录</span><button disabled={loading||cursors.length===1} onClick={()=>setCursors(c=>c.slice(0,-1))}>上一页</button><button disabled={loading||!page.next_cursor} onClick={()=>setCursors(c=>[...c,page.next_cursor!])}>下一页</button><button disabled={loading} onClick={()=>setRefresh(n=>n+1)}>刷新</button></footer>
    {editing&&<div className="product-editor-backdrop"><section ref={editorRef} className="product-editor" role="dialog" aria-modal="true" aria-label={`${resource.singular}编辑`}><header><div><span className="product-eyebrow">{editing==="new"?"新建记录":`版本 ${editing.revision}`}</span><h2>{editing==="new"?`新建${resource.singular}`:editing.title}</h2></div><button disabled={busy} onClick={()=>setEditing(null)} aria-label="关闭编辑"><X size={22}/></button></header>
      {error&&<p className="product-alert" role="alert">{error}</p>}
      {inspecting?<div className="product-revision"><div><strong>历史版本 {inspecting.revision}</strong><button disabled={busy||inspecting.revision<=1} onClick={()=>void version(inspecting,inspecting.revision-1)}>更早版本</button><button onClick={()=>setInspecting(null)}>回到当前编辑</button></div><h3>{inspecting.title}</h3>{resource.fields.map(f=><section key={f.key}><h4>{f.label}</h4><pre>{String(inspecting.data[f.key]??"—")}</pre></section>)}</div>:<form onSubmit={e=>{e.preventDefault();void save()}}>
        <label>标题<input required maxLength={255} value={title} disabled={busy||retry} onChange={e=>setTitle(e.target.value)}/></label>
        <label>状态<select value={status} disabled={busy||retry} onChange={e=>setStatus(e.target.value)}>{(resource.state_order || Object.keys(resource.states)).map(key=><option key={key} value={key}>{resource.states[key]}</option>)}</select></label>
        <div className="product-fields">{resource.fields.map(f=><label key={f.key} className={f.type==="textarea"||f.type==="markdown"?"product-field-wide":""}>{f.label}{f.type==="readonly"?<output>{Number(data[f.key]||0).toFixed(1)}</output>:f.type==="select"?<select value={data[f.key]??""} disabled={busy||retry} onChange={e=>setData(d=>({...d,[f.key]:e.target.value}))}>{f.options?.map(v=><option key={v} value={v}>{f.option_labels?.[v] || v}</option>)}</select>:f.type==="markdown"||f.type==="textarea"?<textarea rows={f.type==="markdown"?12:4} value={data[f.key]??""} disabled={busy||retry} onChange={e=>setData(d=>({...d,[f.key]:e.target.value}))}/>:<input type={f.type==="number"?"number":f.type==="date"?"date":"text"} min={0} step="any" value={data[f.key]??""} disabled={busy||retry} onChange={e=>setData(d=>({...d,[f.key]:f.type==="number"?Number(e.target.value):e.target.value}))}/>}</label>)}</div>
        <footer><button type="submit" className="product-primary" disabled={busy||retry||!title.trim()}>{busy?"正在保存…":"保存新版本"}</button>{editing!=="new"&&<><button type="button" disabled={busy} onClick={()=>void version(editing,editing.revision)}><History size={16}/>查看版本</button><button type="button" disabled={busy||!agentReady||dirty} title={dirty?"请先保存修改":"读取已保存的记录并交给 Agent"} onClick={()=>{const action=resource.kind==="meetings"?"读取会议记录，核对行动项，协助转换为独立待办并保留来源。":"读取这条记录，协助检查内容缺口并改进；保存前保留未修改字段。";void onAsk(`${action} 记录类型：${resource.kind}，ID：${editing.id}，版本：${editing.revision}，标题：${editing.title}`)}}><Sparkles size={16}/>交给 Agent</button></>}</footer>
      </form>}</section></div>}
  </section>
}
