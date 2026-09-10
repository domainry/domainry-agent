import { useEffect, useRef, useState } from "react";
import { Button } from "./components/ui/button";
import { request } from "./api.ts";
import { ApiError, describeError } from "./errors.ts";
import { sessionScope } from "./session.ts";
import type { Attachment } from "./attachment-state.ts";
import { documentCanWrite, documentLabel, documentPath, parsePendingImport, type KnowledgeDocument, type KnowledgeLibrary, type PendingImport } from "./knowledge-document-state.ts";

type LibraryPage = { items: KnowledgeLibrary[]; complete: boolean; next_after?: string };

export function AttachmentLibrarySave({ attachment, disabled, onBusyChange, onOpenLibrary }: { attachment: Attachment; disabled: boolean; onBusyChange: (busy: boolean) => void; onOpenLibrary: () => void }) {
  const key = `agent-attachment-import:${sessionScope()}:${attachment.conversation_id}:${attachment.id}`;
  const [pending, setPending] = useState<PendingImport | null>(() => { try { return parsePendingImport(localStorage.getItem(key), attachment); } catch { return null; } });
  const [opened, setOpened] = useState(!!pending), [page, setPage] = useState<LibraryPage>({ items: [], complete: true });
  const [cursors, setCursors] = useState([""]), [refresh, setRefresh] = useState(0);
  const [target, setTarget] = useState<KnowledgeLibrary | null>(null), [result, setResult] = useState<KnowledgeDocument | null>(null);
  const [loading, setLoading] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState(""), [notice, setNotice] = useState("");
  const lock = useRef(false), action = useRef<AbortController | null>(null);
  const cursor = cursors.at(-1)!;
  useEffect(() => () => { action.current?.abort(); onBusyChange(false); }, [onBusyChange]);
  useEffect(() => {
    if (!opened) return;
    const abort = new AbortController(); setLoading(true);
    const signal = AbortSignal.any([abort.signal, AbortSignal.timeout(15000)]);
    void Promise.all([
      request<LibraryPage>(`/agent/knowledge-libraries?${new URLSearchParams({ after: cursor, limit: "20" })}`, "GET", undefined, signal),
      pending ? request<KnowledgeLibrary>(`/agent/knowledge-libraries/${encodeURIComponent(pending.libraryID)}`, "GET", undefined, signal) : Promise.resolve(null),
    ]).then(([value, previous]) => { if (!abort.signal.aborted) { setPage(value); if (previous) setTarget(previous); } }).catch(failure => { if (!abort.signal.aborted) { setPage({ items: [], complete: true }); setTarget(null); setError(describeError(failure)); } }).finally(() => { if (!abort.signal.aborted) setLoading(false); });
    return () => abort.abort();
  }, [opened, cursor, refresh, pending]);
  const usable = (library: KnowledgeLibrary) => documentCanWrite(library) && library.documents_configured && !library.archived;
  async function save() {
    if (lock.current || disabled || !target || !usable(target)) return;
    lock.current = true; setBusy(true); onBusyChange(true); setError(""); setNotice("");
    const abort = new AbortController(); action.current = abort;
    const signal = AbortSignal.any([abort.signal, AbortSignal.timeout(60000)]), scope = sessionScope();
    try {
      const receipt = pending || { clientID: crypto.randomUUID(), libraryID: target.id, conversationID: attachment.conversation_id, attachmentID: attachment.id, revision: attachment.revision };
      localStorage.setItem(key, JSON.stringify(receipt)); setPending(receipt);
      const doc = await request<KnowledgeDocument>(`${documentPath(receipt.libraryID)}/from-attachment`, "POST", { client_id: receipt.clientID, conversation_id: receipt.conversationID, attachment_id: receipt.attachmentID, expected_revision: receipt.revision }, signal);
      if (signal.aborted) throw new DOMException("Aborted", "AbortError");
      if (scope !== sessionScope()) throw new ApiError("agent.web.identity_changed", 409);
      if (doc.library_id !== receipt.libraryID || !/^kdoc_[a-f0-9]{32}$/.test(doc.id) || doc.filename !== attachment.filename || doc.bytes !== attachment.bytes || doc.sha256 !== attachment.sha256) throw new ApiError("agent.conversation.document_response_invalid");
      localStorage.removeItem(key); setPending(null); setResult(doc);
    } catch (failure) { if (!abort.signal.aborted) setError(describeError(failure)); }
    finally { lock.current = false; onBusyChange(false); if (!abort.signal.aborted) setBusy(false); }
  }
  return <section className="attachment-library-save" aria-label="附件另存资料库">
    {!opened ? <Button variant="outline" disabled={disabled} onClick={() => setOpened(true)}>另存到资料库</Button> : <>
      <h4>另存到资料库</h4>
      <p className="subtle">创建独立副本，原附件继续保持会话私有。删除原附件不会删除已保存的资料库副本。</p>
      {error && <p role="alert" className="error-text">{error}</p>}{notice && <p role="status" className="subtle">{notice}</p>}
      {result ? <><p role="status">已另存到“{target?.name || "资料库"}”：{result.filename} · {documentLabel(result)}</p><Button variant="outline" disabled={disabled || busy} onClick={onOpenLibrary}>前往资料库查看进度</Button></> : <>
        {pending && <p className="subtle">上次另存结果待确认。继续确认会复用原记录，不会重复创建副本。</p>}
        {!pending && <><Button variant="ghost" size="sm" disabled={disabled || busy || loading} onClick={() => { setTarget(null); setError(""); setRefresh(n => n + 1); }}>刷新可选资料库</Button>
          {loading ? <p className="subtle">正在读取可访问的资料库…</p> : <div className="attachment-list">{page.items.map(library => <Button key={library.id} variant={target?.id === library.id ? "secondary" : "outline"} disabled={disabled || busy || !usable(library)} onClick={() => setTarget(library)}><span><strong>{library.name}</strong><small>{library.kind === "personal" ? "个人 · 仅自己可见" : "共享 · 库内阅读成员可见"}{!usable(library) ? " · 尚不可另存" : ""}</small></span></Button>)}</div>}
          {!loading && page.items.length === 0 && <p className="subtle">本页没有可访问的资料库，请先在“资料库”中创建并连接知识源。</p>}
          {(cursors.length > 1 || !page.complete) && <div className="interaction-actions"><Button size="sm" disabled={disabled || busy || loading || cursors.length === 1} onClick={() => setCursors(value => value.slice(0, -1))}>上一页资料库</Button><Button size="sm" disabled={disabled || busy || loading || !page.next_after} onClick={() => setCursors(value => [...value, page.next_after!])}>下一页资料库</Button></div>}
        </>}
        {target && <div className="memory-operation"><p>将“{attachment.filename}”另存到“{target.name}”？{target.kind === "shared" ? "此资料库所有阅读成员都能访问这个副本。" : "副本仅自己可见，可在其他会话中查询。"}知识索引完成后才能检索。</p><Button disabled={disabled || busy || loading || !usable(target)} onClick={() => void save()}>{busy ? "正在另存…" : pending ? "继续确认上次另存" : "确认另存副本"}</Button></div>}
        {pending && <Button size="sm" variant="ghost" disabled={disabled || busy} onClick={() => { try { localStorage.removeItem(key); setPending(null); setTarget(null); setNotice("已结束重试。已经保存的副本仍保留，请先到资料库核对。"); } catch { setError("无法清除本地另存记录。"); } }}>结束这次另存重试</Button>}
      </>}
    </>}
  </section>;
}
