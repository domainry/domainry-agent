import {useEffect,useRef,useState} from 'react';
import {request} from './api.ts';
import type {SkillSummary} from './collaboration-state.ts';
import {describeError} from './errors.ts';
import {Button} from './components/ui/button';
import {Input} from './components/ui/input';

type SkillResource={key:string;name:string;description?:string;media_type:string;content:string};
type SkillVersion={definition:{key:string;version:string;name:string;description?:string;instructions:string;input_schema?:unknown;output_schema?:unknown;resources?:SkillResource[];workflow?:{key:string;name:string;instructions:string;depends_on?:string[];allowed_tools?:string[]}[];allowed_tools?:string[]};digest:string;state:string;revision:number;evaluation_id?:string;published_at?:string};
type Evaluation={suite_version:string;scenario_ids:string[];baseline_completed:number;candidate_completed:number;baseline_omissions:number;candidate_omissions:number;passed:boolean;regressions?:string[];notes?:string};
type Candidate={id:string;kind:'skill'|'agent_prompt'|'delegation_strategy';target_key:string;version:string;baseline_version?:string;baseline_snapshot?:boolean;feedback_ids:string[];proposal:unknown;reason:string;status:string;revision:number;evaluation?:Evaluation;updated_at:string};

const statusLabel:Record<string,string>={candidate:'待评估',evaluated:'已评估',published:'已发布',retired:'历史版本',rejected:'未通过',rolled_back:'已回退'};
const kindLabel:Record<string,string>={skill:'Skill',agent_prompt:'Agent 提示词',delegation_strategy:'委派策略'};
const splitKeys=(value:string)=>value.split(/[\s,，]+/).map(item=>item.trim()).filter(Boolean);

