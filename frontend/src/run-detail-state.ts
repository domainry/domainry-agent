import type { RunAuditEvent } from "./api.ts";

const number = (value: unknown) => typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined;

export function durationLabel(milliseconds?: number) {
  if (milliseconds === undefined || !Number.isFinite(milliseconds) || milliseconds < 0) return "未报告";
  if (milliseconds < 1) return "小于 1 毫秒";
  if (milliseconds < 1000) return `${Math.round(milliseconds)} 毫秒`;
  if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 2 : 1)} 秒`;
  const minutes = Math.floor(milliseconds / 60_000);
  const seconds = Math.round((milliseconds % 60_000) / 1000);
  return seconds ? `${minutes} 分 ${seconds} 秒` : `${minutes} 分钟`;
}

export function usageItems(usage?: Record<string, unknown>) {
  if (!usage) return [];
  const input = number(usage.input_tokens) ?? number(usage.prompt_tokens);
  const output = number(usage.output_tokens) ?? number(usage.completion_tokens);
  const total = number(usage.total_tokens) ?? (input !== undefined && output !== undefined ? input + output : undefined);
  return [
    input === undefined ? undefined : { label: "输入 tokens", value: input },
    output === undefined ? undefined : { label: "输出 tokens", value: output },
    total === undefined ? undefined : { label: "总 tokens", value: total },
  ].filter((item): item is {label: string; value: number} => item !== undefined);
}

const eventTypes: Record<string, string> = {
  run: "运行", step: "步骤", model: "模型调用", tool: "工具调用",
  authorization: "授权检查", confirmation: "用户确认", interaction: "用户交互",
};
const eventStatuses: Record<string, string> = {
  queued: "已排队", running: "执行中", completed: "已完成", failed: "失败", cancelled: "已停止",
  prepared: "已准备", started: "已开始", granted: "已授权", denied: "已拒绝", authorization_failed: "授权检查失败",
  confirmation_required: "需要确认", pending: "等待处理", approved: "已批准", rejected: "已拒绝", resolved: "已解决",
  waiting_confirmation: "等待确认", waiting_user: "等待补充信息", needs_reconciliation: "等待核查", uncertain: "结果待核查",
};
export function auditEventLabel(event: RunAuditEvent) {
  const target = event.tool ? ` · ${event.tool}` : event.type !== "run" && event.step >= 0 ? ` · 第 ${event.step + 1} 步` : "";
  return `${eventTypes[event.type] || event.type} · ${eventStatuses[event.status] || event.status}${target}`;
}
