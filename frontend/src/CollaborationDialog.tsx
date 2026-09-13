import type { CollaborationAuthorization } from './collaboration-state.ts';
import { DelegationParticipants } from './DelegationParticipants.tsx';
import type { DelegationParticipant } from './collaboration-state.ts';
import { Disagreements } from './Disagreements.tsx';
import { agentWriteInput, initialDeliveryReview, withCompletionConditions } from './collaboration-state.ts';
import { useEffect, useRef, useState } from 'react';
import { request, type ConversationRecord } from './api.ts';
import { describeError } from './errors.ts';
import { sessionScope, sessionUserID } from './session.ts';
import { Button } from './components/ui/button';
import { Input } from './components/ui/input';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from './components/ui/dialog';
import { RunDialog } from './RunDialog';
import { outcomeInspectionTargets } from './execution-outcome.ts';
import { DependencyEditor, RequirementDetails } from './CollaborationRequirements';
import { PeerMessages } from './PeerMessages';
import { StructuredInputFields, parseStructuredInput } from './StructuredTaskInput';
import { AgentAvailability, AgentMatchDialog } from './AgentDiscovery';
import { DeliveryReviewFields, DeliveryVerificationSection, VerificationRuleFields } from './DeliveryVerification.tsx';
import type { ConditionAssessment } from './collaboration-state.ts';
import { deliveryIsCurrent, reviewedDependencies, type DependencyInput, delegationActions, delegationLabel, delegationNeedsAttention, decisionLabel, lines, peerPath, type AgentPage, type PeerAgent, type Delegation, type TaskBrief, type AgentRequirements } from './collaboration-state.ts';

const emptyPage: AgentPage = { items: [], tools: [], models: [], complete: true };
const emptyAgent: PeerAgent = { id: '', name: '', description: '', instructions: '', tools: [], skill_keys: [], model_key: 'default', enabled: true, max_concurrent: 1, revision: 0 };
const emptyBrief: TaskBrief = { version: 1, goal: '', deliverable: '', audience: '', constraints: [], completion_conditions: [], assumptions: [] };

// Preserve the exact retry identity across transport failures and page reloads.
function usePeerMutation() {
 const lock = useRef(false); const [busy, setBusy] = useState(false);
 async function mutate<T>(path: string, method: string, body: Record<string,unknown>): Promise<T> {
  if (lock.current) throw new Error('操作正在提交');
  lock.current = true; setBusy(true);
  const storageKey = `agent-collaboration:${sessionScope()}:${path}`;
  const signature = JSON.stringify({ method, body });
  try {
   let clientID = crypto.randomUUID();
   try { const saved = JSON.parse(localStorage.getItem(storageKey) || 'null'); if (saved?.signature === signature) clientID = saved.clientID; } catch { /* malformed draft */ }
   localStorage.setItem(storageKey, JSON.stringify({ signature, clientID }));
   const value = await request<T>(path, method, { ...body, client_id: clientID });
   localStorage.removeItem(storageKey); return value;
  } finally { lock.current = false; setBusy(false); }
 }
 return { mutate, busy };
}

