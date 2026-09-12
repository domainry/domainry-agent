import { conversationPath } from "./api.ts";
import { ApiError } from "./errors.ts";
import { sessionFetch, sessionScope } from "./session.ts";

export const attachmentMaxBytes = 16 * 1024 * 1024;
export const attachmentAccept = ".pdf,.doc,.docx,.xls,.xlsx,.txt,.md,.csv,.tsv,.json";
export type Attachment = { id: string; conversation_id: string; filename: string; content_type: string; bytes: number; sha256: string; visibility: string; state: "uploading" | "stored" | "indexing" | "ready" | "failed" | "needs_reconcile" | "deleting" | "deleted"; revision: number; error_code?: string; index_status?: string; indexing?: { requested: boolean; can_start: boolean; can_check: boolean; max_bytes?: number; reason?: string; last_check_revision?: number }; updated_at: string };
export type AttachmentPage = { items: Attachment[]; next_after?: string; complete: boolean };
// Apply one mutation response only within the current server cursor window.
// A removed boundary still remains a valid keyset cursor for the next page.
export function updateAttachmentPage(page: AttachmentPage, result: Attachment, after: string, add = false): AttachmentPage {
  let items = page.items.map(item => item.id === result.id ? result : item).filter(item => !["deleting", "deleted"].includes(item.state));
  if (add && result.id > after && !["deleting", "deleted"].includes(result.state) && !items.some(item => item.id === result.id) && (page.complete || !!page.next_after && result.id <= page.next_after)) {
    items = [...items, result].sort((a, b) => a.id.localeCompare(b.id));
  }
  if (items.length > 20) return { items: items.slice(0, 20), complete: false, next_after: items[19].id };
  return { ...page, items };
}
export type PendingAttachment = { clientID: string; filename: string; bytes: number; sha256: string };
export const attachmentPath = (conversationID: string, id?: string) => `${conversationPath(conversationID)}/attachments${id ? `/${encodeURIComponent(id)}` : ""}`;
export const attachmentLabel = (state: Attachment["state"]) => ({ uploading: "上传未完成", stored: "已保存 · 未入库", indexing: "正在建立索引", ready: "可检索", failed: "处理失败", needs_reconcile: "上传结果待核查", deleting: "正在清理", deleted: "已删除" })[state] || "状态未知";
export function parsePendingAttachment(raw: string | null): PendingAttachment | null {
  try {
    const value = JSON.parse(raw || "null") as PendingAttachment | null;
    if (!value || typeof value.clientID !== "string" || !/^[a-zA-Z0-9_-]{1,96}$/.test(value.clientID) || typeof value.filename !== "string" || !value.filename || value.filename.length > 255 || !Number.isSafeInteger(value.bytes) || value.bytes < 1 || value.bytes > attachmentMaxBytes || typeof value.sha256 !== "string" || !/^[a-f0-9]{64}$/.test(value.sha256)) return null;
    return value;
  } catch { return null; }
}
export async function attachmentHash(data: ArrayBuffer) {
  return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", data)), n => n.toString(16).padStart(2, "0")).join("");
}
async function checked(response: Response) {
  if (!response.ok) { const data = await response.json().catch(() => ({})); throw new ApiError(typeof data.code === "string" ? data.code : "request_failed", response.status); }
  return response;
}
export async function uploadAttachment(conversationID: string, file: File, pending: PendingAttachment, signal: AbortSignal): Promise<Attachment> {
  const response = await checked(await sessionFetch(`${attachmentPath(conversationID)}?${new URLSearchParams({ client_id: pending.clientID, filename: file.name })}`, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/octet-stream" }, body: file, signal }));
  return response.json();
}
export async function readAttachment(value: Attachment, signal: AbortSignal): Promise<Blob> {
  const scope = sessionScope();
  const response = await checked(await sessionFetch(`${attachmentPath(value.conversation_id, value.id)}/content`, { method: "GET", credentials: "same-origin", cache: "no-store", signal }));
  const blob = await response.blob();
  const hash = await attachmentHash(await blob.arrayBuffer());
  if (scope !== sessionScope()) throw new ApiError("agent.web.identity_changed", 409);
  if (blob.size !== value.bytes || hash !== value.sha256) throw new ApiError("agent.conversation.attachment_content_mismatch");
  return blob;
}
