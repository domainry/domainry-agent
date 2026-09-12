import assert from "node:assert/strict";
import test from "node:test";
import { taskListPath, taskNeedsRefresh, taskPath, taskStatusLabel, type ConversationTaskSummary } from "./task-state.ts";

const task = (status: ConversationTaskSummary["status"], waiting?: ConversationTaskSummary["waiting"]): ConversationTaskSummary => ({
  id: "task:a/b", status, goal: "核对发布", allowed_tools: ["time_now"],
  budget: { max_steps: 3, max_tool_calls: 1, max_output_bytes: 1024, timeout_seconds: 30 },
  source_conversation_id: "conv", source_run_id: "source", progress: { steps: 1, tool_calls: 1 },
  waiting, artifacts: [], artifacts_complete: true, created_at: "2026-09-12T00:00:00Z", updated_at: "2026-09-12T00:00:00Z",
});

test("task paths encode IDs and bind list filters", () => {
  assert.equal(taskPath("task:a/b"), "/agent/conversation-tasks/task%3Aa%2Fb");
  const path = taskListPath({ query: "发布 A&B", status: "running", sourceConversationID: "conv/1", cursor: "next+1", limit: 10 });
  const url = new URL(path, "https://agent.invalid");
  assert.equal(url.pathname, "/agent/conversation-tasks");
  assert.deepEqual(Object.fromEntries(url.searchParams), { query: "发布 A&B", status: "running", cursor: "next+1", limit: "10", source_conversation_id: "conv/1" });
});

test("task display distinguishes durable execution from waiting state", () => {
  assert.equal(taskStatusLabel(task("queued")), "等待开始");
  assert.equal(taskStatusLabel(task("running", { id: "i", kind: "confirmation", question: "确认？", revision: 1, expires_at: "2026-09-12T01:00:00Z" })), "等待操作确认");
  assert.equal(taskStatusLabel(task("completed")), "已完成");
  assert.equal(taskNeedsRefresh(task("running")), true);
  assert.equal(taskNeedsRefresh(task("completed")), false);
});
