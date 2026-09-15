import { useEffect, useRef, useState } from 'react';
import { Button } from './components/ui/button';
import { request } from './api.ts';
import { describeError } from './errors.ts';
import { pendingPeerMutation, usePeerMutation } from './peer-mutation.ts';
import { peerPath, type Delegation, type DeliveryPublicationCandidates, type DeliveryPublicationPreview } from './collaboration-state.ts';

const kinds:Record<string,string>={current:'当前交付',deliver:'提交交付',review_delivery:'核对交付',accept_delivery:'验收通过',legacy:'原有交付',republish_delivery:'重新共享'};

export function DeliveryPublicationSection({value:d,onRefresh}:{value:Delegation;onRefresh:()=>void}) {
 const [index,setIndex]=useState<DeliveryPublicationCandidates|null>(null),[selected,setSelected]=useState(0),[preview,setPreview]=useState<DeliveryPublicationPreview|null>(null);
 const [busy,setBusy]=useState(false),[reason,setReason]=useState(''),[error,setError]=useState('');
 const [submission,setSubmission]=useState<Record<string,unknown>|null>(null);
 const pending=useRef<AbortController|null>(null),mutation=usePeerMutation();
 function clear(){pending.current?.abort();pending.current=null;setIndex(null);setPreview(null);setReason('');setBusy(false);setError('');}
 useEffect(()=>{clear();const saved=pendingPeerMutation(`${peerPath(d.id)}/decisions`);setSubmission(saved?.action==='republish_delivery'?saved:null);window.addEventListener('blur',clear);return ()=>{pending.current?.abort();window.removeEventListener('blur',clear);};},[d.id,d.revision]);
 async function load(before?:number) {
  pending.current?.abort();const controller=new AbortController();pending.current=controller;setBusy(true);setPreview(null);setError('');
  try {
   const next=await request<DeliveryPublicationCandidates>(`${peerPath(d.id)}/delivery-publications${before?`?before_revision=${before}`:''}`,'GET',undefined,controller.signal);
   if(!controller.signal.aborted){setIndex(previous=>before&&previous?{...next,items:[...previous.items,...next.items]}:next);if(!before)setSelected(next.current_available?0:next.items[0]?.revision||0);}
  } catch(e){if(!controller.signal.aborted){setIndex(null);setError(describeError(e));}}
  finally{if(pending.current===controller){pending.current=null;setBusy(false);}}
 }
 async function prepare() {
  pending.current?.abort();const controller=new AbortController();pending.current=controller;setBusy(true);setPreview(null);setError('');
  try {
   const value=await request<DeliveryPublicationPreview>(`${peerPath(d.id)}/delivery-publication`,'POST',{delivery_revision:selected},controller.signal);
   if(!controller.signal.aborted)setPreview(value);
  } catch(e){if(!controller.signal.aborted)setError(describeError(e));}
  finally{if(pending.current===controller){pending.current=null;setBusy(false);}}
 }
 async function publish(retry?:Record<string,unknown>) {
  if(!preview&&!retry)return;
  const body=retry||{action:'republish_delivery',reason:reason.trim(),expected_revision:preview!.expected_revision,publication:{delivery_revision:preview!.record.revision,record_digest:preview!.record_digest}};
  setSubmission(body);
  setError('');
  try {
   await mutation.mutate(`${peerPath(d.id)}/decisions`,'POST',body);
   clear();setSubmission(null);onRefresh();
  } catch(e){setPreview(null);setError(describeError(e));}
 }
 return <section className="peer-dependencies peer-form" aria-label="重新共享原交付"><h4 className="font-medium">重新共享原交付</h4><p>选择原交付并核对内容，再共享给委派发起方。原交付、任务状态和验收记录保留。</p><div className="todo-toolbar"><Button variant="outline" disabled={busy||mutation.busy} onClick={()=>void load()}>选择要共享的原交付</Button>{index&&<Button variant="ghost" disabled={mutation.busy} onClick={clear}>收起共享预览</Button>}</div>{index&&<><label>原交付版本<select aria-label="原交付版本" disabled={busy||mutation.busy} value={selected} onChange={e=>{setSelected(Number(e.target.value));setPreview(null);setError('');}}>{index.current_available&&<option value={0}>当前交付</option>}{index.items.map(entry=><option key={entry.revision} value={entry.revision}>记录 {entry.revision} · {kinds[entry.kind]||entry.kind} · 需求 v{entry.brief_version} / 约定 {entry.agreement_revision}</option>)}</select></label>{!index.current_available&&!index.items.length?<p>没有已保存的原交付。</p>:<Button disabled={busy||mutation.busy} onClick={()=>void prepare()}>预览原交付</Button>}{!index.complete&&<Button variant="ghost" disabled={busy||mutation.busy} onClick={()=>void load(index.next_before)}>更早的原交付版本</Button>}</>}{preview&&<form className="peer-form" onSubmit={e=>{e.preventDefault();void publish();}} aria-label="原交付共享预览"><p>{kinds[preview.record.kind]||preview.record.kind} · 需求 v{preview.record.delivery.brief_version} / 约定 {preview.record.delivery.agreement_revision||1}</p><p>{preview.record.delivery.summary}</p>{preview.record.delivery.data!==undefined&&<pre>{JSON.stringify(preview.record.delivery.data,null,2)}</pre>}<p>发布身份：{preview.publisher.user_id} · {preview.publisher.role_key||'当前角色'}；接收用户：{preview.recipient_user_id}</p><label>共享说明<textarea aria-label="原交付共享说明" required maxLength={4096} rows={2} value={reason} onChange={e=>setReason(e.target.value)}/></label><Button type="submit" disabled={busy||mutation.busy||!reason.trim()}>{mutation.busy?'正在共享…':'确认共享原交付'}</Button></form>}{submission&&!preview&&<Button variant="outline" disabled={busy||mutation.busy} onClick={()=>void publish(submission)}>重试上次共享</Button>}{error&&<p role="alert">{error}</p>}</section>;
}
