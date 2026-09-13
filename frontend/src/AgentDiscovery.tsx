import { useEffect, useRef, useState } from 'react';
import { request, type MessagePage, type MessageRecord } from './api.ts';
import { describeError } from './errors.ts';
import { Button } from './components/ui/button';
import { Input } from './components/ui/input';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from './components/ui/dialog';
import { agentAvailabilityLabel, agentReasonLabel, type AgentCandidate, type AgentMatches, type AgentPage, type AgentRequirements } from './collaboration-state.ts';

export function AgentAvailability({value:v}:{value?:AgentCandidate}) {
 if (!v) return <p className="subtle">接单状态尚未核实</p>;
 return <div className="peer-availability"><strong>{agentAvailabilityLabel[v.state]||v.state}</strong><p>{v.running} 项执行中 · {v.queued} 项排队 · {v.waiting} 项等待处理</p>
  {v.reasons.map(reason=><p key={reason}>{agentReasonLabel[reason]||reason}</p>)}
  {!!v.missing_tools.length&&<p>缺少工具：{v.missing_tools.join('、')}</p>}{!!v.missing_skills.length&&<p>缺少 Skill：{v.missing_skills.join('、')}</p>}{!!v.unavailable_tools.length&&<p>不可用工具：{v.unavailable_tools.join('、')}</p>}
  <p>{v.cost.known?`预估模型费用：${v.cost.amount.toLocaleString(undefined,{maximumSignificantDigits:4})} ${v.cost.currency}`:'预估模型费用：未知'}</p>
  {v.cost.known&&<small className="subtle">{v.cost.basis==='historical_mean_per_run'?'按同配置历史单次运行用量估计，后续运行可能增加费用':'按本次填写的总用量估计'}；不含工具费用，不作为实际扣费上限。</small>}
  {v.cost.price&&<small className="subtle">价格依据：{v.cost.price.basis} · {new Date(v.cost.price.updated_at).toLocaleDateString()}</small>}
  {v.history.runs>0&&<p>近期同配置记录：{v.history.completed_runs}/{v.history.runs} 次运行完成；{v.history.accepted_deliveries}/{v.history.reviewed_deliveries} 项交付验收通过</p>}
  <small className="subtle">核实于 {new Date(v.checked_at).toLocaleTimeString()} · 接单时重新检查</small>
 </div>;
}

