import { useCallback, useEffect, useRef, useState } from "react";
import { ToolsClient, type ToolSetting } from "@domainry/tools-client";
import { RefreshCw } from "lucide-react";
import { request } from "./api.ts";
import type { AppSession } from "./session.ts";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";

const client = new ToolsClient({request: (path, options) => request(path, options?.method, options?.body, options?.signal)});
const states = {available: "可用", disabled: "已关闭", connection_unavailable: "连接或授权范围不可用", connection_unknown: "连接状态暂时无法确认"};
const names: Record<string,string> = {report_query:"查询报表",calendar_write_accounts:"发现日历写入账号",calendar_event_inspect:"核对日程修改目标",calendar_event_create:"创建日程",calendar_event_update:"修改日程",mail_write_accounts:"发现邮件发送账号",mail_send:"发送邮件",mail_reply:"回复邮件",calculate:"计算",time_now:"当前时间",knowledge_search:"检索知识",knowledge_read:"读取知识",todo_list:"查询待办",todo_create:"创建待办",todo_update:"更新待办",todo_delete:"删除待办",memory_list:"查询记忆",memory_save:"保存记忆",memory_delete:"删除记忆",web_search:"搜索公开网页",web_fetch:"读取网页正文",mail_accounts:"发现邮件账号",mail_list:"查看邮件列表",mail_search:"搜索邮件",mail_read:"读取邮件正文",calendar_accounts:"发现日历账号",calendar_list:"查看日历目录",calendar_events:"查询日历安排",calendar_event:"读取事件详情",calendar_availability:"查询共同空闲"};
const descriptions: Record<string,string> = {
 report_query:"发现当前可见报表，按声明参数执行查询；保留来源、行数上限、分页及是否完整。",
 calendar_write_accounts:"发现当前获准创建或修改日程的账号，固定账号状态。",
 calendar_event_inspect:"修改前读取事件的准确版本、单次或系列类型，以及完整参与者。",
 calendar_event_create:"确认完整内容后创建日程，并请求向明确列出的参与者发送通知。",
 calendar_event_update:"按已读取版本修改指定事件或系列，显示修改内容、清空字段和通知范围。",
 mail_write_accounts:"发现当前获准发送或回复邮件的账号；读取权限不能代替发送授权。",
 mail_send:"确认全部收件人、抄送、密送、主题和正文后发送；受理不表示送达。",
 mail_reply:"确认明确原邮件和完整回复内容后发送，不自动扩大为回复全部。",
 web_search:"搜索工作空间服务允许的公开来源，保留排名片段、链接和读取时间。",
 web_fetch:"读取公开页面正文，保留来源链接、读取时间及内容缩短和完整性提示；登录后内容需使用对应账号工具。",
 mail_accounts:"查看当前可用的邮件账号，按列表、搜索或正文读取权限筛选。",
 mail_list:"查看邮件头和分页信息；基础授权下不会读取正文。",
 mail_search:"按所选邮箱的查询语法搜索邮件，标记分页和搜索上限。",
 mail_read:"读取指定邮件正文，用于摘要、事项提取和未发送的本地草稿；保留原邮件引用和不完整提示。",
 calendar_accounts:"查看当前有权读取的日历账号。不同读取操作可能需要不同的账号授权范围。",
 calendar_list:"查看已授权账号中的日历，供安排、详情和空闲查询选择。",
 calendar_events:"查询指定时间段的安排，区分全天活动与具体时间，并保留时区和分页提示。",
 calendar_event:"查看所选事件的详情，内容过长时会标记缩短。",
 calendar_availability:"查询多个日历共同空闲的时间段。来源不完整时会明确说明，避免误判空闲。",
};
function failure(error: unknown) {
 const status = error && typeof error === "object" && "status" in error ? error.status : undefined;
 if (status === 401) return "登录已过期，请重新登录。";
 if (status === 403) return "你当前没有这项工具设置权限，请联系工作空间管理员。";
 if (status === 409) return "设置或工具版本已变化，请刷新后重新选择。";
 return "暂时无法确认保存结果。请刷新核对当前设置后再操作。";
}

