import { ApiError } from "./errors.ts";
import { sessionFetch, sessionScope } from "./session.ts";
import { applyExecutionEvent, executionEventNames, type ContextView, type ExecutionEvent, type StepView } from "./execution-state.ts";
import { applyInteractionEvent, interactionEventNames, waiting, type Interaction } from "./interaction-state.ts";
import type { Citation } from "./knowledge-state.ts";
import type { ExecutionSubject } from "./collaboration-state.ts";
export type ConversationRecord = {
  agent_id?: string; delegation_id?: string;
	fork?: { conversation_id: string; run_id: string; boundary_event_seq: number; created_at: string };
  id: string;
  title: string;
  archived: boolean;
  memory_enabled: boolean;
  last_seq: number;
  active_run_id?: string;
  summary_id?: string;
  revision: number;
  updated_at: string;
};
export type MessageRecord = {
 peer_event?: { message_id: string; delegation_id: string; from_agent_id?: string; to_agent_id: string; kind: string };
	citations?: Citation[];
	access_error?: string;
	background_task_id?: string;
  id: string;
	conversation_id: string;
  run_id: string;
  seq: number;
  role: "user" | "assistant";
  content: string;
	content_blocks?: ContentBlock[];
};
export type ImageReference = { source?: RunReference; attachment_id: string; conversation_id: string; filename: string; content_type: string; bytes: number; sha256: string; revision: number; detail?: "auto" | "low" | "high" };
export type ContentBlock = { type: "text"; text: string } | { type: "image"; image: ImageReference };
export type Run = {
	agent?: {id:string;revision:number;owner_user_id?:string;execution_subject?:ExecutionSubject};
	access_error?: string;
  id: string;
  conversation_id: string;
  status: "queued" | "running" | "completed" | "cancelled" | "failed" | "waiting_user" | "waiting_confirmation" | "needs_reconciliation";
  user_seq: number;
  attempt: number;
  draft_text?: string;
  draft_bytes: number;
  last_event_seq: number;
  error_code?: string;
  steps?: StepView[];
  interaction?: Interaction;
  model?: string;
  usage?: Record<string, unknown>;
  model_attempts?: {step:number;run_attempt:number;number:number;status:string;error_code?:string;retry_at?:string;retry_delay_ms?:number;usage?:Record<string,unknown>;started_at:string;completed_at?:string}[];
  context?: ContextView;
  correlation_id?: string;
  metrics?: { steps: number; model_calls: number; model_retries:number; tool_calls: number; tool_attempts: number; parallel_tool_batches: number; parallel_tool_calls: number; peak_parallel_tools: number; authorization_checks: number; confirmation_decisions: number; context_compactions: number; compacted_results: number; compacted_intervals: number; peak_context_bytes: number; context_limit_bytes: number; cache_read_input_tokens: number; cache_creation_input_tokens: number };
  audit?: RunAuditEvent[];
  audit_complete?: boolean;
  started_at?: string;
  completed_at?: string;
  duration_ms?: number;
  queue_duration_ms?: number;
  created_at?: string;
  updated_at?: string;
  write_scope?: { personal_memory: boolean; personal_todos?: boolean; personal_artifacts?: boolean; background_tasks?: boolean };
  background_task?: { task_id: string; tool_scope: { key: string; version: string; action_key: string; definition_hash: string; authorization_revision?: string }[]; budget: { max_steps: number; max_tool_calls: number; max_output_bytes: number; timeout_seconds: number } };
};
export type RunAuditEvent = {
  seq: number;
  type: "run" | "context" | "step" | "model" | "tool" | "authorization" | "confirmation" | "interaction";
  status: string;
  step: number;
  attempt?: number;
  model_attempt?:number;
  retry_delay_ms?:number;
  call_id?: string;
  tool?: string;
  action_key?: string;
  authorization_revision?: string;
  interaction_id?: string;
  actor_id?: string;
  error_code?: string;
  duration_ms?: number;
  occurred_at: string;
};
export type RunReference = { conversation_id: string; run_id: string; before_step?: number };
export type TrajectoryMessage = { role: string; content: string; content_blocks?: ContentBlock[]; tool_calls?: { id: string; name: string; arguments: string }[]; tool_call_id?: string; is_error?: boolean };
export type TrajectoryToolDefinition = { definition: { key: string; version: string; description: string; input_schema: unknown; output_schema: unknown; action_key: string; effect: string; idempotency: string; parallelism?: string; timeout_ms: number; max_output_bytes: number }; sha256: string };
export type TrajectoryRequest = { index: number; step: number; purpose: string; model: { provider: string; protocol: string; model: string; fingerprint: string }; reasoning_effort?: string; messages: TrajectoryMessage[]; tools?: TrajectoryToolDefinition[]; context?: ContextView; sha256: string };
export type TrajectoryResponse = { request_index: number; step: number; finish_reason: string; model?: string; message: TrajectoryMessage; usage?: Record<string, unknown>; sha256: string };
export type TrajectoryTool = { step: number; call: { id: string; name: string; arguments: string }; definition: TrajectoryToolDefinition; state: string; result?: { completion?: string; status: string; content?: unknown; error_code?: string; resource_id?: string }; sha256: string };
export type ConversationTrajectory = { version: number; mode: "display"; source: RunReference; boundary_event_seq: number; run_status: string; requests: TrajectoryRequest[]; responses: TrajectoryResponse[]; tools: TrajectoryTool[]; sha256: string; recorded_at: string };
export type TrajectoryReplay = { mode: "display" | "model_fixture" | "live_rerun"; source: RunReference; trajectory_sha256: string; consumed_requests: number; recorded_responses?: TrajectoryResponse[]; recorded_tools?: TrajectoryTool[]; effects_executed: boolean; ready_for_input?: boolean; fork?: ConversationRecord };
export type TrajectoryComparison = { left: RunReference; right: RunReference; left_sha256: string; right_sha256: string; equal: boolean; differences: { kind: string; index: number; left_sha256?: string; right_sha256?: string }[] };
export type MemoryKind = "user_preference" | "project_fact" | "task_context";
export type MemoryScopeKind = "workspace" | "conversation" | "task";
export type Memory = {
  id: string;
  kind: MemoryKind;
  title: string;
  content: string;
  enabled: boolean;
  scope: { kind: MemoryScopeKind; conversation_id?: string; task_id?: string };
  applies_to: string[];
  source?: { kind: string; conversation_id?: string; message_id?: string; run_id?: string; task_id?: string; artifact_id?: string; artifact_version?: number; feedback_id?: string; captured_at: string };
  correction?: { previous_revision: number; reason: string };
  uncertainty?: string;
  revision: number;
  created_at: string;
  updated_at: string;
};
export type MessagePage = { items: MessageRecord[]; next_before_seq?: number };
export type ConversationPage = {
  items: ConversationRecord[];
  next_cursor?: string;
};
export const active = (run: Run | null) =>
  !!run && (run.status === "queued" || run.status === "running");
