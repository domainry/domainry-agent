import assert from "node:assert/strict";
import { test } from "node:test";
import type { Run } from "./api.ts";
import { applyExecutionEvent, type ToolView } from "./execution-state.ts";
import { executionOutcome, outcomeInspectionTargets, recoveryTarget, repairRequest } from "./execution-outcome.ts";

const call = (id: string, status: string, effect: "read" | "write" = "write"): ToolView => ({id, name:"fixture", arguments:"{}", status, effect});

test("a reused receipt retains its original identity across streamed completion and stopping", () => {
  const initial = run("running", [call("reuse", "queued")]);
  const receipt = {conversation_id:"original-conversation",run_id:"original-run",step:0,call_id:"original-call",sha256:"original-hash"};
  const completed = applyExecutionEvent(initial,{seq:1,type:"tool.completed",data:{attempt:1,step:0,call_id:"reuse",status:"completed",effect:"write",resource_id:"existing"}});
  const reused = applyExecutionEvent(completed,{seq:2,type:"tool.receipt.reused",data:{attempt:1,step:0,call_id:"reuse",reference:receipt}});
  const stopped = {...applyExecutionEvent(reused,{seq:3,type:"run.cancelled"}),status:"cancelled" as const};
  assert.equal(stopped.steps![0].calls[0].status,"completed");
  assert.deepEqual(stopped.steps![0].calls[0].reused_from,receipt);
  assert.equal(stopped.steps![0].calls[0].resource_id,"existing");
  assert.deepEqual(outcomeInspectionTargets(stopped.status,stopped.steps),[]);
  assert.equal(initial.steps![0].calls[0].reused_from,undefined);
});

test("receipt inspection leaves execution stopped and cannot create a retry target for known effects", () => {
  const r = run("cancelled", [call("unknown","interrupted"),call("known","completed"),call("read","interrupted","read"),call("unstarted","not_started")]);
  assert.deepEqual(outcomeInspectionTargets(r.status,r.steps).map(x=>x.call.id),["unknown"]);
  assert.deepEqual(outcomeInspectionTargets("running",r.steps),[]);
  const started = applyExecutionEvent(r,{seq:1,type:"tool.inspection.started",data:{attempt:1,step:0,call_id:"unknown",status:"reading",actor_id:"owner",checked_at:"2026-09-13T10:00:00Z"}});
  assert.equal(started.status,"cancelled");
  assert.equal(started.steps![0].calls[0].status,"interrupted");
  const result = applyExecutionEvent(started,{seq:2,type:"tool.completed",data:{attempt:1,step:0,call_id:"unknown",status:"completed",effect:"write",resource_id:"existing",result_preview:'{"id":"existing"}'}});
  const done = applyExecutionEvent(result,{seq:3,type:"tool.inspection.completed",data:{attempt:1,step:0,call_id:"unknown",status:"completed",actor_id:"owner",checked_at:"2026-09-13T10:00:01Z"}});
  assert.equal(done.status,"cancelled");
  assert.equal(done.steps![0].calls[0].resource_id,"existing");
  assert.equal(done.steps![0].calls[0].outcome_inspection?.status,"completed");
  assert.deepEqual(outcomeInspectionTargets(done.status,done.steps),[]);
});
function run(status: Run["status"], calls: ToolView[]): Run {
  return {id:"source-run",conversation_id:"conversation",status,attempt:1,user_seq:1,draft_bytes:0,last_event_seq:0,steps:[{number:0,attempt:1,status:"tools",text:"",calls}]};
}

test("a saved reply does not hide a failed operation or mistake workflow acceptance for completion", () => {
  const r = run("completed",[call("done","completed"),call("failed","failed"),{...call("flow","completed"),completion:"accepted"}]);
  const result = executionOutcome(r);
  assert.equal(result.title,"部分完成");
  assert.equal(result.counts.completed,1);
  assert.equal(result.counts.accepted,1);
  assert.equal(result.counts.failed,1);
  assert.equal(recoveryTarget(r),null,"completed receipts cannot be retried by resuming their old Run");
  assert.match(repairRequest(r,0,r.steps![0].calls[1],"写入"),/source-run；步骤：0；调用：failed/);
  assert.equal(executionOutcome(run("completed",[{...call("flow","completed"),completion:"accepted"}])).title,"调用已结束，含已受理请求");
  assert.equal(executionOutcome(run("completed",[call("failed","failed")])).title,"工具执行失败");
});

test("failure projects actual effects and directs recovery to the first unfinished call", () => {
  const initial = run("running",[call("done","completed"),call("read","running","read"),call("later","queued")]);
  const failed = {...applyExecutionEvent(initial,{seq:1,type:"run.failed"}),status:"failed" as const};
  assert.deepEqual(failed.steps![0].calls.map(c=>c.status),["completed","interrupted","not_started"]);
  assert.equal(executionOutcome(failed).title,"部分完成，后续处理失败");
  assert.deepEqual(recoveryTarget(failed),{kind:"resume",step:0,callID:"read"});
  const uncertain = run("cancelled",[call("done","completed"),call("write","interrupted"),call("later","queued")]);
  assert.equal(executionOutcome(uncertain).title,"部分完成，仍有结果待核查");
  assert.deepEqual(recoveryTarget(uncertain),{kind:"reconcile",step:0,callID:"write"});
  const old = run("failed",[{...call("old","interrupted"),effect:undefined}]);
  assert.equal(executionOutcome(old).counts.unconfirmed,1,"missing old metadata is conservative");
});

test("unfinished model generation can resume without repeating successful effects", () => {
  const initial = run("running",[call("done","completed")]);
  initial.steps!.push({number:1,attempt:1,status:"generating",text:"partial",calls:[]});
  const failed = {...applyExecutionEvent(initial,{seq:1,type:"run.failed"}),status:"failed" as const};
  assert.equal(failed.steps![1].status,"interrupted");
  assert.deepEqual(recoveryTarget(failed),{kind:"resume",step:1});
  assert.equal(executionOutcome(failed).counts.completed,1);
});

test("waiting approval, denied evidence and closed interactions expose no retry", () => {
  const waiting = run("waiting_confirmation",[call("first","waiting_confirmation"),call("second","queued")]);
  assert.equal(executionOutcome(waiting).title,"等待操作确认");
  assert.equal(executionOutcome(waiting).counts.waiting,1);
  assert.equal(executionOutcome(waiting).counts.unstarted,1);
  assert.equal(recoveryTarget(waiting),null);
  const denied = {...run("failed",[call("secret","completed")]),access_error:"tool_not_authorized"};
  assert.equal(recoveryTarget(denied),null);
  assert.equal(executionOutcome(denied).counts.completed,0);
  const rejected = {...run("cancelled",[call("write","not_started")]),interaction:{kind:"confirmation",status:"rejected"} as Run["interaction"]};
  assert.equal(recoveryTarget(rejected),null);
});

test("streamed completion disposition survives terminal projection and unknown terminal states are never successes", () => {
  const initial = run("running",[call("flow","running")]);
  const finished = applyExecutionEvent(initial,{seq:1,type:"tool.completed",data:{attempt:1,step:0,call_id:"flow",status:"completed",effect:"write",completion:"accepted"}});
  const failed = {...applyExecutionEvent(finished,{seq:2,type:"run.failed"}),status:"failed" as const};
  assert.equal(executionOutcome(failed).counts.accepted,1);
  assert.equal(failed.steps![0].calls[0].effect,"write");
  assert.equal(executionOutcome(run("completed",[call("unknown","future-status")])).counts.unconfirmed,1);
});
