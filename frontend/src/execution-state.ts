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
	parent_call_id?: string; dispatch_index?: number; subcalls?: ToolView[];
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
  model_attempts?:number;retry_at?:string;retry_delay_ms?:number;last_model_error?:string;
};
export type ExecutionEvent = {
  seq: number; type: string;
	 data?: {
	 parent_call_id?: string; dispatch_index?: number;
	 reference?: ResultReference;
    actor_id?: string; checked_at?: string;
    effect?: "read" | "write"; completion?: "accepted";
    citations?: Citation[];
    interaction?: Interaction;
    attempt?: number; model_attempt?:number;retry_at?:string;retry_delay_ms?:number;started_at?:string;completed_at?:string;step?: number; index?: number; offset?: number;
    text?: string; delta?: string; draft_reset?: boolean; call_id?: string; arguments?: string;
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
  "model.attempt.started", "model.attempt.failed", "model.retry.scheduled", "model.attempt.completed",
  "step.started", "step.attempt.started", "step.text.delta", "step.tool.started",
  "step.tool.arguments.delta", "step.completed", "tool.queued", "tool.started", "tool.completed", "tool.uncertain", "tool.waiting",
  "tool.inspection.started", "tool.inspection.completed",
  "tool.receipt.reused",
];
const bytes = (text: string) => new TextEncoder().encode(text).length;
const cloneTool = (call: ToolView): ToolView => ({...call, subcalls: call.subcalls?.map(cloneTool)});
function findTool(calls: ToolView[], id?: string): ToolView | undefined {
  if (!id) return undefined;
  for (const call of calls) {
    if (call.id === id) return call;
    const nested = findTool(call.subcalls || [], id);
    if (nested) return nested;
  }
}
function interruptTools(calls: ToolView[]): ToolView[] {
  return calls.map(call => ({...call,
    status: ["receiving", "queued", "waiting_user", "waiting_confirmation"].includes(call.status) ? "not_started" : call.status === "running" ? "interrupted" : call.status,
    subcalls: interruptTools(call.subcalls || []),
  }));
}

// Projection mirrors the server's atomic run snapshot. Incomplete arguments
// are display text only; the browser never dispatches a model tool call.
export function applyExecutionEvent(run: Run, event: ExecutionEvent): Run {
  if (run.access_error) return run;
  if (event.type === "run.cancelled" || event.type === "run.failed") return {...run, steps: run.steps?.map(step => ({...step,
    status: step.status === "generating" || step.status === "failed" && !!step.last_model_error ? "interrupted" : step.status,
    calls: interruptTools(step.calls),
  }))};
  if (!executionEventNames.includes(event.type)) return run;
  const data = event.data || {};
  if (event.type === "context.assembled") {
    if (data.attempt !== run.attempt || !data.context) throw new Error("Invalid context event");
    return {...run, context: data.context};
  }
  if (event.type.startsWith("model.")) {
    if (!Number.isInteger(data.step) || data.step! < -257 || data.step! >= 256 || data.attempt !== run.attempt || !Number.isInteger(data.model_attempt) || data.model_attempt! < 1) throw new Error("Invalid model attempt event");
    const attempts=[...(run.model_attempts||[])];let index=-1;
    for(let i=attempts.length-1;i>=0;i--)if(attempts[i].step===data.step&&attempts[i].run_attempt===data.attempt){index=i;break;}
    if(event.type==="model.attempt.started"){
      if(!data.started_at||(index>=0&&attempts[index].number>=data.model_attempt!))throw new Error("Invalid model attempt order");
      attempts.push({step:data.step!,run_attempt:data.attempt!,number:data.model_attempt!,status:"started",started_at:data.started_at});
    }else{
      if(index<0||attempts[index].number!==data.model_attempt)throw new Error("Missing model attempt");
      const item={...attempts[index]};
      if(event.type==="model.attempt.failed"){item.status="failed";item.error_code=data.error_code;item.usage=data.usage;item.completed_at=data.completed_at;}
      if(event.type==="model.retry.scheduled"){item.status="retry_scheduled";item.retry_at=data.retry_at;item.retry_delay_ms=data.retry_delay_ms;}
      if(event.type==="model.attempt.completed"){item.status="completed";item.usage=data.usage;item.completed_at=data.completed_at;}
      attempts[index]=item;
    }
    if(data.step===-1&&(event.type==="model.attempt.started"||event.type==="model.attempt.failed"))return {...run,model_attempts:attempts,draft_text:"",draft_bytes:0};
    if(data.step!<0)return {...run,model_attempts:attempts};
    const steps=(run.steps||[]).map(step=>({...step,calls:step.calls.map(cloneTool)}));const step=steps.find(step=>step.number===data.step);
    if(!step)throw new Error("Missing step snapshot");
    if(event.type==="model.attempt.started"){step.model_attempts=data.model_attempt;step.text="";step.calls=[];step.status="generating";step.retry_at=undefined;step.retry_delay_ms=undefined;step.last_model_error=undefined;}
    if(event.type==="model.attempt.failed"){step.text="";step.calls=[];step.status="failed";step.last_model_error=data.error_code;}
    if(event.type==="model.retry.scheduled"){step.status="retry_wait";step.retry_at=data.retry_at;step.retry_delay_ms=data.retry_delay_ms;}
    return {...run,model_attempts:attempts,steps};
  }
  if (!Number.isInteger(data.step) || data.step! < 0 || data.step! >= 256 || data.attempt !== run.attempt) throw new Error("Invalid step event");
  const steps = (run.steps || []).map(step => ({...step, calls: step.calls.map(cloneTool)}));
  let step = steps.find(step => step.number === data.step);
  if (!step) {
    if (event.type !== "step.started") throw new Error("Missing step snapshot");
    step = {number: data.step!, attempt: data.attempt!, text: "", status: "generating", calls: [], context: data.context};
    steps.push(step);
  }
  switch (event.type) {
    case "tool.receipt.reused": {
      const call=findTool(step.calls, data.call_id);
      if (!call || !data.reference) throw new Error("Invalid reused receipt");
      call.reused_from=data.reference;break;
    }
    case "tool.inspection.started": case "tool.inspection.completed": {
      const call = findTool(step.calls, data.call_id);
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
    case "tool.queued": case "tool.started": case "tool.completed": case "tool.uncertain": case "tool.waiting": {
      let call = findTool(step.calls, data.call_id);
      if (!call && data.parent_call_id) {
        const parent = findTool(step.calls, data.parent_call_id);
        if (!parent || data.dispatch_index !== (parent.subcalls || []).length || !data.call_id || !data.tool) throw new Error("Invalid nested tool execution");
        parent.subcalls = [...(parent.subcalls || []), {parent_call_id: data.parent_call_id, dispatch_index: data.dispatch_index, id: data.call_id, name: data.tool, arguments: data.arguments || "", status: "queued", effect: data.effect}];
        call = parent.subcalls.at(-1);
      }
      if (!call) throw new Error("Unknown tool execution");
	  if (event.type === "tool.queued") break;
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
