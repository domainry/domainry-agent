import { useEffect, useState } from "react";
import { request } from "./api.ts";
import { describeError } from "./errors.ts";
import { Button } from "./components/ui/button";
import { Textarea } from "./components/ui/textarea";
import {
  taskCompletionReviewPath, taskCompletionsPath,
  type ConversationConditionAssessment, type ConversationTaskCompletionHistory,
  type ConversationTaskCompletionRecord, type ConversationTaskDetail,
} from "./task-state.ts";

const methods: Record<string, string> = { program: "程序核对", agent: "Agent 评估", user: "用户决定", recipient: "执行 Agent 自评", pending: "尚未核对" };
const verdicts: Record<string, string> = { met: "满足", unmet: "不满足", unknown: "尚未确定" };
const kinds: Record<string, string> = { agent_assessment: "Agent 提交", agent_review: "Agent 复核", user_review: "用户复核", execution_end: "执行结束但未提交", legacy_response: "旧任务响应检查" };
const blockers: Record<string, string> = {
  completion_submission_missing: "执行已结束，但 Agent 没有提交逐项验收单",
  completion_conditions_pending: "仍有完成条件未通过",
  completion_outdated: "核对对应旧任务约定",
};

function initialReview(task: ConversationTaskDetail): ConversationConditionAssessment[] {
  return (task.brief?.completion_conditions || []).map((_, condition) => {
    const check = task.completion?.verification.checks.find(item => item.condition === condition);
    return { condition, verdict: check?.verdict || "unknown", basis: check?.basis || "", receipts: check?.receipts || [] };
  });
}

function CompletionChecks({ record, onRun }: { record: ConversationTaskCompletionRecord; onRun: (conversationID: string, runID: string) => void }) {
  return <>
    <ol aria-label="任务完成条件核对记录">{record.verification.checks.map(check => <li key={check.condition}>
      <strong>{check.condition + 1}. {check.requirement} · {verdicts[check.verdict] || check.verdict}</strong>
      <p>{methods[check.method] || check.method} · {check.basis}</p>
      {(check.receipts || []).map((receipt, index) => <Button key={`${receipt.run_id}:${receipt.step}:${receipt.call_id}`} type="button" size="sm" variant="ghost" onClick={() => onRun(receipt.conversation_id, receipt.run_id)}>查看原回执 {index + 1}</Button>)}
    </li>)}</ol>
    {!!record.verification.blockers.length && <p className="task-blocker">{record.verification.blockers.map(item => blockers[item] || item).join("；")}</p>}
    {record.verification.source && <Button type="button" size="sm" variant="ghost" onClick={() => onRun(record.verification.source!.conversation_id, record.verification.source!.run_id)}>查看本次评估运行</Button>}
  </>;
}