export function CollaborationDialog({ conversationID, onClose, onSource, onArtifact }: { conversationID: string; onClose: () => void; onSource: (id: string) => void; onArtifact: (id: string, version: number) => void }) {
 const [authorization,setAuthorization]=useState<CollaborationAuthorization>({allowed:[],revision:''});
 const can=(op:NonNullable<CollaborationAuthorization['allowed']>[number])=>!!authorization.allowed?.includes(op);
 const [setup,setSetup]=useState<{administrator:boolean;ready:boolean}|null>(null);
 const [tab,setTab] = useState<'delegations'|'agents'>('delegations');
 const [page,setPage] = useState(emptyPage), [delegations,setDelegations] = useState<Delegation[]>([]);
 const [complete,setComplete] = useState(true), [selected,setSelected] = useState(''), [attention,setAttention] = useState(false), [scope,setScope] = useState('all');
 const [error,setError] = useState(''), [loading,setLoading] = useState(true), [refresh,setRefresh] = useState(0);
 const [editor,setEditor] = useState<PeerAgent|null>(null), [receiver,setReceiver] = useState('');
 const [matching,setMatching] = useState(false), [receiverRequirements,setReceiverRequirements] = useState<AgentRequirements>({});
 const [inspection,setInspection] = useState<{ conversationID: string; runID: string }|null>(null);
 const mutation = usePeerMutation();
 useEffect(() => {
  const controller = new AbortController();
  void (async()=>{
   try {
    const access=await request<CollaborationAuthorization>('/agent/collaboration-access','GET',undefined,controller.signal);
    if(controller.signal.aborted)return;
    setAuthorization(access);
    const allowed=new Set(access.allowed||[]);
    if(!allowed.has('configure'))setEditor(null);
    if(!allowed.has('initiate')||!allowed.has('discover')){setReceiver('');setMatching(false);}
    if(!allowed.has('execution_read'))setInspection(null);
    if(!allowed.has('view')){setDelegations([]);setSelected('');}
    if(!allowed.has('discover'))setPage(emptyPage);
    const [agents,work]=await Promise.allSettled([
     allowed.has('discover')?request<AgentPage>('/agent/agents','GET',undefined,controller.signal):Promise.resolve(emptyPage),
     allowed.has('view')?request<{items:Delegation[];complete:boolean}>('/agent/delegations','GET',undefined,controller.signal):Promise.resolve({items:[],complete:true}),
    ]);
    if(controller.signal.aborted)return;
    setPage(agents.status==='fulfilled'?agents.value:emptyPage);
    const items=work.status==='fulfilled'?work.value.items:[];
    setDelegations(items);setComplete(work.status==='fulfilled'&&work.value.complete);
    setSelected(id=>items.some(d=>d.id===id)?id:items[0]?.id||'');
    setInspection(current=>current&&items.some(d=>d.access?.execution_read&&(d.conversation_id===current.conversationID||d.source_conversation_id===current.conversationID||d.assignments?.some(a=>a.conversation_id===current.conversationID)))?current:null);
    const failed=[agents,work].find(r=>r.status==='rejected');
    setError(failed?.status==='rejected'?describeError(failed.reason):'');
    if(!allowed.size){
     try{const value=await request<{administrator:boolean;ready:boolean}>('/app/product/collaboration-setup','GET',undefined,controller.signal);if(!controller.signal.aborted)setSetup(value);}catch{if(!controller.signal.aborted)setSetup(null);}
    }else setSetup(null);
   }catch(e){if(!controller.signal.aborted){setAuthorization({allowed:[],revision:''});setPage(emptyPage);setDelegations([]);setInspection(null);setEditor(null);setReceiver('');setMatching(false);setError(describeError(e));}}
   finally{if(!controller.signal.aborted)setLoading(false);}
  })();
  return ()=>controller.abort();
 },[refresh]);
 useEffect(()=>{ const timer = window.setTimeout(()=>setRefresh(x=>x+1),3000); return ()=>window.clearTimeout(timer); },[refresh]);
 const detail = delegations.find(d=>d.id===selected);
 const name = (id:string) => page.items.find(a=>a.id===id)?.name || id;
 async function enableCollaboration(){try{await mutation.mutate('/app/product/collaboration-setup','POST',{});setRefresh(x=>x+1);}catch(e){setError(describeError(e));}}
 async function start(agent: PeerAgent) {
  try { const c = await mutation.mutate<ConversationRecord>('/agent/conversations','POST',{ agent_id:agent.id, title:agent.name }); onSource(c.id); }
  catch(e) { setError(describeError(e)); }
 }
 return <Dialog open onOpenChange={open=>{ if(!open) onClose(); }}><DialogContent className="collaboration-dialog">
  <DialogHeader><DialogTitle>Agent 协作</DialogTitle><DialogDescription>每个 Agent 独立工作。委派记录双方的责任、需求版本、沟通和实际执行过程。</DialogDescription></DialogHeader>
  <div className="todo-toolbar"><Button variant={tab==='delegations'?'default':'outline'} onClick={()=>setTab('delegations')}>委派与进度</Button><Button variant={tab==='agents'?'default':'outline'} onClick={()=>setTab('agents')}>Agent 目录</Button><Button variant="ghost" onClick={()=>setRefresh(x=>x+1)}>刷新</Button></div>
  {error && <p role="alert" className="text-destructive">{error}</p>}
  {!loading&&!authorization.allowed?.length&&<section className="task-waiting" aria-label="协作权限"><p>当前角色尚未获准使用 Agent 协作。可分别配置委派、沟通、执行查看和交付阅读权限。</p>{setup?.administrator&&!setup.ready&&<Button disabled={mutation.busy} onClick={()=>void enableCollaboration()}>启用管理员协作权限</Button>}</section>}
  {loading && <p role="status">正在读取协作记录…</p>}
  {tab==='agents' ? <>
   <div className="todo-toolbar">{can('configure')&&can('discover')&&<Button onClick={()=>setEditor({...emptyAgent})}>创建 Agent</Button>}{can('discover')&&can('initiate')&&<Button variant="outline" disabled={!conversationID} onClick={()=>setMatching(true)}>按工作要求选择 Agent</Button>}<small className="subtle">配置修改后，已接单的旧版本执行会停止，需重新委派。</small></div>
   <div className="peer-grid">{page.items.map(agent=><section className="peer-card" key={agent.id}><div className="task-detail-heading"><h3>{agent.name}</h3><span>{agent.enabled?'已启用':'已停用'}</span></div>{agent.shared&&<small className="subtle">共享 Agent · 配置所有者 {agent.owner_user_id} · {agent.delegation_execution==='owner'?`委派由所有者的 ${agent.delegation_role_key} 角色执行`:'按你的当前权限执行'}</small>}{!!agent.shared_with_user_ids?.length&&<small className="subtle">已共享给 {agent.shared_with_user_ids.length} 位用户</small>}<p>{agent.description || '尚未填写擅长的工作'}</p><small className="subtle">{agent.model_key} · 版本 {agent.revision} · 最多 {agent.max_concurrent} 个并行执行</small><AgentAvailability value={page.availability?.find(item=>item.agent_id===agent.id)}/><p className="peer-tools">{agent.tools.join('、') || '未选择工具'}</p><div className="todo-toolbar"><Button size="sm" disabled={!agent.enabled || mutation.busy} onClick={()=>void start(agent)}>开始对话</Button>{can('initiate')&&<Button size="sm" variant="outline" disabled={!conversationID || !agent.enabled || page.availability?.find(item=>item.agent_id===agent.id)?.can_accept===false} onClick={()=>{setReceiverRequirements({});setReceiver(agent.id);}}>委派当前工作</Button>}{can('configure')&&!agent.shared&&agent.id!=='default' && <Button size="sm" variant="ghost" onClick={()=>setEditor(agent)}>配置</Button>}</div></section>)}</div>
   {!conversationID && <p className="subtle">先打开一个会话，就可以从该会话发起委派。</p>}
  </> : <>
   <label className="peer-scope">查看范围 <select aria-label="委派查看范围" value={scope} onChange={e=>setScope(e.target.value)}><option value="all">全部委派</option><option value="sent" disabled={!conversationID}>当前会话发出的</option><option value="received" disabled={!conversationID}>当前会话接收的</option></select></label>
   <label className="memory-write-scope"><input type="checkbox" checked={attention} onChange={e=>setAttention(e.target.checked)}/>需要我处理（{delegations.filter(delegationNeedsAttention).length}）</label>
   <div className="task-list" aria-label="委派列表">{delegations.filter(d=>(!attention||delegationNeedsAttention(d)) && (scope==='all' || scope==='sent'&&d.source_conversation_id===conversationID || scope==='received'&&d.conversation_id===conversationID)).map(d=><button key={d.id} className={selected===d.id?'selected':''} onClick={()=>setSelected(d.id)}><span><strong>{d.brief.goal}</strong><small>{name(d.from_agent_id)} → {name(d.to_agent_id)} · {delegationLabel[d.status] || d.status} · 需求 v{d.brief.version}{d.task?.waiting ? ' · 等待你的处理' : ''}</small></span>{d.status==='running'&&!d.task?.waiting&&<span className="live-dot"/>}</button>)}</div>
   {!delegations.length&&!loading&&can('view')&&<p className="subtle">还没有委派。可以在 Agent 目录中选择接收方，或在对话中让 Agent 发起委派。</p>}
   {!complete&&<p className="subtle">这里显示最近 100 条委派。</p>}
   {detail && <DelegationDetail key={`${detail.id}:${authorization.revision}:${JSON.stringify(detail.access)}`} page={page} value={detail} items={delegations} name={name} onRefresh={()=>setRefresh(x=>x+1)} onRun={(conversationID,runID)=>setInspection({conversationID,runID})} onSource={onSource} onArtifact={onArtifact}/>}
  </>}
  {editor && can('configure')&&can('discover')&&<AgentEditor key={editor.id} value={editor} page={page} canShare={can('share')} canBind={can('share')&&can('receive')} onClose={()=>setEditor(null)} onSaved={saved=>{setPage(current=>({...current,items:current.items.some(item=>item.id===saved.id)?current.items.map(item=>item.id===saved.id?saved:item):[...current.items,saved]}));setEditor(null);setRefresh(x=>x+1);}}/>}
  {matching && can('initiate')&&can('discover')&&<AgentMatchDialog conversationID={conversationID} page={page} onClose={()=>setMatching(false)} onChoose={(id,requirements)=>{setMatching(false);setReceiverRequirements(requirements);setReceiver(id);}}/>}
  {receiver && can('initiate')&&can('discover')&&<DelegateForm items={delegations} requirements={receiverRequirements} receiver={receiver} name={name(receiver)} conversationID={conversationID} onClose={()=>setReceiver('')} onCreated={d=>{setReceiver('');setTab('delegations');setSelected(d.id);setRefresh(x=>x+1);}}/>}
  {inspection&&can('execution_read')&&<RunDialog key={inspection.runID} {...inspection} onClose={()=>{setInspection(null);setRefresh(x=>x+1);}}/>}
 </DialogContent></Dialog>;
}

