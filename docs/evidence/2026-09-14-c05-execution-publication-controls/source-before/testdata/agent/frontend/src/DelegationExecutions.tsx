import {useEffect,useState} from 'react';
import {request} from './api';
import {sessionUserID} from './session';
import {describeError} from './errors';
import type {Delegation} from './collaboration-state';
import {Button} from './components/ui/button';
import {RunDialog} from './RunDialog';

type Reference={conversation_id:string;run_id:string};
type Publication={reference:Reference;publisher:{user_id:string;role_key:string}};

export function DelegationExecutions({value:d,canShare,onRefresh}:{value:Delegation;canShare:boolean;onRefresh:()=>void}){
 const [items,setItems]=useState<Publication[]>([]),[selected,setSelected]=useState<Reference|null>(null),[error,setError]=useState(''),[reason,setReason]=useState(''),[busy,setBusy]=useState(false),[refresh,setRefresh]=useState(0);
 const permitted=!!d.access?.execution_read;
 const own=d.execution_subject?.user_id===sessionUserID()||!d.execution_subject&&d.owner_user_id===sessionUserID();
 useEffect(()=>{
  setItems([]);setSelected(null);setError('');
  if(!permitted)return;
  const abort=new AbortController();let timer:ReturnType<typeof setTimeout>|undefined;
  const load=async()=>{
   try{const next=await request<Publication[]>(`/agent/delegations/${encodeURIComponent(d.id)}/executions`,'GET',undefined,abort.signal);if(!abort.signal.aborted){setItems(next);setSelected(old=>old&&next.some(p=>p.reference.conversation_id===old.conversation_id&&p.reference.run_id===old.run_id)?old:null);setError('');}}
   catch(e){if(!abort.signal.aborted){setItems([]);setSelected(null);setError(describeError(e));}}
   finally{if(!abort.signal.aborted)timer=setTimeout(()=>void load(),2000);}
  };void load();return()=>{abort.abort();clearTimeout(timer);};
 },[d.id,d.revision,permitted,refresh]);
 async function share(reference:Reference,withdraw=false){
  setBusy(true);setError('');try{await request(`/agent/delegations/${encodeURIComponent(d.id)}/execution-publications`,'POST',{reference,expected_revision:d.revision,client_id:crypto.randomUUID(),reason:reason.trim(),withdraw});setReason('');setRefresh(x=>x+1);onRefresh();}catch(e){setError(describeError(e));}finally{setBusy(false);}
 }
 if(!permitted)return null;
 return <section className="peer-dependencies" aria-label="共享执行过程"><h4>已共享的执行过程</h4>
  {items.length?<ol>{items.map(p=><li key={`${p.reference.conversation_id}:${p.reference.run_id}`}><p>发布用户 {p.publisher.user_id} · 角色 {p.publisher.role_key}</p><Button variant="outline" onClick={()=>setSelected(p.reference)}>查看共享执行过程</Button>{own&&p.publisher.user_id===sessionUserID()&&<Button variant="ghost" disabled={busy||!reason.trim()} onClick={()=>void share(p.reference,true)}>撤回执行共享</Button>}</li>)}</ol>:<p className="subtle">执行人尚未共享运行，或当前授权已失效。</p>}
  {own&&<><label>执行共享说明<textarea aria-label="执行共享说明" maxLength={4096} value={reason} onChange={e=>setReason(e.target.value)}/></label>{canShare&&d.task?.execution_run_id&&<Button disabled={busy||!reason.trim()} onClick={()=>void share({conversation_id:d.conversation_id,run_id:d.task!.execution_run_id!})}>共享这次执行过程</Button>}</>}
  {error&&<p role="alert">{error}</p>}
  {selected&&<RunDialog conversationID={selected.conversation_id} runID={selected.run_id} delegationID={d.id} onClose={()=>setSelected(null)}/>}
 </section>;
}
