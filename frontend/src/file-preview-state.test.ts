import test from "node:test";
import assert from "node:assert/strict";
import { citationFile, downloadFilename, previewPath } from "./file-preview-state.ts";
import type { Citation } from "./knowledge-state.ts";
test("preview accepts only managed document identities, never source URLs", () => {
 const id = "kdoc_" + "a".repeat(32), libraryID = "lib_" + "b".repeat(32);
 const citation = { provider: "agent_library_documents", library_id: libraryID, doc_id: id, url: "https://untrusted.example/document.pdf" } as Citation;
 assert.deepEqual(citationFile(citation), { kind:"library", libraryID, id });
 assert.equal(previewPath(citationFile(citation)!), `/agent/knowledge-libraries/${libraryID}/documents/${id}/content`);
 assert.equal(citationFile({...citation,provider:"verdent"}),null);
 assert.equal(citationFile({...citation,doc_id:"../../secret"}),null);
 assert.throws(()=>previewPath({kind:"library",libraryID,id:"https://untrusted.example/file"}));
});
test("download response controls filename, supports Unicode and rejects paths", () => {
 assert.equal(downloadFilename("attachment; filename*=utf-8''%E5%90%88%E5%90%8C.docx"),"合同.docx");
 assert.equal(downloadFilename('attachment; filename="test file.pdf"'),"test file.pdf");
 for(const header of [null, 'attachment; filename="../private"', "attachment; filename*=UTF-8''%00secret", "attachment; filename*=UTF-8''%XX"]) assert.throws(()=>downloadFilename(header));
});
test("private citations open only their scoped attachment original", () => {
 const conversationID = "conv_" + "a".repeat(32), id = "att_" + "b".repeat(32);
 const citation = { provider: "agent_conversation_documents", conversation_id: conversationID, doc_id: id, url: "https://untrusted.example/private.xlsx" } as Citation;
 assert.deepEqual(citationFile(citation), { kind: "attachment", conversationID, id });
 assert.equal(previewPath(citationFile(citation)!), `/agent/conversations/${conversationID}/attachments/${id}/content`);
 for (const changed of [{ conversation_id: undefined }, { conversation_id: "../other" }, { doc_id: "kdoc_" + "b".repeat(32) }, { library_id: "lib_" + "c".repeat(32) }, { provider: "verdent" }]) assert.equal(citationFile({ ...citation, ...changed }), null);
});
