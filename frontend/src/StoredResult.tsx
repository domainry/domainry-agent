import { useEffect, useRef, useState } from "react";
import { request } from "./api.ts";
import type { ResultReference } from "./execution-state.ts";
import { describeError } from "./errors.ts";
import { readStoredResult, type ResultSlice } from "./stored-result.ts";
import { Button } from "./components/ui/button";
import { ArtifactPreview } from "./ArtifactPreview.tsx";
import { parseAnalysisResult } from "./analysis-result.ts";

export function StoredResult({reference}:{reference:ResultReference}) {
  const [text,setText] = useState(""), [busy,setBusy] = useState(false), [error,setError] = useState("");
  const active = useRef<AbortController|null>(null);
  const container = useRef<HTMLElement|null>(null);
  const key = JSON.stringify(reference);
  function clear() { active.current?.abort(); active.current=null; setText("");setError("");setBusy(false); }
  useEffect(()=>{
    clear();
    const hidden=()=>{if(document.hidden)clear();};
    const details=container.current?.closest("details");
    const closed=()=>{if(details&&!details.open)clear();};
    details?.addEventListener("toggle",closed);
    window.addEventListener("blur",clear);document.addEventListener("visibilitychange",hidden);
    return ()=>{active.current?.abort();details?.removeEventListener("toggle",closed);window.removeEventListener("blur",clear);document.removeEventListener("visibilitychange",hidden);};
  },[key]);
  async function load() {
    if(active.current)return;
    const abort=new AbortController();active.current=abort;setBusy(true);setError("");
    try {
      const path=`/agent/conversations/${encodeURIComponent(reference.conversation_id)}/runs/${encodeURIComponent(reference.run_id)}/result`;
      const result=await readStoredResult(reference,offset=>request<ResultSlice>(path,"POST",{reference,offset,max_bytes:8192},abort.signal),abort.signal);
      if(!abort.signal.aborted)setText(result);
    } catch(error){if(!abort.signal.aborted){setText("");setError(describeError(error));}}
    finally {if(active.current===abort){active.current=null;setBusy(false);}}
  }
  const analysis = text ? parseAnalysisResult(text) : null;
  return <section ref={container} className="stored-result">
    {text||busy?<Button type="button" variant="outline" size="sm" onClick={clear}>{busy?"取消读取":"收起完整结果"}</Button>:<Button type="button" variant="outline" size="sm" onClick={()=>void load()}>查看完整结果</Button>}
    {busy&&<p role="status">正在核对来源并读取完整结果…</p>}
    {error&&<p role="alert">{error}</p>}
    {text&&<><p className="subtle">已核对来源权限与内容完整性。关闭或切换窗口后需重新读取。</p>
      {analysis&&<section className="analysis-result" aria-label="结构化分析结果">
        <div className="todo-toolbar"><strong>结构化分析结果</strong><span>完整：{analysis.complete ? "是" : "否"}</span><span>截断：{analysis.truncated ? "是" : "否"}</span><span>{analysis.returnedRows} / 最多 {analysis.requestedMaxRows} 行</span></div>
        <ArtifactPreview content={analysis.content} />
        {analysis.chartOmittedReason&&<p className="subtle">未生成图表：{analysis.chartOmittedReason}</p>}
        <p className="subtle">来源：{analysis.references.map(reference => `${reference.label || reference.id}（${reference.kind}:${reference.id}${reference.subresource ? `/${reference.subresource}` : ""}${reference.version ? `@${reference.version}` : ""}）`).join("；")}</p>
        {analysis.missing.length>0&&<p className="subtle">缺失声明：{analysis.missing.map(item => `${item.column}/${item.code} × ${item.count}`).join("；")}</p>}
      </section>}
      <details><summary>查看原始结构化结果</summary><pre aria-label="完整工具结果">{text}</pre></details>
    </>}
  </section>;
}
