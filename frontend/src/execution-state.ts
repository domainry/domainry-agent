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
export type ToolProgressView = {
  key: string;
  label: string;
  detail?: string;
  optional?: boolean;
  index: number;
  total: number;
  status: "planned" | "started" | "completed" | "skipped" | "failed";
  updated_at: string;
};
export type ToolView = {
	parent_call_id?: string; dispatch_index?: number; subcalls?: ToolView[];
	access_error?:string;
	 reused_from?: ResultReference;
  outcome_inspection?: { status: string; actor_id: string; checked_at: string };
  progress?: ToolProgressView[];
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
	 created_at?: string;
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
		kind?: "history_summary" | "history_rebuild" | "execution_context";
		from_seq?: number; through_seq?: number; messages?: number; results?: number; intervals?: number; before_bytes?: number; after_bytes?: number; rebuild?: boolean;
    progress_key?: string; detail?: string; updated_at?: string;
    key?: string; label?: string;
    stages?: { key: string; label: string; detail?: string; optional?: boolean; binding?: { kind: string; tools?: string[]; progress_key?: string } }[];
  };
};
export const executionEventNames = [
  "run.plan.created", "run.progress",
  "context.assembled", "context.compacted", "context.tools_compacted",
  "model.attempt.started", "model.attempt.failed", "model.retry.scheduled", "model.attempt.completed",
  "step.started", "step.attempt.started", "step.text.delta", "step.tool.started",
  "step.tool.arguments.delta", "step.completed", "tool.queued", "tool.started", "tool.completed", "tool.uncertain", "tool.waiting",
  "tool.inspection.started", "tool.inspection.completed",
  "tool.plan.created", "tool.progress",
  "tool.receipt.reused",
];
const bytes = (text: string) => new TextEncoder().encode(text).length;
const cloneTool = (call: ToolView): ToolView => ({...call, progress: call.progress?.map(item => ({...item})), subcalls: call.subcalls?.map(cloneTool)});
const validExecutionText = (value: unknown, limit: number, required: boolean) =>
  typeof value === "string" && bytes(value) <= limit && !value.includes("\0") && (!required || value.trim() !== "");
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
  if (event.type === "context.compacted") {
		const rebuild = data.kind === "history_rebuild";
		if (data.attempt !== run.attempt || data.kind !== "history_summary" && !rebuild || rebuild !== (data.rebuild === true) || !Number.isSafeInteger(data.from_seq) || data.from_seq! < 1 || !Number.isSafeInteger(data.through_seq) || data.through_seq! < data.from_seq! || !Number.isSafeInteger(data.messages) || data.messages! < 1 || !Number.isSafeInteger(data.before_bytes) || !Number.isSafeInteger(data.after_bytes) || data.after_bytes! < 1 || !rebuild && data.after_bytes! >= data.before_bytes! || !validExecutionText(event.created_at, 128, true)) throw new Error("Invalid history compaction event");
		const kind = rebuild ? "history_rebuild" : "history_summary";
		return {...run, compactions:[...(run.compactions || []), {kind,step:-1,from_seq:data.from_seq,through_seq:data.through_seq,messages:data.messages,before_bytes:data.before_bytes!,after_bytes:data.after_bytes!,rebuild,occurred_at:event.created_at!}]};
	}
	if (event.type === "context.tools_compacted") {
		if (data.attempt !== run.attempt || data.kind !== "execution_context" || !Number.isSafeInteger(data.step) || data.step! < 0 || !Number.isSafeInteger(data.results) || data.results! < 0 || !Number.isSafeInteger(data.intervals) || data.intervals! < 0 || data.results! + data.intervals! < 1 || !Number.isSafeInteger(data.before_bytes) || !Number.isSafeInteger(data.after_bytes) || data.after_bytes! < 1 || data.after_bytes! >= data.before_bytes! || !validExecutionText(event.created_at, 128, true)) throw new Error("Invalid execution compaction event");
		return {...run, compactions:[...(run.compactions || []), {kind:"execution_context",step:data.step!,results:data.results,intervals:data.intervals,before_bytes:data.before_bytes!,after_bytes:data.after_bytes!,occurred_at:event.created_at!}]};
	}
  if (event.type === "context.assembled") {
    if (data.attempt !== run.attempt || !data.context) throw new Error("Invalid context event");
    return {...run, context: data.context};
  }
  if (event.type === "run.plan.created") {
    if (data.attempt !== run.attempt || !validExecutionText(data.key,96,true) || !validExecutionText(data.label,160,true) || !Array.isArray(data.stages) || data.stages.length < 1 || data.stages.length > 32 || !validExecutionText(data.updated_at,128,true)) throw new Error("Invalid run plan");
    // The durable snapshot wins over replayed creation events. Reinitializing
    // here would visibly move a completed plan back to its first step.
    if (run.plan) return run;
    const keys=new Set<string>();
    const steps=data.stages.map((stage,index)=>{
      if(!validExecutionText(stage.key,96,true)||keys.has(stage.key)||!validExecutionText(stage.label,160,true)||!validExecutionText(stage.detail||"",512,false))throw new Error("Invalid run plan");
      keys.add(stage.key);
      return {...stage,binding:stage.binding||{kind:""},index:index+1,total:data.stages!.length,status:"planned",updated_at:data.updated_at!};
    });
    return {...run,plan:{key:data.key!,label:data.label!,steps,created_at:data.updated_at!,updated_at:data.updated_at!}};
  }
  if (event.type === "run.progress") {
    const status=data.status;
    if(data.attempt!==run.attempt||!run.plan||!validExecutionText(data.progress_key,96,true)||!validExecutionText(status,64,true)||!validExecutionText(data.call_id||"",128,false)||!validExecutionText(data.detail||"",512,false)||!validExecutionText(data.updated_at,128,true)||!status||!["started","waiting_user","waiting_confirmation","completed","skipped","failed"].includes(status))throw new Error("Invalid run progress");
    const steps=run.plan.steps.map(step=>({...step}));const index=steps.findIndex(item=>item.key===data.progress_key);const step=steps[index];
    if(!step)throw new Error("Missing run plan step");
    if(step.binding.kind==="model"){
      if(data.call_id)throw new Error("Invalid run progress call");
    }else if(status==="skipped"){
      if(data.call_id||step.call_id)throw new Error("Invalid run progress call");
    }else if(!data.call_id||step.call_id&&step.call_id!==data.call_id)throw new Error("Invalid run progress call");
    if(step.status===status){
      if(step.binding.kind!=="model"&&!step.call_id)step.call_id=data.call_id;
      if(data.detail)step.detail=data.detail;
      step.updated_at=data.updated_at!;
      return {...run,plan:{...run.plan,steps,updated_at:data.updated_at!}};
    }
    if(["completed","skipped","failed"].includes(step.status))return run;
    if(status==="started"){
      if(!["planned","waiting_user","waiting_confirmation"].includes(step.status)||steps.slice(0,index).some(previous=>previous.status!=="completed"&&previous.status!=="skipped"))throw new Error("Invalid run progress order");
    }else if(status==="waiting_user"||status==="waiting_confirmation"){
      if(step.status!=="started")throw new Error("Invalid run progress order");
    }else if(status==="completed"||status==="failed"){
      if(!["started","waiting_user","waiting_confirmation"].includes(step.status))throw new Error("Invalid run progress order");
    }else if(status==="skipped"&&step.status!=="planned")throw new Error("Invalid run progress order");
    step.status=status;if(data.detail)step.detail=data.detail;step.updated_at=data.updated_at!;
    if(step.binding.kind!=="model"&&!step.call_id)step.call_id=data.call_id;
    return {...run,plan:{...run.plan,steps,updated_at:data.updated_at!}};
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
    case "tool.plan.created": {
      const call = findTool(step.calls, data.call_id);
      const stages = data.stages;
      if (!call || !Array.isArray(stages) || stages.length < 1 || stages.length > 32 || !validExecutionText(data.updated_at, 128, true)) throw new Error("Invalid tool progress plan");
      const keys = new Set<string>();
      for (const stage of stages) {
        if (!validExecutionText(stage.key, 64, true) || !validExecutionText(stage.label, 160, true) || !validExecutionText(stage.detail || "", 512, false) || keys.has(stage.key)) throw new Error("Invalid tool progress plan");
        keys.add(stage.key);
      }
      if (call.progress?.length) {
        if (call.progress.length !== stages.length || stages.some((stage, index) => {
          const current = call.progress![index];
          return current.key !== stage.key || current.label !== stage.label || (current.detail || "") !== (stage.detail || "") || Boolean(current.optional) !== (stage.optional === true) || current.index !== index + 1 || current.total !== stages.length;
        })) throw new Error("Tool progress plan changed");
        break;
      }
      call.progress = stages.map((stage, index) => ({
        key: stage.key,
        label: stage.label,
        detail: stage.detail || undefined,
        ...(stage.optional === true ? {optional: true} : {}),
        index: index + 1,
        total: stages.length,
        status: "planned",
        updated_at: data.updated_at!,
      }));
      break;
    }
    case "tool.progress": {
      const call = findTool(step.calls, data.call_id);
      const status = data.status;
      if (!call || !validExecutionText(data.progress_key, 64, true) || !validExecutionText(data.detail || "", 512, false) || !validExecutionText(data.updated_at, 128, true) || status !== "started" && status !== "completed" && status !== "skipped" && status !== "failed") throw new Error("Invalid tool progress");
      const index = call.progress?.findIndex(item => item.key === data.progress_key) ?? -1;
      if (index < 0) throw new Error("Tool progress was not planned");
      const progress = call.progress![index];
      if (progress.status === status) {
        progress.detail = data.detail || undefined;
        progress.updated_at = data.updated_at!;
        break;
      }
      if ((progress.status === "completed" || progress.status === "skipped" || progress.status === "failed") && status === "started") break;
      if (status === "started") {
		if (progress.status !== "planned" || call.progress!.slice(0, index).some(previous => previous.status !== "completed" && previous.status !== "skipped")) throw new Error("Invalid tool progress order");
	  } else if (status === "skipped") {
		if (!progress.optional || progress.status !== "planned") throw new Error("Invalid tool progress order");
      } else if (progress.status !== "started") {
        throw new Error("Invalid tool progress order");
      }
      progress.detail = data.detail || undefined;
      progress.status = status;
      progress.updated_at = data.updated_at!;
      break;
    }
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
