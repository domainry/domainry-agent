import { useEffect, useRef, useState } from 'react';
import { request } from './api.ts';
import { describeError } from './errors.ts';
import { Button } from './components/ui/button';
import { ArtifactPreview } from './ArtifactPreview.tsx';
import { downloadArtifact, type ArtifactExport, type ArtifactVersion } from './artifact-state.ts';
import type { ResultReference } from './execution-state.ts';
import type { DeliveryResultScope } from './StoredResult.tsx';

type Candidate = {id:string;version:number;title:string;export?:ArtifactExport};
function candidates(text:string):Candidate[] {
 try {
  const content=JSON.parse(text)?.content;
  const exported=content?.export;
  const values=content?.artifact?[content.artifact]:exported?[{id:exported.artifact_id,version:exported.version,title:exported.filename,export:exported}]:Array.isArray(content?.items)?content.items:[];
  return values.filter((v:Candidate)=>v&&typeof v.id==='string'&&typeof v.title==='string'&&Number.isSafeInteger(v.version)&&v.version>0).slice(0,50);
 } catch {return [];}
}

// The server verifies that the exact released artifact receipt identifies this
// version. These display references are not permissions or editable resources.
export function DeliveryArtifactResult({text,reference,scope}:{text:string;reference:ResultReference;scope:DeliveryResultScope}) {
 const [value,setValue]=useState<ArtifactVersion|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const active=useRef<AbortController|null>(null);
 const key=JSON.stringify([reference,scope]);
 const items=candidates(text);
 function clear(){active.current?.abort();active.current=null;setValue(null);setBusy(false);setError('');}
 useEffect(()=>{clear();return ()=>active.current?.abort();},[key]);
 async function read(item:Candidate,download=false){
  clear();const controller=new AbortController();active.current=controller;setBusy(true);
  const body={delivery_revision:scope.revision,reference,artifact_id:item.id,version:item.version};
  const path=`/agent/delegations/${encodeURIComponent(scope.id)}`;
  try {
   if(download&&item.export){await downloadArtifact(item.export,{path:path+'/delivery-export',body:{...body,export_id:item.export.id},signal:controller.signal});}
   else {
    const result=await request<ArtifactVersion>(path+'/delivery-artifact','POST',body,controller.signal);
    if(result.artifact.id!==item.id||result.artifact.version!==item.version)throw new Error('成果版本已变化，请重新读取交付。');
    if(!controller.signal.aborted)setValue(result);
   }
  }catch(e){if(!controller.signal.aborted){setValue(null);setError(describeError(e));}}
  finally{if(active.current===controller){active.current=null;setBusy(false);}}
 }
 if(!items.length)return null;
 return <section aria-label="交付成果版本">{items.map(item=><div className="todo-toolbar" key={`${item.id}:${item.version}`}><strong>{item.title}</strong><Button type="button" size="sm" variant="outline" disabled={busy} onClick={()=>void read(item)}>查看完整成果 · 版本 {item.version}</Button>{item.export&&<Button type="button" size="sm" variant="outline" disabled={busy} onClick={()=>void read(item,true)}>下载已导出的版本</Button>}</div>)}
 {busy&&<p role="status">正在核对成果来源…</p>}{(busy||value)&&<Button type="button" size="sm" variant="ghost" onClick={clear}>{busy?'取消读取成果':'收起成果正文'}</Button>}
 {error&&<p role="alert">{error}</p>}{value&&<section aria-label="完整交付成果"><p>{value.artifact.title} · 版本 {value.artifact.version}</p><ArtifactPreview content={value.content}/></section>}
 </section>;
}