function AssignmentDetails({value:d,name,onRun,onSource}:{value:Delegation;name:(id:string)=>string;onRun:(c:string,r:string)=>void;onSource:(id:string)=>void}){
 if(!d.assignments?.length)return null;
 const ownExecution=!d.execution_subject||d.execution_subject.user_id===sessionUserID();
 return <details className="peer-dependencies"><summary>接单与转交记录（{d.assignments.length} 次）</summary><ol aria-label="接单历史">{d.assignments.map(item=><li key={item.number}><strong>第 {item.number} 次 · {name(item.agent_id)}{item.conversation_id===d.conversation_id?' · 当前接收方':''}</strong><p>{item.reason}</p><small>{new Date(item.created_at).toLocaleString()} · {item.actor_id} · Agent 配置 {item.agent_revision}</small><div className="todo-toolbar">{d.access?.execution_read&&ownExecution&&<Button variant="ghost" onClick={()=>onSource(item.conversation_id)}>查看第 {item.number} 次接单会话</Button>}{d.access?.execution_read&&ownExecution&&d.handoff?.runs.filter(ref=>ref.conversation_id===item.conversation_id).map(ref=><Button variant="ghost" key={ref.run_id} onClick={()=>onRun(ref.conversation_id,ref.run_id)}>查看原执行记录</Button>)}</div>{item.previous_delivery&&<details><summary>转交时保留的交付</summary><p>{item.previous_delivery.summary}</p><small>需求 v{item.previous_delivery.brief_version} / 约定 {item.previous_delivery.agreement_revision||1}</small></details>}</li>)}</ol>{d.handoff&&<><h4>剩余工作</h4><p>{d.handoff.remaining_work}</p><h4>原操作回执</h4>{d.handoff.effects.length?d.handoff.effects.map(effect=><div key={`${effect.reference.run_id}:${effect.reference.call_id}`}><p>{effect.tool} · {effect.status==='completed'?effect.completion==='accepted'?'已受理':'已完成':'失败'}{effect.resource_id?' · '+effect.resource_id:''}</p><Button variant="ghost" onClick={()=>onRun(effect.reference.conversation_id,effect.reference.run_id)}>查看原操作证据</Button></div>):<p>没有已记录的写入操作。</p>}</>}</details>;
}

