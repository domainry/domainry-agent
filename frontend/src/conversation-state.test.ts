import assert from "node:assert/strict";
import { test } from "node:test";
import { DraftStore } from "./drafts.ts";
import { ApiError, errorMessage } from "./errors.ts";
import { request } from "./api.ts";
import { sessionFetch, setSession, type AppSession } from "./session.ts";
import { applyExecutionEvent, liveStepText, type ExecutionEvent } from "./execution-state.ts";
import type { Run } from "./api.ts";
import { applyInteractionEvent, resumable, waiting, type Interaction } from "./interaction-state.ts";
import { parseTodoMutation, todoDeadline } from "./todo-state.ts";
import { parseArtifactMutation } from "./artifact-state.ts";
import { citationMarkdown, runCitations, safeCitationURL, type Citation } from "./knowledge-state.ts";

test("only actual completed knowledge citations become verified links and revocation hides them", () => {
  const citation: Citation = { id: "kc_1234567890abcdef1234567890abcdef", provider: "test", kb_id: "kb", operation: "search", doc_id: "policy", title: "费用规则", excerpt: "金额 9007199254740993.01" };
  const run = { id: "run", conversation_id: "chat", status: "running", user_seq: 1, attempt: 1, draft_bytes: 0, last_event_seq: 2, steps: [{ number: 0, attempt: 1, text: "", status: "tools", calls: [{ id: "search", name: "knowledge_search", arguments: "{}", status: "running" }] }] } as Run;
  const completed = applyExecutionEvent(run, {seq: 3, type: "tool.completed", data: { attempt: 1, step: 0, call_id: "search", status: "completed", citations: [citation], result_truncated: true }});
  assert.deepEqual(runCitations(completed), [citation]);
  const text = `依据 [[cite:${citation.id}]]；虚构 [[cite:kc_00000000000000000000000000000000]]`;
  assert.equal(citationMarkdown(text, runCitations(completed)), `依据 [来源 1](#knowledge-${citation.id})；虚构 【引用未验证】`);
  assert.deepEqual(runCitations({...completed, access_error: "source_access_unavailable"}), []);
  assert.equal(citationMarkdown(text, []), "依据 【引用未验证】；虚构 【引用未验证】");
  assert.equal(safeCitationURL("javascript:alert(1)"), undefined);
  assert.equal(safeCitationURL("https://user:secret@example.com/document"), undefined);
  assert.equal(safeCitationURL("https://example.com/document#section"), "https://example.com/document#section");
});

test("artifact scope survives uncertain send without borrowing memory/todo grants or carrying to the next request", () => {
  const disk = storage();
  const first = new DraftStore(() => disk, "artifact-user");
  first.writeArtifactScope("chat", true);
  const pending = first.pending("chat", "生成周报", false, false, true);
  const next = new DraftStore(() => disk, "artifact-user");
  assert.equal(next.read("chat").artifactWrite, true);
  assert.deepEqual(next.pending("chat", "生成周报", true, true, false), pending);
  assert.equal(pending.memoryWrite, undefined);
  assert.equal(pending.todoWrite, undefined);
  assert.equal(new DraftStore(() => disk, "different-user").read("chat").pending, undefined);
  next.acknowledge("chat", pending);
  assert.equal(next.read("chat").artifactWrite, undefined);
  assert.equal(next.pending("chat", "修改记忆", true, true).artifactWrite, undefined);
});

test("artifact retry preserves exact version and decimal strings and cannot target unrelated paths", () => {
  const edit = { kind: "edit", artifactID: "artifact_a", body: { client_id: "edit-retry", expected_version: 3, patch: { cells: [{ row: 0, column: "amount", value: "9007199254740993.01" }] } } };
  assert.deepEqual(parseArtifactMutation(JSON.stringify(edit)), edit);
  const exported = { kind: "export", artifactID: "artifact_a", body: { client_id: "export-retry", version: 1, format: "csv" } };
  assert.deepEqual(parseArtifactMutation(JSON.stringify(exported)), exported);
  for (const artifactID of ["../../identity", "https://example.com", "", "a?delete=1"]) assert.equal(parseArtifactMutation(JSON.stringify({ ...edit, artifactID })), null);
  assert.equal(parseArtifactMutation(JSON.stringify({ ...edit, body: { ...edit.body, expected_version: 0 } })), null);
  assert.equal(parseArtifactMutation(JSON.stringify({ ...exported, body: { ...exported.body, format: "html" } })), null);
});

