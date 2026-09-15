import { useEffect, useRef, useState } from 'react';
import { Button } from './components/ui/button';
import { request } from './api.ts';
import { describeError } from './errors.ts';
import { pendingPeerMutation, usePeerMutation } from './peer-mutation.ts';
import { peerPath, type Delegation, type ContractPublicationCandidates, type ContractPublicationPreview, type ContractPublicationHistory } from './collaboration-state.ts';

export function ContractPublicationSection({value:d,onRefresh}:{value:Delegation;onRefresh:()=>void}) {
 const [index,setIndex]=useState<ContractPublicationCandidates|null>(null),[selected,setSelected]=useState(0),[preview,setPreview]=useState<ContractPublicationPreview|null>(null);
 const [busy,setBusy]=useState(false),[reason,setReason]=useState(''),[error,setError]=useState(''),[submission,setSubmission]=useState<Record<string,unknown>|null>(null);
 const pending=useRef<AbortController|null>(null),mutation=usePeerMutation();
 function clear(){pending.current?.abort();pending.current=null;setIndex(null);setPreview(null);setReason('');setBusy(false);setError('');}
 useEffect(()=>{clear();const saved=pendingPeerMutation(`${peerPath(d.id)}/decisions`);setSubmission(saved?.action==='republish_contract'?saved:null);window.addEventListener('blur',clear);return ()=>{pending.current?.abort();window.removeEventListener('blur',clear);};},[d.id,d.revision]);
 async function load(before?:number) {
  pending.current?.abort();const controller=new AbortController();pending.current=controller;setBusy(true);setPreview(null);setError('');
  try {
   const next=await request<ContractPublicationCandidates>(`${peerPath(d.id)}/contract-candidates${before?`?before_revision=${before}`:''}`,'GET',undefined,controller.signal);
   if(!controller.signal.aborted){setIndex(previous=>before&&previous?{...next,items:[...previous.items,...next.items]}:next);if(!before)setSelected(next.items.find(item=>item.revision===next.current_agreement_revision)?.revision||next.items[0]?.revision||0);}
  } catch(e){if(!controller.signal.aborted){setIndex(null);setError(describeError(e));}}
  finally{if(pending.current===controller){pending.current=null;setBusy(false);}}
 }
 async function prepare() {
  pending.current?.abort();const controller=new AbortController();pending.current=controller;setBusy(true);setPreview(null);setError('');
  try {const value=await request<ContractPublicationPreview>(`${peerPath(d.id)}/contract-publication`,'POST',{agreement_revision:selected},controller.signal);if(!controller.signal.aborted)setPreview(value);}
  catch(e){if(!controller.signal.aborted)setError(describeError(e));}
  finally{if(pending.current===controller){pending.current=null;setBusy(false);}}
 }
 async function publish(retry?:Record<string,unknown>) {
  if(!preview&&!retry)return;
  const body=retry||{action:'republish_contract',reason:reason.trim(),expected_revision:preview!.expected_revision,contract_publication:{agreement_revision:preview!.agreement.revision,record_digest:preview!.record_digest}};
  setSubmission(body);setError('');
  try {await mutation.mutate(`${peerPath(d.id)}/decisions`,'POST',body);clear();setSubmission(null);onRefresh();}
  catch(e){setPreview(null);setError(describeError(e));}
 }
 return <section className="peer-dependencies peer-form" aria-label="重新共享原约定"><h4 className="font-medium">重新共享原约定</h4><p>核对原任务要求和来源，再共享给接收方。当前要求、原任务状态和已有交付保留。</p><div className="todo-toolbar"><Button variant="outline" disabled={busy||mutation.busy||!!submission} onClick={()=>void load()}>选择要共享的原约定</Button>{index&&<Button variant="ghost" disabled={mutation.busy} onClick={clear}>收起约定预览</Button>}</div>
 {index&&<><label>原约定版本<select aria-label="原约定版本" disabled={busy||mutation.busy} value={selected} onChange={e=>{setSelected(Number(e.target.value));setPreview(null);setError('');}}>{index.items.map(entry=><option key={entry.revision} value={entry.revision}>约定 {entry.revision} · 需求 v{entry.brief_version}{entry.revision===index.current_agreement_revision?' · 当前':''}</option>)}</select></label>{!index.complete&&index.next_before&&<Button variant="ghost" disabled={busy||mutation.busy} onClick={()=>void load(index.next_before)}>更早约定</Button>}<Button variant="outline" disabled={busy||mutation.busy||!selected} onClick={()=>void prepare()}>核对原约定内容</Button></>}
 {preview&&<form aria-label="原约定共享预览" onSubmit={e=>{e.preventDefault();void publish();}}><dl className="run-metadata"><dt>原约定</dt><dd>{preview.agreement.revision} · 需求 v{preview.agreement.brief.version}</dd><dt>目标</dt><dd>{preview.agreement.brief.goal}</dd><dt>交付要求</dt><dd>{preview.agreement.brief.deliverable}</dd><dt>完成条件</dt><dd>{(preview.agreement.brief.completion_conditions||[]).join('；')||'未填写'}</dd><dt>约束</dt><dd>{(preview.agreement.brief.constraints||[]).join('；')||'未填写'}</dd><dt>任务类型</dt><dd>{preview.requirements.task_type||'未指定'}</dd><dt>所需工具</dt><dd>{(preview.requirements.tools||[]).join('、')||'未指定'}</dd><dt>原始来源</dt><dd>{preview.sources.length} 份</dd><dt>发布用户／角色</dt><dd>{preview.publisher.user_id} · {preview.publisher.role_key||'默认角色'}</dd><dt>接收用户</dt><dd>{preview.recipient_user_id}</dd></dl>{preview.agreement.structured_input&&<details><summary>原输入资料</summary><pre>{JSON.stringify(preview.agreement.structured_input.data,null,2)}</pre></details>}<label>约定共享说明<textarea aria-label="约定共享说明" required maxLength={4096} rows={3} value={reason} onChange={e=>setReason(e.target.value)}/></label><Button type="submit" disabled={busy||mutation.busy||!reason.trim()}>确认共享原约定</Button></form>}
 {submission&&!preview&&<div role="status"><p>原共享请求尚未确认。重试会沿用原约定版本和请求，不另建发布。</p><Button disabled={busy||mutation.busy} onClick={()=>void publish(submission)}>按原请求重试约定共享</Button></div>}{error&&<p role="alert" className="text-destructive">{error}</p>}</section>;
}

