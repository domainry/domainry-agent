import { ApiError } from "./errors.ts";
import { sessionFetch, sessionScope } from "./session.ts";
import { attachmentAccept, attachmentMaxBytes, attachmentHash, parsePendingAttachment, type PendingAttachment } from "./attachment-state.ts";

export const documentAccept = attachmentAccept;
export const documentMaxBytes = attachmentMaxBytes;
export const documentHash = attachmentHash;
export type PendingDocument = PendingAttachment;
export const parsePendingDocument = parsePendingAttachment;
export type KnowledgeLibrary = { knowledge_configured?: boolean; documents_configured?: boolean; sources_manageable?: boolean; datasource_key?: string; document_max_bytes?: number; id: string; kind: "personal" | "shared"; name: string; description: string; role: string; owner_user_id: string; archived: boolean; revision: number };
export const documentUploadMaxBytes = (library: KnowledgeLibrary) => Number.isSafeInteger(library.document_max_bytes) && library.document_max_bytes! > 0 ? Math.min(library.document_max_bytes!, documentMaxBytes) : documentMaxBytes;
export type KnowledgeDocument = { id: string; library_id: string; filename: string; content_type: string; bytes: number; sha256: string; created_by_user_id: string; state: "uploading" | "queued" | "indexing" | "ready" | "failed" | "needs_reconcile" | "deleting" | "deleted"; index_status?: string; error_code?: string; revision: number; created_at: string; updated_at: string };
export type KnowledgeDocumentPage = { items: KnowledgeDocument[]; next_after?: string; complete: boolean };
export const documentPath = (library: string, id?: string) => `/agent/knowledge-libraries/${encodeURIComponent(library)}/documents${id ? `/${encodeURIComponent(id)}` : ""}`;
export const documentCanWrite = (library: KnowledgeLibrary) => library.role === "editor" || library.role === "manager";
export const documentCanDownload = (doc: KnowledgeDocument) => ["queued", "indexing", "ready", "failed", "needs_reconcile"].includes(doc.state);
export function documentLabel(doc: Pick<KnowledgeDocument, "state" | "index_status" | "error_code">) {
  if (doc.state === "indexing" && doc.error_code === "document_index_failed") return "索引失败";
  return ({ uploading: "上传未完成", queued: "等待入库", indexing: "正在建立索引", ready: "可检索", failed: "处理未完成", needs_reconcile: "上传结果待核查", deleting: "已停止访问 · 正在清理", deleted: "已删除" })[doc.state] || "状态未知";
}
export function documentStatusDetail(doc: KnowledgeDocument) {
  if (doc.state === "deleting") return "已停止检索和下载；原文件与远端索引清理完成前，此记录会继续保留。";
  if (doc.state === "needs_reconcile") return "远端可能已收到文件，系统正在核对结果，不会盲目重复推送。长时间未更新时请联系管理员核查。";
  if (doc.error_code === "document_index_failed") return "知识服务未能完成索引，暂时不能检索；如原文件已保存，仍可下载查看。";
  if (doc.state === "indexing") return doc.index_status === "CHUNKED" ? "资料已完成分段，正在等待索引完成。" : "原文件已保存，等待知识服务完成索引后才可检索。";
  if (doc.state === "queued") return "原文件已保存，后台将继续处理；可以关闭窗口，稍后查看状态。";
  if (doc.state === "uploading") return "原文件上传尚未完成。请重新选择同一文件继续上次上传，或删除这条未完成记录。";
  if (doc.state === "ready") return "已完成知识索引。可在对话中指定此资料库查询，仍需当前阅读权限。";
  return "处理尚未完成，请查看状态提示；关闭窗口不会删除已经保存的文件。";
}
async function checked(response: Response) {
  if (!response.ok) { const data = await response.json().catch(() => ({})); throw new ApiError(typeof data.code === "string" ? data.code : "request_failed", response.status); }
  return response;
}
function checkedDocument(value: KnowledgeDocument, library: string, pending?: PendingDocument) {
  if (!value || value.library_id !== library || !/^kdoc_[a-f0-9]{32}$/.test(value.id) || (pending && (value.filename !== pending.filename || value.bytes !== pending.bytes || value.sha256 !== pending.sha256))) throw new ApiError("agent.conversation.document_response_invalid");
  return value;
}
export async function uploadKnowledgeDocument(library: string, file: File, pending: PendingDocument, signal: AbortSignal): Promise<KnowledgeDocument> {
  const scope = sessionScope();
  const response = await checked(await sessionFetch(`${documentPath(library)}?${new URLSearchParams({ client_id: pending.clientID, filename: file.name })}`, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/octet-stream" }, body: file, signal }));
  const result = checkedDocument(await response.json(), library, pending);
  if (signal.aborted) throw new DOMException("Aborted", "AbortError");
  if (scope !== sessionScope()) throw new ApiError("agent.web.identity_changed", 409);
  return result;
}
export async function readKnowledgeDocument(value: KnowledgeDocument, signal: AbortSignal): Promise<Blob> {
  checkedDocument(value, value.library_id);
  const scope = sessionScope();
  const response = await checked(await sessionFetch(`${documentPath(value.library_id, value.id)}/content`, { method: "GET", credentials: "same-origin", cache: "no-store", signal }));
  const blob = await response.blob();
  if (blob.size > documentMaxBytes || blob.size !== value.bytes) throw new ApiError("agent.conversation.document_content_mismatch");
  const hash = await documentHash(await blob.arrayBuffer());
  if (signal.aborted) throw new DOMException("Aborted", "AbortError");
  if (scope !== sessionScope()) throw new ApiError("agent.web.identity_changed", 409);
  if (hash !== value.sha256) throw new ApiError("agent.conversation.document_content_mismatch");
  return blob;
}

export type PendingImport = { clientID: string; libraryID: string; conversationID: string; attachmentID: string; revision: number };
export function parsePendingImport(raw: string | null, source: { id: string; conversation_id: string }): PendingImport | null {
  if (!raw || raw.length > 2048) return null;
  try {
    const value = JSON.parse(raw) as PendingImport;
    return value && typeof value.clientID === "string" && /^[a-zA-Z0-9_.:-]{1,96}$/.test(value.clientID) && typeof value.libraryID === "string" && /^lib_[a-f0-9]{32}$/.test(value.libraryID) && value.conversationID === source.conversation_id && value.attachmentID === source.id && Number.isSafeInteger(value.revision) && value.revision > 0 ? { clientID: value.clientID, libraryID: value.libraryID, conversationID: value.conversationID, attachmentID: value.attachmentID, revision: value.revision } : null;
  } catch { return null; }
}
