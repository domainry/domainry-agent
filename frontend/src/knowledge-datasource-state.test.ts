import assert from "node:assert/strict";
import test from "node:test";
import { ApiError } from "./errors.ts";
import { setSession, type AppSession } from "./session.ts";
import { bindLibrarySource, parsePendingDatasource, readLibrarySources } from "./knowledge-datasource-state.ts";

const session: AppSession = { mode: "identity", scope: "r/w/a", runtime_id: "r", workspace_id: "w", user_id: "a", name: "A", must_change_password: false, ready: true, model: "fixture" };
const library = "lib_" + "a".repeat(32);
const pending = { datasource_key: "approved", expected_revision: 4 };
test("binding receipts contain only a bounded source key and frozen revision", () => {
  assert.deepEqual(parsePendingDatasource(JSON.stringify({ ...pending, api_key: "discard", permission_ids: ["discard"] })), pending);
  for (const value of [null, [], {}, { ...pending, datasource_key: "../forged" }, { ...pending, datasource_key: [] }, { ...pending, expected_revision: 0 }, { ...pending, expected_revision: 4.1 }]) assert.equal(parsePendingDatasource(JSON.stringify(value)), null);
  assert.equal(parsePendingDatasource("x".repeat(1025)), null);
});

test("a confirmed binding is one PUT and a lost response retries the same frozen assignment", async () => {
  const original = globalThis.fetch;
  try {
    setSession(session);
    const requests: { url: string; body: unknown; method: string | undefined }[] = [];
    let lost = true;
    globalThis.fetch = async (input, init) => {
      requests.push({ url: String(input), body: JSON.parse(String(init?.body)), method: init?.method });
      assert.equal(new Headers(init?.headers).get("X-Agent-Scope"), session.scope);
      if (lost) { lost = false; throw new TypeError("lost acknowledgment"); }
      return Response.json({ id: library, datasource_key: "approved", revision: 5, documents_configured: true, knowledge_configured: true });
    };
    const signal = new AbortController().signal;
    await assert.rejects(bindLibrarySource(library, pending, signal));
    assert.equal(requests.length, 1, "unknown result was automatically repeated");
    assert.equal((await bindLibrarySource(library, parsePendingDatasource(JSON.stringify(pending))!, signal)).revision, 5);
    assert.equal(requests.length, 2, "success caused a hidden fetch fanout");
    assert.deepEqual(requests[0], requests[1]);
    assert.equal(requests[0].method, "PUT");
    assert.deepEqual(requests[0].body, pending);
    assert.equal(requests[0].url, `/agent/knowledge-libraries/${library}/source`);
    for (const bad of [{ id: library, datasource_key: "wrong" }, { id: "wrong", datasource_key: "approved" }]) {
      globalThis.fetch = async () => Response.json({ revision: 5, documents_configured: true, knowledge_configured: true, ...bad });
      await assert.rejects(bindLibrarySource(library, pending, signal), (e: unknown) => e instanceof ApiError && e.code.endsWith("datasource_response_invalid"));
    }
  } finally { globalThis.fetch = original; setSession(null); }
});

test("catalog validates scope, pagination and effective permission flags", async () => {
  const original = globalThis.fetch;
  try {
    setSession(session);
    const page = { library_id: library, revision: 4, status: "unbound", items: [{ key: "approved", name: "资料", available: true }], complete: true, can_bind: true };
    let calls = 0;
    globalThis.fetch = async (input, init) => {
      calls++; const url = new URL(String(input), "https://test.invalid");
      assert.equal(url.searchParams.get("after"), "earlier"); assert.equal(url.searchParams.get("limit"), "10"); assert.equal(init?.method, "GET");
      return Response.json(page);
    };
    assert.deepEqual(await readLibrarySources(library, "earlier", new AbortController().signal), page);
    assert.equal(calls, 1);
    for (const change of [{ library_id: "other" }, { can_bind: "yes" }, { complete: false }, { next_after: "../forged" }, { items: [page.items[0], page.items[0]] }]) {
      globalThis.fetch = async () => Response.json({ ...page, ...change });
      await assert.rejects(readLibrarySources(library, "", new AbortController().signal), (e: unknown) => e instanceof ApiError && e.code.endsWith("datasource_response_invalid"));
    }
  } finally { globalThis.fetch = original; setSession(null); }
});
