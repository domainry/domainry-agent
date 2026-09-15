import { useState, type ReactNode } from "react";
import { request } from "./api.ts";
import { describeError } from "./errors.ts";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { taskAgreementCanUpdate, taskAgreementPath, taskBriefFieldSource, taskGoalStatusLabel, taskVerificationRulesForConditions, type ConversationTaskBrief, type ConversationTaskBriefField, type ConversationTaskDetail } from "./task-state.ts";

const coreFields: ConversationTaskBriefField[] = ["goal", "deliverable", "audience", "constraints", "completion_conditions", "assumptions"];
const lines = (value: string) => value.split("\n").map(item => item.trim()).filter(Boolean);
const localDateTime = (value?: string) => {
  if (!value) return "";
  const date = new Date(value), offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
};

function AgreementValue({ label, source, children }: { label: string; source: string; children: ReactNode }) {
  return <div className="task-agreement-value"><div><strong>{label}</strong><span>{source}</span></div>{children}</div>;
}

export function TaskAgreement({ task, acting, setActing, onUpdated, onError, onRun }: {
  task: ConversationTaskDetail; acting: boolean; setActing: (value: boolean) => void;
  onUpdated: (task: ConversationTaskDetail) => void; onError: (message: string) => void;
  onRun: (conversationID: string, runID: string) => void;
}) {
  const brief: ConversationTaskBrief = task.brief || {
    version: 1, goal: task.goal, deliverable: task.goal, audience: "requesting user",
    constraints: [], completion_conditions: ["任务结果可在回复中查看"], assumptions: [],
    inferred_fields: ["goal", "deliverable", "audience", "constraints", "completion_conditions", "assumptions"],
  };
  const [editing, setEditing] = useState(false);
  const [goal, setGoal] = useState(brief?.goal || task.goal), [deliverable, setDeliverable] = useState(brief?.deliverable || task.goal), [audience, setAudience] = useState(brief?.audience || "");
  const [constraints, setConstraints] = useState((brief?.constraints || []).join("\n")), [completion, setCompletion] = useState((brief?.completion_conditions || []).join("\n")), [assumptions, setAssumptions] = useState((brief?.assumptions || []).join("\n"));
  const [dueAt, setDueAt] = useState(localDateTime(brief?.due_at)), [reason, setReason] = useState("");
  const values: Record<Exclude<ConversationTaskBriefField, "due_at">, string | string[]> = {
    goal, deliverable, audience, constraints: lines(constraints), completion_conditions: lines(completion), assumptions: lines(assumptions),
  };
  const changed = (field: Exclude<ConversationTaskBriefField, "due_at">) => JSON.stringify(values[field]) !== JSON.stringify(brief[field]);

  async function save() {
    if (acting || !reason.trim() || !goal.trim() || !deliverable.trim() || !audience.trim() || !lines(completion).length) return;
    setActing(true); onError("");
    try {
      const explicit: ConversationTaskBriefField[] = [], inferred: ConversationTaskBriefField[] = [];
      for (const field of coreFields) {
        if (changed(field as Exclude<ConversationTaskBriefField, "due_at">) || brief.explicit_fields?.includes(field)) explicit.push(field);
        else inferred.push(field);
      }
      const originalDue = brief.due_at ? new Date(brief.due_at).toISOString() : "";
      const nextDue = dueAt ? new Date(dueAt).toISOString() : "";
      if (nextDue) (nextDue !== originalDue || brief.explicit_fields?.includes("due_at") ? explicit : inferred).push("due_at");
		const nextConditions = lines(completion);
		const verificationRules = taskVerificationRulesForConditions(brief, nextConditions);
      const nextBrief: ConversationTaskBrief = {
        version: brief.version + 1, goal: goal.trim(), deliverable: deliverable.trim(), audience: audience.trim(),
		constraints: lines(constraints), completion_conditions: nextConditions, assumptions: lines(assumptions),
		verification_rules: verificationRules,
        explicit_fields: explicit.sort(), inferred_fields: inferred.sort(), ...(nextDue ? { due_at: nextDue } : {}),
      };
      const value = await request<ConversationTaskDetail>(taskAgreementPath(task.id), "PATCH", {
        client_id: crypto.randomUUID(), expected_revision: task.agreement_revision, reason: reason.trim(), brief: nextBrief,
      });
      onUpdated(value); setEditing(false); setReason("");
    } catch (failure) {
      onError(describeError(failure));
    } finally {
      setActing(false);
    }
  }

  return <div className="task-agreement" aria-label="当前任务约定">
    <header><div><strong>当前约定 v{brief.version}</strong><small>约定修订 {task.agreement_revision}</small></div><span className={`goal-status ${task.goal_progress.status}`}>{taskGoalStatusLabel(task.goal_progress.status)}</span></header>
    <div className="task-goal-progress"><span>当前阶段：{task.goal_progress.phase}</span><span>剩余 {task.goal_progress.remaining_items.length} 项 · 已完成 {task.goal_progress.completed_items.length} 项</span></div>
    {task.goal_progress.blocker && <p className="task-blocker">阻塞原因：{task.goal_progress.blocker}</p>}
    {!editing ? <>
      <div className="task-agreement-grid">
        <AgreementValue label="目标" source={taskBriefFieldSource(brief, "goal")}><p>{brief.goal}</p></AgreementValue>
        <AgreementValue label="交付物" source={taskBriefFieldSource(brief, "deliverable")}><p>{brief.deliverable}</p></AgreementValue>
        <AgreementValue label="使用者" source={taskBriefFieldSource(brief, "audience")}><p>{brief.audience || "未指定"}</p></AgreementValue>
        <AgreementValue label="截止时间" source={brief.due_at ? taskBriefFieldSource(brief, "due_at") : "未设置"}><p>{brief.due_at ? new Date(brief.due_at).toLocaleString() : "未设置截止时间"}</p></AgreementValue>
		<AgreementValue label="完成条件" source={taskBriefFieldSource(brief, "completion_conditions")}><ul>{brief.completion_conditions.map((item, index) => <li key={`${index}:${item}`}>{item}{brief.verification_rules?.some(rule => rule.condition === index) && <small className="subtle"> · 程序核对</small>}</li>)}</ul></AgreementValue>
        <AgreementValue label="约束" source={taskBriefFieldSource(brief, "constraints")}><p>{brief.constraints.join("；") || "无"}</p></AgreementValue>
        <AgreementValue label="假设" source={taskBriefFieldSource(brief, "assumptions")}><p>{brief.assumptions.join("；") || "无"}</p></AgreementValue>
      </div>
      <div className="todo-toolbar">{taskAgreementCanUpdate(task) && <Button size="sm" variant="outline" onClick={() => setEditing(true)}>修改任务约定</Button>}{!task.delegation_id && task.status === "running" && <small className="subtle">先停止当前运行，再修改任务约定。</small>}{task.delegation_id && <small className="subtle">委派任务请在协作详情中变更约定。</small>}</div>
    </> : <form className="task-agreement-form" onSubmit={event => { event.preventDefault(); void save(); }}>
      <p className="subtle">你修改的字段会标记为“用户明确”；未修改字段保留已有来源。</p>
      <label>目标<Input aria-label="任务约定目标" value={goal} onChange={event => setGoal(event.target.value)} required maxLength={2048} /></label>
      <label>交付物<Input aria-label="任务约定交付物" value={deliverable} onChange={event => setDeliverable(event.target.value)} required maxLength={2048} /></label>
      <label>使用者<Input aria-label="任务约定使用者" value={audience} onChange={event => setAudience(event.target.value)} required maxLength={512} /></label>
      <label>截止时间<Input aria-label="任务约定截止时间" type="datetime-local" value={dueAt} onChange={event => setDueAt(event.target.value)} /></label>
		<label>完成条件（每行一项）<Textarea aria-label="任务约定完成条件" value={completion} onChange={event => setCompletion(event.target.value)} required /></label>
		{changed("completion_conditions") && !!brief.verification_rules?.length && <p className="subtle">修改过的完成条件不会沿用旧程序核对规则；下一轮执行需按新约定重新建立核对依据。</p>}
      <label>约束（每行一项）<Textarea aria-label="任务约定约束" value={constraints} onChange={event => setConstraints(event.target.value)} /></label>
      <label>假设（每行一项）<Textarea aria-label="任务约定假设" value={assumptions} onChange={event => setAssumptions(event.target.value)} /></label>
      <label>变更原因<Input aria-label="任务约定变更原因" value={reason} onChange={event => setReason(event.target.value)} required maxLength={4096} /></label>
      <div className="todo-toolbar"><Button type="submit" disabled={acting || !reason.trim() || !goal.trim() || !deliverable.trim() || !audience.trim() || !lines(completion).length}>保存新版本</Button><Button type="button" variant="ghost" disabled={acting} onClick={() => setEditing(false)}>取消</Button></div>
    </form>}
    {!!task.previous_execution_runs?.length && <div className="task-previous-runs"><strong>先前运行</strong>{task.previous_execution_runs.map((run, index) => <Button key={`${run.conversation_id}:${run.run_id}`} size="sm" variant="ghost" onClick={() => onRun(run.conversation_id, run.run_id)}>运行 {index + 1}</Button>)}</div>}
  </div>;
}