function AgentEditor({value,page,canShare,canBind,onClose,onSaved}:{value:PeerAgent;page:AgentPage;canShare:boolean;canBind:boolean;onClose:()=>void;onSaved:(agent:PeerAgent)=>void}) {
 const [form,setForm]=useState(value),[error,setError]=useState(''); const [execution,setExecution]=useState<'caller'|'owner'>(); const {mutate,busy}=usePeerMutation();
 async function save(e:React.FormEvent) { e.preventDefault(); try { const {id}=form; const saved=await mutate<PeerAgent>(`/agent/agents${id?`/${encodeURIComponent(id)}`:''}`,id?'PUT':'POST',agentWriteInput(form,canShare,execution));onSaved(saved); } catch(e){setError(describeError(e));} }
 return <Dialog open onOpenChange={open=>{if(!open)onClose();}}><DialogContent className="peer-editor"><DialogHeader><DialogTitle>{value.id?'配置 Agent':'创建 Agent'}</DialogTitle><DialogDescription>工具选择限制执行能力，实际使用时仍会检查当前权限。</DialogDescription></DialogHeader><form className="peer-form" onSubmit={save}>
  <label>能力定义<select aria-label="能力定义" value={form.definition_key||''} onChange={e=>{const d=page.definitions?.find(d=>d.key===e.target.value);setForm({...form,definition_key:e.target.value,...(d?{instructions:d.instructions,tools:d.tools,skill_keys:d.skill_keys,description:d.description}:{})});}}><option value="">自定义能力</option>{page.definitions?.map(d=><option key={d.key} value={d.key}>{d.name} · {d.version}</option>)}</select></label><small className="subtle">同一个能力定义可以创建多个独立 Agent，各自接单和工作。</small>
  <label>名称<Input required maxLength={128} value={form.name} onChange={e=>setForm({...form,name:e.target.value})}/></label>
  <label>擅长的工作<Input maxLength={2048} value={form.description} onChange={e=>setForm({...form,description:e.target.value})}/></label>
  <label>工作要求<textarea disabled={!!form.definition_key} required maxLength={32768} rows={5} value={form.instructions} onChange={e=>setForm({...form,instructions:e.target.value})}/></label>
  <div className="todo-toolbar"><label>模型<select aria-label="模型" value={form.model_key} onChange={e=>setForm({...form,model_key:e.target.value})}>{page.models.map(key=><option key={key}>{key}</option>)}</select></label><label>并行上限<Input type="number" min={1} max={32} required value={form.max_concurrent} onChange={e=>setForm({...form,max_concurrent:Number(e.target.value)})}/></label><label><input type="checkbox" checked={form.enabled} onChange={e=>setForm({...form,enabled:e.target.checked})}/>启用</label></div>
  {canShare&&<label>可使用此 Agent 的用户 ID（每行一项）<textarea aria-label="可使用此 Agent 的用户 ID（每行一项）" rows={3} maxLength={16384} value={(form.shared_with_user_ids||[]).join('\n')} onChange={e=>setForm({...form,shared_with_user_ids:e.target.value.split('\n')})} onBlur={()=>setForm({...form,shared_with_user_ids:lines((form.shared_with_user_ids||[]).join('\n'))})}/><small className="subtle">仅限当前工作空间的有效用户。清空后撤回共享；委派执行身份在下方单独设置。</small></label>}
  <label>委派执行身份<select aria-label="委派执行身份" value={execution||form.delegation_execution||'caller'} onChange={e=>setExecution(e.target.value as 'caller'|'owner')}><option value="caller">使用发起人的权限</option><option value="owner" disabled={!canBind}>使用我的当前角色接单</option></select><small className="subtle">允许使用此 Agent 的用户可发起委派。选择我的角色后，委派使用保存时选定角色的当前权限；普通对话仍使用对话发起人的权限。</small></label>
  {(execution||form.delegation_execution)==='owner'&&<div className="peer-execution-binding"><small className="subtle">{execution==='owner'?'保存后绑定我当前选定的角色':`已绑定角色：${form.delegation_role_key}`}</small>{canBind&&execution!=='owner'&&<Button type="button" variant="ghost" onClick={()=>setExecution('owner')}>改用我当前的角色</Button>}</div>}
  <fieldset><legend>可用工具</legend><div className="peer-tool-options">{page.tools.map(tool=><label key={tool.key} title={tool.description}><input type="checkbox" disabled={!!form.definition_key} checked={form.tools.includes(tool.key)} onChange={e=>setForm({...form,tools:e.target.checked?[...form.tools,tool.key]:form.tools.filter(k=>k!==tool.key)})}/><span>{tool.key}<small>{tool.effect==='write'?'需要操作授权':'读取'}</small></span></label>)}</div></fieldset>
  {!!page.skills?.length&&<fieldset><legend>Skill</legend><div className="peer-tool-options">{page.skills.map(skill=><label key={skill.key} title={skill.description}><input type="checkbox" disabled={!!form.definition_key} checked={form.skill_keys.includes(skill.key)} onChange={e=>setForm({...form,skill_keys:e.target.checked?[...form.skill_keys,skill.key]:form.skill_keys.filter(k=>k!==skill.key)})}/>{skill.name} · {skill.version}</label>)}</div></fieldset>}
  {error&&<p role="alert" className="text-destructive">{error}</p>}<Button disabled={busy} type="submit">{busy?'正在保存…':'保存 Agent'}</Button>
 </form></DialogContent></Dialog>;
}