export function SkillCatalog({canConfigure}:{canConfigure:boolean}){
	const proposalLoad=useRef(0);
 const [skills,setSkills]=useState<SkillSummary[]>([]),[candidates,setCandidates]=useState<Candidate[]>([]),[selected,setSelected]=useState<SkillVersion|null>(null),[resource,setResource]=useState<SkillResource|null>(null),[error,setError]=useState(''),[loading,setLoading]=useState(true);
 const [creating,setCreating]=useState(false),[kind,setKind]=useState<Candidate['kind']>('skill'),[target,setTarget]=useState(''),[version,setVersion]=useState(''),[feedbackIDs,setFeedbackIDs]=useState(''),[proposal,setProposal]=useState(''),[reason,setReason]=useState('');
 const [evaluating,setEvaluating]=useState<Candidate|null>(null),[suite,setSuite]=useState('v01-'),[scenarios,setScenarios]=useState(''),[baselineCompleted,setBaselineCompleted]=useState(0),[candidateCompleted,setCandidateCompleted]=useState(0),[baselineOmissions,setBaselineOmissions]=useState(0),[candidateOmissions,setCandidateOmissions]=useState(0),[regressions,setRegressions]=useState(''),[passed,setPassed]=useState(false),[notes,setNotes]=useState('');
 const [rollingBack,setRollingBack]=useState<Candidate|null>(null),[rollbackVersion,setRollbackVersion]=useState(''),[rollbackReason,setRollbackReason]=useState('');
 const [busy,setBusy]=useState(false);

 async function reload(signal?:AbortSignal){
  const [catalog,improvements]=await Promise.all([
   request<{items:SkillSummary[]}>('/agent/skills','GET',undefined,signal),
   canConfigure?request<{items:Candidate[]}>('/agent/improvement-candidates','GET',undefined,signal):Promise.resolve({items:[]}),
  ]);
  setSkills(catalog.items);setCandidates(improvements.items);
 }
 useEffect(()=>{const controller=new AbortController();void reload(controller.signal).catch(e=>{if(!controller.signal.aborted)setError(describeError(e));}).finally(()=>{if(!controller.signal.aborted)setLoading(false);});return()=>controller.abort();},[canConfigure]);
 async function inspect(skill:SkillSummary){try{setError('');setResource(null);setSelected(await request<SkillVersion>(`/agent/skills/${encodeURIComponent(skill.key)}/versions/${encodeURIComponent(skill.version)}`));}catch(e){setError(describeError(e));}}
 async function loadResource(key:string){if(!selected)return;try{setError('');setResource(await request<SkillResource>(`/agent/skills/${encodeURIComponent(selected.definition.key)}/versions/${encodeURIComponent(selected.definition.version)}/resources/${encodeURIComponent(key)}`));}catch(e){setError(describeError(e));}}
	 async function hydrateSkillProposal(definition:SkillVersion['definition'],load:number){
	  setBusy(true);setError('');
	  try{
	   const resources=await Promise.all((definition.resources||[]).map(item=>request<SkillResource>(`/agent/skills/${encodeURIComponent(definition.key)}/versions/${encodeURIComponent(definition.version)}/resources/${encodeURIComponent(item.key)}`)));
	   if(proposalLoad.current===load)setProposal(JSON.stringify({...definition,resources,version:''},null,2));
	  }catch(e){if(proposalLoad.current===load)setError(describeError(e));}
	  finally{if(proposalLoad.current===load)setBusy(false);}
	 }
	 async function startCandidate(){
	  const load=++proposalLoad.current;
	  const definition=selected?.definition;
	  setKind('skill');setTarget(definition?.key||skills[0]?.key||'');setVersion('');setFeedbackIDs('');setReason('');
	  setProposal(definition?JSON.stringify({...definition,version:''},null,2):'{}');setCreating(true);
	  if(!definition)return;
	  await hydrateSkillProposal(definition,load);
	 }
	 function changeKind(value:Candidate['kind']){
	  const load=++proposalLoad.current;setBusy(false);
	  setKind(value);
	  if(value==='skill'){
	   const definition=selected?.definition;setTarget(definition?.key||skills[0]?.key||'');setVersion('');setProposal(definition?JSON.stringify({...definition,version:''},null,2):'{}');if(definition)void hydrateSkillProposal(definition,load);
  }else if(value==='agent_prompt'){
   setTarget('');setVersion('');setProposal(JSON.stringify({instructions:''},null,2));
  }else{
   setTarget('default');setVersion('');setProposal(JSON.stringify({minimum_reviewed_deliveries:3,prefer_cost:true,prefer_coordination:true,prefer_duration:true},null,2));
  }
 }
 async function createCandidate(event:React.FormEvent){
  event.preventDefault();if(busy)return;setBusy(true);setError('');
  try{
   const parsed=JSON.parse(proposal) as Record<string,unknown>;
   if(kind==='skill')parsed.version=version;
   await request<Candidate>('/agent/improvement-candidates','POST',{client_id:crypto.randomUUID(),kind,target_key:target.trim(),version:version.trim(),feedback_ids:splitKeys(feedbackIDs),proposal:parsed,reason:reason.trim()});
   await reload();setCreating(false);
  }catch(e){setError(describeError(e));}finally{setBusy(false);}
 }
 async function evaluateCandidate(event:React.FormEvent){
  event.preventDefault();if(!evaluating||busy)return;setBusy(true);setError('');
  try{
   await request<Candidate>(`/agent/improvement-candidates/${encodeURIComponent(evaluating.id)}/evaluation`,'POST',{client_id:crypto.randomUUID(),expected_revision:evaluating.revision,suite_version:suite.trim(),scenario_ids:splitKeys(scenarios),baseline_completed:baselineCompleted,candidate_completed:candidateCompleted,baseline_omissions:baselineOmissions,candidate_omissions:candidateOmissions,regressions:splitKeys(regressions),passed,notes:notes.trim()});
   await reload();setEvaluating(null);
  }catch(e){setError(describeError(e));}finally{setBusy(false);}
 }
 async function publishCandidate(item:Candidate){
  if(busy)return;setBusy(true);setError('');try{await request<Candidate>(`/agent/improvement-candidates/${encodeURIComponent(item.id)}/publish`,'POST',{client_id:crypto.randomUUID(),expected_revision:item.revision});await reload();}catch(e){setError(describeError(e));}finally{setBusy(false);}
 }
 async function rollbackCandidate(event:React.FormEvent){
  event.preventDefault();if(!rollingBack||busy)return;setBusy(true);setError('');try{await request<Candidate>(`/agent/improvement-candidates/${encodeURIComponent(rollingBack.id)}/rollback`,'POST',{client_id:crypto.randomUUID(),expected_revision:rollingBack.revision,target_version:rollbackVersion.trim(),reason:rollbackReason.trim()});await reload();setRollingBack(null);}catch(e){setError(describeError(e));}finally{setBusy(false);}
 }

 return <section aria-label="Skill 与能力改进" className="task-detail"><div className="task-detail-heading"><h3>Skill 目录</h3><span>摘要优先 · 精确版本</span></div><p className="subtle">运行时只把这些摘要交给 Agent。Agent 需要使用时才读取正文或指定资源；Skill 不能增加 Agent 的工具权限。</p>{loading&&<p role="status">正在读取 Skill…</p>}{error&&<p role="alert" className="text-destructive">{error}</p>}
	  <div className="peer-grid">{skills.map(skill=><section className="peer-card" key={skill.key}><div className="task-detail-heading"><h4>{skill.name}</h4><span>{skill.version}</span></div><p>{skill.description||'没有摘要'}</p><small className="subtle">{skill.allowed_tools?.length?`使用现有工具：${skill.allowed_tools.join('、')}`:'不使用工具'} · {skill.workflow_steps} 个流程步骤 · {skill.resources?.length||0} 项资源</small><Button size="sm" variant="outline" onClick={()=>void inspect(skill)}>按需读取正文</Button></section>)}</div>{!skills.length&&!loading&&<p className="subtle">当前部署没有可用 Skill。</p>}
	  {selected&&<section className="skill-detail" aria-label="Skill 版本详情"><div className="task-detail-heading"><h4>{selected.definition.name} · {selected.definition.version}</h4><span>{selected.state==='published'?'当前发布':'历史版本'}</span></div><pre>{selected.definition.instructions}</pre>{Boolean(selected.definition.input_schema||selected.definition.output_schema)&&<details><summary>输入输出约定</summary>{Boolean(selected.definition.input_schema)&&<><strong>输入</strong><pre>{JSON.stringify(selected.definition.input_schema,null,2)||'{}'}</pre></>}{Boolean(selected.definition.output_schema)&&<><strong>输出</strong><pre>{JSON.stringify(selected.definition.output_schema,null,2)||'{}'}</pre></>}</details>}{!!selected.definition.workflow?.length&&<details open><summary>可复用流程（{selected.definition.workflow.length} 步）</summary><ol>{selected.definition.workflow.map(step=><li key={step.key}><strong>{step.name}</strong><p>{step.instructions}</p><small className="subtle">{step.depends_on?.length?`依赖 ${step.depends_on.join('、')}`:'无前置依赖'}{step.allowed_tools?.length?` · 工具 ${step.allowed_tools.join('、')}`:''}</small></li>)}</ol></details>}{!!selected.definition.resources?.length&&<div><h4>按需资源</h4><div className="todo-toolbar">{selected.definition.resources.map(item=><Button key={item.key} size="sm" variant="ghost" onClick={()=>void loadResource(item.key)}>{item.name}</Button>)}</div></div>}{resource&&<details open><summary>{resource.name} · {resource.media_type}</summary><pre>{resource.content}</pre></details>}{canConfigure&&<Button size="sm" disabled={busy} onClick={()=>void startCandidate()}>以此 Skill 创建改进候选</Button>}</section>}
	  {canConfigure&&<section aria-label="能力改进候选"><div className="task-detail-heading"><h3>能力改进候选</h3><span>反馈 → 对照评估 → 发布</span></div><p className="subtle">候选必须关联实际任务反馈和相同基线配置。V01 对照评估通过且没有退步后才能发布；回退只切换后续任务使用的配置版本。</p><Button size="sm" variant="outline" disabled={busy} onClick={()=>{if(creating){proposalLoad.current++;setCreating(false);}else void startCandidate();}}>{creating?'收起候选表单':'创建改进候选'}</Button>
   {creating&&<form className="peer-form" aria-label="创建能力改进候选" onSubmit={createCandidate}><label>配置类型<select value={kind} onChange={event=>changeKind(event.target.value as Candidate['kind'])}><option value="skill">Skill</option><option value="agent_prompt">Agent 提示词</option><option value="delegation_strategy">委派策略</option></select></label><label>目标键<Input required maxLength={96} value={target} disabled={kind==='delegation_strategy'} onChange={event=>setTarget(event.target.value)}/></label><label>新版本<Input required maxLength={128} value={version} onChange={event=>setVersion(event.target.value)}/></label><label>关联反馈 ID（逗号或空格分隔）<Input required value={feedbackIDs} onChange={event=>setFeedbackIDs(event.target.value)}/></label><label>候选配置 JSON<textarea required rows={12} value={proposal} onChange={event=>setProposal(event.target.value)}/></label><label>改进原因<textarea required maxLength={4096} rows={3} value={reason} onChange={event=>setReason(event.target.value)}/></label><Button disabled={busy} type="submit">保存候选</Button></form>}
   <div className="peer-grid">{candidates.map(item=><section className="peer-card" key={item.id}><div className="task-detail-heading"><strong>{kindLabel[item.kind]||item.kind} · {item.target_key} · {item.version}</strong><span>{item.baseline_snapshot?'回退基线':statusLabel[item.status]||item.status}</span></div><p>{item.reason}</p><small className="subtle">{item.baseline_snapshot?'系统归档':'候选'} {item.id} · revision {item.revision} · 基线 {item.baseline_version||'无'} · 反馈 {item.feedback_ids.join('、')||'无'}{item.evaluation?` · ${item.evaluation.suite_version} · ${item.evaluation.scenario_ids.length} 个场景 · ${item.evaluation.passed?'通过':'未通过'}`:' · 尚无评估'}</small><details><summary>查看候选配置</summary><pre>{JSON.stringify(item.proposal,null,2)}</pre></details><div className="todo-toolbar">{item.status==='candidate'&&<Button size="sm" variant="outline" onClick={()=>{setEvaluating(item);setSuite('v01-');setScenarios('');setRegressions('');setNotes('');setPassed(false);}}>登记 V01 对照评估</Button>}{item.status==='evaluated'&&item.evaluation?.passed&&<Button size="sm" disabled={busy} onClick={()=>void publishCandidate(item)}>发布此版本</Button>}{item.status==='published'&&<Button size="sm" variant="outline" onClick={()=>{setRollingBack(item);setRollbackVersion('');setRollbackReason('');}}>回退配置</Button>}</div></section>)}</div>{!candidates.length&&<p className="subtle">还没有能力改进候选。</p>}
   {evaluating&&<form className="peer-form" aria-label="登记 V01 对照评估" onSubmit={evaluateCandidate}><h4>评估 {evaluating.target_key} · {evaluating.version}</h4><label>评估套件版本<Input required value={suite} onChange={event=>setSuite(event.target.value)}/></label><label>场景 ID（逗号或空格分隔）<Input required value={scenarios} onChange={event=>setScenarios(event.target.value)}/></label><div className="todo-toolbar"><label>基线完成数<Input type="number" min={0} value={baselineCompleted} onChange={event=>setBaselineCompleted(Number(event.target.value))}/></label><label>候选完成数<Input type="number" min={0} value={candidateCompleted} onChange={event=>setCandidateCompleted(Number(event.target.value))}/></label><label>基线遗漏数<Input type="number" min={0} value={baselineOmissions} onChange={event=>setBaselineOmissions(Number(event.target.value))}/></label><label>候选遗漏数<Input type="number" min={0} value={candidateOmissions} onChange={event=>setCandidateOmissions(Number(event.target.value))}/></label></div><label>退步项（逗号或空格分隔）<Input value={regressions} onChange={event=>setRegressions(event.target.value)}/></label><label>评估说明<textarea maxLength={8192} rows={3} value={notes} onChange={event=>setNotes(event.target.value)}/></label><label><input type="checkbox" checked={passed} onChange={event=>setPassed(event.target.checked)}/>对照评估通过</label><div className="todo-toolbar"><Button disabled={busy} type="submit">保存评估结果</Button><Button type="button" variant="ghost" onClick={()=>setEvaluating(null)}>取消</Button></div></form>}
   {rollingBack&&<form className="peer-form" aria-label="回退能力配置" onSubmit={rollbackCandidate}><h4>回退 {rollingBack.target_key} · {rollingBack.version}</h4><label>恢复到已评估版本<Input required value={rollbackVersion} onChange={event=>setRollbackVersion(event.target.value)}/></label><label>回退原因<textarea required maxLength={4096} rows={3} value={rollbackReason} onChange={event=>setRollbackReason(event.target.value)}/></label><p className="subtle">回退只影响后续任务的配置选择，已经发生的工具操作和业务效果仍保留。</p><div className="todo-toolbar"><Button disabled={busy} type="submit">确认回退</Button><Button type="button" variant="ghost" onClick={()=>setRollingBack(null)}>取消</Button></div></form>}
  </section>}
 </section>;
}
