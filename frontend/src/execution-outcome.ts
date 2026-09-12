import type { Run } from "./api.ts";
import type { ToolView } from "./execution-state.ts";
import { resumable } from "./interaction-state.ts";

export type ResultKind = "completed" | "accepted" | "failed" | "unconfirmed" | "unstarted" | "interrupted" | "waiting" | "active";
export type RecoveryTarget = { kind: "resume" | "reconcile"; step?: number; callID?: string };
const terminal = (run: Run) => ["completed", "failed", "cancelled"].includes(run.status);

export function resultKind(run: Run, call: ToolView): ResultKind {
  if (call.status === "completed") return call.completion === "accepted" ? "accepted" : "completed";
  if (call.status === "pending") return "accepted";
  if (call.status === "failed") return "failed";
  if (["uncertain", "needs_reconciliation"].includes(call.status)) return "unconfirmed";
  if (call.status === "interrupted" || terminal(run) && call.status === "running") return call.effect === "read" ? "interrupted" : "unconfirmed";
  if (["not_started", "queued"].includes(call.status) || terminal(run) && ["receiving", "waiting_user", "waiting_confirmation"].includes(call.status)) return "unstarted";
  if (["waiting_user", "waiting_confirmation"].includes(call.status)) return "waiting";
  if (terminal(run)) return "unconfirmed";
  return "active";
}

// This is a presentation of persisted results, not a claim that the model met
// every business goal. A saved assistant reply can contain failed tool calls.
export function executionOutcome(run: Run) {
  const counts: Record<ResultKind, number> = { completed: 0, accepted: 0, failed: 0, unconfirmed: 0, unstarted: 0, interrupted: 0, waiting: 0, active: 0 };
  if (run.access_error) return { title: "结果暂不可查看", counts, partial: false };
  for (const step of run.steps || []) for (const call of step.calls) counts[resultKind(run, call)]++;
  const success = counts.completed + counts.accepted;
  const incomplete = counts.failed + counts.unconfirmed + counts.unstarted + counts.interrupted;
  const partial = success > 0 && (incomplete > 0 || ["failed", "cancelled"].includes(run.status));
  let title = "处理中";
  if (run.status === "waiting_confirmation") title = "等待操作确认";
  else if (run.status === "waiting_user") title = "等待补充信息";
  else if (run.status === "needs_reconciliation" || counts.unconfirmed && terminal(run)) title = partial ? "部分完成，仍有结果待核查" : "结果待核查";
  else if (run.status === "cancelled") title = partial ? "部分完成，后续已停止" : "已停止";
  else if (run.status === "failed") title = partial ? "部分完成，后续处理失败" : "处理失败";
  else if (run.status === "completed") title = incomplete ? partial ? "部分完成" : "工具执行失败" : counts.accepted ? "调用已结束，含已受理请求" : counts.completed ? "工具调用已完成" : "回复已完成";
  return { title, counts, partial };
}

// Resume stays attached to the original Run. The backend replays receipts and
// its first unfinished step; the UI never dispatches a tool or changes its key.
export function recoveryTarget(run: Run): RecoveryTarget | null {
  if (run.access_error || !resumable(run)) return null;
  if (run.interaction?.kind === "reconciliation" && ["pending", "cancelled"].includes(run.interaction.status)) return { kind: "reconcile", step: run.interaction.step, callID: run.interaction.call_id };
  const steps = [...(run.steps || [])].sort((a, b) => a.number - b.number);
  for (const step of steps) {
    if (["generating", "interrupted"].includes(step.status)) return { kind: "resume", step: step.number };
    for (const call of step.calls) {
      const kind = resultKind(run, call);
      if (kind === "unconfirmed") return { kind: "reconcile", step: step.number, callID: call.id };
      if (["unstarted", "interrupted", "active", "waiting"].includes(kind)) return { kind: "resume", step: step.number, callID: call.id };
    }
  }
  return { kind: "resume" };
}

export function repairRequest(run: Run, step: number, call: ToolView, label: string): string {
  return `请修复这次处理第 ${step + 1} 步「${label}」的失败项。先读取保存的失败结果并核对当前权限，只处理这一项，保留已经完成的操作。\n来源会话：${run.conversation_id}；来源处理记录：${run.id}；步骤：${step}；调用：${call.id}。`;
}
