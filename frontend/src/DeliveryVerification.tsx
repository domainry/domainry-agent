import { useEffect, useRef, useState } from 'react';
import { StoredResult, type DeliveryResultScope } from './StoredResult.tsx';
import { request } from './api.ts';
import { describeError } from './errors.ts';
import { Button } from './components/ui/button';
import { Input } from './components/ui/input';
import { deliveryIsCurrent, peerPath, type CompletionRule, type ConditionAssessment, type Delegation, type DeliveryHistory, type DeliveryVerification, type TaskBrief } from './collaboration-state.ts';

const methods:Record<string,string>={program:'程序核对',recipient:'接收方自评',agent:'发起方 Agent 评估',user:'用户决定',pending:'尚未核对'};
const verdicts:Record<string,string>={met:'满足',unmet:'不满足',unknown:'尚未确定'};
const blockers:Record<string,string>={disagreements_pending:"结论分歧尚未解决或需按当前要求重新核对",unresolved_items:'交付还有未解决事项',completion_conditions_pending:'完成条件尚未全部核对通过',delivery_outdated:'核对对应旧要求',legacy_delivery_unverified:'旧交付尚无逐项核对记录'};

function SchemaField({label,value,onChange,required=false}:{label:string;value:unknown;onChange:(value:unknown)=>void;required?:boolean}) {
 const [raw,setRaw]=useState(value===undefined?'':JSON.stringify(value,null,2));
 function read(text:string){if(!text.trim()&&!required)return undefined;return JSON.parse(text);}
 return <label>{label}<textarea aria-label={label} required={required} maxLength={8192} rows={4} spellCheck={false} value={raw} onChange={e=>{setRaw(e.target.value);try{read(e.target.value);e.target.setCustomValidity('');}catch{e.target.setCustomValidity('请输入有效的 JSON Schema');}}} onBlur={e=>{try{onChange(read(e.target.value));}catch{e.target.reportValidity();}}}/></label>;
}

export function VerificationRuleFields({value,onChange}:{value:TaskBrief;onChange:(brief:TaskBrief)=>void}) {
 const rules=value.verification_rules||[];
 function update(condition:number,rule?:CompletionRule){onChange({...value,verification_rules:[...rules.filter(r=>r.condition!==condition),...(rule?[rule]:[])].sort((a,b)=>a.condition-b.condition)});}
 return <details className="peer-dependencies"><summary>完成条件的检查方式（可选）</summary><p>可由程序核对交付数据或原操作回执。其余条件由发起方 Agent 或用户逐项评估。修改条件文字后，请重新设置该项检查方式。</p>{value.completion_conditions.map((condition,index)=>{
  const rule=rules.find(r=>r.condition===index);
  return <fieldset key={index}><legend>第 {index+1} 项 · {condition||'尚未填写条件'}</legend><label>检查方式<select aria-label={`第 ${index+1} 项检查方式`} value={rule?.kind||'judgment'} onChange={e=>update(index,e.target.value==='judgment'?undefined:e.target.value==='data'?{condition:index,kind:'data',schema:{type:'object'}}:{condition:index,kind:'receipt',tool:'',completion:'completed',min_receipts:1})}><option value="judgment">Agent 或用户评估</option><option value="data">交付数据检查</option><option value="receipt">原操作回执检查</option></select></label>
  {rule?.kind==='data'&&<><p className="subtle">检查字段和值是否符合约定；实际业务事实仍需依据证据核对。</p><SchemaField key={`data-${index}`} label={`第 ${index+1} 项数据 JSON Schema`} required value={rule.schema} onChange={schema=>update(index,{...rule,schema})}/></>}
  {rule?.kind==='receipt'&&<><label>工具名称<Input aria-label={`第 ${index+1} 项工具名称`} required pattern="[a-zA-Z0-9_\-]{1,64}" value={rule.tool||''} onChange={e=>update(index,{...rule,tool:e.target.value})}/></label><label>所需回执数<Input aria-label={`第 ${index+1} 项回执数`} type="number" min={1} max={16} required value={rule.min_receipts||1} onChange={e=>update(index,{...rule,min_receipts:Number(e.target.value)})}/></label><label>操作状态<select aria-label={`第 ${index+1} 项操作状态`} value={rule.completion||'completed'} onChange={e=>update(index,{...rule,completion:e.target.value as 'completed'|'accepted'})}><option value="completed">业务操作已完成</option><option value="accepted">已受理即可（提交类条件）</option></select></label><SchemaField key={`arguments-${index}`} label={`第 ${index+1} 项参数 JSON Schema（可选）`} value={rule.arguments_schema} onChange={arguments_schema=>update(index,{...rule,arguments_schema})}/><SchemaField key={`result-${index}`} label={`第 ${index+1} 项结果 JSON Schema（可选）`} value={rule.result_schema} onChange={result_schema=>update(index,{...rule,result_schema})}/></>}
  </fieldset>;
 })}{rules.filter(rule=>rule.condition>=value.completion_conditions.length).map(rule=><p role="alert" key={rule.condition}>第 {rule.condition+1} 项条件已删除，请处理其检查规则。<Button variant="ghost" type="button" onClick={()=>update(rule.condition)}>移除该规则</Button></p>)}</details>;
}