export function ToolSettingsDialog({session,onClose}:{session:AppSession;onClose:()=>void}) {
 const [items,setItems]=useState<ToolSetting[]>([]), [search,setSearch]=useState("");
 const [setup,setSetup]=useState<{ready:boolean;administrator:boolean}|null>(null);
 const [loading,setLoading]=useState(true), [busy,setBusy]=useState(false), [error,setError]=useState("");
 const [uncertain,setUncertain]=useState(false), [notice,setNotice]=useState("");
 const mounted=useRef(true), lock=useRef(false), generation=useRef(0);
 const connected=session.modules?.includes("tools")===true;
 const reload=useCallback(async(signal?:AbortSignal)=>{
  if (!connected) {setLoading(false);return;}
  const current=++generation.current;setLoading(true);setError("");
  const [list,ready]=await Promise.allSettled([client.listToolSettings(signal),request<{ready:boolean;administrator:boolean}>("/app/product/tool-settings-setup","GET",undefined,signal)]);
  if(signal?.aborted||!mounted.current||current!==generation.current)return;
  setSetup(ready.status==="fulfilled"?ready.value:null);
  setItems(list.status==="fulfilled"?list.value.items:[]);
  if(list.status==="rejected")setError(failure(list.reason));else setUncertain(false);
  setLoading(false);
 },[connected]);
 useEffect(()=>{mounted.current=true;const abort=new AbortController();void reload(abort.signal);return()=>{mounted.current=false;generation.current++;abort.abort();};},[reload]);
 async function perform(action:()=>Promise<void>) {
  if(lock.current)return;lock.current=true;setBusy(true);setError("");setNotice("");
  try {await action();}catch(error){if(mounted.current){setUncertain(true);setError(failure(error));}}finally{lock.current=false;if(mounted.current)setBusy(false);}
 }
 const visible=items.filter(item=>`${item.key} ${descriptions[item.key]||item.description} ${names[item.key]||""}`.toLowerCase().includes(search.toLowerCase()));
 return <Dialog open onOpenChange={open=>{if(!open)onClose();}}><DialogContent className="attachment-dialog tool-settings-dialog"><DialogHeader><DialogTitle>工具设置</DialogTitle><DialogDescription>选择你允许 Agent 使用的工具。设置保存在当前工作空间的个人账号下；开启后仍需具备工具权限及有效连接。</DialogDescription></DialogHeader>
 {!connected?<p role="status">此工作空间尚未启用工具设置服务。</p>:<>
 <div className="tool-settings-toolbar"><Input aria-label="搜索工具" placeholder="搜索工具" value={search} onChange={event=>setSearch(event.target.value)}/><Button variant="outline" disabled={busy||loading} onClick={()=>void reload()}><RefreshCw size={15}/>刷新设置</Button></div>
 {setup?.administrator&&!setup.ready&&<section className="external-authorization"><p>管理员可为当前管理员角色启用个人工具设置权限。其他角色需要由管理员分配。</p><Button disabled={busy} onClick={()=>void perform(async()=>{await request("/app/product/tool-settings-setup","POST",{});await reload();})}>启用管理员工具设置</Button></section>}
 {error&&<p role="alert" className="error-text">{error}</p>}{notice&&<p role="status">{notice}</p>}
 {loading?<p role="status">正在读取工具设置…</p>:<div className="tool-settings-list">{visible.map(item=><section key={item.key} data-tool-key={item.key} className="tool-setting-row"><div><strong>{names[item.key]||item.key}</strong><code>{item.key}</code><p>{descriptions[item.key]||item.description}</p><span className="subtle">{states[item.state]||"请刷新核对状态"}</span></div><Switch aria-label={`${item.key} 工具开关`} checked={item.enabled} disabled={busy||uncertain} onCheckedChange={enabled=>void perform(async()=>{const saved=await client.updateToolSetting(item.key,{enabled,expected_revision:item.revision,tool_version:item.version});if(!mounted.current)return;setItems(current=>current.map(value=>value.key===saved.key?saved:value));setNotice(`${names[saved.key]||saved.key}已${saved.enabled?"开启":"关闭"}。后续调用会检查最新设置。`);})}/></section>)}{!visible.length&&!error&&<p role="status">{search?"没有匹配的工具。":"当前没有已授权且已挂载的工具。开启设置不会增加工具权限。"}</p>}</div>}
 </>}
 </DialogContent></Dialog>;
}
