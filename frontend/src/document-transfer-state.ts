import { sessionScope } from "./session.ts";
import type { KnowledgeDocument } from "./knowledge-document-state.ts";

export type TransferSource = Pick<KnowledgeDocument, "id" | "library_id" | "filename" | "bytes" | "sha256" | "revision">;
export type TransferDraft = { source: TransferSource; canMove: boolean };
export type PendingTransfer = { source: TransferSource; clientID: string; targetID: string; mode: "copy" | "move" };
export const transferReceiptKey = () => `agent-document-transfer:${sessionScope()}`;
export function parsePendingTransfer(raw: string | null): PendingTransfer | null {
  if (!raw || raw.length > 4096) return null;
  try {
    const p = JSON.parse(raw) as PendingTransfer, s = p.source;
    if (!s || !/^kdoc_[a-f0-9]{32}$/.test(s.id) || !/^lib_[a-f0-9]{32}$/.test(s.library_id) || !/^lib_[a-f0-9]{32}$/.test(p.targetID) || s.library_id === p.targetID || !/^[a-zA-Z0-9_.:-]{1,96}$/.test(p.clientID) || p.mode !== "copy" && p.mode !== "move" || !/^[a-f0-9]{64}$/.test(s.sha256) || typeof s.filename !== "string" || !s.filename.length || s.filename.length > 255 || !Number.isSafeInteger(s.bytes) || s.bytes < 1 || s.bytes > 16 * 1024 * 1024 || !Number.isSafeInteger(s.revision) || s.revision < 1) return null;
    return { clientID: p.clientID, targetID: p.targetID, mode: p.mode, source: { id: s.id, library_id: s.library_id, filename: s.filename, bytes: s.bytes, sha256: s.sha256, revision: s.revision } };
  } catch { return null; }
}
export function loadPendingTransfer(): PendingTransfer | null {
  try { return parsePendingTransfer(localStorage.getItem(transferReceiptKey())); } catch { return null; }
}
