import type { Run } from "./api.ts";
import type { Interaction } from "./interaction-state.ts";
import type { Citation } from "./knowledge-state.ts";

export type ResultReference = { conversation_id: string; run_id: string; step: number; call_id: string; sha256: string };
export type ContextView = {
  window?: { limit_bytes: number; input_bytes: number; pressure_permille: number; provider_serialized: boolean };
  sources?: { key: string; kind: string; scope: string; refresh: string; version: string; order: number; stable_prefix?: boolean; updated_at: string }[];
  changes?: { key: string; previous_version: string; current_version: string; changed_at: string }[];
  compaction?: { version: number; results: number; intervals?: number; before_bytes: number; after_bytes: number };
  cache_read_input_tokens?: number;
  cache_creation_input_tokens?: number;
};
export type ToolView = {
	access_error?:string;
	 reused_from?: ResultReference;
  outcome_inspection?: { status: string; actor_id: string; checked_at: string };
  effect?: "read" | "write"; completion?: "accepted";
  citations?: Citation[];
  id: string; name: string; arguments: string; status: string;
  error_code?: string; resource_id?: string;
  result_preview?: string; result_truncated?: boolean; result_reference?: ResultReference | null;
  authorization?: { status: string; revision?: string; checks: number };
  confirmation?: { id: string; status: string; responded_by?: string; responded_at?: string };
  started_at?: string; completed_at?: string; duration_ms?: number;
};
export type StepView = {
  number: number; attempt: number; status: string; text: string; calls: ToolView[];
  tool_execution?: "parallel_read"; parallel_tool_calls?: number;
  context?: ContextView;
  usage?: Record<string, unknown>; started_at?: string; completed_at?: string; duration_ms?: number;
};
export type ExecutionEvent = {
  seq: number; type: string;
  data?: {
	 reference?: ResultReference;
    actor_id?: string; checked_at?: string;
    effect?: "read" | "write"; completion?: "accepted";
    citations?: Citation[];
    interaction?: Interaction;
    attempt?: number; step?: number; index?: number; offset?: number;
    text?: string; delta?: string; draft_reset?: boolean; call_id?: string;
    name?: string; tool?: string; status?: string; finish_reason?: string;
    calls?: { id: string; name: string; arguments: string }[];
    parallel_width?: number; parallel_calls?: number;
    usage?: Record<string, unknown>;
    error_code?: string; resource_id?: string;
    result_preview?: string; result_truncated?: boolean; result_reference?: ResultReference | null;
    context?: ContextView;
  };
};
export const executionEventNames = [
  "context.assembled",
  "step.started", "step.attempt.started", "step.text.delta", "step.tool.started",
  "step.tool.arguments.delta", "step.completed", "tool.started", "tool.completed", "tool.uncertain", "tool.waiting",
  "tool.inspection.started", "tool.inspection.completed",
  "tool.receipt.reused",
];
const bytes = (text: string) => new TextEncoder().encode(text).length;

