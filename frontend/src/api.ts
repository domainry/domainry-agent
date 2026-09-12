import { ApiError } from "./errors.ts";
import { sessionFetch, sessionScope } from "./session.ts";
import { applyExecutionEvent, executionEventNames, type ExecutionEvent, type StepView } from "./execution-state.ts";
import { applyInteractionEvent, interactionEventNames, waiting, type Interaction } from "./interaction-state.ts";
import type { Citation } from "./knowledge-state.ts";
export type ConversationRecord = {
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
	citations?: Citation[];
	access_error?: string;
	background_task_id?: string;
  id: string;
  run_id: string;
  seq: number;
  role: "user" | "assistant";
  content: string;
};
export type Run = {
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
  created_at?: string;
  updated_at?: string;
  write_scope?: { personal_memory: boolean; personal_todos?: boolean; personal_artifacts?: boolean; background_tasks?: boolean };
  background_task?: { task_id: string; tool_scope: { key: string; version: string; action_key: string; definition_hash: string; authorization_revision?: string }[]; budget: { max_steps: number; max_tool_calls: number; max_output_bytes: number; timeout_seconds: number } };
};
export type Memory = {
  id: string;
  title: string;
  content: string;
  enabled: boolean;
  revision: number;
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
