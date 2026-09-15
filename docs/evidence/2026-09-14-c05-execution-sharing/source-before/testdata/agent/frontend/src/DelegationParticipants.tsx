import {useState} from 'react';
import {Button} from './components/ui/button';
import {Input} from './components/ui/input';
import {sessionUserID} from './session.ts';
import type {Delegation,DelegationParticipant} from './collaboration-state.ts';

export function DelegationParticipants({value:d,busy,onSave}:{value:Delegation;busy:boolean;onSave:(participants:DelegationParticipant[],revision:number,reason:string)=>Promise<boolean>}) {
 const canEdit=d.owner_user_id===sessionUserID()&&d.access?.manage&&d.access.share;
 const [draft,setDraft]=useState<DelegationParticipant[]|null>(null),[revision,setRevision]=useState(0),[reason,setReason]=useState('');
 const open=()=>{setDraft((d.participants||[]).map(p=>({user_id:p.user_id,operations:[...p.operations]})));setRevision(d.revision);setReason('');};
 async function save(e:React.FormEvent){e.preventDefault();if(draft&&await onSave(draft.map(p=>({user_id:p.user_id.trim(),operations:p.operations})),revision,reason.trim()))setDraft(null);}
 if(!canEdit&&!d.participants?.length)return null;
 return <section className="peer-dependencies" aria-label="委派参与人"><h4>这项委派的参与人</h4>
  {(d.participants||[]).length?<ul>{d.participants!.map(p=><li key={p.user_id}>{p.user_id} · {['查看约定',...(p.operations.includes('communicate')?['参与沟通']:[]),...(p.operations.includes('manage')?['管理委派']:[]),...(p.operations.includes('delivery_read')?['阅读交付']:[])].join('、')}</li>)}</ul>:<p className="subtle">尚未加入其他用户。</p>}
  {canEdit&&<Button variant="outline" onClick={open}>管理参与人</Button>}
  {draft&&canEdit&&<form className="peer-form" onSubmit={save}><p className="subtle">分别授权这项委派的查看、沟通、管理和交付阅读范围。实际执行用户及专业工具权限另行核对。</p>
   {draft.map((p,index)=><div className="todo-toolbar" key={index}><label>用户 ID<Input aria-label={`参与人 ${index+1} 用户 ID`} required maxLength={255} value={p.user_id} onChange={e=>setDraft(draft.map((v,i)=>i===index?{...v,user_id:e.target.value}:v))}/></label><label>参与范围<select aria-label={`参与人 ${index+1} 范围`} value={p.operations.includes('communicate')?'communicate':'view'} onChange={e=>setDraft(draft.map((v,i)=>i===index?{...v,operations:[...(e.target.value==='communicate'?['view','communicate'] as const:['view'] as const),...v.operations.filter(op=>op!=='view'&&op!=='communicate')]}:v))}><option value="view">查看约定</option><option value="communicate">查看约定、参与沟通</option></select></label><label><input type="checkbox" aria-label={`参与人 ${index+1} 管理委派`} checked={p.operations.includes('manage')} onChange={e=>setDraft(draft.map((v,i)=>i===index?{...v,operations:e.target.checked?[...v.operations,'manage']:v.operations.filter(op=>op!=='manage')}:v))}/>管理委派</label><label><input type="checkbox" aria-label={`参与人 ${index+1} 阅读交付`} checked={p.operations.includes('delivery_read')} onChange={e=>setDraft(draft.map((v,i)=>i===index?{...v,operations:e.target.checked?[...v.operations,'delivery_read']:v.operations.filter(op=>op!=='delivery_read')}:v))}/>阅读交付</label><Button type="button" variant="ghost" onClick={()=>setDraft(draft.filter((_,i)=>i!==index))}>移除参与人 {index+1}</Button></div>)}
   <Button type="button" variant="outline" disabled={draft.length>=64} onClick={()=>setDraft([...draft,{user_id:'',operations:['view']}])}>添加参与人</Button>
   <label>参与范围调整说明<textarea aria-label="参与范围调整说明" required maxLength={4096} rows={2} value={reason} onChange={e=>setReason(e.target.value)}/></label>
   <div className="todo-toolbar"><Button type="submit" disabled={busy}>保存参与范围</Button><Button type="button" variant="ghost" onClick={()=>setDraft(null)}>收起</Button></div>
  </form>}
 </section>;
}