// Projection mirrors the server's atomic run snapshot. Incomplete arguments
// are display text only; the browser never dispatches a model tool call.
export function applyExecutionEvent(run: Run, event: ExecutionEvent): Run {
  if (run.access_error) return run;
  if (event.type === "run.cancelled" || event.type === "run.failed") return {...run, steps: run.steps?.map(step => ({...step,
    status: step.status === "generating" ? "interrupted" : step.status,
    calls: step.calls.map(call => ({...call, status: ["receiving", "queued", "waiting_user", "waiting_confirmation"].includes(call.status) ? "not_started" : call.status === "running" ? "interrupted" : call.status})),
  }))};
  if (!executionEventNames.includes(event.type)) return run;
  const data = event.data || {};
  if (event.type === "context.assembled") {
    if (data.attempt !== run.attempt || !data.context) throw new Error("Invalid context event");
    return {...run, context: data.context};
  }
  if (!Number.isInteger(data.step) || data.step! < 0 || data.step! >= 256 || data.attempt !== run.attempt) throw new Error("Invalid step event");
  const steps = (run.steps || []).map(step => ({...step, calls: step.calls.map(call => ({...call}))}));
  let step = steps.find(step => step.number === data.step);
  if (!step) {
    if (event.type !== "step.started") throw new Error("Missing step snapshot");
    step = {number: data.step!, attempt: data.attempt!, text: "", status: "generating", calls: [], context: data.context};
    steps.push(step);
  }
  switch (event.type) {
    case "tool.receipt.reused": {
      const call=step.calls.find(call=>call.id===data.call_id);
      if (!call || !data.reference) throw new Error("Invalid reused receipt");
      call.reused_from=data.reference;break;
    }
    case "tool.inspection.started": case "tool.inspection.completed": {
      const call = step.calls.find(call => call.id === data.call_id);
      if (!call || !data.status || !data.actor_id || !data.checked_at) throw new Error("Invalid outcome inspection");
      call.outcome_inspection = {status: data.status, actor_id: data.actor_id, checked_at: data.checked_at}; break;
    }
    case "step.attempt.started":
      step.attempt = data.attempt!; step.text = ""; step.calls = []; step.tool_execution = undefined; step.parallel_tool_calls = undefined; step.status = "generating"; break;
    case "step.text.delta":
      if (step.attempt !== data.attempt || step.status !== "generating" || data.offset !== bytes(step.text) || typeof data.delta !== "string") throw new Error("Invalid text offset");
      step.text += data.delta; break;
    case "step.tool.started":
      if (step.attempt !== data.attempt || step.status !== "generating" || data.index !== step.calls.length || !data.call_id || !data.name || step.calls.some(c => c.id === data.call_id)) throw new Error("Invalid tool preview");
      step.calls.push({id: data.call_id, name: data.name, arguments: "", status: "receiving"}); break;
    case "step.tool.arguments.delta": {
      const call = step.calls[data.index ?? -1];
      if (step.attempt !== data.attempt || step.status !== "generating" || !call || call.id !== data.call_id || call.name !== data.name || data.offset !== bytes(call.arguments) || typeof data.delta !== "string") throw new Error("Invalid argument offset");
      call.arguments += data.delta; break;
    }
    case "step.completed":
      step.text = data.text || ""; step.status = data.finish_reason === "tool_calls" ? "tools" : "completed"; step.usage = data.usage;
      step.calls = (data.calls || []).map(call => ({...call, status: "queued"}));
	  step.tool_execution = undefined; step.parallel_tool_calls = undefined;
      if (step.context) {
        const details = data.usage?.input_tokens_details as Record<string, unknown> | undefined;
        const promptDetails = data.usage?.prompt_tokens_details as Record<string, unknown> | undefined;
        const read = data.usage?.cache_read_input_tokens ?? details?.cached_tokens ?? promptDetails?.cached_tokens ?? data.usage?.cached_tokens;
        const creation = data.usage?.cache_creation_input_tokens;
        if (typeof read === "number" && Number.isSafeInteger(read) && read >= 0) step.context.cache_read_input_tokens = read;
        if (typeof creation === "number" && Number.isSafeInteger(creation) && creation >= 0) step.context.cache_creation_input_tokens = creation;
      }
      if ((data.parallel_width || 0) > 1 && (data.parallel_calls || 0) > 1) {
        step.tool_execution = "parallel_read"; step.parallel_tool_calls = data.parallel_calls;
      }
      break;
    case "tool.started": case "tool.completed": case "tool.uncertain": case "tool.waiting": {
      const call = step.calls.find(call => call.id === data.call_id);
      if (!call) throw new Error("Unknown tool execution");
      call.status = data.status || "running";
      if (data.effect) call.effect = data.effect;
      call.completion = data.completion;
      call.error_code = data.error_code; call.resource_id = data.resource_id;
      call.citations = data.citations; call.result_reference = data.result_reference; call.result_preview = data.result_preview; call.result_truncated = data.result_truncated; break;
    }
  }
  return {...run, steps};
}

export function liveStepText(run: Run): string {
  if (run.access_error) return "";
  if (run.draft_text) return run.draft_text;
  return run.steps?.at(-1)?.text || "";
}
