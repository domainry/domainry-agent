import {
  fileSHA256Header,
  type KnowledgeClient,
  type KnowledgeDocument,
  type KnowledgeDocumentPage,
  type KnowledgeLibrary,
  type KnowledgeLibraryMemberPage,
  type KnowledgeLibraryPage,
  type KnowledgeLibrarySources,
  type KnowledgeAccessExplanation,
  type KnowledgePage,
  type KnowledgePageComment,
  type KnowledgePageCommentPage,
  type KnowledgePageAttachment,
  type KnowledgePageAttachmentPage,
  type KnowledgePagePage,
  type KnowledgePageVersion,
  type KnowledgePageVersionDiff,
  type KnowledgePageVersionPage,
  type KnowledgePageView,
  type KnowledgePrincipal,
  type KnowledgePrincipalPage,
  type KnowledgeResult,
} from "@domainry/knowledge-ui";
import { request } from "./api.ts";
import { documentPath, uploadKnowledgeDocument } from "./knowledge-document-state.ts";
import { bindLibrarySource, readLibrarySources } from "./knowledge-datasource-state.ts";
import { sessionFetch, sessionScope } from "./session.ts";

const librariesPath = "/knowledge/libraries";

export const agentKnowledgeClient: KnowledgeClient = {
  search({ query, libraryID, signal }) {
    const parameters = new URLSearchParams({ query });
    if (libraryID) parameters.set("library_id", libraryID);
    return request<KnowledgeResult>(`/app/product/knowledge/search?${parameters}`, "GET", undefined, signal);
  },
  read(documentID, libraryID, signal) {
    const query = libraryID ? `?${new URLSearchParams({ library_id: libraryID })}` : "";
    return request<KnowledgeResult>(`/app/product/knowledge/documents/${encodeURIComponent(documentID)}${query}`, "GET", undefined, signal);
  },
  listLibraries(after, limit, signal) {
    return request<KnowledgeLibraryPage>(`${librariesPath}?${new URLSearchParams({ after, limit: String(limit) })}`, "GET", undefined, signal);
  },
  createLibrary({ kind, name, clientID }, signal) {
    return request<KnowledgeLibrary>(librariesPath, "POST", { kind, name, client_id: clientID }, signal);
  },
  getLibrary(libraryID, signal) {
    return request<KnowledgeLibrary>(`${librariesPath}/${encodeURIComponent(libraryID)}`, "GET", undefined, signal);
  },
  updateLibrary(libraryID, { name, description, expectedRevision }, signal) {
    return request<KnowledgeLibrary>(`${librariesPath}/${encodeURIComponent(libraryID)}`, "PATCH", { name, description, expected_revision: expectedRevision }, signal);
  },
  setLibraryArchived(libraryID, archived, expectedRevision, signal) {
    return request<KnowledgeLibrary>(`${librariesPath}/${encodeURIComponent(libraryID)}/${archived ? "archive" : "restore"}`, "POST", { expected_revision: expectedRevision }, signal);
  },
  listMembers(libraryID, after, limit, signal) {
    return request<KnowledgeLibraryMemberPage>(`${librariesPath}/${encodeURIComponent(libraryID)}/members?${new URLSearchParams({ after, limit: String(limit) })}`, "GET", undefined, signal);
  },
  setMember(libraryID, userID, role, expectedRevision, signal) {
    return request<KnowledgeLibrary>(`${librariesPath}/${encodeURIComponent(libraryID)}/members/${encodeURIComponent(userID)}`, "PUT", { role, expected_revision: expectedRevision }, signal);
  },
  removeMember(libraryID, userID, expectedRevision, signal) {
    return request<KnowledgeLibrary>(`${librariesPath}/${encodeURIComponent(libraryID)}/members/${encodeURIComponent(userID)}?expected_revision=${expectedRevision}`, "DELETE", undefined, signal);
  },
  searchPrincipals(query, after, limit, signal) {
    return request<KnowledgePrincipalPage>(`/knowledge/principals?${new URLSearchParams({ query, after, limit: String(limit) })}`, "GET", undefined, signal);
  },
  async resolvePrincipals(userIDs, signal) {
	return request<KnowledgePrincipal[]>("/knowledge/principals/resolve", "POST", { user_ids: userIDs }, signal);
  },
  searchLibraryMembers(libraryID, query, after, limit, signal) {
    return request<KnowledgePrincipalPage>(`${librariesPath}/${encodeURIComponent(libraryID)}/members/search?${new URLSearchParams({ query, after, limit: String(limit) })}`, "GET", undefined, signal);
  },
  explainAccess(libraryID, input, signal) {
    return request<KnowledgeAccessExplanation>(`${librariesPath}/${encodeURIComponent(libraryID)}/access/explain`, "POST", input, signal);
  },
  listPages(libraryID, parentPageID, after, limit, includeArchived, signal) {
    return request<KnowledgePagePage>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages?${new URLSearchParams({ parent_page_id: parentPageID, after, limit: String(limit), include_archived: String(includeArchived) })}`, "GET", undefined, signal);
  },
  getPage(libraryID, pageID, includeDraft, signal) {
    return request<KnowledgePageView>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}?draft=${includeDraft}`, "GET", undefined, signal);
  },
  createPage(libraryID, input, signal) {
    return request<KnowledgePageView>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages`, "POST", input, signal);
  },
  savePageDraft(libraryID, pageID, input, signal) {
    return request<KnowledgePageView>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/draft`, "PUT", input, signal);
  },
  movePage(libraryID, pageID, input, signal) {
    return request<KnowledgePage>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/move`, "POST", input, signal);
  },
  copyPage(libraryID, pageID, input, signal) {
    return request<KnowledgePageView>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/copy`, "POST", input, signal);
  },
  reorderPages(libraryID, input, signal) {
    return request<KnowledgePagePage>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/reorder`, "POST", input, signal);
  },
  setPageArchived(libraryID, pageID, archived, input, signal) {
    return request<KnowledgePage>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/${archived ? "archive" : "restore"}`, "POST", input, signal);
  },
  publishPage(libraryID, pageID, input, signal) {
    return request<KnowledgePageView>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/publish`, "POST", input, signal);
  },
  listPageVersions(libraryID, pageID, before, limit, signal) {
    const parameters = new URLSearchParams({ limit: String(limit) });
    if (before > 0) parameters.set("before", String(before));
    return request<KnowledgePageVersionPage>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/versions?${parameters}`, "GET", undefined, signal);
  },
  getPageVersion(libraryID, pageID, version, signal) {
    return request<KnowledgePageVersion>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/versions/${version}`, "GET", undefined, signal);
  },
  diffPageVersions(libraryID, pageID, fromVersion, toVersion, signal) {
    return request<KnowledgePageVersionDiff>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/versions/diff`, "POST", { from_version: fromVersion, to_version: toVersion }, signal);
  },
  restorePageVersion(libraryID, pageID, version, input, signal) {
    return request<KnowledgePageView>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/versions/${version}/restore`, "POST", input, signal);
  },
  listPageComments(libraryID, pageID, after, limit, unresolved, signal) {
    return request<KnowledgePageCommentPage>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/comments?${new URLSearchParams({ after, limit: String(limit), unresolved: String(unresolved) })}`, "GET", undefined, signal);
  },
  createPageComment(libraryID, pageID, input, signal) {
    return request<KnowledgePageComment>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/comments`, "POST", input, signal);
  },
  updatePageComment(libraryID, pageID, commentID, input, signal) {
    return request<KnowledgePageComment>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/comments/${encodeURIComponent(commentID)}`, "PUT", input, signal);
  },
  resolvePageComment(libraryID, pageID, commentID, input, signal) {
    return request<KnowledgePageComment>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/comments/${encodeURIComponent(commentID)}/resolve`, "POST", input, signal);
  },
  deletePageComment(libraryID, pageID, commentID, input, signal) {
    return request<KnowledgePageComment>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/comments/${encodeURIComponent(commentID)}`, "DELETE", input, signal);
  },
  listPageAttachments(libraryID, pageID, signal) {
    return request<KnowledgePageAttachmentPage>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/attachments`, "GET", undefined, signal);
  },
  setPageAttachment(libraryID, pageID, documentID, input, signal) {
    return request<KnowledgePageAttachment>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/attachments/${encodeURIComponent(documentID)}`, "PUT", input, signal);
  },
  async removePageAttachment(libraryID, pageID, documentID, input, signal) {
    await request<unknown>(`${librariesPath}/${encodeURIComponent(libraryID)}/pages/${encodeURIComponent(pageID)}/attachments/${encodeURIComponent(documentID)}`, "DELETE", input, signal);
  },
  listSources(libraryID, after, _limit, signal): Promise<KnowledgeLibrarySources> {
    return readLibrarySources(libraryID, after, signal);
  },
  bindSource(libraryID, datasourceKey, expectedRevision, signal) {
    return bindLibrarySource(libraryID, { datasource_key: datasourceKey, expected_revision: expectedRevision }, signal);
  },
  listDocuments(libraryID, after, limit, signal) {
    return request<KnowledgeDocumentPage>(`${documentPath(libraryID)}?${new URLSearchParams({ after, limit: String(limit) })}`, "GET", undefined, signal);
  },
  getDocument(libraryID, documentID, signal) {
    return request<KnowledgeDocument>(documentPath(libraryID, documentID), "GET", undefined, signal);
  },
  uploadDocument({ libraryID, file, clientID, sha256, signal, onProgress }) {
    return uploadKnowledgeDocument(libraryID, file, { clientID, filename: file.name, bytes: file.size, sha256 }, signal, onProgress);
  },
  deleteDocument(libraryID, documentID, expectedRevision, signal) {
    return request<KnowledgeDocument>(`${documentPath(libraryID, documentID)}?expected_revision=${expectedRevision}`, "DELETE", undefined, signal);
  },
  retryDocument(libraryID, documentID, expectedRevision, signal) {
    return request<KnowledgeDocument>(`${documentPath(libraryID, documentID)}/retry?expected_revision=${expectedRevision}`, "POST", undefined, signal);
  },
  reindexDocument(libraryID, documentID, expectedRevision, signal) {
    return request<KnowledgeDocument>(`${documentPath(libraryID, documentID)}/reindex?expected_revision=${expectedRevision}`, "POST", undefined, signal);
  },
  transferDocument({ source, targetLibraryID, mode, clientID, signal }) {
    return request<KnowledgeDocument>(`${documentPath(targetLibraryID)}/transfer`, "POST", { client_id: clientID, source_library_id: source.library_id, source_document_id: source.id, expected_revision: source.revision, mode }, signal);
  },
  async readDocumentContent(libraryID, documentID, { signal, range }) {
    const response = await sessionFetch(`${documentPath(libraryID, documentID)}/content`, { method: "GET", credentials: "same-origin", cache: "no-store", headers: range ? { Range: range } : undefined, signal });
    const headers = new Headers(response.headers);
    const sha256 = headers.get("X-Knowledge-File-SHA256");
    headers.delete("X-Knowledge-File-SHA256");
    if (sha256) headers.set(fileSHA256Header, sha256);
    return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
  },
  documentContentURL(libraryID, documentID) {
    return `${documentPath(libraryID, documentID)}/content`;
  },
  scope: sessionScope,
};
