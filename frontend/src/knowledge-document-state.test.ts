import assert from "node:assert/strict";
import test from "node:test";
import { ApiError } from "./errors.ts";
import { setSession, type AppSession } from "./session.ts";
import { canTransferToLibrary, parsePendingTransfer } from "./document-transfer-state.ts";
import { documentCanDownload, documentHash, documentLabel, documentMaxBytes, parsePendingDocument, parsePendingImport, readKnowledgeDocument, uploadKnowledgeDocument, type KnowledgeDocument } from "./knowledge-document-state.ts";

const a: AppSession = { mode: "identity", scope: "fixture/workspace/a", runtime_id: "fixture", workspace_id: "workspace", user_id: "a", name: "A", must_change_password: false, ready: true, model: "fixture" };
const bytes = new TextEncoder().encode("immutable library original\n");
const file = new File([bytes], "资料.txt", { type: "text/plain" });
const sha256 = await documentHash(bytes.buffer);
const pending = { clientID: "same-upload", filename: file.name, bytes: file.size, sha256 };
const document: KnowledgeDocument = { id: "kdoc_" + "a".repeat(32), library_id: "lib_" + "b".repeat(32), filename: file.name, bytes: file.size, sha256, content_type: "text/plain", state: "queued", revision: 2, created_by_user_id: "a", created_at: "2026-09-10T00:00:00Z", updated_at: "2026-09-10T00:00:00Z" };

test("document upload retry receipts remain bounded and pending/indexed/deleting states stay distinct", () => {
  assert.deepEqual(parsePendingDocument(JSON.stringify(pending)), pending);
  assert.equal(parsePendingDocument(JSON.stringify({ ...pending, bytes: documentMaxBytes + 1 })), null);
  assert.equal(parsePendingDocument(JSON.stringify({ ...pending, clientID: "../other" })), null);
  assert.equal(parsePendingDocument(JSON.stringify({ ...pending, sha256: "changed" })), null);
  assert.doesNotMatch(documentLabel(document), /可检索/);
  assert.match(documentLabel({ state: "needs_reconcile" }), /待核查/);
  assert.match(documentLabel({ state: "indexing", error_code: "document_index_failed" }), /失败/);
  assert(!documentCanDownload({ ...document, state: "deleting" }));
  assert(!documentCanDownload({ ...document, state: "uploading" }));
});

test("document upload sends one binary request with its persisted identity and rejects unrelated receipts", async () => {
  const original = globalThis.fetch;
  try {
    setSession(a);
    let sends = 0;
    globalThis.fetch = (async (input, init) => {
      sends++;
      const url = new URL(String(input), "https://fixture.example");
      assert.equal(url.pathname, `/agent/knowledge-libraries/${document.library_id}/documents`);
      assert.equal(url.searchParams.get("client_id"), pending.clientID);
      assert.equal(url.searchParams.get("filename"), file.name);
      assert.deepEqual([...url.searchParams.keys()], ["client_id", "filename"]);
      assert.equal(init?.body, file);
      assert.equal(new Headers(init?.headers).get("X-Agent-Scope"), a.scope);
      return Response.json(document);
    }) as typeof fetch;
    const signal = new AbortController().signal;
    assert.equal((await uploadKnowledgeDocument(document.library_id, file, pending, signal)).id, document.id);
    assert.equal(sends, 1);
    for (const bad of [{ ...document, library_id: "lib_" + "c".repeat(32) }, { ...document, sha256: "c".repeat(64) }]) {
      globalThis.fetch = async () => Response.json(bad);
      await assert.rejects(uploadKnowledgeDocument(document.library_id, file, pending, signal), (e: unknown) => e instanceof ApiError && e.code.endsWith("document_response_invalid"));
    }
  } finally { globalThis.fetch = original; setSession(null); }
});

test("original downloads verify bytes and hash and discard body completion after an account change", async () => {
  const original = globalThis.fetch;
  try {
    setSession(a);
    const signal = new AbortController().signal;
    globalThis.fetch = async () => new Response(bytes);
    assert.equal((await readKnowledgeDocument(document, signal)).size, bytes.length);
    globalThis.fetch = async () => new Response(new Uint8Array(bytes.length));
    await assert.rejects(readKnowledgeDocument(document, signal), (e: unknown) => e instanceof ApiError && e.code.endsWith("document_content_mismatch"));
    let release!: () => void;
    globalThis.fetch = async () => new Response(new ReadableStream({ start(controller) { release = () => { controller.enqueue(bytes); controller.close(); }; } }));
    const download = readKnowledgeDocument(document, signal);
    await new Promise(resolve => setTimeout(resolve, 0));
    setSession({ ...a, scope: "fixture/workspace/b", user_id: "b" });
    release();
    await assert.rejects(download, (e: unknown) => e instanceof ApiError && e.code === "agent.web.identity_changed");
  } finally { globalThis.fetch = original; setSession(null); }
});


test("attachment copy receipts bind a source and stable target without keeping file bytes", () => {
  const source = { id: "att_source", conversation_id: "conv_source" };
  const value = { clientID: "copy-1", libraryID: "lib_" + "c".repeat(32), conversationID: source.conversation_id, attachmentID: source.id, revision: 2 };
  assert.deepEqual(parsePendingImport(JSON.stringify({...value, data: "ignored content"}), source), value);
  for (const change of [{ conversationID: "conv_other" }, { attachmentID: "att_other" }, { revision: 0 }, { revision: 1.5 }, { libraryID: "../other" }, { clientID: "../unsafe" }]) assert.equal(parsePendingImport(JSON.stringify({...value,...change}), source), null);
  assert.equal(parsePendingImport(" ".repeat(2049), source), null);
});

test("a pending transfer remains recoverable during a source outage without enabling new writes or bypassing target access", () => {
  const library = { id: "lib_" + "c".repeat(32), kind: "personal" as const, name: "Personal", description: "", role: "manager", owner_user_id: "a", archived: false, revision: 1, documents_configured: false };
  assert.equal(canTransferToLibrary(library, document.library_id), false);
  assert.equal(canTransferToLibrary(library, document.library_id, library.id), true);
  assert.equal(canTransferToLibrary(library, document.library_id, "lib_" + "d".repeat(32)), false);
  assert.equal(canTransferToLibrary({ ...library, role: "reader" }, document.library_id, library.id), false);
  assert.equal(canTransferToLibrary({ ...library, archived: true }, document.library_id, library.id), false);
  assert.equal(canTransferToLibrary(library, library.id, library.id), false);
  const value = { source: { id: document.id, library_id: document.library_id, filename: document.filename, bytes: document.bytes, sha256: document.sha256, revision: document.revision }, targetID: library.id, clientID: "original-request", mode: "move" };
  assert.deepEqual(parsePendingTransfer(JSON.stringify({ ...value, body: "must not be retained" })), value);
  for (const change of [{ targetID: document.library_id }, { targetID: "../other" }, { mode: "share-publicly" }, { clientID: "../unsafe" }, { source: { ...value.source, revision: 0 } }]) assert.equal(parsePendingTransfer(JSON.stringify({ ...value, ...change })), null);
});
