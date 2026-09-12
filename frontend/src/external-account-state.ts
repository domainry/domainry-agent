import { IntegrationClient } from "@domainry/integration-client";
import { request } from "./api.ts";

// Owner SDK defines every Integration path and payload. The product supplies
// the same-origin, session-bound request transport only.
export const accountsClient = new IntegrationClient({
  request: (path, options) => request(path, options?.method, options?.body, options?.signal),
});

export type PendingAuthorization = { id: string; scope: string; returnHash: string };
const key = "domainry:oauth:pending";
export function loadAuthorization(scope?: string): PendingAuthorization | null {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(key) || "null");
    if (!value || typeof value !== "object" || !("id" in value) || typeof value.id !== "string" || !/^oauth_[a-zA-Z0-9_-]+$/.test(value.id) || !("scope" in value) || typeof value.scope !== "string" || (scope && value.scope !== scope)) return null;
    return { id: value.id, scope: value.scope, returnHash: "returnHash" in value && typeof value.returnHash === "string" && /^#[a-zA-Z0-9_-]*$/.test(value.returnHash) ? value.returnHash : "" };
  } catch { return null; }
}
export function saveAuthorization(value: PendingAuthorization) {
  sessionStorage.setItem(key, JSON.stringify(value));
}
export function forgetAuthorization() { sessionStorage.removeItem(key); }

export function authorizationTarget(raw: string) {
  const target = new URL(raw);
  if (target.protocol !== "https:" || target.username || target.password || !["accounts.google.com", "login.microsoftonline.com"].includes(target.hostname) || (target.port && target.port !== "443")) throw new Error("authorization_target_invalid");
  return target.href;
}

export function accountFailure(error: unknown) {
  const status = error && typeof error === "object" && "status" in error ? error.status : undefined;
  if (status === 401) return "登录已过期，请重新登录后查看账号状态。";
  if (status === 403) return "你当前没有这项账号操作的权限，请联系工作空间管理员。";
  if (status === 400 || status === 409) return "配置或状态已变化，请刷新后核对权限、应用配置与授权范围。";
  if (status === 503 || status === 404) return "外部账号服务暂不可用。";
  return "暂时无法确认操作结果。请刷新并核对账号或授权状态。";
}

export const authorizationLabels: Record<string, string> = {
  pending: "等待完成授权", exchanging: "正在确认授权结果", connected: "账号已连接",
  rejected: "授权已拒绝", expired: "授权已过期", needs_reauthorization: "需要重新授权",
};
