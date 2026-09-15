import type { Run } from "./api.ts";
import { webResult } from "./WebResult.tsx";
import { accountWriteResult } from "./AccountWriteResult.tsx";
import { isAccountWrite } from "./account-write-state.ts";
import { artifactResult } from "./ArtifactResult.tsx";
import { KnowledgeSources } from "./KnowledgeSources.tsx";
import { workflowResult, workflowStatus } from "./WorkflowResult.tsx";
import { todoDeadline, type Todo } from "./todo-state.ts";
import type { ContextView, ToolView } from "./execution-state.ts";
import { recoveryTarget, resultKind } from "./execution-outcome.ts";
import { ExecutionOutcome, RecoveryButton } from "./ExecutionOutcome.tsx";
import { errorMessage } from "./errors.ts";
import { Button } from "./components/ui/button";
import { StoredResult } from "./StoredResult.tsx";
import { durationLabel, usageItems } from "./run-detail-state.ts";

const names: Record<string, string> = {
  calendar_write_accounts: "发现日历写入账号", calendar_event_inspect: "核对日程修改目标", calendar_event_create: "创建日程", calendar_event_update: "修改日程",
  mail_write_accounts: "发现邮件发送账号", mail_send: "发送邮件", mail_reply: "回复邮件",
  agent_list: "发现 Agent", agent_delegate: "委派工作", delegation_get: "读取委派", agent_message: "发送协作消息", delegation_update: "管理委派", time_now: "查询时间", calculate: "计算", history_search: "搜索历史对话",
  knowledge_attachments: "查看当前会话附件", knowledge_libraries: "查看可读资料库", knowledge_search: "搜索知识库", knowledge_read: "读取文档", knowledge_extract: "提取文档字段与表格",
  attachment_search: "搜索当前会话附件", attachment_read: "读取附件检索内容",
  business_catalog: "查看业务目录", query_records: "查询业务记录", get_record: "读取业务记录", query_related_records: "查询关联记录", invoke_action: "执行业务动作",
  workflow_start: "启动业务流程", workflow_get: "查询流程进度",
  report_query: "查询报表", analysis_run: "分析数据",
  history_read: "读取原文", memory_search: "查询记忆", memory_save: "保存记忆", memory_forget: "删除记忆", ask_user: "补充信息",
  todo_create: "创建待办", todo_list: "查询待办", todo_get: "读取事项", todo_update: "修改待办", todo_delete: "删除待办",
  tool_result_read: "读取完整工具结果", execution_read: "查看历史执行结果",
	  run_code: "运行受限代码",
  coding_file_read: "读取工作区文件", coding_file_search: "搜索工作区", coding_file_write: "写入工作区文件", coding_file_edit: "编辑工作区文件",
  coding_terminal_open: "打开终端", coding_terminal_send: "输入终端", coding_terminal_read: "读取终端", coding_terminal_close: "关闭终端",
  coding_process_start: "启动后台进程", coding_process_read: "读取进程输出", coding_process_kill: "停止后台进程", coding_lsp: "代码语义导航",
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
function codingResult(text?: string) {
  try {
    const value = JSON.parse(text || "") as Record<string, unknown>;
    if (typeof value.kind !== "string") return null;
    if (value.kind === "file_read") return <div className="coding-result"><strong>{String(value.path)} · {Number(value.size).toLocaleString()} 字节</strong><small className="subtle">版本 {String(value.sha256).slice(0, 16)} · {value.complete ? "读取完成" : `继续位置 ${String(value.next_offset)}`}</small>{typeof value.content === "string" && <pre>{value.content}</pre>}</div>;
    if (value.kind === "file_search") return <div className="coding-result"><strong>搜索 {String(value.query)}</strong><ol>{(Array.isArray(value.matches) ? value.matches : []).map((item, index) => { const hit = item as Record<string, unknown>; return <li key={`${String(hit.path)}:${String(hit.line)}:${index}`}><code>{String(hit.path)}:{String(hit.line)}</code> {String(hit.text)}</li>; })}</ol>{value.truncated === true && <small className="subtle">结果已截断</small>}</div>;
    if (value.kind === "file_change") return <div className="coding-result"><strong>{String(value.path)} · {value.operation === "edit" ? "已编辑" : "已写入"}</strong><small className="subtle">{Number(value.bytes).toLocaleString()} 字节 · 新版本 {String(value.sha256).slice(0, 16)}</small></div>;
    if (["terminal", "terminal_input", "terminal_output"].includes(value.kind)) return <div className="coding-result"><strong>终端 {String(value.terminal_id)} · {String(value.status || "输入已提交")}</strong>{typeof value.output === "string" && <pre>{value.output}</pre>}<small className="subtle">{value.next_cursor !== undefined ? `输出位置 ${String(value.next_cursor)}` : `${String(value.bytes || 0)} 字节`}</small></div>;
    if (["process", "process_output"].includes(value.kind)) return <div className="coding-result"><strong>进程 {String(value.process_id)} · {String(value.status)}</strong>{Array.isArray(value.argv) && <code>{value.argv.map(String).join(" ")}</code>}{typeof value.output === "string" && <pre>{value.output}</pre>}{value.exit_code !== undefined && value.exit_code !== null && <small className="subtle">退出码 {String(value.exit_code)}</small>}</div>;
    if (value.kind === "lsp") return <div className="coding-result"><strong>{value.operation === "references" ? "引用" : "定义"} · {String(value.path)}:{String(value.line)}:{String(value.character)}</strong><ol>{(Array.isArray(value.locations) ? value.locations : []).map((item, index) => { const location = item as Record<string, unknown>; const start = location.start as Record<string, unknown>; return <li key={`${String(location.path)}:${index}`}><code>{String(location.path)}:{String(start?.line)}:{String(start?.character)}</code></li>; })}</ol></div>;
  } catch { /* The generic preview remains available. */ }
  return null;
}
function CodeSubcalls({calls, executionID}: {calls: ToolView[]; executionID?: string}) {
  if (!calls.length) return null;
  return <div className="code-subcalls" aria-label="代码内工具调用"><small className="subtle">代码内调用</small>{calls.map(call => <details className="execution-tool code-subcall" key={call.id} data-tool-status={call.status} open={call.status === "failed"}>
    <summary><span>{names[call.name] || call.name}</span><small>{statuses[call.status] || call.status}</small></summary>
    {call.arguments && <><small className="subtle">调用参数</small><pre>{readable(call.arguments)}</pre></>}
    {call.result_preview && <><small className="subtle">执行结果{call.result_truncated ? "（节选）" : ""}</small><pre>{readable(call.result_preview)}</pre></>}
    {call.error_code && <p role="alert" className="subtle">{errorMessage(call.error_code)} <small>（{call.error_code}）</small></p>}
    {call.result_truncated && call.result_reference && <StoredResult reference={call.result_reference} executionID={executionID} />}
    <CodeSubcalls calls={call.subcalls || []} executionID={executionID} />
  </details>)}</div>;
}
export function ContextDiagnostic({context}: {context: ContextView}) {
  return <details className="context-diagnostic">
    <summary><span>上下文装配</span><small>{context.window ? `${(context.window.pressure_permille / 10).toFixed(1)}%` : "未计量"}{context.compaction ? ` · 已压缩 ${context.compaction.results} 个结果、${context.compaction.intervals || 0} 个执行区间` : ""}</small></summary>
    {context.window && <p className="subtle">{context.window.input_bytes.toLocaleString()} / {context.window.limit_bytes.toLocaleString()} 字节 · {context.window.provider_serialized ? "按实际模型协议计量" : "按完整冻结快照保守计量"}</p>}
    {!!context.sources?.length && <ul>{context.sources.map(source => <li key={source.key}><strong>{source.key}</strong> · {source.kind} · {source.scope} · 版本 {source.version} · {source.refresh === "step" ? "每步刷新" : "本运行冻结"}{source.stable_prefix ? " · 稳定前缀" : ""}</li>)}</ul>}
    {!!context.changes?.length && <p className="subtle">本运行更新：{context.changes.map(change => `${change.key} ${change.previous_version} → ${change.current_version}`).join("；")}</p>}
    {(context.cache_read_input_tokens || context.cache_creation_input_tokens) ? <p className="subtle">模型报告缓存：命中 {context.cache_read_input_tokens || 0} token · 新建 {context.cache_creation_input_tokens || 0} token</p> : null}
  </details>;
}
export function ExecutionActivity({run, onResume, onRepair, recoveryDisabled, repairDisabled,executionID}: {run: Run; onResume?: () => void; onRepair?: (step: number, call: ToolView, label: string) => void; recoveryDisabled?: boolean; repairDisabled?: boolean;executionID?:string}) {
  if (run.access_error) return null;
  const steps = run.steps?.filter(step => step.context || step.calls.length || ["generating", "interrupted"].includes(step.status)) || [];
  if (!steps.length && !["failed", "cancelled"].includes(run.status)) return null;
  const recovery = recoveryTarget(run);
  return <div className="execution-activity" aria-label="工具执行记录">
    <ExecutionOutcome run={run} />
    {recovery && recovery.step === undefined && onResume && <RecoveryButton target={recovery} disabled={recoveryDisabled} onResume={onResume} />}
    {run.status === "cancelled" && <p role="status" className="subtle">已停止后续处理。已完成或已受理的业务操作会保留；结果待核查的操作不能视为已撤销。可在处理记录中刷新查看回执。</p>}
    {steps.map(step => <div key={step.number} className="execution-step">
      <small className="subtle">第 {step.number + 1} 步 · {step.started_at ? durationLabel(step.duration_ms) : "未报告"}{step.tool_execution === "parallel_read" ? ` · ${step.parallel_tool_calls} 项独立读取按并行模式调度` : ""}{usageItems(step.usage).length ? ` · ${usageItems(step.usage).map(item => `${item.label} ${item.value}`).join(" · ")}` : ""}</small>
      {step.context && <ContextDiagnostic context={step.context} />}
      {!step.calls.length && <p className="subtle">{step.status === "generating" && ["running", "queued"].includes(run.status) ? "正在生成回复…" : "这一步的回复尚未完成。"}</p>}
      {recovery?.step === step.number && !recovery.callID && onResume && <RecoveryButton target={recovery} disabled={recoveryDisabled} onResume={onResume} />}
      {step.calls.map(call => {
        const kind = resultKind(run, call);
        const disposition = kind === "unstarted" ? "未执行" : kind === "unconfirmed" ? "结果待核查" : kind === "accepted" ? "已受理" : kind === "interrupted" ? "已中断" : undefined;
        const result = call.citations?.length ? <KnowledgeSources citations={call.citations} /> : call.result_truncated ? null : call.name.startsWith("coding_") ? codingResult(call.result_preview) : isAccountWrite(call.name) ? accountWriteResult(call.name, call.result_preview) : ["web_search", "web_fetch"].includes(call.name) ? webResult(call.name, call.result_preview) : call.name === "calculate" ? calculation(call.result_preview) : ["memory_save", "memory_forget"].includes(call.name) ? memoryResult(call.result_preview) : call.name.startsWith("todo_") ? todoResult(call.result_preview) : call.name.startsWith("artifact_") ? artifactResult(call.result_preview) : call.name.startsWith("workflow_") ? workflowResult(call.name, call.result_preview) : null;
        return <details className="execution-tool" key={call.id} data-tool-status={call.status} open={["failed", "unconfirmed"].includes(kind) || recovery?.callID === call.id}>
          <summary><span>{names[call.name] || call.name}</span><small>{disposition || workflowStatus(call.name, call.status, call.result_preview, call.result_truncated) || statuses[call.status] || call.status}</small></summary>
          {result || <>
            {call.arguments && <><small className="subtle">{call.status === "receiving" ? "参数尚未接收完整" : "调用参数"}</small><pre>{readable(call.arguments)}</pre></>}
            {call.result_preview && <><small className="subtle">执行结果{call.result_truncated ? "（节选）" : ""}</small><pre>{readable(call.result_preview)}</pre></>}
          </>}
          {call.error_code && <p role="alert" className="subtle">{errorMessage(call.error_code)} <small>（{call.error_code}）</small></p>}
          {call.access_error&&<p role="status" className="subtle">这项调用尚无可验证的成功回执，参数和结果暂不可查看。</p>}
          <dl className="tool-audit">
            {call.reused_from&&<><dt>复用原回执</dt><dd>本次未重新提交操作 · 原执行 {call.reused_from.run_id} · 第 {call.reused_from.step+1} 步</dd></>}
            {call.outcome_inspection&&<><dt>原回执核查</dt><dd>{call.outcome_inspection.status==='reading'?'查询中':call.outcome_inspection.status==='completed'?'已取得明确回执':'结果尚未明确'} · {new Date(call.outcome_inspection.checked_at).toLocaleString()} · {call.outcome_inspection.actor_id}</dd></>}
            <dt>调用耗时</dt><dd>{call.reused_from ? "本次未调用外部服务" : call.started_at ? durationLabel(call.duration_ms) : "未报告"}</dd>
            {call.authorization && <><dt>授权结果</dt><dd>{call.reused_from && call.authorization.status === "confirmation_required" ? "新操作需确认；本次复用原回执" : ({granted:"已授权", confirmation_required:"需要确认", denied:"已拒绝", failed:"检查失败"} as Record<string,string>)[call.authorization.status] || call.authorization.status} · 检查 {call.authorization.checks} 次{call.authorization.revision ? ` · 版本 ${call.authorization.revision}` : ""}</dd></>}
            {call.confirmation && <><dt>确认结果</dt><dd>{({approved:"已批准", rejected:"已拒绝", pending:"等待确认", resolved:"已解决"} as Record<string,string>)[call.confirmation.status] || call.confirmation.status}{call.confirmation.responded_by ? ` · ${call.confirmation.responded_by}` : ""}{call.confirmation.responded_at ? ` · ${new Date(call.confirmation.responded_at).toLocaleString()}` : ""}</dd></>}
          </dl>
          {call.resource_id && <small className="subtle">结果引用：{call.resource_id}</small>}
		  <CodeSubcalls calls={call.subcalls || []} executionID={executionID} />
          {call.result_truncated && call.result_reference && <StoredResult key={JSON.stringify(call.result_reference)} reference={call.result_reference} executionID={executionID} />}
          {recovery?.step === step.number && recovery.callID === call.id && onResume && <RecoveryButton target={recovery} disabled={recoveryDisabled} onResume={onResume} />}
          {call.status === "failed" && ["completed", "failed", "cancelled"].includes(run.status) && onRepair && <div className="space-y-2"><p className="subtle">这次调用已记录为失败。准备新请求核对并修复此项，原结果保留。</p><Button type="button" variant="outline" size="sm" disabled={repairDisabled} title={repairDisabled ? "请先处理当前运行或发送／清空草稿" : undefined} onClick={() => onRepair(step.number, call, names[call.name] || call.name)}>准备修复这项失败</Button></div>}
        </details>;
      })}
    </div>)}
  </div>;
}