function BriefFields({value,onChange}:{value:TaskBrief;onChange:(value:TaskBrief)=>void}) {
 return <><label>目标<Input required maxLength={2048} value={value.goal} onChange={e=>onChange({...value,goal:e.target.value})}/></label><label>交付物<Input required maxLength={2048} value={value.deliverable} onChange={e=>onChange({...value,deliverable:e.target.value})}/></label><label>面向谁<Input maxLength={512} value={value.audience} onChange={e=>onChange({...value,audience:e.target.value})}/></label>{([['completion_conditions','完成条件'],['constraints','约束'],['assumptions','已知假设']] as const).map(([key,label])=><label key={key}>{label}（每行一项）<textarea aria-label={`${label}（每行一项）`} rows={2} value={(value[key] || []).join('\n')} onChange={e=>onChange(key==='completion_conditions'?withCompletionConditions(value,e.target.value.split('\n')):{...value,[key]:e.target.value.split('\n')})} onBlur={()=>onChange(key==='completion_conditions'?withCompletionConditions(value,lines(value.completion_conditions.join('\n'))):{...value,[key]:lines((value[key] || []).join('\n'))})}/></label>)}<VerificationRuleFields value={value} onChange={onChange}/></>;
}
function DelegateForm({items,requirements,receiver,name,conversationID,onClose,onCreated}:{items:Delegation[];requirements:AgentRequirements;receiver:string;name:string;conversationID:string;onClose:()=>void;onCreated:(d:Delegation)=>void}) {
 const source=items.find(item=>item.conversation_id===conversationID); const root=source?.root_conversation_id||conversationID;
 const choices=items.filter(item=>item.root_conversation_id===root&&!['cancelled','rejected'].includes(item.status));
 const [dependencies,setDependencies]=useState<DependencyInput[]>(source?[{delegation_id:source.id,brief_version:source.brief.version,agreement_revision:source.agreement_revision||1}]:[]);
 const [inputJSON,setInputJSON]=useState(''),[inputSchema,setInputSchema]=useState(''),[outputSchema,setOutputSchema]=useState('');
 const [brief,setBrief]=useState({...emptyBrief}),[purpose,setPurpose]=useState(''),[input,setInput]=useState(''),[error,setError]=useState('');const {mutate,busy}=usePeerMutation();
 async function submit(e:React.FormEvent){e.preventDefault();try{onCreated(await mutate<Delegation>('/agent/delegations','POST',{conversation_id:conversationID,agent_id:receiver,requirements,purpose,brief,input,dependencies,structured_input:parseStructuredInput(inputJSON,inputSchema),...(outputSchema.trim()?{output_schema:JSON.parse(outputSchema)}:{}),budget:{}}));}catch(e){setError(describeError(e));}}
 return <Dialog open onOpenChange={open=>{if(!open)onClose();}}><DialogContent className="peer-editor"><DialogHeader><DialogTitle>委派给 {name}</DialogTitle><DialogDescription>接收方使用独立会话。请提供完成这项工作所需的上下文。</DialogDescription></DialogHeader><form className="peer-form" onSubmit={submit}><label>委派原因<Input required maxLength={2048} value={purpose} onChange={e=>setPurpose(e.target.value)}/></label><BriefFields value={brief} onChange={setBrief}/><details><summary>结构化输入与交付格式（可选）</summary><StructuredInputFields data={inputJSON} schema={inputSchema} onData={setInputJSON} onSchema={setInputSchema}/><label>输出 JSON Schema（可选）<textarea rows={4} maxLength={16384} spellCheck={false} value={outputSchema} onChange={e=>setOutputSchema(e.target.value)}/></label></details><DependencyEditor items={choices} value={dependencies} onChange={setDependencies}/><label>输入资料<textarea maxLength={8192} rows={4} value={input} onChange={e=>setInput(e.target.value)}/></label>{error&&<p role="alert" className="text-destructive">{error}</p>}<Button type="submit" disabled={busy}>{busy?'正在委派…':'发起委派'}</Button></form></DialogContent></Dialog>;
}

