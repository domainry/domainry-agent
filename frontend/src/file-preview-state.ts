import {
  canPreviewFilename,
  downloadFile,
  downloadFilename,
  fileSHA256Header,
  type FilePreviewLocation,
  type FilePreviewSource,
} from "@domainry/knowledge-ui";
import { ApiError, apiErrorFromResponse } from "./errors.ts";
import { sessionFetch, sessionScope } from "./session.ts";
import { attachmentPath } from "./attachment-state.ts";
import { documentPath } from "./knowledge-document-state.ts";
import type { Citation } from "./knowledge-state.ts";

export type FileTarget = { kind: "library"; libraryID: string; id: string } | { kind: "attachment"; conversationID: string; id: string };
export type ExpectedFile = { bytes: number; sha256: string };

export { canPreviewFilename, downloadFilename };

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

function targetIdentity(target: FileTarget) {
  return target.kind === "library" ? `library:${target.libraryID}:${target.id}` : `attachment:${target.conversationID}:${target.id}`;
}

export function filePreviewSource(target: FileTarget, filename: string, expected?: ExpectedFile, location?: FilePreviewLocation): FilePreviewSource {
  const path = previewPath(target);
  return {
    identity: `${targetIdentity(target)}:${expected?.bytes || "unknown"}:${expected?.sha256 || "unknown"}`,
    file: { filename, bytes: expected?.bytes, sha256: expected?.sha256, location },
    streamURL: path,
    async read({ signal, range }) {
      const scope = sessionScope();
      const response = await sessionFetch(path, { method: "GET", credentials: "same-origin", cache: "no-store", headers: range ? { Range: range } : undefined, signal });
      if (!response.ok && response.status !== 206) {
        throw await apiErrorFromResponse(response);
      }
      if (scope !== sessionScope()) throw new ApiError("agent.web.identity_changed", 409);
      const headers = new Headers(response.headers);
      const sha256 = headers.get("X-Knowledge-File-SHA256");
      headers.delete("X-Knowledge-File-SHA256");
      if (sha256) headers.set(fileSHA256Header, sha256);
      return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
    },
  };
}

export function downloadPreviewFile(target: FileTarget, filename: string, signal: AbortSignal, expected?: ExpectedFile) {
  return downloadFile(filePreviewSource(target, filename, expected), signal);
}