test("todo and memory scopes stay separate through refresh, uncertain retry and next send", () => {
  const disk = storage();
  const drafts = new DraftStore(() => disk, "todo-scope-user");
  drafts.write("chat", "整理成待办");
  drafts.writeTodoScope("chat", true);
  const pending = drafts.pending("chat", "整理成待办", false, true);
  const refreshed = new DraftStore(() => disk, "todo-scope-user");
  assert.equal(refreshed.read("chat").todoWrite, true);
  assert.equal(refreshed.read("chat").memoryWrite, undefined);
  assert.deepEqual(refreshed.pending("chat", "整理成待办", true, false), pending);
  assert.equal(new DraftStore(() => disk, "another-user").read("chat").pending, undefined);
  refreshed.acknowledge("chat", pending);
  assert.equal(refreshed.read("chat").todoWrite, undefined);
  const next = refreshed.pending("chat", "第二项改到周五", true, false);
  assert.equal(next.todoWrite, undefined);
  assert.equal(next.memoryWrite, true);
  assert.notEqual(next.id, pending.id);
});

test("todo retry preserves the exact target, revision and payload and rejects unrelated endpoints", () => {
  const command = { path: "/agent/todos/todo_a", method: "PATCH", body: { client_id: "same-logical-edit", expected_revision: 3, patch: { due_date: "2026-09-11", description: "费用\\n明细" } }, label: "修改事项" };
  assert.deepEqual(parseTodoMutation(JSON.stringify(command)), command);
  assert.deepEqual(parseTodoMutation(JSON.stringify({ ...command, method: "DELETE" })), { ...command, method: "DELETE" });
  assert.ok(parseTodoMutation(JSON.stringify({ path: "/agent/todos", method: "POST", body: { client_id: "create", items: [{ title: "核对费用", timezone: "UTC" }] }, label: "创建事项" })));
  for (const path of ["/agent/conversations", "/agent/todos/../conversations", "https://example.com/agent/todos/id", "/agent/todos/id?extra=1", "/agent/todos", "/agent/todos/a/b"]) {
    assert.equal(parseTodoMutation(JSON.stringify({ ...command, path })), null);
  }
  for (const body of [null, [], {}, { client_id: "" }, { client_id: "id", expected_revision: 0 }]) assert.equal(parseTodoMutation(JSON.stringify({ ...command, body })), null);
  assert.equal(parseTodoMutation("{bad-json"), null);
  assert.equal(parseTodoMutation(JSON.stringify({ ...command, method: "POST" })), null);
});

test("todo date-only deadlines preserve the user's calendar day and precise times use the stored zone", () => {
  assert.equal(todoDeadline({ due_date: "2026-09-11", timezone: "America/Los_Angeles" }), "2026-09-11 · America/Los_Angeles");
  assert.equal(todoDeadline({ timezone: "UTC" }), "未设截止日期");
  const instant = "2026-09-11T00:30:00Z";
  assert.equal(todoDeadline({ due_at: instant, timezone: "America/Los_Angeles" }), new Date(instant).toLocaleString(undefined, { timeZone: "America/Los_Angeles", timeZoneName: "short" }));
});

test("waiting snapshots retain questions and reject cross-run or stale response events", () => {
  let run: Run = {id:"run",conversation_id:"chat",status:"running",attempt:1,user_seq:1,draft_bytes:0,last_event_seq:0};
  const question: Interaction = {id:"question",conversation_id:"chat",run_id:"run",step:0,call_id:"call",kind:"input",status:"pending",question:"格式？",choices:["简洁版","详细版"],tool:"ask_user",tool_version:"1",action_key:"agent.conversation_tools.ask_user",arguments:"{}",arguments_hash:"hash",definition_hash:"definition",revision:1,expires_at:"2026-09-11T00:00:00Z"};
  run=applyInteractionEvent(run,{seq:1,type:"run.waiting_user",data:{interaction:question}});
  run=JSON.parse(JSON.stringify(run)) as Run;
  assert.ok(waiting(run)); assert.equal(resumable(run),false);
  assert.deepEqual(run.interaction?.choices,["简洁版","详细版"]);
  assert.throws(()=>applyInteractionEvent(run,{seq:2,type:"interaction.responded",data:{interaction:{...question,run_id:"other",status:"answered",revision:2}}}));
  run=applyInteractionEvent(run,{seq:2,type:"interaction.responded",data:{interaction:{...question,status:"answered",answer:"详细版，按项目组织",revision:2}}});
  assert.equal(run.interaction?.answer,"详细版，按项目组织");
  assert.throws(()=>applyInteractionEvent(run,{seq:3,type:"run.waiting_user",data:{interaction:question}}));
  const closed={...run,status:"cancelled" as const,interaction:{...question,status:"cancelled" as const,revision:2}};
  assert.equal(resumable(closed),false);
  assert.equal(resumable({...closed,interaction:{...closed.interaction,kind:"reconciliation"}}),true);
});

