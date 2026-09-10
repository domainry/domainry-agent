import { useEffect, useState } from "react";
import { active, request, runPath, watchRun, type Run } from "./api";
import { liveStepText } from "./execution-state";
import { waiting } from "./interaction-state";
import { describeError, errorMessage } from "./errors";
import { ExecutionActivity } from "./ExecutionActivity";
import { KnowledgeResponse } from "./KnowledgeSources";
import { runCitations } from "./knowledge-state";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";

const statuses: Record<Run["status"], string> = {
  queued: "等待处理", running: "正在处理", completed: "处理完成", failed: "处理失败", cancelled: "已停止",
  waiting_user: "等待补充信息", waiting_confirmation: "等待操作确认", needs_reconciliation: "结果待核查",
};

// Inspection owns its request and SSE subscription. It never replaces the
// conversation's active run or starts, retries, or confirms an operation.
export function RunDialog({ conversationID, runID, onClose }: { conversationID: string; runID: string; onClose: () => void }) {
  const [run, setRun] = useState<Run | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    let stop = () => {};
    setLoading(true); setError(""); setRun(null);
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
  }, [conversationID, runID, refresh]);

  return <Dialog open onOpenChange={open => { if (!open) onClose(); }}>
    <DialogContent className="run-dialog">
      <DialogHeader><DialogTitle>处理记录</DialogTitle><DialogDescription>查看这次请求的工具调用、实际结果和回复。记录中的业务状态是执行当时的状态。</DialogDescription></DialogHeader>
      <div className="run-dialog-toolbar"><span role="status">{loading ? "正在读取记录…" : run ? statuses[run.status] : "记录未加载"}</span><Button variant="outline" size="sm" disabled={loading} onClick={() => setRefresh(value => value + 1)}>刷新记录</Button></div>
      {error && <p role="alert" className="text-destructive text-sm">{error}</p>}
      {run && <>
        <dl className="run-metadata">
          {run.created_at && <><dt>开始时间</dt><dd>{new Date(run.created_at).toLocaleString()}</dd></>}
          {run.updated_at && <><dt>更新时间</dt><dd>{new Date(run.updated_at).toLocaleString()}</dd></>}
          {run.model && <><dt>模型</dt><dd>{run.model}</dd></>}
          <dt>处理次数</dt><dd>{run.attempt}</dd>
        </dl>
        {run.error_code && <p role="alert" className="text-destructive text-sm">{errorMessage(run.error_code)}</p>}
        {run.access_error && <p role="alert" className="text-destructive text-sm">{errorMessage(run.access_error)}</p>}
        {!run.access_error && (run.steps?.some(step => step.calls.length) ? <ExecutionActivity run={run} /> : <p className="subtle">这次处理没有已保存的工具调用记录。</p>)}
        {!run.access_error && run.interaction?.status === "pending" && <div className="run-waiting"><strong>{statuses[run.status]}</strong><p>{run.interaction.question || "请回到对话中的操作卡片继续处理。"}</p><Button variant="outline" onClick={onClose}>回到对话</Button></div>}
        {liveStepText(run) && <section aria-label={run.status === "completed" ? "已保存的回复" : "未完成的回复"}><p className="subtle">{run.status === "completed" ? "已保存的回复" : "未完成的回复"}</p><KnowledgeResponse text={liveStepText(run)} citations={runCitations(run)} /></section>}
      </>}
    </DialogContent>
  </Dialog>;
}
