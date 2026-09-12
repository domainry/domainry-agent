import { request } from "./api.ts";
import { ApiError } from "./errors.ts";
import type { KnowledgeLibrary } from "./knowledge-document-state.ts";

export type DatasourceChoice = { key: string; name: string; description?: string; available: boolean };
export type LibrarySources = { library_id: string; revision: number; current_key?: string; status: "unbound" | "connected" | "unavailable" | "host_managed"; items: DatasourceChoice[]; next_after?: string; complete: boolean; can_bind: boolean };
export type PendingDatasource = { datasource_key: string; expected_revision: number };
const sourceKey = /^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,95}$/;
export function parsePendingDatasource(raw: string | null): PendingDatasource | null {
  if (!raw || raw.length > 1024) return null;
  try {
    const value = JSON.parse(raw) as PendingDatasource;
    return typeof value?.datasource_key === "string" && sourceKey.test(value.datasource_key) && Number.isSafeInteger(value.expected_revision) && value.expected_revision > 0 ? { datasource_key: value.datasource_key, expected_revision: value.expected_revision } : null;
  } catch { return null; }
}
export async function readLibrarySources(library: string, after: string, signal: AbortSignal): Promise<LibrarySources> {
  const page = await request<LibrarySources>(`/agent/knowledge-libraries/${encodeURIComponent(library)}/sources?${new URLSearchParams({ after, limit: "10" })}`, "GET", undefined, signal);
  if (!page || page.library_id !== library || !Number.isSafeInteger(page.revision) || page.revision < 1 || !["unbound", "connected", "unavailable", "host_managed"].includes(page.status) || !Array.isArray(page.items) || page.items.length > 10 || page.items.some(item => !item || typeof item.key !== "string" || !sourceKey.test(item.key) || typeof item.name !== "string" || !item.name || typeof item.available !== "boolean") || typeof page.complete !== "boolean" || typeof page.can_bind !== "boolean" || new Set(page.items.map(item => item.key)).size !== page.items.length || page.current_key !== undefined && (typeof page.current_key !== "string" || !sourceKey.test(page.current_key)) || page.next_after !== undefined && (typeof page.next_after !== "string" || !sourceKey.test(page.next_after)) || (!page.complete && !page.next_after) || (page.complete && !!page.next_after)) throw new ApiError("agent.conversation.datasource_response_invalid");
  return page;
}
export async function bindLibrarySource(library: string, pending: PendingDatasource, signal: AbortSignal): Promise<KnowledgeLibrary> {
  const result = await request<KnowledgeLibrary>(`/agent/knowledge-libraries/${encodeURIComponent(library)}/source`, "PUT", pending, signal);
  if (!result || result.id !== library || result.datasource_key !== pending.datasource_key || !result.knowledge_configured || !result.documents_configured || !Number.isSafeInteger(result.revision) || result.revision < 1) throw new ApiError("agent.conversation.datasource_response_invalid");
  return result;
}