test("a restricted snapshot ignores queued tool events and hides stale reply text", () => {
  const run: Run = { id: "run", conversation_id: "chat", status: "running", attempt: 1, user_seq: 1, draft_bytes: 6, last_event_seq: 10, access_error: "source_access_unavailable", draft_text: "secret", steps: [{ number: 0, attempt: 1, status: "generating", text: "secret", calls: [] }] };
  const event: ExecutionEvent = { seq: 11, type: "step.text.delta", data: { step: 0, attempt: 1, offset: 6, delta: " from a queued event" } };
  assert.equal(applyExecutionEvent(run, event), run);
  assert.equal(liveStepText(run), "");
  const restored = { ...run, access_error: undefined };
  assert.equal(applyExecutionEvent(restored, event).steps?.[0].text, "secret from a queued event");
});

test("a completed run can show its saved final step after the server clears the draft", () => {
  const run: Run = { id: "run", conversation_id: "chat", status: "completed", attempt: 1, user_seq: 1, draft_bytes: 0, last_event_seq: 10, draft_text: "", steps: [{ number: 0, attempt: 1, status: "tools", text: "查询中", calls: [] }, { number: 1, attempt: 1, status: "completed", text: "已保存的最终回复", calls: [] }] };
  assert.equal(liveStepText(run), "已保存的最终回复");
  assert.equal(liveStepText({ ...run, access_error: "source_access_unavailable" }), "");
});

