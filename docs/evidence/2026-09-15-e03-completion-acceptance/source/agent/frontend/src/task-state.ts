import type { Artifact } from "./artifact-state.ts";
import type { ResultReference, StepView } from "./execution-state.ts";
import type { Interaction } from "./interaction-state.ts";

export type ConversationTaskStatus = "queued" | "running" | "awaiting_review" | "completed" | "failed" | "cancelled";
export type ConversationGoalStatus = "active" | "blocked" | "paused" | "completed" | "budget_exhausted";
export type ConversationTaskBriefField = "goal" | "deliverable" | "audience" | "constraints" | "completion_conditions" | "assumptions" | "due_at";
export type ConversationTaskBrief = {
  version: number; goal: string; deliverable: string; audience: string;
  constraints: string[]; completion_conditions: string[]; assumptions: string[];
  verification_rules?: ConversationCompletionRule[];
  explicit_fields?: ConversationTaskBriefField[]; inferred_fields?: ConversationTaskBriefField[]; due_at?: string;
};
export type ConversationCompletionRule = { condition: number; kind: "data" | "receipt"; schema?: unknown; tool?: string; arguments_schema?: unknown; result_schema?: unknown; min_receipts?: number; completion?: "completed" | "accepted" };
export type ConversationConditionAssessment = { condition: number; verdict: "met" | "unmet" | "unknown"; basis: string; receipts?: ResultReference[] };
export type ConversationCompletionCheck = ConversationConditionAssessment & { requirement: string; method: string };
export type ConversationTaskCompletionVerification = { delivery_digest: string; brief_version: number; agreement_revision: number; checks: ConversationCompletionCheck[]; ready: boolean; blockers: string[]; actor_id: string; agent_id?: string; source?: ConversationRunReference; checked_at: string };
export type ConversationTaskCompletionRecord = { revision: number; kind: string; submission: { agreement_revision: number; summary: string; data?: unknown; conditions: ConversationConditionAssessment[]; artifacts: ConversationArtifactReference[]; source?: ConversationRunReference; submitted_at: string }; verification: ConversationTaskCompletionVerification; reason: string; recorded_at: string };
export type ConversationTaskCompletionHistory = { items: ConversationTaskCompletionRecord[]; next_before?: number; complete: boolean };
export type ConversationGoalProgress = { revision: number; status: ConversationGoalStatus; phase: string; completed_items: string[]; remaining_items: string[]; blocker?: string; updated_at: string };
export type ConversationPlanStepStatus = "pending" | "in_progress" | "completed" | "blocked" | "skipped" | "needs_review";
export type ConversationRunReference = { conversation_id: string; run_id: string; before_step?: number };
export type ConversationResultReference = { conversation_id: string; run_id: string; step: number; call_id: string; sha256: string };
export type ConversationArtifactReference = { id: string; version: number; sha256: string };
export type ConversationPlanStep = {
  id: string; title: string; status: ConversationPlanStepStatus; depends_on: string[]; input: string; expected_output: string;
  requirement_fields: string[]; executor: { agent_id: string; run_id: string }; evidence: ConversationResultReference[];
  artifacts: ConversationArtifactReference[]; outcome?: string; blocker?: string;
};
export type ConversationPlan = { task_id: string; version: number; agreement_revision: number; reason: string; steps: ConversationPlanStep[]; source?: ConversationRunReference; created_at: string };
export type ConversationPlanHistory = { items: ConversationPlan[]; next_before?: number; complete: boolean };
export type ConversationTaskProgress = { run_status?: string; attempt?: number; steps: number; tool_calls: number; last_event_seq?: number };
export type ConversationTaskWaiting = { id: string; kind: string; question: string; tool?: string; revision: number; expires_at: string };
export type ConversationTaskResult = { message_id: string; preview: string; bytes: number; complete: boolean };
export type ConversationTaskControlState = { can_cancel: boolean; can_resume: boolean; resume_blocker?: string };
export type ConversationTaskSummary = {
  agent_id?: string; delegation_id?: string; execution_conversation_id?: string;
  id: string; status: ConversationTaskStatus; goal: string; allowed_tools: string[];
  brief?: ConversationTaskBrief; agreement_revision: number; goal_progress: ConversationGoalProgress; plan?: ConversationPlan;
  completion_mode: "legacy_response" | "assessed"; completion?: ConversationTaskCompletionRecord;
  budget: { max_steps: number; max_tool_calls: number; max_output_bytes: number; timeout_seconds: number };
  source_conversation_id: string; source_run_id: string; execution_run_id?: string;
  previous_execution_runs?: { conversation_id: string; run_id: string }[];
  progress: ConversationTaskProgress; control: ConversationTaskControlState; waiting?: ConversationTaskWaiting; result?: ConversationTaskResult;
  artifacts: Artifact[]; artifacts_complete: boolean; artifacts_omitted?: boolean; access_error?: string;
  completion_event_id?: string; completion_event_seq?: number; error_code?: string;
  created_at: string; updated_at: string; completed_at?: string;
};
export type ConversationTaskDetail = ConversationTaskSummary & { input: string; steps: StepView[]; interaction?: Interaction };
export type ConversationTaskPage = { items: ConversationTaskSummary[]; next_cursor?: string; complete: boolean };

