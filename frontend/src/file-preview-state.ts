import { ApiError } from "./errors.ts";
import { sessionFetch, sessionScope } from "./session.ts";
import { attachmentHash, attachmentMaxBytes, attachmentPath } from "./attachment-state.ts";
import { documentPath } from "./knowledge-document-state.ts";
import type { Citation } from "./knowledge-state.ts";

export type FileTarget = { kind: "library"; libraryID: string; id: string } | { kind: "attachment"; conversationID: string; id: string };
export type PreviewFile = { filename: string; blob: Blob; format: string };
export function previewFormat(filename: string) { return filename.split(".").at(-1)?.toLowerCase() || ""; }
export function citationFile(citation: Citation): FileTarget | null {
	if (citation.provider === "agent_conversation_documents") {
		return !citation.library_id && /^conv_[a-f0-9]{32}$/.test(citation.conversation_id || "") && /^att_[a-f0-9]{32}$/.test(citation.doc_id)
			? { kind: "attachment", conversationID: citation.conversation_id!, id: citation.doc_id } : null;
	}
  return ["agent_library_documents", "agent_parsed_document"].includes(citation.provider) && /^lib_[a-f0-9]{32}$/.test(citation.library_id || "") && /^kdoc_[a-f0-9]{32}$/.test(citation.doc_id)
    ? { kind: "library", libraryID: citation.library_id!, id: citation.doc_id } : null;
}
export function previewPath(target: FileTarget): string {
  if (target.kind === "library" && /^lib_[a-f0-9]{32}$/.test(target.libraryID) && /^kdoc_[a-f0-9]{32}$/.test(target.id)) return `${documentPath(target.libraryID, target.id)}/content`;
  if (target.kind === "attachment" && /^conv_[a-f0-9]{32}$/.test(target.conversationID) && /^att_[a-f0-9]{32}$/.test(target.id)) return `${attachmentPath(target.conversationID, target.id)}/content`;
  throw new ApiError("agent.conversation.document_response_invalid");
}
export function downloadFilename(header: string | null): string {
  const encoded = header?.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
  const plain = header?.match(/filename="((?:[^"\\]|\\.)*)"/i)?.[1]?.replace(/\\(.)/g, "$1") || header?.match(/filename=([^;\s]+)/i)?.[1];
  const value = encoded ? decodeURIComponent(encoded) : plain;
  if (!value || value.length > 512 || /[\x00-\x1f/\\]/.test(value)) throw new ApiError("agent.conversation.document_response_invalid");
  return value;
}
// One authorized content operation, including authoritative filename and digest.
// A citation URL is never fetched here; external source URLs are not file capabilities.
export async function readPreviewFile(target: FileTarget, signal: AbortSignal): Promise<PreviewFile> {
  const scope = sessionScope();
  const response = await sessionFetch(previewPath(target), { method: "GET", credentials: "same-origin", cache: "no-store", signal });
  if (!response.ok) { const data = await response.json().catch(() => ({})); throw new ApiError(data.code || "request_failed", response.status); }
  const filename = downloadFilename(response.headers.get("Content-Disposition"));
  const length = Number(response.headers.get("Content-Length")), sha = response.headers.get("X-Agent-File-SHA256");
  if (!Number.isSafeInteger(length) || length <= 0 || length > attachmentMaxBytes || !/^[a-f0-9]{64}$/.test(sha || "")) throw new ApiError("agent.conversation.document_content_mismatch");
  const reader = response.body!.getReader(), chunks: Uint8Array<ArrayBuffer>[] = []; let bytes = 0;
  try { for (;;) { const chunk = await reader.read(); if (chunk.done) break; bytes += chunk.value.length; if (bytes > length) throw new ApiError("agent.conversation.document_content_mismatch"); chunks.push(new Uint8Array(chunk.value)); } }
  finally { await reader.cancel(); }
  const blob = new Blob(chunks);
  if (bytes !== length || await attachmentHash(await blob.arrayBuffer()) !== sha) throw new ApiError("agent.conversation.document_content_mismatch");
  signal.throwIfAborted();
  if (scope !== sessionScope()) throw new ApiError("agent.web.identity_changed", 409);
  return { filename, blob, format: previewFormat(filename) };
}