test("tool argument fragments stay previews and reconnect resumes Unicode byte offsets", () => {
  let run: Run = {id:"run",conversation_id:"chat",status:"running",attempt:1,user_seq:1,draft_bytes:0,last_event_seq:0};
  const apply = (type:string,data:ExecutionEvent["data"]) => {run=applyExecutionEvent(run,{seq:1,type,data:{step:0,attempt:1,...data}})};
  apply("step.started",{});
  apply("step.tool.started",{index:0,call_id:"call",name:"calculate"});
  const args='{"说明":"金额",';
  apply("step.tool.arguments.delta",{index:0,call_id:"call",name:"calculate",offset:0,delta:args});
  assert.equal(run.steps![0].calls[0].status,"receiving");
  run=JSON.parse(JSON.stringify(run)) as Run; // a persisted server snapshot after refresh
  assert.throws(()=>apply("step.tool.arguments.delta",{index:0,call_id:"call",name:"calculate",offset:args.length,delta:'"expression":"0.1+0.2"}'}));
  const end='"expression":"0.1+0.2"}';
  apply("step.tool.arguments.delta",{index:0,call_id:"call",name:"calculate",offset:new TextEncoder().encode(args).length,delta:end});
  apply("step.completed",{finish_reason:"tool_calls",calls:[{id:"call",name:"calculate",arguments:args+end}]});
  assert.equal(run.steps![0].calls[0].status,"queued");
  apply("tool.started",{call_id:"call"});
  const reference = {conversation_id:"chat",run_id:"run",step:0,call_id:"call",sha256:"a".repeat(64)};
  apply("tool.completed",{call_id:"call",status:"completed",result_reference:reference,result_preview:'{"value":"0.30"}'});
  assert.deepEqual(JSON.parse(JSON.stringify(run)).steps[0].calls[0].result_reference,reference);
  assert.equal(run.steps![0].calls[0].result_preview,'{"value":"0.30"}');
  apply("step.started",{step:1});
  apply("step.text.delta",{step:1,offset:0,delta:"结果是 "});
  assert.equal(liveStepText(run),"结果是 ");
  run={...run,attempt:2};
  apply("step.attempt.started",{step:1,attempt:2});
  assert.equal(liveStepText(run),"");
  assert.equal(run.steps![0].calls[0].status,"completed");
  assert.throws(()=>apply("step.text.delta",{step:1,attempt:1,offset:0,delta:"旧响应"}));
});
const storage = () => {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
  };
};
test("drafts survive refresh and stay isolated by conversation and scope", () => {
  const disk = storage();
  const first = new DraftStore(() => disk, "user-a");
  first.write("one", "中文草稿\n第二行");
  first.write("two", "另一会话");
  const refreshed = new DraftStore(() => disk, "user-a");
  assert.equal(refreshed.read("one").text, "中文草稿\n第二行");
  assert.equal(refreshed.read("two").text, "另一会话");
  assert.equal(new DraftStore(() => disk, "user-b").read("one").text, "");
  refreshed.remove("one");
  assert.equal(refreshed.read("one").text, "");
  assert.equal(refreshed.read("two").text, "另一会话");
});
test("an uncertain send reuses its identity after refresh and acknowledgement clears only that draft", () => {
  const disk = storage();
  const first = new DraftStore(() => disk, "test");
  first.write("one", "  send me  ");
  first.write("two", "keep");
  const pending = first.pending("one", "send me");
  const refreshed = new DraftStore(() => disk, "test");
  assert.deepEqual(refreshed.pending("one", "send me"), pending);
  refreshed.acknowledge("one", pending);
  assert.equal(refreshed.read("one").text, "");
  assert.equal(refreshed.read("one").pending, undefined);
  assert.equal(refreshed.read("two").text, "keep");
});
test("personal memory scope stays with one logical send and cannot expand on retry", () => {
  const disk = storage();
  const first = new DraftStore(() => disk, "scope-test");
  first.write("chat", "记住格式");
  first.writeMemoryScope("chat", true);
  const pending = first.pending("chat", "记住格式", true);
  const refreshed = new DraftStore(() => disk, "scope-test");
  assert.equal(refreshed.read("chat").memoryWrite, true);
  assert.deepEqual(refreshed.pending("chat", "记住格式", false), pending);
  refreshed.acknowledge("chat", pending);
  assert.equal(refreshed.read("chat").memoryWrite, undefined);
  const next = refreshed.pending("chat", "忘记格式");
  assert.equal(next.memoryWrite, undefined);
  assert.deepEqual(refreshed.pending("chat", "忘记格式", true), next);
  assert.notEqual(next.id, pending.id);
});
test("late acknowledgement cannot erase newer text or another logical send from another tab", () => {
  const disk = storage();
  const one = new DraftStore(() => disk, "test");
  const two = new DraftStore(() => disk, "test");
  one.write("chat", "old");
  const old = one.pending("chat", "old");
  two.write("chat", "new text");
  one.acknowledge("chat", old);
  assert.equal(two.read("chat").text, "new text");
  const next = two.pending("chat", "new text");
  one.acknowledge("chat", old);
  assert.equal(two.read("chat").pending?.id, next.id);
});
test("disabled or full storage retains current-page drafts without stale disk text overwriting them", () => {
  const disk = storage();
  const first = new DraftStore(() => disk, "test");
  first.write("chat", "old");
  let full = true;
  const limited = {
    ...disk,
    setItem: (k: string, v: string) => {
      if (full) throw new Error("full");
      disk.setItem(k, v);
    },
  };
  const current = new DraftStore(() => limited, "test");
  current.write("chat", "new");
  assert.equal(current.available, false);
  assert.equal(current.read("chat").text, "new");
  current.write("two", "other");
  assert.equal(current.read("chat").text, "new");
  assert.equal(current.read("two").text, "other");
  full = false;
  const disabled = new DraftStore(() => {
    throw new Error("blocked");
  }, "test");
  disabled.write("chat", "kept");
  assert.equal(disabled.read("chat").text, "kept");
});
test("provider errors explain the next action and unknown provider text is never displayed", () => {
  assert.match(errorMessage("provider_quota_exhausted"), /额度/);
  assert.match(
    errorMessage("agent.conversation.provider_rate_limited"),
    /限流/,
  );
  assert.match(errorMessage("provider_timeout"), /超时/);
  assert.match(
    errorMessage("agent.conversation.revision_conflict", 409),
    /其他页面/,
  );
  assert.doesNotMatch(
    errorMessage("private-secret-provider-response", 500),
    /private-secret/,
  );
});
test("HTTP, network and malformed successful responses retain distinct safe error categories", async () => {
  const original = globalThis.fetch;
  try {
    globalThis.fetch = async () =>
      new Response("local cookie expired", { status: 403 });
    await assert.rejects(
      request("/agent/conversations"),
      (e: unknown) =>
        e instanceof ApiError && e.status === 403 && /身份/.test(e.message),
    );
    globalThis.fetch = async () => {
      throw new TypeError("secret endpoint URL");
    };
    await assert.rejects(
      request("/agent/conversations"),
      (e: unknown) =>
        e instanceof ApiError &&
        e.code === "network_error" &&
        !e.message.includes("secret"),
    );
    globalThis.fetch = async () => new Response("not JSON", { status: 200 });
    await assert.rejects(
      request("/agent/conversations"),
      (e: unknown) => e instanceof ApiError && e.code === "response_unreadable",
    );
  } finally {
    globalThis.fetch = original;
  }
});

