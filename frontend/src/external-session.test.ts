import test from "node:test";
import assert from "node:assert/strict";
import { configureAuthentication, restoreSession, sessionFetch, setSession, type AppSession } from "./session.ts";
import { ApiError } from "./errors.ts";

test("external sessions never refresh Identity or replay writes after an account change", async () => {
  const originalFetch = globalThis.fetch;
  const oldWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  const events = new EventTarget();
  Object.defineProperty(globalThis, "window", { configurable: true, value: events });
  const a: AppSession = { mode: "identity", scope: "a", runtime_id: "r", workspace_id: "wa", user_id: "a", name: "A", must_change_password: false, ready: true, model: "test" };
  const paths: string[] = [];
  let expired = 0;
  events.addEventListener("agent-session-expired", () => expired++);
  try {
    configureAuthentication("external");
    setSession(a);
    globalThis.fetch = (async (path) => { paths.push(String(path)); return Response.json({}, {status: 401}); }) as typeof fetch;
    await assert.rejects(restoreSession(), (e: unknown) => e instanceof ApiError && e.status === 401);
    assert.deepEqual(paths, ["/app/session"]);
    paths.length = 0;
    await assert.rejects(sessionFetch("/agent/conversations", {method: "POST", body: "unchanged"}));
    assert.deepEqual(paths, ["/agent/conversations", "/app/session"]);
    assert.equal(expired, 1);
    paths.length = 0;
    globalThis.fetch = (async (path) => {
      paths.push(String(path));
      return path === "/app/session" ? Response.json({...a, scope: "b", workspace_id: "wb", user_id: "b"}) : Response.json({}, {status: 401});
    }) as typeof fetch;
    await assert.rejects(sessionFetch("/agent/conversations", {method: "POST"}), (e: unknown) => e instanceof ApiError && e.code === "agent.web.identity_changed");
    assert.deepEqual(paths, ["/agent/conversations", "/app/session"]);
    assert.equal(expired, 2);
  } finally {
    configureAuthentication("managed");
    setSession(null);
    globalThis.fetch = originalFetch;
    if (oldWindow) Object.defineProperty(globalThis, "window", oldWindow); else Reflect.deleteProperty(globalThis, "window");
  }
});
