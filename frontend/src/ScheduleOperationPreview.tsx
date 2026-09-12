import { useEffect } from "react";

type Value = Record<string, unknown>;

function parse(tool: string, text: string): { value?: Value; valid: boolean } {
  try {
    const value = JSON.parse(text) as Value;
    if (!value || typeof value !== "object" || Array.isArray(value)) return { valid: false };
    const revision = Number(value.expected_revision);
    if (["schedule_update", "schedule_pause", "schedule_resume", "schedule_delete"].includes(tool) && (typeof value.plan_id !== "string" || !value.plan_id || !Number.isInteger(revision) || revision < 1)) return { value, valid: false };
    if (tool === "schedule_create" && (!["background_task", "reminder"].includes(String(value.kind)) || typeof value.name !== "string" || !value.name.trim() || !value.trigger || !value.details)) return { value, valid: false };
    return { value, valid: true };
  } catch { return { valid: false }; }
}

export function ScheduleOperationPreview({ tool, argumentsText, onReady }: { tool: string; argumentsText: string; onReady: (ready: boolean) => void }) {
  const parsed = parse(tool, argumentsText);
  useEffect(() => onReady(parsed.valid), [parsed.valid, onReady]);
  if (!parsed.value) return <p role="alert">计划参数不完整，请停止后重新指定。</p>;
  const value = parsed.value;
  const labels: Record<string, string> = { schedule_create: "创建计划", schedule_update: "修改计划", schedule_pause: "暂停计划", schedule_resume: "恢复计划", schedule_delete: "删除计划" };
  return <section className="todo-operation"><strong>{labels[tool] || tool}</strong><dl><dt>计划</dt><dd>{String(value.name || value.plan_id || "—")}</dd>{value.expected_revision !== undefined && <><dt>当前版本</dt><dd>{String(value.expected_revision)}</dd></>}{value.kind !== undefined && <><dt>类型</dt><dd>{value.kind === "reminder" ? "提醒" : "后台任务"}</dd></>}<dt>完整参数</dt><dd><pre>{JSON.stringify(value, null, 2)}</pre></dd></dl>{!parsed.valid && <p role="alert">计划参数不完整，请停止后重新指定。</p>}</section>;
}
