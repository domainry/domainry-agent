import assert from "node:assert/strict";
import test from "node:test";
import { taskCanCancel, taskCanResume, taskControlPath, taskListPath, taskNeedsRefresh, taskPath, taskStatusLabel, type ConversationTaskSummary } from "./task-state.ts";

const task = (status: ConversationTaskSummary["status"], control: ConversationTaskSummary["control"] = { can_cancel: false, can_resume: false }, waiting?: ConversationTaskSummary["waiting"]): ConversationTaskSummary => ({
  id: "task:a/b", status, goal: "核对发布", allowed_tools: ["time_now"],
  budget: { max_steps: 3, max_tool_calls: 1, max_output_bytes: 1024, timeout_seconds: 30 },
  source_conversation_id: "conv", source_run_id: "source", progress: { steps: 1, tool_calls: 1 }, control,
  waiting, artifacts: [], artifacts_complete: true, created_at: "2026-09-12T00:00:00Z", updated_at: "2026-09-12T00:00:00Z",
});

test("task paths encode IDs and bind list filters", () => {
  assert.equal(taskPath("task:a/b"), "/agent/conversation-tasks/task%3Aa%2Fb");
  assert.equal(taskControlPath("task:a/b", "resume"), "/agent/conversation-tasks/task%3Aa%2Fb/resume");
  const path = taskListPath({ query: "发布 A&B", status: "running", sourceConversationID: "conv/1", cursor: "next+1", limit: 10 });
  const url = new URL(path, "https://agent.invalid");
  assert.equal(url.pathname, "/agent/conversation-tasks");
  assert.deepEqual(Object.fromEntries(url.searchParams), { query: "发布 A&B", status: "running", cursor: "next+1", limit: "10", source_conversation_id: "conv/1" });
});

test("task display distinguishes durable execution from waiting state", () => {
  assert.equal(taskStatusLabel(task("queued")), "等待开始");
  assert.equal(taskStatusLabel(task("running", { can_cancel: true, can_resume: false, resume_blocker: "interaction_response_required" }, { id: "i", kind: "confirmation", question: "确认？", revision: 1, expires_at: "2026-09-12T01:00:00Z" })), "等待操作确认");
  assert.equal(taskStatusLabel(task("completed")), "已完成");
  assert.equal(taskNeedsRefresh(task("running")), true);
  assert.equal(taskNeedsRefresh(task("completed")), false);
  assert.equal(taskCanCancel(task("running", { can_cancel: true, can_resume: false })), true);
  assert.equal(taskCanCancel(task("completed")), false);
  assert.equal(taskCanResume(task("failed", { can_cancel: false, can_resume: true })), true);
  assert.equal(taskCanResume(task("running", { can_cancel: true, can_resume: false, resume_blocker: "interaction_response_required" }, { id: "i", kind: "input", question: "渠道？", revision: 1, expires_at: "2026-09-12T01:00:00Z" })), false);
  assert.equal(taskCanResume(task("running", { can_cancel: true, can_resume: true })), true);
});
