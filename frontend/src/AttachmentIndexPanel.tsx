import { useEffect, useRef, useState } from "react";
import { Button } from "./components/ui/button";
import { request } from "./api.ts";
import { describeError } from "./errors.ts";
import { sessionScope } from "./session.ts";
import { attachmentPath, type Attachment } from "./attachment-state.ts";
import { attachmentIndexDetail, attachmentIndexReason, attachmentIndexReceiptKey, indexReceipt, parsePendingAttachmentIndex, submitAttachmentIndex, verifyAttachmentIndexResponse, type PendingAttachmentIndex } from "./attachment-index-state.ts";

export function AttachmentIndexPanel({ attachment, disabled, onChange, onBusyChange }: { attachment: Attachment; disabled: boolean; onChange(a: Attachment): void; onBusyChange(v: boolean): void }) {
 const key=attachmentIndexReceiptKey(attachment), scope=sessionScope();
 const [pending,setPending]=useState<PendingAttachmentIndex|null>(()=>{try{return parsePendingAttachmentIndex(localStorage.getItem(key),attachment);}catch{return null;}});
 const [confirming,setConfirming]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(""),[notice,setNotice]=useState("");
 const lock=useRef(false),active=useRef<AbortController|null>(null);
 useEffect(()=>()=>{active.current?.abort();onBusyChange(false);},[onBusyChange]);
 function clear(){localStorage.removeItem(key);setPending(null);}
 async function perform(action:(signal:AbortSignal)=>Promise<void>){
  if(disabled||lock.current)return;lock.current=true;setBusy(true);onBusyChange(true);setError("");setNotice("");
  const abort=new AbortController();active.current=abort;
  try{await action(AbortSignal.any([abort.signal,AbortSignal.timeout(30000)]));}
  catch(e){if(!abort.signal.aborted)setError(describeError(e));}
  finally{lock.current=false;onBusyChange(false);if(!abort.signal.aborted)setBusy(false);}
 }
 async function submit(operation:PendingAttachmentIndex["operation"],signal:AbortSignal){
  const p=pending||indexReceipt(attachment,operation);
  localStorage.setItem(key,JSON.stringify(p));setPending(p);
  const result=await submitAttachmentIndex(p,signal);
  signal.throwIfAborted();if(sessionScope()!==scope)return;
  clear();setConfirming(false);onChange(result);
  setNotice(operation==="start"?"已提交私有索引，请查看处理状态。":"已提交同一次任务的核对，后台会继续处理。");
 }
 async function recover(signal:AbortSignal){
  if(!pending)return;
  const result=verifyAttachmentIndexResponse(await request<Attachment>(attachmentPath(pending.conversationID,pending.attachmentID),"GET",undefined,signal),pending);
  signal.throwIfAborted();if(sessionScope()!==scope)return;
  onChange(result);
  if(pending.operation==="start"&&result.indexing?.requested || pending.operation==="check"&&result.indexing?.last_check_revision===pending.revision){clear();setNotice("已找回原请求的服务端记录，当前状态如下。");}
  else if(pending.operation==="check"&&result.revision>pending.revision){clear();setNotice("已取得更新后的状态，这次核对尚无确认记录。可按当前版本重新核对已有任务。");}
  else {
   if(pending.operation==="start"&&!result.indexing?.requested&&result.revision!==pending.revision){const updated={...pending,revision:result.revision};localStorage.setItem(key,JSON.stringify(updated));setPending(updated);}
   setNotice("尚未找到这次请求的确认记录。可按原编号继续提交，或稍后再次核对。");
  }
 }
 const blocked=disabled||busy, reason=attachmentIndexReason(attachment);
 return <section className="memory-operation" aria-label="当前会话附件索引"><strong>当前会话检索</strong><p>{attachmentIndexDetail(attachment)}</p>{reason&&<p className="subtle">{reason}</p>}
  {error&&<p role="alert" className="error-text">{error}</p>}{notice&&<p role="status" className="subtle">{notice}</p>}
  {pending?<><p>上次{pending.operation==="start"?"索引":"核对"}请求待确认。先核对服务端记录，避免重复操作。</p><div className="interaction-actions"><Button variant="outline" disabled={blocked} onClick={()=>void perform(recover)}>核对上次请求</Button><Button variant="outline" disabled={blocked||!(pending.operation==="start"?attachment.indexing?.can_start:attachment.indexing?.can_check)} onClick={()=>void perform(signal=>submit(pending.operation,signal))}>继续同一次请求</Button></div></>
  :attachment.indexing?.requested?<Button variant="outline" disabled={blocked||!attachment.indexing.can_check} onClick={()=>void perform(signal=>submit("check",signal))}>核对索引状态</Button>
  :<Button variant="outline" disabled={blocked||!attachment.indexing?.can_start} onClick={()=>setConfirming(true)}>用于当前会话检索</Button>}
  {confirming&&!pending&&<><p>将“{attachment.filename}”提交到已配置的知识服务建立索引，仅当前用户在这段会话中可检索。原文件预览和下载可继续使用。</p><div className="interaction-actions"><Button disabled={blocked||!attachment.indexing?.can_start} onClick={()=>void perform(signal=>submit("start",signal))}>确认开始索引</Button><Button variant="ghost" disabled={blocked} onClick={()=>setConfirming(false)}>暂不索引</Button></div></>}
 </section>;
}
