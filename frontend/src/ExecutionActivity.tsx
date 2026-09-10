import type { Run } from "./api.ts";
import { artifactResult } from "./ArtifactResult.tsx";
import { KnowledgeSources } from "./KnowledgeSources.tsx";
import { workflowResult, workflowStatus } from "./WorkflowResult.tsx";
import { todoDeadline, type Todo } from "./todo-state.ts";

const names: Record<string, string> = {
  time_now: "查询时间", calculate: "计算", history_search: "搜索历史对话",
  knowledge_libraries: "查看可读资料库", knowledge_search: "搜索知识库", knowledge_read: "读取文档",
  business_catalog: "查看业务目录", query_records: "查询业务记录", get_record: "读取业务记录", query_related_records: "查询关联记录", invoke_action: "执行业务动作",
  workflow_start: "启动业务流程", workflow_get: "查询流程进度",
  history_read: "读取原文", memory_search: "查询记忆", memory_save: "保存记忆", memory_forget: "删除记忆", ask_user: "补充信息",
  todo_create: "创建待办", todo_list: "查询待办", todo_get: "读取事项", todo_update: "修改待办", todo_delete: "删除待办",
  tool_result_read: "读取完整工具结果", execution_read: "查看历史执行结果",
  artifact_create: "创建成果", artifact_read: "读取成果", artifact_edit: "修改成果", artifact_export: "导出成果", artifact_list: "查询成果", artifact_versions: "查询成果版本",
};
const statuses: Record<string, string> = {
  receiving: "正在准备参数", queued: "等待执行", running: "正在执行",
  completed: "已完成", failed: "执行失败", pending: "已受理", uncertain: "结果待核查",
  waiting_user: "等待补充信息", waiting_confirmation: "等待确认", needs_reconciliation: "结果待核查",
};
function readable(text: string) {
  try { return JSON.stringify(JSON.parse(text), null, 2); } catch { return text; }
}
function calculation(text?: string) {
  try {
    const value = JSON.parse(text || "") as { value?: unknown; unit?: unknown; precision?: unknown; rounding?: unknown; basis?: { expression?: unknown } };
    if (typeof value.value !== "string" || typeof value.unit !== "string") return null;
    const rounding: Record<string, string> = { half_even: "四舍六入五成双", half_up: "四舍五入", toward_zero: "向零截断" };
    return <div className="calculation-result"><strong>{typeof value.basis?.expression === "string" ? `${value.basis.expression} = ` : ""}{value.value} {value.unit}</strong><small className="subtle">{typeof value.precision === "number" ? `保留 ${value.precision} 位小数` : ""}{typeof value.rounding === "string" && rounding[value.rounding] ? ` · ${rounding[value.rounding]}` : ""}</small></div>;
  } catch { return null; }
}
function memoryResult(text?: string) {
  try {
    const result = JSON.parse(text || "") as { memory?: { title?: unknown; content?: unknown; enabled?: unknown }; deleted?: boolean };
    const memory = result.memory;
    if (memory && typeof memory.title === "string" && typeof memory.content === "string") return <div className="memory-operation"><strong>{memory.title}</strong><p>{memory.content}</p><small className="subtle">{memory.enabled ? "已启用" : "已停用"} · 可在个人记忆中管理</small></div>;
    if (result.deleted === true) return <p>该个人记忆已删除。</p>;
  } catch { /* Fall back to the bounded result preview. */ }
  return null;
}
function todoResult(text?: string) {
  try {
    const result = JSON.parse(text || "") as { items?: Todo[]; id?: string; title?: string; deleted?: boolean };
    if (result.deleted) return <p>该待办已删除，原批次其他事项的项次保持不变。</p>;
    const items = result.items || (result.id && result.title ? [result as Todo] : []);
    if (items.length) return <ol className="todo-result">{items.map(todo => <li value={todo.position} key={todo.id}><strong>{todo.title}</strong><small>{todo.status === "completed" ? "已完成" : "未完成"} · {todoDeadline(todo)}</small></li>)}</ol>;
  } catch { /* The bounded generic preview remains available. */ }
  return null;
}
export function ExecutionActivity({run}: {run: Run}) {
  if (run.access_error) return null;
  const steps = run.steps?.filter(step => step.calls.length) || [];
  if (!steps.length) return null;
  return <div className="execution-activity" aria-label="工具执行记录">
    {steps.map(step => <div key={step.number} className="execution-step">
      <small className="subtle">第 {step.number + 1} 步</small>
      {step.calls.map(call => {
        const interrupted = ["failed", "cancelled"].includes(run.status) && ["receiving", "queued", "running", "waiting_user", "waiting_confirmation", "needs_reconciliation"].includes(call.status);
        const result = call.citations?.length ? <KnowledgeSources citations={call.citations} /> : call.result_truncated ? null : call.name === "calculate" ? calculation(call.result_preview) : ["memory_save", "memory_forget"].includes(call.name) ? memoryResult(call.result_preview) : call.name.startsWith("todo_") ? todoResult(call.result_preview) : call.name.startsWith("artifact_") ? artifactResult(call.result_preview) : call.name.startsWith("workflow_") ? workflowResult(call.name, call.result_preview) : null;
        return <details className="execution-tool" key={call.id} data-tool-status={call.status}>
          <summary><span>{names[call.name] || call.name}</span><small>{interrupted ? "已中断" : workflowStatus(call.name, call.status, call.result_preview, call.result_truncated) || statuses[call.status] || call.status}</small></summary>
          {result || <>
            {call.arguments && <><small className="subtle">{call.status === "receiving" ? "参数尚未接收完整" : "调用参数"}</small><pre>{readable(call.arguments)}</pre></>}
            {call.result_preview && <><small className="subtle">执行结果{call.result_truncated ? "（节选）" : ""}</small><pre>{readable(call.result_preview)}</pre></>}
          </>}
          {call.error_code && <p className="subtle">错误：{call.error_code}</p>}
          {call.resource_id && <small className="subtle">结果引用：{call.resource_id}</small>}
        </details>;
      })}
    </div>)}
  </div>;
}
