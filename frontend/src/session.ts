import { ApiError } from "./errors.ts";

export type AppSession = {
  mode: "local" | "identity";
  scope: string;
  runtime_id: string;
  workspace_id: string;
  user_id: string;
  name: string;
  must_change_password: boolean;
  ready: boolean;
  model: string;
};

let current: AppSession | null = null;
let generation = 0;
let refreshPending: Promise<AppSession> | undefined;
let refreshScope: string | undefined;
let externalAuthentication = false;

export function configureAuthentication(mode?: "managed" | "external") {
  externalAuthentication = mode === "external";
}

export function setSession(session: AppSession | null) {
  current = session;
  generation++;
}
export function sessionScope() { return current?.mode === "identity" ? current.scope : ""; }

// Web Locks serialize refresh-cookie rotation across tabs. No credential is
// stored in localStorage, sessionStorage, a URL, or a cross-tab message.
export async function sessionLock<T>(action: () => Promise<T>): Promise<T> {
  if (typeof navigator !== "undefined" && navigator.locks)
    return await navigator.locks.request("domainry-agent:identity-session", action);
  return action();
}

export async function sessionJSON<T>(path: string, method = "GET", body?: unknown): Promise<T> {
  const response = await fetch(path, { method, credentials: "same-origin", headers: body === undefined ? undefined : { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() }, body: body === undefined ? undefined : JSON.stringify(body) });
  const data = response.status === 204 ? undefined : await response.json().catch(() => ({}));
  if (!response.ok) throw new ApiError(typeof data?.code === "string" ? data.code : "request_failed", response.status);
  return data as T;
}

export async function restoreSession(requiredScope?: string): Promise<AppSession> {
  // A lightweight /app/session check may still succeed when the host rejects
  // the access token's old policy revision. That 401 needs a token refresh.
  if (refreshPending && requiredScope && refreshScope !== requiredScope) {
    await refreshPending;
    return restoreSession(requiredScope);
  }
  if (!refreshPending) {
    refreshScope = requiredScope;
    refreshPending = sessionLock(async () => {
      try {
        const session = await sessionJSON<AppSession>("/app/session");
        if (!requiredScope || session.scope !== requiredScope || externalAuthentication) return session;
      }
      catch (error) { if (!(error instanceof ApiError) || error.status !== 401) throw error; }
      if (externalAuthentication) throw new ApiError("agent.web.login_required", 401);
      await sessionJSON("/auth/refresh", "POST", {});
      return sessionJSON<AppSession>("/app/session");
    }).finally(() => { refreshPending = undefined; refreshScope = undefined; });
  }
  return refreshPending;
}

export async function sessionFetch(path: string, init: RequestInit): Promise<Response> {
  const expected = current;
  const started = generation;
  const headers = new Headers(init.headers);
  if (expected?.mode === "identity" && path.startsWith("/agent/")) headers.set("X-Agent-Scope", expected.scope);
  let response = await fetch(path, { ...init, headers });
  if (response.status === 401 && expected?.mode === "identity") {
    try {
      const refreshed = await restoreSession(expected.scope);
      if (generation !== started || refreshed.scope !== expected.scope) throw new ApiError("agent.web.identity_changed", 409);
      response = await fetch(path, { ...init, headers });
    } catch (error) {
      if (error instanceof ApiError && ([401, 403].includes(error.status || 0) || error.code === "agent.web.identity_changed"))
        window.dispatchEvent(new Event("agent-session-expired"));
      throw error;
    }
  }
  if (generation !== started) throw new ApiError("agent.web.identity_changed", 409);
  if (response.status === 409 && expected?.mode === "identity") {
    const data = await response.clone().json().catch(() => ({}));
    if (data.code === "agent.web.identity_changed") window.dispatchEvent(new Event("agent-session-expired"));
  }
  return response;
}
