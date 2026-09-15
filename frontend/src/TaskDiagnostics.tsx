import { taskDiagnosticActionLabel, taskDiagnosticReasonLabel, type ConversationTaskDetail, type ConversationWorkUsage } from "./task-state.ts";

const seconds = (milliseconds: number) => `${(milliseconds / 1000).toFixed(1)} 秒`;
const usage = (value: ConversationWorkUsage) => `${value.model_calls} 次模型 · ${value.tool_calls} 次工具 · ${value.input_tokens.toLocaleString()} 输入 token · ${value.output_tokens.toLocaleString()} 输出 token · 模型耗时 ${seconds(value.duration_ms)}${value.currency ? ` · ${value.cost_known ? value.model_cost.toFixed(6) : `至少 ${value.model_cost.toFixed(6)}`} ${value.currency} 模型费用` : ""}`;

export function TaskDiagnostics({ task }: { task: ConversationTaskDetail }) {
  const diagnostic = task.diagnostic;
  return <section className={`task-diagnostics ${diagnostic.state}`} aria-label="预算与进度诊断">
    <header><h4>预算与进度诊断</h4><strong>{taskDiagnosticActionLabel[diagnostic.action]}</strong></header>
    <div className="task-diagnostic-columns">
      <div><h5>执行活动</h5><p>{task.progress.model_calls} / {task.budget.max_steps} 次模型步骤 · {task.progress.tool_calls} / {task.budget.max_tool_calls} 次工具调用 · {seconds(task.progress.duration_ms)} / {task.budget.timeout_seconds} 秒</p><p>{task.progress.input_tokens.toLocaleString()} 输入 token · {task.progress.output_tokens.toLocaleString()} 输出 token · {diagnostic.execution_attempts} 次执行尝试</p></div>
      <div><h5>业务进度</h5><p>{diagnostic.verified_items} 项完成条件已核实 · {diagnostic.remaining_items} 项剩余</p><p>{diagnostic.stage_outcomes} 项阶段成果 · {diagnostic.remaining_stages} 个计划阶段未结束 · {diagnostic.open_dependencies} 项依赖待核对</p><p>约定修订 {diagnostic.agreement_revisions} 次 · 重复工具调用 {diagnostic.repeated_tool_calls} 次</p></div>
    </div>
    {task.work && <div className="task-work-budget"><p><strong>本项委派累计：</strong>{usage(task.work.allocation.usage)}</p><p><strong>整个工作累计：</strong>{usage(task.work.total_usage)}</p><p><strong>整个工作上限：</strong>{task.work.budget.max_input_tokens.toLocaleString()} 输入 token · {task.work.budget.max_output_tokens.toLocaleString()} 输出 token · {task.work.budget.max_duration_seconds.toLocaleString()} 秒{task.work.budget.max_model_cost !== undefined ? ` · ${task.work.budget.max_model_cost.toFixed(6)} ${task.work.budget.currency} 模型费用` : ""}</p></div>}
    {!!diagnostic.reasons.length && <ul>{diagnostic.reasons.map(reason => <li key={reason}>{taskDiagnosticReasonLabel(reason)}</li>)}</ul>}
    <small className="subtle">诊断依据：当前约定、程序核对结果、持久运行尝试、工具调用指纹、依赖版本和共享工作账本。</small>
  </section>;
}