export async function request<T>(
  path: string,
  method = "GET",
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  let response: Response;
  try {
    response = await sessionFetch(path, {
      method,
      credentials: "same-origin",
      headers:
        body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    if (error instanceof ApiError) throw error;
    throw new ApiError("network_error");
  }
  let data: unknown;
  try {
    data = await response.json();
  } catch {
    if (response.ok) throw new ApiError("response_unreadable", response.status);
    data = {};
  }
  if (!response.ok) {
    const code =
      typeof data === "object" &&
      data !== null &&
      "code" in data &&
      typeof data.code === "string"
        ? data.code
        : "request_failed";
    throw new ApiError(code, response.status);
  }
  return data as T;
}
export const conversationPath = (id: string) =>
  `/agent/conversations/${encodeURIComponent(id)}`;
export const runPath = (id: string, runId: string) =>
  `${conversationPath(id)}/runs/${encodeURIComponent(runId)}`;
export const trajectoryPath = (id: string, runId: string) => `${runPath(id, runId)}/trajectory`;

export async function exportConversationTrajectory(id: string, runId: string, signal?: AbortSignal) {
  let response: Response;
  try {
    response = await sessionFetch(`${trajectoryPath(id, runId)}/export`, { credentials: "same-origin", signal });
  } catch (error) {
    if (signal?.aborted) throw error;
    if (error instanceof ApiError) throw error;
    throw new ApiError("network_error");
  }
  if (!response.ok) {
    let code = "request_failed";
    try {
      const value = await response.json() as { code?: string };
      if (value.code) code = value.code;
    } catch { /* use the generic code */ }
    throw new ApiError(code, response.status);
  }
  const disposition = response.headers.get("Content-Disposition") || "";
  const encoded = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
  const quoted = disposition.match(/filename="([^"]+)"/i)?.[1];
  const plain = disposition.match(/filename=([^;]+)/i)?.[1]?.trim().replace(/^"|"$/g, "");
  return {
    blob: await response.blob(),
    filename: encoded ? decodeURIComponent(encoded) : quoted || plain || `conversation-trajectory-${runId}.json`,
    sha256: response.headers.get("X-Content-SHA256") || "",
  };
}

// The server owns the stream. Reconnect from a persisted run snapshot; never
// repeat the message POST when an EventSource connection ends.
export function watchRun(
  initial: Run,
  onRun: (run: Run) => void,
  onDone: () => void,
  onError: (error: Error) => void,
) {
  let current = initial,
    stopped = false,
    source: EventSource | undefined,
    timer: ReturnType<typeof setTimeout> | undefined;
  const path = runPath(initial.conversation_id, initial.id);
  const scope = sessionScope();
  const reconnect = () => {
    source?.close();
    if (stopped) return;
    clearTimeout(timer);
    timer = setTimeout(async () => {
      try {
        const snapshot = await request<Run>(path);
        if (stopped) return;
        current = snapshot;
        onRun(current);
		if (active(current) || waiting(current)) { if (current.access_error) reconnect(); else connect(); }
        else onDone();
      } catch (error) {
        if (!stopped) {
          onError(error as Error);
          if (
            !(error instanceof ApiError) ||
            ![401, 403, 404].includes(error.status || 0)
          )
            reconnect();
        }
      }
    }, current.access_error ? 5000 : 750);
  };
  const receive = (raw: Event) => {
    if (stopped) return;
    if (current.access_error) { reconnect(); return; }
    try {
      const event = JSON.parse((raw as MessageEvent).data) as ExecutionEvent;
      if (event.seq <= current.last_event_seq) return;
      if (event.seq !== current.last_event_seq + 1) {
        reconnect();
        return;
      }
      const data = event.data || {};
      current = applyExecutionEvent(current, event);
      current = applyInteractionEvent(current, event);
      if (event.type === "message.delta") {
        if (
          data.attempt !== current.attempt ||
          data.offset !== current.draft_bytes ||
          typeof data.text !== "string"
        ) {
          reconnect();
          return;
        }
        current = {
          ...current,
          draft_text: (current.draft_text || "") + data.text,
          draft_bytes:
            current.draft_bytes + new TextEncoder().encode(data.text).length,
        };
      } else if (event.type === "run.started" || data.draft_reset) {
        current = {
          ...current,
          status: event.type === "run.started" ? "running" : "queued",
          attempt: data.attempt ?? current.attempt,
          draft_text: "",
          draft_bytes: 0,
        };
      }
      current = { ...current, last_event_seq: event.seq };
      if (
        ["run.completed", "run.cancelled", "run.failed"].includes(event.type)
      ) {
        source?.close();
        reconnect();
        return;
      }
      onRun(current);
    } catch {
      reconnect();
    }
  };
  function connect() {
    if (stopped) return;
	if (current.access_error) { reconnect(); return; }
    source = new EventSource(
      `${path}/events/stream?after_seq=${current.last_event_seq}&scope=${encodeURIComponent(scope)}`,
    );
    for (const name of [
      "run.queued",
      "run.started",
      "message.delta",
      "context.compacted",
      "context.tools_compacted",
      "run.completed",
      "run.cancelled",
      "run.failed",
      ...executionEventNames,
      ...interactionEventNames,
    ])
      source.addEventListener(name, receive);
    source.addEventListener("stream.error", reconnect);
    source.onerror = reconnect;
  }
  connect();
  return () => {
    stopped = true;
    source?.close();
    clearTimeout(timer);
  };
}
