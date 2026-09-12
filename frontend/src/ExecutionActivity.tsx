import type { Run } from "./api.ts";
import { webResult } from "./WebResult.tsx";
import { accountWriteResult } from "./AccountWriteResult.tsx";
import { isAccountWrite } from "./account-write-state.ts";
import { artifactResult } from "./ArtifactResult.tsx";
import { KnowledgeSources } from "./KnowledgeSources.tsx";
import { workflowResult, workflowStatus } from "./WorkflowResult.tsx";
import { todoDeadline, type Todo } from "./todo-state.ts";
import type { ToolView } from "./execution-state.ts";
import { recoveryTarget, resultKind } from "./execution-outcome.ts";
import { ExecutionOutcome, RecoveryButton } from "./ExecutionOutcome.tsx";
import { errorMessage } from "./errors.ts";
import { Button } from "./components/ui/button";

const names: Record<string, string> = {
  calendar_write_accounts: "发现日历写入账号", calendar_event_inspect: "核对日程修改目标", calendar_event_create: "创建日程", calendar_event_update: "修改日程",
  mail_write_accounts: "发现邮件发送账号", mail_send: "发送邮件", mail_reply: "回复邮件",
  time_now: "查询时间", calculate: "计算", history_search: "搜索历史对话",
  knowledge_attachments: "查看当前会话附件", knowledge_libraries: "查看可读资料库", knowledge_search: "搜索知识库", knowledge_read: "读取文档", knowledge_extract: "提取文档字段与表格",
  attachment_search: "搜索当前会话附件", attachment_read: "读取附件检索内容",
  business_catalog: "查看业务目录", query_records: "查询业务记录", get_record: "读取业务记录", query_related_records: "查询关联记录", invoke_action: "执行业务动作",
  workflow_start: "启动业务流程", workflow_get: "查询流程进度",
  report_query: "查询报表",
  history_read: "读取原文", memory_search: "查询记忆", memory_save: "保存记忆", memory_forget: "删除记忆", ask_user: "补充信息",
  todo_create: "创建待办", todo_list: "查询待办", todo_get: "读取事项", todo_update: "修改待办", todo_delete: "删除待办",
  tool_result_read: "读取完整工具结果", execution_read: "查看历史执行结果",
  mail_accounts: "发现邮件账号", mail_list: "查看邮件列表", mail_search: "搜索邮件", mail_read: "读取邮件正文",
  web_search: "搜索公开网页", web_fetch: "读取网页正文",
  calendar_accounts: "发现日历账号", calendar_list: "查看日历目录", calendar_events: "查询日历安排", calendar_event: "读取事件详情", calendar_availability: "查询共同空闲",
  artifact_create: "创建成果", artifact_read: "读取成果", artifact_edit: "修改成果", artifact_export: "导出成果", artifact_list: "查询成果", artifact_versions: "查询成果版本",
};
const statuses: Record<string, string> = {
  receiving: "正在准备参数", queued: "等待执行", running: "正在执行",
  completed: "已完成", failed: "执行失败", pending: "已受理", uncertain: "结果待核查",
  waiting_user: "等待补充信息", waiting_confirmation: "等待确认", needs_reconciliation: "结果待核查",
  not_started: "未执行", interrupted: "已请求停止",
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
export function ExecutionActivity({run, onResume, onRepair, recoveryDisabled, repairDisabled}: {run: Run; onResume?: () => void; onRepair?: (step: number, call: ToolView, label: string) => void; recoveryDisabled?: boolean; repairDisabled?: boolean}) {
  if (run.access_error) return null;
  const steps = run.steps?.filter(step => step.calls.length || ["generating", "interrupted"].includes(step.status)) || [];
  if (!steps.length && !["failed", "cancelled"].includes(run.status)) return null;
  const recovery = recoveryTarget(run);
  return <div className="execution-activity" aria-label="工具执行记录">
    <ExecutionOutcome run={run} />
    {recovery && recovery.step === undefined && onResume && <RecoveryButton target={recovery} disabled={recoveryDisabled} onResume={onResume} />}
    {run.status === "cancelled" && <p role="status" className="subtle">已停止后续处理。已完成或已受理的业务操作会保留；结果待核查的操作不能视为已撤销。可在处理记录中刷新查看回执。</p>}
    {steps.map(step => <div key={step.number} className="execution-step">
      <small className="subtle">第 {step.number + 1} 步</small>
      {!step.calls.length && <p className="subtle">{step.status === "generating" && ["running", "queued"].includes(run.status) ? "正在生成回复…" : "这一步的回复尚未完成。"}</p>}
      {recovery?.step === step.number && !recovery.callID && onResume && <RecoveryButton target={recovery} disabled={recoveryDisabled} onResume={onResume} />}
      {step.calls.map(call => {
        const kind = resultKind(run, call);
        const disposition = kind === "unstarted" ? "未执行" : kind === "unconfirmed" ? "结果待核查" : kind === "accepted" ? "已受理" : kind === "interrupted" ? "已中断" : undefined;
        const result = call.citations?.length ? <KnowledgeSources citations={call.citations} /> : call.result_truncated ? null : isAccountWrite(call.name) ? accountWriteResult(call.name, call.result_preview) : ["web_search", "web_fetch"].includes(call.name) ? webResult(call.name, call.result_preview) : call.name === "calculate" ? calculation(call.result_preview) : ["memory_save", "memory_forget"].includes(call.name) ? memoryResult(call.result_preview) : call.name.startsWith("todo_") ? todoResult(call.result_preview) : call.name.startsWith("artifact_") ? artifactResult(call.result_preview) : call.name.startsWith("workflow_") ? workflowResult(call.name, call.result_preview) : null;
        return <details className="execution-tool" key={call.id} data-tool-status={call.status} open={["failed", "unconfirmed"].includes(kind) || recovery?.callID === call.id}>
          <summary><span>{names[call.name] || call.name}</span><small>{disposition || workflowStatus(call.name, call.status, call.result_preview, call.result_truncated) || statuses[call.status] || call.status}</small></summary>
          {result || <>
            {call.arguments && <><small className="subtle">{call.status === "receiving" ? "参数尚未接收完整" : "调用参数"}</small><pre>{readable(call.arguments)}</pre></>}
            {call.result_preview && <><small className="subtle">执行结果{call.result_truncated ? "（节选）" : ""}</small><pre>{readable(call.result_preview)}</pre></>}
          </>}
          {call.error_code && <p role="alert" className="subtle">{errorMessage(call.error_code)} <small>（{call.error_code}）</small></p>}
          {call.resource_id && <small className="subtle">结果引用：{call.resource_id}</small>}
          {recovery?.step === step.number && recovery.callID === call.id && onResume && <RecoveryButton target={recovery} disabled={recoveryDisabled} onResume={onResume} />}
          {call.status === "failed" && ["completed", "failed", "cancelled"].includes(run.status) && onRepair && <div className="space-y-2"><p className="subtle">这次调用已记录为失败。准备新请求核对并修复此项，原结果保留。</p><Button type="button" variant="outline" size="sm" disabled={repairDisabled} title={repairDisabled ? "请先处理当前运行或发送／清空草稿" : undefined} onClick={() => onRepair(step.number, call, names[call.name] || call.name)}>准备修复这项失败</Button></div>}
        </details>;
      })}
    </div>)}
  </div>;
}