export function DeliveryReviewFields({value:d,conditions,onChange}:{value:Delegation;conditions:ConditionAssessment[];onChange:(value:ConditionAssessment[])=>void}) {
 return <fieldset className="peer-dependencies"><legend>逐项核对完成条件</legend><p>本次核对绑定当前交付和要求版本。接收方自评保留为参考；程序检查结果不能用评估意见覆盖。</p>{conditions.map(entry=>{
  const rule=d.brief.verification_rules?.find(r=>r.condition===entry.condition);
  const check=d.verification?.checks.find(c=>c.condition===entry.condition);
  return <section key={entry.condition}><strong>第 {entry.condition+1} 项 · {d.brief.completion_conditions[entry.condition]}</strong>{rule?<p>程序核对：{check?verdicts[check.verdict]:'等待核对'} · {check?.basis||'提交后读取证据重新核对'}</p>:<><label>核对结果<select aria-label={`第 ${entry.condition+1} 项核对结果`} value={entry.verdict} onChange={e=>onChange(conditions.map(c=>c.condition===entry.condition?{...c,verdict:e.target.value as ConditionAssessment['verdict']}:c))}><option value="unknown">尚未确定</option><option value="met">满足</option><option value="unmet">不满足</option></select></label><label>核对依据<textarea aria-label={`第 ${entry.condition+1} 项核对依据`} rows={3} maxLength={4096} required={entry.verdict!=='unknown'} value={entry.basis} onChange={e=>onChange(conditions.map(c=>c.condition===entry.condition?{...c,basis:e.target.value}:c))}/></label></>}</section>;
 })}</fieldset>;
}

function VerificationChecks({value,onRun,delivery}:{value:DeliveryVerification;onRun?: (c:string,r:string)=>void;delivery?:DeliveryResultScope}) {
 return <><ol aria-label="完成条件核对记录">{value.checks.map(check=><li key={check.condition}><strong>{check.condition+1}. {check.requirement} · {verdicts[check.verdict]}</strong><p>{methods[check.method]||check.method} · {check.basis}</p>{check.receipts?.map((ref,index)=><div key={`${ref.run_id}:${ref.step}:${ref.call_id}`}>{delivery&&<StoredResult reference={ref} delivery={delivery} label={`查看第 ${check.condition+1} 项原回执 ${index+1}`}/>} {onRun&&<Button variant="ghost" onClick={()=>onRun(ref.conversation_id,ref.run_id)}>查看该回执的执行过程</Button>}</div>)}</li>)}</ol>{!!value.blockers.length&&<p>{value.blockers.map(b=>blockers[b]||b).join('；')}</p>}{value.checked_at&&!value.checked_at.startsWith('0001-')&&<small>核对于 {new Date(value.checked_at).toLocaleString()} · {value.agent_id||value.actor_id}</small>}{onRun&&value.source&&<Button variant="ghost" onClick={()=>onRun(value.source!.conversation_id,value.source!.run_id)}>查看评估执行</Button>}</>;
}

export function DeliveryVerificationSection({value:d,onRun}:{value:Delegation;onRun?: (c:string,r:string)=>void}) {
 const [history,setHistory]=useState<DeliveryHistory|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const pending=useRef<AbortController|null>(null);
 function clearHistory(){pending.current?.abort();pending.current=null;setHistory(null);setBusy(false);setError('');}
 useEffect(()=>{clearHistory();window.addEventListener('blur',clearHistory);return ()=>{pending.current?.abort();window.removeEventListener('blur',clearHistory);};},[d.id,d.revision]);
 async function load(before?:number){
  pending.current?.abort();const controller=new AbortController();pending.current=controller;setBusy(true);setError('');
  try{const next=await request<DeliveryHistory>(`${peerPath(d.id)}/deliveries${before?`?before_revision=${before}`:''}`,'GET',undefined,controller.signal);if(!controller.signal.aborted)setHistory(previous=>before&&previous?{...next,items:[...previous.items,...next.items]}:next);}
  catch(e){if(!controller.signal.aborted){setHistory(null);setError(describeError(e));}}
  finally{if(pending.current===controller){pending.current=null;setBusy(false);}}
 }
 return <section className="peer-dependencies" aria-label="交付完成条件核对"><h4>完成条件核对</h4>{d.verification&&d.delivery?<><p>{!deliveryIsCurrent(d)?'以下核对属于旧要求':d.status==='accepted_delivery'?'以下为验收时的逐项核对记录':d.verification.ready?'条件已核对通过，交付仍需明确验收':'条件尚未全部核对通过'}</p><VerificationChecks value={d.verification} onRun={onRun} delivery={{id:d.id,revision:0}}/></>:<p>提交交付后，按当前约定逐项核对。</p>}<div className="todo-toolbar"><Button variant="ghost" disabled={busy} onClick={()=>void load()}>查看交付与验收历史</Button>{history&&<Button variant="ghost" onClick={clearHistory}>收起交付历史</Button>}</div>{history&&<ol className="peer-messages" aria-label="交付与验收历史">{history.items.map(entry=><li key={entry.revision}><details><summary>{({deliver:'提交交付',review_delivery:'核对交付',accept_delivery:'验收通过',legacy:'原有交付'} as Record<string,string>)[entry.kind]||entry.kind} · 需求 v{entry.delivery.brief_version} / 约定 {entry.delivery.agreement_revision||1} · {entry.reason}</summary><p>{entry.delivery.summary}</p>{entry.delivery.data!==undefined&&<pre>{JSON.stringify(entry.delivery.data,null,2)}</pre>}<VerificationChecks value={entry.verification} onRun={onRun} delivery={{id:d.id,revision:entry.revision}}/></details></li>)}</ol>}{history&&!history.complete&&<Button variant="outline" disabled={busy} onClick={()=>void load(history.next_before)}>更早的交付</Button>}{error&&<p role="alert">{error}</p>}</section>;
}