export function TaskCompletion({ task, acting, setActing, onUpdated, onError, onRun, onArtifact }: {
  task: ConversationTaskDetail; acting: boolean; setActing: (value: boolean) => void;
  onUpdated: (value: ConversationTaskDetail) => void; onError: (message: string) => void;
  onRun: (conversationID: string, runID: string) => void; onArtifact: (id: string, version: number) => void;
}) {
  const [reviewing, setReviewing] = useState(false), [reason, setReason] = useState("");
  const [conditions, setConditions] = useState<ConversationConditionAssessment[]>(() => initialReview(task));
  const [history, setHistory] = useState<ConversationTaskCompletionHistory | null>(null), [historyBusy, setHistoryBusy] = useState(false);
  useEffect(() => { setReviewing(false); setReason(""); setConditions(initialReview(task)); setHistory(null); }, [task.id, task.completion?.revision]);
  if (task.completion_mode !== "assessed" && !task.completion) return null;

  async function review() {
    if (!task.completion || acting || !reason.trim()) return;
    setActing(true); onError("");
    try {
      const value = await request<ConversationTaskDetail>(taskCompletionReviewPath(task.id), "POST", {
        client_id: crypto.randomUUID(), expected_revision: task.completion.revision, reason: reason.trim(),
        review: { delivery_digest: task.completion.verification.delivery_digest, conditions },
      });
      onUpdated(value); setReviewing(false); setReason("");
    } catch (failure) {
      onError(describeError(failure));
    } finally {
      setActing(false);
    }
  }

  async function loadHistory(before = 0) {
    setHistoryBusy(true); onError("");
    try {
      const value = await request<ConversationTaskCompletionHistory>(taskCompletionsPath(task.id, before), "GET");
      setHistory(current => before && current ? { ...value, items: [...current.items, ...value.items] } : value);
    } catch (failure) {
      onError(describeError(failure));
    } finally {
      setHistoryBusy(false);
    }
  }

  const current = task.completion;
  const program = new Set((task.brief?.verification_rules || []).map(rule => rule.condition));
  return <section className="task-agreement" aria-label="任务完成验收">
    <header><div><strong>完成验收</strong><small>{current ? `记录 v${current.revision} · ${kinds[current.kind] || current.kind}` : "等待 Agent 提交"}</small></div>{current && <span className={`goal-status ${current.verification.ready ? "completed" : "blocked"}`}>{current.verification.ready ? "条件已通过" : "需要处理"}</span>}</header>
    {!current ? <p className="subtle">Agent 需要在结束运行前逐项提交完成条件、依据和成果版本。</p> : <>
      <p>{current.submission.summary}</p>
      {current.submission.agreement_revision !== task.agreement_revision && <p className="task-blocker">这份核对对应约定修订 {current.submission.agreement_revision}，当前为 {task.agreement_revision}。</p>}
      {current.submission.data !== undefined && <details><summary>查看结构化验收数据</summary><pre>{JSON.stringify(current.submission.data, null, 2)}</pre></details>}
      <CompletionChecks record={current} onRun={onRun} />
      {!!current.submission.artifacts.length && <div className="task-artifacts"><strong>验收引用的成果版本</strong><div className="artifact-list">{current.submission.artifacts.map(ref => <Button type="button" variant="outline" key={`${ref.id}:${ref.version}`} onClick={() => onArtifact(ref.id, ref.version)}>{ref.id} · v{ref.version}</Button>)}</div></div>}
      {current.submission.source && <Button type="button" size="sm" variant="ghost" onClick={() => onRun(current.submission.source!.conversation_id, current.submission.source!.run_id)}>查看提交运行</Button>}
      {task.status === "awaiting_review" && task.completion_mode === "assessed" && !reviewing && <Button type="button" variant="outline" disabled={acting} onClick={() => setReviewing(true)}>逐项复核</Button>}
      {reviewing && <form className="task-agreement-form" onSubmit={event => { event.preventDefault(); void review(); }}>
        <p className="subtle">本次决定绑定当前验收记录和约定版本。程序核对项只能通过原数据或回执改变。</p>
        {conditions.map(entry => {
          const check = current.verification.checks.find(item => item.condition === entry.condition);
          return <fieldset key={entry.condition}><legend>第 {entry.condition + 1} 项 · {task.brief?.completion_conditions[entry.condition]}</legend>
            {program.has(entry.condition) ? <p>程序核对：{verdicts[check?.verdict || "unknown"]} · {check?.basis}</p> : <>
              <label>核对结果<select aria-label={`任务第 ${entry.condition + 1} 项核对结果`} value={entry.verdict} onChange={event => setConditions(values => values.map(item => item.condition === entry.condition ? { ...item, verdict: event.target.value as ConversationConditionAssessment["verdict"] } : item))}><option value="unknown">尚未确定</option><option value="met">满足</option><option value="unmet">不满足</option></select></label>
              <label>核对依据<Textarea aria-label={`任务第 ${entry.condition + 1} 项核对依据`} required={entry.verdict !== "unknown"} maxLength={4096} value={entry.basis} onChange={event => setConditions(values => values.map(item => item.condition === entry.condition ? { ...item, basis: event.target.value } : item))} /></label>
            </>}
          </fieldset>;
        })}
        <label>复核说明<Textarea aria-label="任务复核说明" required maxLength={4096} value={reason} onChange={event => setReason(event.target.value)} /></label>
        <div className="todo-toolbar"><Button type="submit" disabled={acting || !reason.trim()}>保存复核结果</Button><Button type="button" variant="ghost" disabled={acting} onClick={() => setReviewing(false)}>取消</Button></div>
      </form>}
    </>}
    <div className="todo-toolbar"><Button type="button" size="sm" variant="ghost" disabled={historyBusy} onClick={() => void loadHistory()}>查看验收历史</Button>{history && <Button type="button" size="sm" variant="ghost" onClick={() => setHistory(null)}>收起历史</Button>}</div>
    {history && <ol className="peer-messages" aria-label="任务验收历史">{history.items.map(item => <li key={item.revision}><details><summary>v{item.revision} · {kinds[item.kind] || item.kind} · {new Date(item.recorded_at).toLocaleString()}</summary><p>{item.submission.summary}</p><CompletionChecks record={item} onRun={onRun} /></details></li>)}</ol>}
    {history && !history.complete && <Button type="button" variant="outline" disabled={historyBusy} onClick={() => void loadHistory(history.next_before || 0)}>更早的验收记录</Button>}
  </section>;
}
