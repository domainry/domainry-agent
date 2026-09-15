import { useEffect, useState } from "react";
import { active, request, runPath, watchRun, type Run } from "./api";
import { liveStepText } from "./execution-state";
import { waiting } from "./interaction-state";
import { describeError, errorMessage } from "./errors";
import { InteractionCard } from "./InteractionCard";
import { ContextDiagnostic, ExecutionActivity } from "./ExecutionActivity";
import type { ToolView } from "./execution-state.ts";
import { executionOutcome } from "./execution-outcome.ts";
import { KnowledgeResponse } from "./KnowledgeSources";
import { runCitations } from "./knowledge-state";
import { auditEventLabel, durationLabel, usageItems } from "./run-detail-state";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";

const statuses: Record<Run["status"], string> = {
  queued: "等待处理", running: "正在处理", completed: "处理完成", failed: "处理失败", cancelled: "已停止",
  waiting_user: "等待补充信息", waiting_confirmation: "等待操作确认", needs_reconciliation: "结果待核查",
};

// Inspection owns its request and SSE subscription. Opening or refreshing does
// not mutate execution. Explicit recovery is delegated to the current page,
// which only resumes its latest Run and never replaces another active Run.
export function RunDialog({ conversationID, runID, onClose, onResume, onRepair, recoveryDisabled, repairDisabled,delegationID }: { conversationID: string; runID: string; onClose: () => void; onResume?: (run: Run) => void; onRepair?: (run: Run, step: number, call: ToolView, label: string) => void; recoveryDisabled?: boolean; repairDisabled?: boolean;delegationID?:string }) {
  const [run, setRun] = useState<Run | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    let stop = () => {};
    let poll: ReturnType<typeof setTimeout>|undefined;
    setLoading(true); setError(""); setRun(null);
    if(delegationID){
      const load=async()=>{
        try{const snapshot=await request<Run>(`/agent/delegations/${encodeURIComponent(delegationID)}/execution`,"POST",{conversation_id:conversationID,run_id:runID},controller.signal);if(!controller.signal.aborted){setRun(snapshot);setError("");}}
        catch(reason){if(!controller.signal.aborted){setRun(null);setError(describeError(reason));}}
        finally{if(!controller.signal.aborted){setLoading(false);poll=setTimeout(()=>void load(),2000);}}
      };
      void load();
      return()=>{controller.abort();clearTimeout(poll);};
    }
    request<Run>(runPath(conversationID, runID), "GET", undefined, controller.signal).then(snapshot => {
      if (controller.signal.aborted) return;
      setRun(snapshot);
      if (active(snapshot) || waiting(snapshot)) stop = watchRun(snapshot,
        value => { if (!controller.signal.aborted) { setRun(value); setError(""); } },
        () => {},
        reason => { if (!controller.signal.aborted) setError(describeError(reason)); },
      );
    }).catch(reason => { if (!controller.signal.aborted) setError(describeError(reason)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => { controller.abort(); stop(); };
  }, [conversationID, runID, refresh,delegationID]);

  return <Dialog open onOpenChange={open => { if (!open) onClose(); }}>
    <DialogContent className="run-dialog">
      <DialogHeader><DialogTitle>处理记录</DialogTitle><DialogDescription>查看这次请求的工具调用、实际结果和回复。记录中的业务状态是执行当时的状态。</DialogDescription></DialogHeader>
      <div className="run-dialog-toolbar"><span role="status">{loading ? "正在读取记录…" : run ? run.access_error ? "结果暂不可查看" : run.steps?.length ? executionOutcome(run).title : statuses[run.status] : "记录未加载"}</span><Button variant="outline" size="sm" disabled={loading} onClick={() => setRefresh(value => value + 1)}>刷新记录</Button></div>
      {error && <p role="alert" className="text-destructive text-sm">{error}</p>}
      {run && <>
        <dl className="run-metadata">
          {run.agent && <><dt>Agent</dt><dd>{run.agent.id} · 配置版本 {run.agent.revision}</dd></>}
          {run.agent?.owner_user_id && <><dt>配置所有者</dt><dd>{run.agent.owner_user_id}</dd></>}
          {run.agent?.execution_subject && <><dt>执行用户</dt><dd>{run.agent.execution_subject.user_id} · 工作空间 {run.agent.execution_subject.workspace_id}</dd></>}
          {run.correlation_id && <><dt>关联 ID</dt><dd><code>{run.correlation_id}</code></dd></>}
          {run.started_at && <><dt>开始时间</dt><dd>{new Date(run.started_at).toLocaleString()}</dd></>}
          {run.completed_at && <><dt>结束时间</dt><dd>{new Date(run.completed_at).toLocaleString()}</dd></>}
          <dt>排队耗时</dt><dd>{durationLabel(run.queue_duration_ms)}</dd>
          <dt>执行耗时</dt><dd>{durationLabel(run.duration_ms)}</dd>
          {run.model && <><dt>模型</dt><dd>{run.model}</dd></>}
          <dt>处理次数</dt><dd>{run.attempt}</dd>
          {run.metrics && <><dt>执行统计</dt><dd>{run.metrics.steps} 个步骤 · {run.metrics.model_calls} 次模型调用 · {run.metrics.tool_calls} 个工具调用（实际尝试 {run.metrics.tool_attempts} 次）</dd><dt>本运行工具并行</dt><dd>{run.metrics.parallel_tool_batches ? `${run.metrics.parallel_tool_calls} 个读取调用 · ${run.metrics.parallel_tool_batches} 批 · 峰值 ${run.metrics.peak_parallel_tools}` : "未使用"}</dd><dt>上下文管理</dt><dd>{run.metrics.peak_context_bytes ? `峰值 ${run.metrics.peak_context_bytes.toLocaleString()} / ${run.metrics.context_limit_bytes.toLocaleString()} 字节` : "未计量"}{run.metrics.context_compactions ? ` · ${run.metrics.context_compactions} 步压缩 · ${run.metrics.compacted_results} 个结果 · ${run.metrics.compacted_intervals} 个执行区间` : ""}{run.metrics.cache_read_input_tokens || run.metrics.cache_creation_input_tokens ? ` · 缓存命中 ${run.metrics.cache_read_input_tokens || 0} token · 新建 ${run.metrics.cache_creation_input_tokens || 0} token` : ""}</dd><dt>安全检查</dt><dd>{run.metrics.authorization_checks} 次授权检查 · {run.metrics.confirmation_decisions} 次确认决定</dd></>}
          <dt>模型用量</dt><dd>{usageItems(run.usage).length ? usageItems(run.usage).map(item => `${item.label} ${item.value}`).join(" · ") : "未报告"}</dd>
        </dl>
        {run.error_code && <p role="alert" className="text-destructive text-sm">{errorMessage(run.error_code)}</p>}
        {run.access_error && <p role="alert" className="text-destructive text-sm">{errorMessage(run.access_error)}</p>}
        {!run.access_error && run.context && !run.steps?.length && <ContextDiagnostic context={run.context} />}
        {!run.access_error && (run.steps?.length || ["failed", "cancelled"].includes(run.status) ? <ExecutionActivity run={run} executionID={delegationID} onResume={!delegationID&&onResume ? () => { onResume(run); onClose(); } : undefined} onRepair={!delegationID&&onRepair ? (step, call, label) => onRepair(run, step, call, label) : undefined} recoveryDisabled={recoveryDisabled} repairDisabled={repairDisabled} /> : <p className="subtle">这次处理没有已保存的工具调用记录。</p>)}
        {!run.access_error && run.interaction?.status === "pending" && <InteractionCard key={run.interaction.id + ":" + run.interaction.revision} interaction={run.interaction} onRun={setRun} onRefresh={() => setRefresh(value => value + 1)} />}
        {liveStepText(run) && <section aria-label={run.status === "completed" ? "已保存的回复" : "未完成的回复"}><p className="subtle">{run.status === "completed" ? "已保存的回复" : "未完成的回复"}</p><KnowledgeResponse text={liveStepText(run)} citations={runCitations(run)} /></section>}
        {!run.access_error && run.audit?.length ? <section className="run-audit" aria-label="运行审计"><h3>运行审计</h3><ol>{run.audit.map(event => <li key={event.seq}><div><strong>{auditEventLabel(event)}</strong><time dateTime={event.occurred_at}>{new Date(event.occurred_at).toLocaleString()}</time></div><small className="subtle">序号 {event.seq}{event.attempt ? ` · 尝试 ${event.attempt}` : ""}{event.authorization_revision ? ` · 授权版本 ${event.authorization_revision}` : ""}{event.actor_id ? ` · 操作人 ${event.actor_id}` : ""}{event.duration_ms !== undefined ? ` · ${durationLabel(event.duration_ms)}` : ""}{event.error_code ? ` · ${event.error_code}` : ""}</small></li>)}</ol>{run.audit_complete === false && <p className="subtle">这里只显示本次运行的部分审计记录；完整原始事件仍可按序读取。</p>}</section> : null}
      </>}
    </DialogContent>
  </Dialog>;
}