export function AgentMatchDialog({conversationID,page,onClose,onChoose}:{conversationID:string;page:AgentPage;onClose:()=>void;onChoose:(id:string,requirements:AgentRequirements)=>void}) {
 const [requirements,setRequirements] = useState<AgentRequirements>({tools:[],skills:[]});
	const [sources,setSources] = useState<MessageRecord[]>([]), [sourceError,setSourceError] = useState('');
 useEffect(()=>{const controller=new AbortController();request<MessagePage>(`/agent/conversations/${encodeURIComponent(conversationID)}/messages?limit=30`,'GET',undefined,controller.signal).then(page=>setSources(page.items.filter(m=>m.role==='assistant'&&m.run_id&&!m.access_error))).catch(e=>{if(!controller.signal.aborted)setSourceError(describeError(e));});return ()=>controller.abort();},[conversationID]);
 const [result,setResult] = useState<AgentMatches|null>(null),[busy,setBusy] = useState(false),[error,setError] = useState('');
 const version=useRef(0);
 function change(next:AgentRequirements) { version.current++;setRequirements(next);setResult(null);setError(''); }
 async function match(e:React.FormEvent) {
  e.preventDefault();if(busy)return;setBusy(true);setError('');const expected=version.current;
  try { const out=await request<AgentMatches>('/agent/agents/matches','POST',{conversation_id:conversationID,requirements});if(expected===version.current)setResult(out); }
  catch(e){if(expected===version.current)setError(describeError(e));}finally{setBusy(false);}
 }
 const name=(id:string)=>page.items.find(a=>a.id===id)?.name||id;
 return <Dialog open onOpenChange={open=>{if(!open)onClose();}}><DialogContent className="peer-editor"><DialogHeader><DialogTitle>按工作要求选择 Agent</DialogTitle><DialogDescription>核对能力、资料权限、负载和模型费用。没有匹配项时可以调整要求或等待，不会自动扩大权限。</DialogDescription></DialogHeader>
  <form className="peer-form" onSubmit={match}><label>任务类型（用于匹配历史记录）<Input maxLength={96} value={requirements.task_type||''} onChange={e=>change({...requirements,task_type:e.target.value})} placeholder="例如 report_review"/></label>
   <fieldset><legend>必需工具</legend><div className="peer-tool-options">{page.tools.map(tool=><label key={tool.key} title={tool.description}><input type="checkbox" checked={requirements.tools?.includes(tool.key)||false} onChange={e=>change({...requirements,tools:e.target.checked?[...(requirements.tools||[]),tool.key]:requirements.tools?.filter(k=>k!==tool.key)})}/>{tool.key}</label>)}</div></fieldset>
   {!!page.skills?.length&&<fieldset><legend>必需 Skill</legend><div className="peer-tool-options">{page.skills.map(skill=><label key={skill.key}><input type="checkbox" checked={requirements.skills?.includes(skill.key)||false} onChange={e=>change({...requirements,skills:e.target.checked?[...(requirements.skills||[]),skill.key]:requirements.skills?.filter(k=>k!==skill.key)})}/>{skill.name}</label>)}</div></fieldset>}
   <details><summary>模型费用筛选与资料引用</summary><p className="subtle">总用量是这项委派所有模型调用的估计。留空时参考同配置历史单次运行，没有样本则显示未知。费用筛选不会限制实际扣费。</p>
    <div className="peer-form"><label>预计总输入 token<Input type="number" min={1} max={1000000000} value={requirements.input_tokens||''} onChange={e=>change({...requirements,input_tokens:e.target.value?Number(e.target.value):undefined})}/></label><label>预计总输出 token<Input type="number" min={1} max={1000000000} value={requirements.output_tokens||''} onChange={e=>change({...requirements,output_tokens:e.target.value?Number(e.target.value):undefined})}/></label><label>预估模型费用上限<Input type="number" min={0} step="any" value={requirements.max_model_cost??''} onChange={e=>change({...requirements,max_model_cost:e.target.value?Number(e.target.value):undefined})}/></label><label>币种<Input maxLength={16} value={requirements.currency||''} onChange={e=>change({...requirements,currency:e.target.value.trim().toUpperCase()})} placeholder="与部署配置的价格币种相同"/></label>
    <fieldset><legend>所需资料：当前会话最近的执行结果</legend>{sources.map(source=><label className="peer-source-choice" key={source.id}><input type="checkbox" checked={requirements.sources?.some(r=>r.run_id===source.run_id)||false} onChange={e=>change({...requirements,sources:e.target.checked?[...(requirements.sources||[]),{conversation_id:conversationID,run_id:source.run_id}]:requirements.sources?.filter(r=>r.run_id!==source.run_id)})}/><span>{source.content.slice(0,100)||'已完成的执行结果'}</span></label>)}{!sources.length&&!sourceError&&<p className="subtle">当前没有可选择的执行结果。</p>}{sourceError&&<p role="alert" className="text-destructive">{sourceError}</p>}</fieldset><small className="subtle">接单与执行时核对来源权限；私有附件须先通过资料库的共享流程提供。</small></div>
   </details>
   <Button type="submit" disabled={busy}>{busy?'正在核对…':'检查匹配'}</Button>
  </form>
  {error&&<p role="alert" className="text-destructive">{error}</p>}
  {result&&<section aria-label="Agent 匹配结果"><p role="status">{result.recommended_agent_id?`建议选择：${name(result.recommended_agent_id)}`:'没有满足当前要求的 Agent'}</p><p className="subtle">按可执行状态、已有验收记录、排队情况、可比较费用和耗时排序；历史仅取当前用户最近 200 次终态运行。</p>
   {result.items.map(item=><section className="peer-card" key={item.agent_id}><h3>{name(item.agent_id)}</h3><AgentAvailability value={item}/><Button variant={item.agent_id===result.recommended_agent_id?'default':'outline'} disabled={!item.can_accept} onClick={()=>onChoose(item.agent_id,requirements)}>选择 {name(item.agent_id)}</Button></section>)}
   {!result.history_complete&&<p className="subtle">历史记录已按最近 200 次截取，不代表全部工作表现。</p>}
  </section>}
 </DialogContent></Dialog>;
}