function DelegationDetail({value:d,page,items,name,onRefresh,onRun,onSource,onArtifact}:{value:Delegation;page:AgentPage;items:Delegation[];name:(id:string)=>string;onRefresh:()=>void;onRun:(c:string,r:string)=>void;onSource:(id:string)=>void;onArtifact:(id:string,v:number)=>void}) {
 const [reviewConditions,setReviewConditions]=useState<ConditionAssessment[]>([]),[reviewDigest,setReviewDigest]=useState('');
 const [transferTo,setTransferTo]=useState(''),[remainingWork,setRemainingWork]=useState('');
 const [inputJSON,setInputJSON]=useState(JSON.stringify(d.structured_input?.data,null,2)||''),[inputSchema,setInputSchema]=useState(JSON.stringify(d.structured_input?.schema,null,2)||'');
 const [expectedRevision,setExpectedRevision]=useState(d.revision);
 const [action,setAction]=useState(''),[reason,setReason]=useState(''),[brief,setBrief]=useState(d.brief),[dependencies,setDependencies]=useState<DependencyInput[]>(reviewedDependencies(d)),[reviewed,setReviewed]=useState(false),[error,setError]=useState('');const {mutate,busy}=usePeerMutation();
 async function decide(e:React.FormEvent){e.preventDefault();try{await mutate(peerPath(d.id)+'/decisions','POST',{expected_revision:expectedRevision,action,reason,...(['accept_delivery','review_delivery'].includes(action)?{review:{delivery_digest:reviewDigest,conditions:reviewConditions}}:{}),...(action==='transfer'?{transfer:{agent_id:transferTo,remaining_work:remainingWork},dependencies}:{}),...(action==='update_input'?{structured_input:parseStructuredInput(inputJSON,inputSchema)}:{}),...(['resume','set_dependencies'].includes(action)?{dependencies}:{}),...(action==='update_brief'?{brief:{...brief,version:brief.version+1}}:{})});setAction('');setReason('');onRefresh();}catch(e){setError(describeError(e));onRefresh();}}
 async function send(body:Record<string,unknown>):Promise<boolean>{try{await mutate(peerPath(d.id)+'/messages','POST',body);setError('');onRefresh();return true;}catch(e){setError(describeError(e));return false;}}
 async function saveParticipants(participants:DelegationParticipant[],revision:number,reason:string){try{setError('');await mutate(peerPath(d.id)+'/decisions','POST',{expected_revision:revision,action:'set_participants',reason,participants});onRefresh();return true;}catch(e){setError(describeError(e));onRefresh();return false;}}
 async function mutateDisagreement(disagreement:Record<string,unknown>,reason:string,revision:number){try{setError('');await mutate(peerPath(d.id)+'/decisions','POST',{expected_revision:revision,action:'disagreement',reason,disagreement});onRefresh();return true;}catch(e){setError(describeError(e));onRefresh();return false;}}
 const outcomeTargets = outcomeInspectionTargets(d.task?.progress.run_status, d.task?.steps);
 async function inspectOutcome(step:number,callID:string){try{setError('');await mutate(peerPath(d.id)+'/decisions','POST',{expected_revision:d.revision,action:'inspect_outcome',reason:'核查原操作回执',inspection:{run_id:d.task?.execution_run_id,step,call_id:callID}});}catch(e){setError(describeError(e));}finally{onRefresh();}}
 const ownExecution=!d.execution_subject||d.execution_subject.user_id===sessionUserID(),ownSource=!d.owner_user_id||d.owner_user_id===sessionUserID();
 return <section className="task-detail" aria-label="委派详情"><div className="task-detail-heading"><h3>{d.brief.goal}</h3><span>{delegationLabel[d.status]} · v{d.brief.version}</span></div><p>{d.purpose}</p><dl className="run-metadata"><dt>双方</dt><dd>{name(d.from_agent_id)} → {name(d.to_agent_id)}</dd>{d.execution_subject&&<><dt>执行用户</dt><dd>{d.execution_subject.user_id} · 工作空间 {d.execution_subject.workspace_id}</dd></>}<dt>交付物</dt><dd>{d.brief.deliverable || '未填写'}</dd><dt>面向谁</dt><dd>{d.brief.audience || '未填写'}</dd>{([['completion_conditions','完成条件'],['constraints','约束'],['assumptions','假设']] as const).map(([key,label])=><div className="peer-definition" key={key}><dt>{label}</dt><dd>{(d.brief[key] || []).join('；')||'未填写'}</dd></div>)}<dt>当前决定</dt><dd>{d.decision||'无'}</dd></dl>
 <DelegationParticipants value={d} busy={busy} onSave={saveParticipants}/>
 <RequirementDetails value={d} items={items}/>
 <AssignmentDetails value={d} name={name} onRun={onRun} onSource={onSource}/>
 {d.access?.manage&&d.access.execution_read&&!!outcomeTargets.length&&<section className="task-waiting" aria-label="原操作结果核查"><h4>原操作结果待核查</h4><p>先查询原操作回执。核查完成后，按当前要求继续需要单独操作。</p>{outcomeTargets.map(({step,call})=><div key={`${step}:${call.id}`}><p>第 {step+1} 步 · {call.name}{call.outcome_inspection&&<small> · 上次核查 {new Date(call.outcome_inspection.checked_at).toLocaleString()}，结果尚未明确</small>}</p><Button variant="outline" disabled={busy} onClick={()=>inspectOutcome(step,call.id)}>{busy?'正在核查…':'核查原操作结果'}</Button></div>)}</section>}
 {d.access?.execution_read&&d.task&&<>{d.task.access_error?<p role="status" className="subtle">执行详情当前不可读取，委派状态和获准的交付仍可查看。</p>:<><p>{d.task.progress.steps} 个步骤 · {d.task.progress.tool_calls} 次工具调用</p>{d.task.waiting&&<div className="task-waiting"><strong>需要你处理</strong><p>{d.task.waiting.question}</p></div>}{d.task.result&&<div className="task-result"><strong>实际执行结果</strong><p>{d.task.result.preview}</p></div>}<div className="todo-toolbar">{d.task.execution_run_id&&<Button onClick={()=>onRun(d.conversation_id,d.task!.execution_run_id!)}>{d.task.waiting?'处理等待事项':'查看实时执行与工具调用'}</Button>}{d.task.artifacts?.map(a=><Button key={a.id} variant="outline" onClick={()=>onArtifact(a.id,a.version)}>{a.title}</Button>)}</div></>}</>}
 {d.access?.delivery_read&&d.delivery&&<section className="task-result"><h4>交付 · 需求 v{d.delivery.brief_version} / 约定 {d.delivery.agreement_revision||1}</h4>{!deliveryIsCurrent(d)&&<p className="text-destructive">这是旧要求下的交付，需更新后重新验收。</p>}<p>{d.delivery.summary}</p>{d.delivery.data!==undefined&&<pre>{JSON.stringify(d.delivery.data,null,2)}</pre>}{!!d.delivery.unresolved?.length&&<p>未解决：{d.delivery.unresolved.join('；')}</p>}{d.access?.execution_read&&d.delivery.evidence?.map(ref=><Button variant="ghost" key={ref.run_id} onClick={()=>onRun(ref.conversation_id,ref.run_id)}>查看执行证据</Button>)}</section>}
 {d.access?.delivery_read&&<DeliveryVerificationSection value={d} onRun={d.access.execution_read?onRun:undefined}/>}
 {d.access?.delivery_read&&<Disagreements value={d} name={name} busy={busy} onMutate={mutateDisagreement} onRun={d.access.execution_read?onRun:undefined}/>}
 {d.access?.execution_read&&<div className="todo-toolbar">{ownSource&&<Button variant="outline" onClick={()=>onSource(d.source_conversation_id)}>发起方会话</Button>}{ownExecution&&<Button variant="outline" onClick={()=>onSource(d.conversation_id)}>接收方会话</Button>}</div>}
 {d.access?.communicate&&<PeerMessages value={d} name={name} busy={busy} onSend={send}/>}
 <div className="todo-toolbar">{delegationActions(d).map(key=><Button key={key} variant="outline" disabled={busy} onClick={()=>{setInputJSON(JSON.stringify(d.structured_input?.data,null,2)||'');setInputSchema(JSON.stringify(d.structured_input?.schema,null,2)||'');setAction(key);setTransferTo('');setRemainingWork('');setExpectedRevision(d.revision);setBrief(d.brief);setReason('');setDependencies(reviewedDependencies(d));setReviewed(false);setReviewConditions(initialDeliveryReview(d));setReviewDigest(d.verification?.delivery_digest||'');}}>{decisionLabel[key]}</Button>)}</div>
 {action&&delegationActions(d).includes(action)&&<form className="peer-form" onSubmit={decide}><h4>{decisionLabel[action]}</h4>{['accept_delivery','review_delivery'].includes(action)&&<DeliveryReviewFields value={d} conditions={reviewConditions} onChange={setReviewConditions}/>}
 {action==='transfer'&&<><p>当前要求 v{d.brief.version} / 约定 {d.agreement_revision||1}。转交保留原操作及委派总预算，请说明还需要完成的工作。</p><label>接收 Agent<select aria-label="接收 Agent" required value={transferTo} onChange={e=>setTransferTo(e.target.value)}><option value="">选择接收方</option>{page.items.filter(a=>a.enabled&&a.id!==d.to_agent_id&&a.id!==d.from_agent_id).map(a=><option key={a.id} value={a.id}>{a.name}</option>)}</select></label><label>剩余工作<textarea aria-label="剩余工作" required maxLength={8192} rows={4} value={remainingWork} onChange={e=>setRemainingWork(e.target.value)}/></label>{!!outcomeTargets.length&&<p role="status">请先核查上方原操作结果，再转交工作。</p>}</>}{action==='update_brief'&&<><p className="subtle">提交后停止旧版本执行，检查新需求后可继续。</p><BriefFields value={brief} onChange={setBrief}/></>}{action==='update_input'&&<><p>输入变化后，任务及依赖该输入的工作需核对新版本再继续。</p><StructuredInputFields data={inputJSON} schema={inputSchema} onData={setInputJSON} onSchema={setInputSchema}/></>}{action==='set_dependencies'&&<DependencyEditor items={items.filter(item=>item.id!==d.id&&item.root_conversation_id===d.root_conversation_id&&!['cancelled','rejected'].includes(item.status))} value={dependencies} onChange={setDependencies}/>}
 {action==='resume'&&!!d.pending_changes?.length&&<><p>本次采用：{dependencies.map(edge=>`${items.find(item=>item.id===edge.delegation_id)?.brief.goal||edge.delegation_id} · 需求 v${edge.brief_version} / 约定 ${edge.agreement_revision||1}`).join('；')||`当前需求 v${d.brief.version}`}</p>{d.dependency_states?.some(state=>state.state==='needs_review')&&<p role="status">先在上游任务中核对要求并继续，再处理本任务。</p>}<label><input type="checkbox" checked={reviewed} onChange={e=>setReviewed(e.target.checked)}/>已核对上方新要求和依赖，按显示版本继续</label></>}<label>{action==='accept_delivery'?'验收依据与完成条件核对':'原因与处理意见'}<textarea required maxLength={4096} rows={3} value={reason} onChange={e=>setReason(e.target.value)}/></label><div className="todo-toolbar"><Button disabled={busy||action==='transfer'&&!!outcomeTargets.length||action==='resume'&&!!d.pending_changes?.length&&(!reviewed||d.dependency_states?.some(state=>state.state==='needs_review'))} type="submit">提交{decisionLabel[action]}</Button><Button type="button" variant="ghost" onClick={()=>setAction('')}>收起</Button></div></form>}
 {error&&<p role="alert" className="text-destructive">{error}</p>}
 </section>;
}
