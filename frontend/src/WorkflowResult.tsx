type WorkflowState = {
  process_id: string;
  status: string;
  name?: string;
  terminal?: boolean;
  business_outcome?: string;
  current_steps?: string[];
  steps_truncated?: boolean;
  completed_at?: string;
};

function state(text: string | undefined, operation: string): WorkflowState | null {
  try {
    const evidence = JSON.parse(text || "");
    const value = evidence.data;
    if (evidence.version !== 1 || evidence.operation !== operation || !value
      || typeof value.process_id !== "string" || !value.process_id || typeof value.status !== "string") return null;
    if (operation === "workflow_start") return value.status === "accepted" ? value : null;
    if (operation !== "workflow_get" || typeof value.terminal !== "boolean" || !Array.isArray(value.current_steps)
      || !value.current_steps.every((step: unknown) => typeof step === "string")) return null;
    for (const field of ["name", "business_outcome", "completed_at"]) {
      if (value[field] !== undefined && typeof value[field] !== "string") return null;
    }
    return value;
  } catch { return null; }
}

export function workflowStatus(operation: string, status: string, text?: string, truncated?: boolean): string | null {
  if (status !== "completed") return null;
  if (operation === "workflow_start") return !truncated && state(text, operation) ? "已受理" : "启动请求已处理";
  return operation === "workflow_get" ? "查询完成" : null;
}

export function workflowResult(operation: string, text?: string) {
  const value = state(text, operation);
  if (!value) return null;
  if (operation === "workflow_start") return <div className="memory-operation">
    <strong>流程启动已受理</strong>
    <p>当前进度以流程查询结果为准；需要审批的流程会继续等待审批。</p>
    <small>流程实例：{value.process_id}</small>
  </div>;
  const statuses: Record<string, string> = { accepted: "已受理", running: "处理中", waiting: "等待处理", success: "已完成", completed: "已完成", approved: "审批通过", rejected: "已拒绝", cancelled: "已取消", failed: "失败", configuration_error: "配置错误", terminated: "已终止" };
  const outcomes: Record<string, string> = { approved: "审批通过", rejected: "审批拒绝", cancelled: "已取消", failed: "失败" };
  return <div className="memory-operation">
    <strong>{value.name || "业务流程"} · {statuses[value.status] || value.status}</strong>
    <p>{value.terminal ? "流程已结束" : "流程尚未结束"}{value.business_outcome ? ` · ${outcomes[value.business_outcome] || value.business_outcome}` : ""}</p>
    {!!value.current_steps?.length && <p>当前步骤：{value.current_steps.join("、")}{value.steps_truncated ? "（部分步骤）" : ""}</p>}
    {value.completed_at && <small>完成时间：{value.completed_at}</small>}
    <small>流程实例：{value.process_id}</small>
  </div>;
}