export function ContractPublicationHistorySection({value:d}:{value:Delegation}) {
 const [history,setHistory]=useState<ContractPublicationHistory|null>(null),[error,setError]=useState('');
 const pending=useRef<AbortController|null>(null);
 function clear(){pending.current?.abort();pending.current=null;setHistory(null);setError('');}
 useEffect(()=>{clear();window.addEventListener('blur',clear);return ()=>{pending.current?.abort();window.removeEventListener('blur',clear);};},[d.id,d.revision]);
 async function load(before?:number) {
  pending.current?.abort();const controller=new AbortController();pending.current=controller;setError('');
  try {const next=await request<ContractPublicationHistory>(`${peerPath(d.id)}/contract-publications${before?`?before_revision=${before}`:''}`,'GET',undefined,controller.signal);if(!controller.signal.aborted)setHistory(previous=>before&&previous?{...next,items:[...previous.items,...next.items]}:next);}
  catch(e){if(!controller.signal.aborted){setHistory(null);setError(describeError(e));}}
 }
 return <section className="peer-dependencies" aria-label="约定共享记录"><Button variant="ghost" onClick={()=>void load()}>查看约定共享记录</Button>{history&&<><ol>{history.items.map(item=><li key={item.revision}>约定 {item.agreement_revision} · {item.publisher.user_id}／{item.publisher.role_key||'默认角色'} → {item.recipient_user_id} · {new Date(item.published_at).toLocaleString()}<p>{item.reason}</p></li>)}</ol>{!history.items.length&&<p>还没有重新共享记录。</p>}{!history.complete&&history.next_before&&<Button variant="ghost" onClick={()=>void load(history.next_before)}>更早共享记录</Button>}<Button variant="ghost" onClick={clear}>收起共享记录</Button></>}{error&&<p role="alert" className="text-destructive">{error}</p>}</section>;
}