export const taskPath = (id: string) => `/agent/conversation-tasks/${encodeURIComponent(id)}`;
export const taskControlPath = (id: string, action: "cancel" | "resume") => `${taskPath(id)}/${action}`;
export const taskAgreementPath = (id: string) => `${taskPath(id)}/agreement`;
export const taskPlansPath = (id: string, before = 0) => `${taskPath(id)}/plans?before_version=${before}`;
export const taskCompletionsPath = (id: string, before = 0) => `${taskPath(id)}/completions${before ? `?before_revision=${before}` : ""}`;
export const taskCompletionReviewPath = (id: string) => `${taskPath(id)}/completion-review`;
export const taskNeedsRefresh = (task: ConversationTaskSummary) => task.status === "queued" || task.status === "running";
export const taskCanCancel = (task: ConversationTaskSummary) => task.control.can_cancel;
export const taskCanResume = (task: ConversationTaskSummary) => task.control.can_resume;
export const taskStatusLabel = (task: ConversationTaskSummary) => {
  if (task.waiting?.kind === "input") return "等待补充信息";
  if (task.waiting?.kind === "confirmation") return "等待操作确认";
  if (task.waiting?.kind === "reconciliation") return "等待结果核查";
  return ({ queued: "等待开始", running: "正在处理", awaiting_review: "等待验收", completed: "已完成", failed: "处理失败", cancelled: "已停止" } as const)[task.status];
};
export const taskGoalStatusLabel = (status: ConversationGoalStatus) => ({ active: "目标推进中", blocked: "目标受阻", paused: "目标已暂停", completed: "目标已完成", budget_exhausted: "预算已耗尽" } as const)[status];
export const taskBriefFieldLabel: Record<ConversationTaskBriefField, string> = { goal: "目标", deliverable: "交付物", audience: "使用者", constraints: "约束", completion_conditions: "完成条件", assumptions: "假设", due_at: "截止时间" };
export const taskBriefFieldSource = (brief: ConversationTaskBrief, field: ConversationTaskBriefField) => brief.explicit_fields?.includes(field) ? "用户明确" : brief.inferred_fields?.includes(field) ? "Agent 推断" : "来源未记录";
export const taskPlanStatusLabel = (status: ConversationPlanStepStatus) => ({ pending: "待执行", in_progress: "执行中", completed: "已完成", blocked: "受阻", skipped: "已跳过", needs_review: "待核实" } as const)[status];
export const taskAgreementCanUpdate = (task: ConversationTaskSummary) => !task.delegation_id && task.status !== "running" && task.status !== "completed";
export const taskVerificationRulesForConditions = (brief: ConversationTaskBrief, next: string[]) =>
	(brief.verification_rules || []).filter(rule => brief.completion_conditions[rule.condition] === next[rule.condition]);

export function taskListPath(input: { query?: string; status?: string; sourceConversationID?: string; cursor?: string; limit?: number }) {
  const params = new URLSearchParams({ query: input.query || "", status: input.status || "", cursor: input.cursor || "", limit: String(input.limit || 20) });
  if (input.sourceConversationID) params.set("source_conversation_id", input.sourceConversationID);
  return `/agent/conversation-tasks?${params}`;
}
