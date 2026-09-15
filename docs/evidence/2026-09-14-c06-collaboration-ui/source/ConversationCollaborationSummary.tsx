import { useEffect, useState } from 'react';
import { request } from './api.ts';
import { Button } from './components/ui/button';
import { delegationAttentionReasons, delegationBusinessStage, delegationConditionProgress, delegationCurrentActivity, delegationOpenQuestions, type AgentPage, type CollaborationAuthorization, type Delegation } from './collaboration-state.ts';

export function ConversationCollaborationSummary({conversationID,delegationID,userID,onOpen}:{conversationID:string;delegationID?:string;userID:string;onOpen:(delegationID:string)=>void}) {
 const [items,setItems]=useState<Delegation[]>([]),[agents,setAgents]=useState<AgentPage['items']>([]),[refresh,setRefresh]=useState(0),[loading,setLoading]=useState(false);
 useEffect(()=>{
  if(!conversationID){setItems([]);return;}
  const controller=new AbortController();setLoading(true);
  void (async()=>{
   try{
    const access=await request<CollaborationAuthorization>('/agent/collaboration-access','GET',undefined,controller.signal);
    if(controller.signal.aborted||!access.allowed?.includes('view')){if(!controller.signal.aborted)setItems([]);return;}
    const params=new URLSearchParams({source_conversation_id:conversationID});
    const [work,directory]=await Promise.allSettled([
     request<{items:Delegation[];complete:boolean}>(`/agent/delegations?${params}`,'GET',undefined,controller.signal),
     access.allowed.includes('discover')?request<AgentPage>('/agent/agents','GET',undefined,controller.signal):Promise.resolve({items:[],tools:[],models:[],complete:true} as AgentPage),
    ]);
    if(controller.signal.aborted)return;
    setItems(work.status==='fulfilled'?work.value.items.filter(item=>item.source_conversation_id===conversationID||item.conversation_id===conversationID||item.assignments?.some(assignment=>assignment.conversation_id===conversationID)||item.id===delegationID):[]);
    setAgents(directory.status==='fulfilled'?directory.value.items:[]);
   }catch{if(!controller.signal.aborted){setItems([]);setAgents([]);}}
   finally{if(!controller.signal.aborted)setLoading(false);}
  })();
  return()=>controller.abort();
 },[conversationID,delegationID,refresh]);
 useEffect(()=>{if(!conversationID)return;const timer=window.setTimeout(()=>setRefresh(value=>value+1),3000);return()=>window.clearTimeout(timer);},[conversationID,refresh]);
 if(!items.length)return loading?<p className="collaboration-inline-loading" role="status">正在同步协作状态…</p>:null;
 const name=(id:string)=>agents.find(agent=>agent.id===id)?.name||items.find(item=>item.from_agent_id===id)?.source_agent?.name||id;
 const attention=items.reduce((total,item)=>total+delegationAttentionReasons(item,userID).length,0);
 return <section className="collaboration-inline" aria-label="当前会话的 Agent 协作">
  <header><div><strong>Agent 协作</strong><span>{items.length} 项关联委派{attention?` · ${attention} 项需要你处理`:''}</span></div><Button variant="ghost" size="sm" onClick={()=>setRefresh(value=>value+1)}>刷新</Button></header>
  <div className="collaboration-inline-list">{items.map(item=><DelegationInlineItem key={item.id} value={item} userID={userID} name={name} onOpen={()=>onOpen(item.id)}/>)}</div>
 </section>;
}

function DelegationInlineItem({value:d,userID,name,onOpen}:{value:Delegation;userID:string;name:(id:string)=>string;onOpen:()=>void}) {
 const reasons=delegationAttentionReasons(d,userID),questions=delegationOpenQuestions(d),conditions=delegationConditionProgress(d);
 const met=conditions.filter(item=>item.verdict==='met').length,unmet=conditions.filter(item=>item.verdict==='unmet').length;
 const impacts=[
  d.pending_changes?.length?`${d.pending_changes.length} 项上游要求变化`: '',
  d.dependency_states?.filter(item=>item.state==='changed'||item.state==='needs_review').length?`${d.dependency_states.filter(item=>item.state==='changed'||item.state==='needs_review').length} 项依赖待核对`:'',
  d.disagreements_omitted||d.disagreements?.some(item=>item.status!=='resolved')?'存在待处理交付分歧':'',
  d.delivery_omitted?'交付资料当前不可读':'',
  d.task?.access_error?'执行详情当前不可读':'',
  d.handoff?.remaining_work?`转交后剩余：${d.handoff.remaining_work}`:'',
 ].filter(Boolean);
 const activity=delegationCurrentActivity(d);
 return <article className={reasons.length?'needs-attention':''}>
  <button type="button" className="collaboration-inline-main" onClick={onOpen}>
   <span className="collaboration-inline-title"><strong>{d.contract_omitted?'待恢复资料的委派':d.brief.goal}</strong><small>{name(d.from_agent_id)} → {name(d.to_agent_id)}</small></span>
   <span className="collaboration-inline-stage">{delegationBusinessStage(d)}</span>
   {!!conditions.length&&<span className="collaboration-inline-evidence">完成条件：{met} 项已满足{unmet?` · ${unmet} 项未满足`:''} · {conditions.length-met-unmet} 项待核对</span>}
   {activity&&<span className={d.task?.waiting?'collaboration-inline-waiting':'collaboration-inline-activity'}>{activity}</span>}
   {!!impacts.length&&<span className="collaboration-inline-impact">关联影响：{impacts.join('；')}</span>}
   {!!questions.length&&<span className="collaboration-inline-questions">待回复问题：{questions.slice(0,2).map(item=>`${item.content}${item.count>1?` ×${item.count}`:''}`).join('；')}{questions.length>2?` 等 ${questions.length} 类`:''}</span>}
   {!!reasons.length&&<span className="collaboration-inline-attention">需要你处理：{reasons.join('；')}</span>}
  </button>
 </article>;
}
