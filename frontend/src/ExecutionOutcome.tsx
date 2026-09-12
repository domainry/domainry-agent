import type { Run } from "./api.ts";
import { executionOutcome, type RecoveryTarget } from "./execution-outcome.ts";
import { errorMessage } from "./errors.ts";
import { Button } from "./components/ui/button";

export function RecoveryButton({ target, disabled, onResume }: { target: RecoveryTarget; disabled?: boolean; onResume: () => void }) {
  return <Button type="button" variant="outline" size="sm" disabled={disabled} data-recovery-kind={target.kind} onClick={onResume}>
    {target.kind === "reconcile" ? "核查此操作并继续" : target.step !== undefined ? `从第 ${target.step + 1} 步继续` : "继续未完成处理"}
  </Button>;
}

export function ExecutionOutcome({ run }: { run: Run }) {
  const outcome = executionOutcome(run), counts = outcome.counts;
  if (run.access_error) return null;
  const labels = { completed: "已完成", accepted: "已受理", failed: "失败", unconfirmed: "待核查", unstarted: "未执行", interrupted: "已中断", waiting: "等待确认或补充", active: "处理中" } as const;
  return <section className="rounded-lg border p-3 space-y-2" aria-label="本次实际结果" data-outcome={outcome.partial ? "partial" : run.status}>
    <strong>{outcome.title}</strong>
    <p className="subtle">{Object.entries(counts).filter(([, count]) => count).map(([kind, count]) => `${labels[kind as keyof typeof labels]} ${count} 项`).join(" · ") || "本次没有已保存的工具结果。"}</p>
    {!!counts.accepted && <p className="subtle">已受理表示请求已提交，业务进度和最终结果以对应的查询记录为准。</p>}
    {!!counts.unconfirmed && <p className="subtle">结果尚未确认，不能按失败重新发起相同操作。继续时沿用原记录核查或复用幂等回执。</p>}
    {outcome.partial && <p className="subtle">已完成或已受理的操作会保留。继续原运行会复用已保存的回执。</p>}
    {run.status === "completed" && counts.failed > 0 && <p className="subtle">回复已保存，但上方计数包含确定失败的调用。展开对应步骤可准备新的修复请求。</p>}
    <small className="subtle">按已保存的工具调用统计；业务是否完成，以具体业务结果为准。</small>
    {run.error_code && <p role="alert">{errorMessage(run.error_code)}</p>}
  </section>;
}
