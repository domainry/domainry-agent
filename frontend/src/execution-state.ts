import type { Run } from "./api.ts";
import type { Interaction } from "./interaction-state.ts";
import type { Citation } from "./knowledge-state.ts";

export type ResultReference = { conversation_id: string; run_id: string; step: number; call_id: string; sha256: string };
export type ToolView = {
  citations?: Citation[];
  id: string; name: string; arguments: string; status: string;
  error_code?: string; resource_id?: string;
  result_preview?: string; result_truncated?: boolean; result_reference?: ResultReference | null;
};
export type StepView = {
  number: number; attempt: number; status: string; text: string; calls: ToolView[];
};
export type ExecutionEvent = {
  seq: number; type: string;
  data?: {
    citations?: Citation[];
    interaction?: Interaction;
    attempt?: number; step?: number; index?: number; offset?: number;
    text?: string; delta?: string; draft_reset?: boolean; call_id?: string;
    name?: string; tool?: string; status?: string; finish_reason?: string;
    calls?: { id: string; name: string; arguments: string }[];
    error_code?: string; resource_id?: string;
    result_preview?: string; result_truncated?: boolean; result_reference?: ResultReference | null;
  };
};
export const executionEventNames = [
  "step.started", "step.attempt.started", "step.text.delta", "step.tool.started",
  "step.tool.arguments.delta", "step.completed", "tool.started", "tool.completed", "tool.uncertain", "tool.waiting",
];
const bytes = (text: string) => new TextEncoder().encode(text).length;

// Projection mirrors the server's atomic run snapshot. Incomplete arguments
// are display text only; the browser never dispatches a model tool call.
export function applyExecutionEvent(run: Run, event: ExecutionEvent): Run {
  if (run.access_error) return run;
  if (!executionEventNames.includes(event.type)) return run;
  const data = event.data || {};
  if (!Number.isInteger(data.step) || data.step! < 0 || data.step! >= 256 || data.attempt !== run.attempt) throw new Error("Invalid step event");
  const steps = (run.steps || []).map(step => ({...step, calls: step.calls.map(call => ({...call}))}));
  let step = steps.find(step => step.number === data.step);
  if (!step) {
    if (event.type !== "step.started") throw new Error("Missing step snapshot");
    step = {number: data.step!, attempt: data.attempt!, text: "", status: "generating", calls: []};
    steps.push(step);
  }
  switch (event.type) {
    case "step.attempt.started":
      step.attempt = data.attempt!; step.text = ""; step.calls = []; step.status = "generating"; break;
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
      step.text = data.text || ""; step.status = data.finish_reason === "tool_calls" ? "tools" : "completed";
      step.calls = (data.calls || []).map(call => ({...call, status: "queued"})); break;
    case "tool.started": case "tool.completed": case "tool.uncertain": case "tool.waiting": {
      const call = step.calls.find(call => call.id === data.call_id);
      if (!call) throw new Error("Unknown tool execution");
      call.status = data.status || "running";
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
