import type { Artifact } from "./artifact-state.ts";
import type { StepView } from "./execution-state.ts";
import type { Interaction } from "./interaction-state.ts";

export type ConversationTaskStatus = "queued" | "running" | "completed" | "failed" | "cancelled";
export type ConversationTaskProgress = { run_status?: string; attempt?: number; steps: number; tool_calls: number; last_event_seq?: number };
export type ConversationTaskWaiting = { id: string; kind: string; question: string; tool?: string; revision: number; expires_at: string };
export type ConversationTaskResult = { message_id: string; preview: string; bytes: number; complete: boolean };
export type ConversationTaskControlState = { can_cancel: boolean; can_resume: boolean; resume_blocker?: string };
export type ConversationTaskSummary = {
  id: string; status: ConversationTaskStatus; goal: string; allowed_tools: string[];
  budget: { max_steps: number; max_tool_calls: number; max_output_bytes: number; timeout_seconds: number };
  source_conversation_id: string; source_run_id: string; execution_run_id?: string;
  progress: ConversationTaskProgress; control: ConversationTaskControlState; waiting?: ConversationTaskWaiting; result?: ConversationTaskResult;
  artifacts: Artifact[]; artifacts_complete: boolean; artifacts_omitted?: boolean; access_error?: string;
  completion_event_id?: string; completion_event_seq?: number; error_code?: string;
  created_at: string; updated_at: string; completed_at?: string;
};
export type ConversationTaskDetail = ConversationTaskSummary & { input: string; steps: StepView[]; interaction?: Interaction };
export type ConversationTaskPage = { items: ConversationTaskSummary[]; next_cursor?: string; complete: boolean };

export const taskPath = (id: string) => `/agent/conversation-tasks/${encodeURIComponent(id)}`;
export const taskControlPath = (id: string, action: "cancel" | "resume") => `${taskPath(id)}/${action}`;
export const taskNeedsRefresh = (task: ConversationTaskSummary) => task.status === "queued" || task.status === "running";
export const taskCanCancel = (task: ConversationTaskSummary) => task.control.can_cancel;
export const taskCanResume = (task: ConversationTaskSummary) => task.control.can_resume;
export const taskStatusLabel = (task: ConversationTaskSummary) => {
  if (task.waiting?.kind === "input") return "等待补充信息";
  if (task.waiting?.kind === "confirmation") return "等待操作确认";
  if (task.waiting?.kind === "reconciliation") return "等待结果核查";
  return ({ queued: "等待开始", running: "正在处理", completed: "已完成", failed: "处理失败", cancelled: "已停止" } as const)[task.status];
};

export function taskListPath(input: { query?: string; status?: string; sourceConversationID?: string; cursor?: string; limit?: number }) {
  const params = new URLSearchParams({ query: input.query || "", status: input.status || "", cursor: input.cursor || "", limit: String(input.limit || 20) });
  if (input.sourceConversationID) params.set("source_conversation_id", input.sourceConversationID);
  return `/agent/conversation-tasks?${params}`;
}
