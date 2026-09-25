import { sessionUserID } from './session.ts';
import { formatLocalDateTime } from './time.ts';
import { useState } from 'react';
import { request } from './api.ts';
import { describeError } from './errors.ts';
import { Button } from './components/ui/button';
import { briefFieldLabel, peerPath, type AgreementHistory, type Delegation, type DependencyInput, type TaskBrief } from './collaboration-state.ts';

export function DependencyEditor({ items, value, onChange }: { items: Delegation[]; value: DependencyInput[]; onChange: (value:DependencyInput[])=>void }) {
 const fields = Object.keys(briefFieldLabel).filter(key=>key!=='dependencies'&&key!=='assignment');
 return <fieldset className="peer-dependencies"><legend>依赖哪些任务的要求</legend><p className="subtle">所选要求变化时，这项任务会暂停并收到通知。未勾选具体字段时跟随全部要求。</p>{items.map(d=>{
  const edge=value.find(e=>e.delegation_id===d.id);
  return <section key={d.id}><label><input type="checkbox" checked={!!edge} onChange={e=>onChange(e.target.checked?[...value,{delegation_id:d.id,brief_version:d.brief.version,agreement_revision:d.agreement_revision||1,fields:[]}]:value.filter(v=>v.delegation_id!==d.id))}/>{d.brief.goal} · 需求 v{d.brief.version} · 约定 {d.agreement_revision||1}</label>{edge&&<div className="peer-field-options">{fields.map(field=><label key={field}><input type="checkbox" checked={edge.fields?.includes(field)||false} onChange={e=>onChange(value.map(v=>v.delegation_id===d.id?{...v,brief_version:d.brief.version,agreement_revision:d.agreement_revision||1,fields:e.target.checked?[...(v.fields||[]),field]:(v.fields||[]).filter(f=>f!==field)}:v))}/>{briefFieldLabel[field]}</label>)}</div>}</section>;
 })}{!items.length&&<p className="subtle">当前工作目标下还没有可选任务。</p>}</fieldset>;
}
function RequirementsValues({value}:{value:Partial<TaskBrief>}) {
 return <dl className="run-metadata">{Object.entries(value).filter(([key])=>key!=='version').map(([key,text])=><div className="peer-definition" key={key}><dt>{briefFieldLabel[key]||key}</dt><dd>{Array.isArray(text)?text.map(v=>typeof v==='object'?JSON.stringify(v):v).join('；')||'未填写':typeof text==='object'&&text!==null?JSON.stringify(text):String(text||'未填写')}</dd></div>)}</dl>;
}
export function RequirementDetails({value:d,items}:{value:Delegation;items:Delegation[]}) {
 const [history,setHistory]=useState<AgreementHistory|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const title=(id:string)=>items.find(item=>item.id===id)?.brief.goal||id;
 async function load(before?:number){setBusy(true);try{const page=await request<AgreementHistory>(`${peerPath(d.id)}/requirements${before?`?before_revision=${before}`:''}`);setHistory(current=>({...page,items:before?[...(current?.items||[]),...page.items]:page.items}));setError('');}catch(e){setError(describeError(e));}finally{setBusy(false);}}
 return <section className="peer-requirements" aria-label="要求版本与依赖"><h4>要求版本与依赖</h4><p>当前约定 {d.agreement_revision||1} · {d.adopted_agreement_revision?`执行已采用约定 ${d.adopted_agreement_revision}`:'执行尚未采用'}{d.adopted_at?` · ${formatLocalDateTime(d.adopted_at)}`:''}</p>
 {!!d.pending_changes?.length&&<div className="task-waiting"><strong>需要核对新要求</strong><ul>{d.pending_changes.map(change=><li key={change.id}>{title(change.source_delegation_id)} · 需求 v{change.source_brief_version} / 约定 {change.source_agreement_revision} · {change.changed_fields.map(field=>briefFieldLabel[field]||field).join('、')}{change.via_delegation_id&&change.via_delegation_id!==change.source_delegation_id?`，经由「${title(change.via_delegation_id)}」影响本任务`:''}</li>)}</ul><p>旧执行已停止，历史交付保留。核对依赖后可继续执行。</p></div>}
 {(d.dependencies||[]).map(edge=>{const state=d.dependency_states?.find(s=>s.delegation_id===edge.delegation_id);const latest=items.find(item=>item.id===edge.delegation_id);const fields=edge.fields?.length?edge.fields:Object.keys(briefFieldLabel).filter(k=>k!=='dependencies');return <details key={edge.delegation_id}><summary>{title(edge.delegation_id)} · 使用需求 v{edge.brief_version} / 约定 {edge.agreement_revision||1} · 当前 v{state?.current_brief_version||edge.brief_version} / 约定 {state?.current_agreement_revision||edge.agreement_revision||1}{state?.state==='needs_review'?' · 上游尚未核对':state?.state==='changed'?' · 要求已变化':''}</summary><p>关注：{edge.fields?.length?edge.fields.map(f=>briefFieldLabel[f]||f).join('、'):'全部要求'}</p><strong>本任务采用的要求</strong><RequirementsValues value={edge.values}/>{latest&&<><strong>上游当前要求</strong><RequirementsValues value={Object.fromEntries(Object.entries({...latest.brief,input:latest.structured_input}).filter(([k])=>fields.includes(k)))}/></>}</details>;})}
 {d.structured_input&&<details><summary>当前结构化输入</summary><pre>{JSON.stringify(d.structured_input.data,null,2)}</pre>{d.structured_input.schema!==undefined&&<details><summary>输入格式约定</summary><pre>{JSON.stringify(d.structured_input.schema,null,2)}</pre></details>}</details>}
 {(!d.owner_user_id||d.owner_user_id===sessionUserID())&&<div className="todo-toolbar"><Button variant="ghost" disabled={busy} onClick={()=>void load()}>查看要求历史</Button>{history&&<Button variant="ghost" onClick={()=>setHistory(null)}>收起历史</Button>}</div>}
 {history&&<ol className="peer-messages" aria-label="要求历史">{history.items.map(entry=><li key={entry.revision}><details><summary>约定 {entry.revision} · 需求 v{entry.brief.version} · {entry.reason}</summary><p>{entry.from_user_id?'用户':entry.from_agent_id||'历史记录'} · {formatLocalDateTime(entry.created_at)}</p><RequirementsValues value={entry.brief}/>{entry.structured_input&&<><strong>结构化输入快照</strong><pre>{JSON.stringify(entry.structured_input,null,2)}</pre></>}{entry.dependencies?.map(edge=><p key={edge.delegation_id}>依赖 {title(edge.delegation_id)} · 需求 v{edge.brief_version} / 约定 {edge.agreement_revision||1}</p>)}</details></li>)}</ol>}
 {history&&!history.complete&&<Button variant="outline" disabled={busy} onClick={()=>void load(history.next_before)}>更早的要求</Button>}{error&&<p role="alert" className="text-destructive">{error}</p>}
 </section>;
}
