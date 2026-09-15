import assert from "node:assert/strict";
import test from "node:test";
import { taskAgreementCanUpdate, taskAgreementPath, taskBriefFieldSource, taskCanCancel, taskCanResume, taskCompletionReviewPath, taskCompletionsPath, taskControlPath, taskDiagnosticActionLabel, taskDiagnosticReasonLabel, taskGoalStatusLabel, taskListPath, taskNeedsRefresh, taskPath, taskPlansPath, taskPlanStatusLabel, taskStatusLabel, taskVerificationRulesForConditions, type ConversationTaskSummary } from "./task-state.ts";

const task = (status: ConversationTaskSummary["status"], control: ConversationTaskSummary["control"] = { can_cancel: false, can_resume: false }, waiting?: ConversationTaskSummary["waiting"]): ConversationTaskSummary => ({
  id: "task:a/b", status, goal: "核对发布", allowed_tools: ["time_now"], completion_mode: "legacy_response",
  agreement_revision: 1, brief: { version: 1, goal: "核对发布", deliverable: "发布报告", audience: "负责人", constraints: [], completion_conditions: ["报告可复核"], assumptions: [], explicit_fields: ["goal", "completion_conditions"], inferred_fields: ["deliverable", "audience", "constraints", "assumptions"] },
  goal_progress: { revision: 1, status: status === "completed" ? "completed" : "active", phase: status, completed_items: [], remaining_items: ["报告可复核"], updated_at: "2026-09-12T00:00:00Z" },
  budget: { max_steps: 3, max_tool_calls: 1, max_output_bytes: 1024, timeout_seconds: 30 },
  source_conversation_id: "conv", source_run_id: "source", progress: { steps: 1, model_calls: 1, tool_calls: 1, input_tokens: 20, output_tokens: 4, duration_ms: 120 },
  diagnostic: { state: "progressing", action: "continue", reasons: [], execution_attempts: 1, agreement_revisions: 1, repeated_tool_calls: 0, verified_items: 0, remaining_items: 1, stage_outcomes: 0, remaining_stages: 0, open_dependencies: 0, basis: "server facts" }, control,
  waiting, artifacts: [], artifacts_complete: true, created_at: "2026-09-12T00:00:00Z", updated_at: "2026-09-12T00:00:00Z",
});

test("task paths encode IDs and bind list filters", () => {
  assert.equal(taskPath("task:a/b"), "/agent/conversation-tasks/task%3Aa%2Fb");
  assert.equal(taskControlPath("task:a/b", "resume"), "/agent/conversation-tasks/task%3Aa%2Fb/resume");
	assert.equal(taskAgreementPath("task:a/b"), "/agent/conversation-tasks/task%3Aa%2Fb/agreement");
	assert.equal(taskPlansPath("task:a/b", 4), "/agent/conversation-tasks/task%3Aa%2Fb/plans?before_version=4");
	assert.equal(taskCompletionsPath("task:a/b", 3), "/agent/conversation-tasks/task%3Aa%2Fb/completions?before_revision=3");
	assert.equal(taskCompletionReviewPath("task:a/b"), "/agent/conversation-tasks/task%3Aa%2Fb/completion-review");
  const path = taskListPath({ query: "发布 A&B", status: "running", sourceConversationID: "conv/1", cursor: "next+1", limit: 10 });
  const url = new URL(path, "https://agent.invalid");
  assert.equal(url.pathname, "/agent/conversation-tasks");
  assert.deepEqual(Object.fromEntries(url.searchParams), { query: "发布 A&B", status: "running", cursor: "next+1", limit: "10", source_conversation_id: "conv/1" });
});

test("task plan retains versioned execution states", () => {
	assert.equal(taskPlanStatusLabel("in_progress"), "执行中");
	assert.equal(taskPlanStatusLabel("needs_review"), "待核实");
});

test("task diagnostic keeps execution activity separate from business progress", () => {
  const running = task("running");
  assert.equal(running.progress.model_calls, 1);
  assert.equal(running.diagnostic.remaining_items, 1);
  assert.equal(taskDiagnosticActionLabel.adjust_strategy, "调整执行策略");
  assert.equal(taskDiagnosticReasonLabel("repeated_tool_calls"), "发现相同工具与参数的重复调用");
  assert.equal(taskDiagnosticReasonLabel("blocker:work_budget_exhausted"), "当前阻塞：work_budget_exhausted");
});

test("task agreement exposes business progress, provenance and update eligibility", () => {
  const queued = task("queued");
  assert.equal(taskGoalStatusLabel(queued.goal_progress.status), "目标推进中");
  assert.equal(taskBriefFieldSource(queued.brief!, "goal"), "用户明确");
  assert.equal(taskBriefFieldSource(queued.brief!, "audience"), "Agent 推断");
  assert.equal(taskBriefFieldSource({ ...queued.brief!, explicit_fields: [], inferred_fields: [] }, "goal"), "来源未记录");
  assert.equal(taskAgreementCanUpdate(queued), true);
  assert.equal(taskAgreementCanUpdate(task("running")), false);
  assert.equal(taskAgreementCanUpdate({ ...queued, delegation_id: "delegation" }), false);
});

test("changed completion conditions do not inherit an old program rule", () => {
	const brief = { ...task("queued").brief!, completion_conditions: ["金额字段正确", "原操作已完成"], verification_rules: [
		{ condition: 0, kind: "data" as const, schema: { type: "object" } },
		{ condition: 1, kind: "receipt" as const, tool: "report_save" },
	] };
	assert.deepEqual(taskVerificationRulesForConditions(brief, ["金额字段正确", "原操作已完成"]), brief.verification_rules);
	assert.deepEqual(taskVerificationRulesForConditions(brief, ["金额和币种字段正确", "原操作已完成"]), [brief.verification_rules![1]]);
	assert.deepEqual(taskVerificationRulesForConditions(brief, ["新增核对项", "金额字段正确", "原操作已完成"]), []);
});

test("task display distinguishes durable execution from waiting state", () => {
  assert.equal(taskStatusLabel(task("queued")), "等待开始");
  assert.equal(taskStatusLabel(task("running", { can_cancel: true, can_resume: false, resume_blocker: "interaction_response_required" }, { id: "i", kind: "confirmation", question: "确认？", revision: 1, expires_at: "2026-09-12T01:00:00Z" })), "等待操作确认");
  assert.equal(taskStatusLabel(task("completed")), "已完成");
  assert.equal(taskStatusLabel(task("awaiting_review")), "等待验收");
  assert.equal(taskNeedsRefresh(task("running")), true);
  assert.equal(taskNeedsRefresh(task("completed")), false);
  assert.equal(taskCanCancel(task("running", { can_cancel: true, can_resume: false })), true);
  assert.equal(taskCanCancel(task("completed")), false);
  assert.equal(taskCanResume(task("failed", { can_cancel: false, can_resume: true })), true);
  assert.equal(taskCanResume(task("running", { can_cancel: true, can_resume: false, resume_blocker: "interaction_response_required" }, { id: "i", kind: "input", question: "渠道？", revision: 1, expires_at: "2026-09-12T01:00:00Z" })), false);
  assert.equal(taskCanResume(task("running", { can_cancel: true, can_resume: true })), true);
});