const accountA: AppSession = { mode: "identity", scope: "runtime/workspace/A", runtime_id: "runtime", workspace_id: "workspace", user_id: "A", name: "A", must_change_password: false, ready: true, model: "test" };

test("refresh retries the same scoped request once and never replays a mutation under another account", async () => {
  const originalFetch = globalThis.fetch;
  const oldWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  const events = new EventTarget();
  Object.defineProperty(globalThis, "window", { configurable: true, value: events });
  let expired = 0;
  events.addEventListener("agent-session-expired", () => expired++);
  try {
    setSession(accountA);
    let sends = 0, refreshed = false;
    const identities: string[] = [];
    globalThis.fetch = (async (input, init) => {
      // Session ownership remains valid while Runtime rejects the token's
      // stale authorization revision. /app/session alone cannot repair it.
      if (input === "/app/session") return Response.json(accountA);
      if (input === "/auth/refresh") { refreshed = true; return Response.json({}); }
      sends++;
      identities.push(new Headers(init?.headers).get("X-Agent-Scope") || "");
      assert.equal(init?.body, '{"client_message_id":"unchanged"}');
      return Response.json({}, { status: sends === 1 ? 401 : 202 });
    }) as typeof fetch;
    assert.equal((await sessionFetch("/agent/conversations/a/messages", { method: "POST", body: '{"client_message_id":"unchanged"}' })).status, 202);
    assert.equal(sends, 2);
    assert.equal(refreshed, true);
    assert.deepEqual(identities, [accountA.scope, accountA.scope]);
    assert.equal(expired, 0);

    sends = 0;
    globalThis.fetch = (async (input) => {
      if (input === "/app/session") return Response.json({ ...accountA, user_id: "B", scope: "runtime/workspace/B" });
      sends++;
      return Response.json({}, { status: 401 });
    }) as typeof fetch;
    await assert.rejects(sessionFetch("/agent/conversations/a/messages", { method: "POST" }), (e: unknown) => e instanceof ApiError && e.code === "agent.web.identity_changed");
    assert.equal(sends, 1);
    assert.equal(expired, 1);
  } finally {
    globalThis.fetch = originalFetch;
    setSession(null);
    if (oldWindow) Object.defineProperty(globalThis, "window", oldWindow); else Reflect.deleteProperty(globalThis, "window");
  }
});

test("an in-flight response from a prior login is discarded after an account change", async () => {
  const originalFetch = globalThis.fetch;
  try {
    setSession(accountA);
    let finish!: (response: Response) => void;
    globalThis.fetch = (() => new Promise<Response>((resolve) => { finish = resolve; })) as typeof fetch;
    const pending = sessionFetch("/agent/conversations", { method: "GET" });
    setSession({ ...accountA, user_id: "B", scope: "runtime/workspace/B" });
    finish(Response.json({ items: ["A's private conversation"] }));
    await assert.rejects(pending, (e: unknown) => e instanceof ApiError && e.code === "agent.web.identity_changed");
  } finally { globalThis.fetch = originalFetch; setSession(null); }
});
