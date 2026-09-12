import { FilePreviewDialog } from "./FilePreviewDialog";
import { useEffect, useRef, useState } from "react";
import { Download, FileText, RefreshCw, Trash2, Upload } from "lucide-react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { request } from "./api.ts";
import { ApiError, describeError, errorMessage } from "./errors.ts";
import { sessionScope } from "./session.ts";
import { documentAccept, documentCanDownload, documentCanWrite, documentHash, documentLabel, documentUploadMaxBytes, documentPath, documentStatusDetail, parsePendingDocument, readKnowledgeDocument, uploadKnowledgeDocument, type KnowledgeDocument, type KnowledgeDocumentPage, type KnowledgeLibrary, type PendingDocument } from "./knowledge-document-state.ts";

export function KnowledgeDocumentsPanel({ library: initialLibrary, disabled, onBusyChange, onTransfer }: { library: KnowledgeLibrary; disabled: boolean; onBusyChange: (value: boolean) => void; onTransfer: (doc: KnowledgeDocument, library: KnowledgeLibrary) => void }) {
  const libraryID = initialLibrary.id;
  const storageKey = `agent-library-document-upload:${sessionScope()}:${libraryID}`;
  const [library, setLibrary] = useState(initialLibrary);
  const [pending, setPending] = useState<PendingDocument | null>(() => { try { return parsePendingDocument(localStorage.getItem(storageKey)); } catch { return null; } });
  const [file, setFile] = useState<File | null>(null), [page, setPage] = useState<KnowledgeDocumentPage>({ items: [], complete: true });
  const [cursors, setCursors] = useState([""]), [refresh, setRefresh] = useState(0);
  const [selectedID, setSelectedID] = useState(""), [selected, setSelected] = useState<KnowledgeDocument | null>(null);
  const [filePreview, setFilePreview] = useState(false);
  const [confirmation, setConfirmation] = useState<KnowledgeDocument | null>(null);
  const [busy, setBusy] = useState(false), [loading, setLoading] = useState(true), [available, setAvailable] = useState(false);
  const [error, setError] = useState(""), [loadError, setLoadError] = useState(""), [notice, setNotice] = useState("");
  const lock = useRef(false), readActive = useRef(false), input = useRef<HTMLInputElement>(null);
  const mutation = useRef<AbortController | null>(null), reading = useRef<AbortController | null>(null);
  const cursor = cursors.at(-1)!;

  useEffect(() => setLibrary(initialLibrary), [initialLibrary]);
  useEffect(() => {
    const reload = () => { if (!lock.current && !readActive.current && document.visibilityState !== "hidden") setRefresh(n => n + 1); };
    const timer = setInterval(reload, 5000);
    window.addEventListener("focus", reload);
    document.addEventListener("visibilitychange", reload);
    return () => { clearInterval(timer); window.removeEventListener("focus", reload); document.removeEventListener("visibilitychange", reload); mutation.current?.abort(); onBusyChange(false); };
  }, [onBusyChange]);
  useEffect(() => {
    const abort = new AbortController(); reading.current = abort; readActive.current = true; setLoading(true);
    const signal = AbortSignal.any([abort.signal, AbortSignal.timeout(15000)]);
    void Promise.all([
      request<KnowledgeLibrary>(`/agent/knowledge-libraries/${encodeURIComponent(libraryID)}`, "GET", undefined, signal),
      request<KnowledgeDocumentPage>(`${documentPath(libraryID)}?${new URLSearchParams({ after: cursor, limit: "20" })}`, "GET", undefined, signal),
      selectedID ? request<KnowledgeDocument>(documentPath(libraryID, selectedID), "GET", undefined, signal).catch(failure => { if (failure instanceof ApiError && failure.status === 404) return null; throw failure; }) : Promise.resolve(null),
    ]).then(([current, result, document]) => {
      if (abort.signal.aborted) return;
      if (current.id !== libraryID || result.items.some(item => item.library_id !== libraryID) || document && document.library_id !== libraryID) throw new ApiError("agent.conversation.document_response_invalid");
      setLibrary(current); setPage(result); setSelected(document); setAvailable(true); setLoadError("");
      if (selectedID && !document) { setSelectedID(""); setConfirmation(null); }
      if (document?.state === "deleting") setConfirmation(null);
    }).catch(failure => {
      if (!abort.signal.aborted) { setPage({ items: [], complete: true }); setSelected(null); setConfirmation(null); setAvailable(false); setLoadError(describeError(failure)); }
    }).finally(() => {
      if (reading.current === abort) readActive.current = false;
      if (!abort.signal.aborted) setLoading(false);
    });
    return () => abort.abort();
  }, [libraryID, cursor, selectedID, refresh]);

  const blocked = busy || disabled;
  const writable = available && documentCanWrite(library);
  const uploadEnabled = writable && library.documents_configured && !library.archived;
  const maxUploadBytes = documentUploadMaxBytes(library);
  async function perform(action: (signal: AbortSignal) => Promise<void>) {
    if (lock.current || disabled) return;
    lock.current = true; setBusy(true); onBusyChange(true); setError(""); setNotice("");
    reading.current?.abort(); readActive.current = false; setLoading(false);
    const abort = new AbortController(); mutation.current = abort;
    try { await action(AbortSignal.any([abort.signal, AbortSignal.timeout(60000)])); }
    catch (failure) {
      if (!abort.signal.aborted) {
        setError(describeError(failure));
        if (failure instanceof ApiError && [401, 403, 404].includes(failure.status || 0)) { setPage({ items: [], complete: true }); setSelected(null); setAvailable(false); setConfirmation(null); }
        if (failure instanceof ApiError && failure.status === 409) setConfirmation(null);
      }
    } finally {
      lock.current = false; onBusyChange(false);
      if (!abort.signal.aborted) { setBusy(false); setRefresh(n => n + 1); }
    }
  }
  function clearPending() { localStorage.removeItem(storageKey); setPending(null); }
  async function upload(signal: AbortSignal) {
    if (!file || !uploadEnabled) return;
    if (!file.size || file.size > maxUploadBytes) throw new ApiError("agent.conversation.document_size_invalid");
    if (!documentAccept.split(",").some(extension => file.name.toLowerCase().endsWith(extension))) throw new ApiError("agent.conversation.document_type_unsupported");
    const sha256 = await documentHash(await file.arrayBuffer());
    if (signal.aborted) return;
    if (pending && (pending.filename !== file.name || pending.bytes !== file.size || pending.sha256 !== sha256)) { setError(`请重新选择上次的“${pending.filename}”以继续上传，或先结束这次重试。`); return; }
    const next = pending || { clientID: crypto.randomUUID(), filename: file.name, bytes: file.size, sha256 };
    try { localStorage.setItem(storageKey, JSON.stringify(next)); } catch { setError("无法保存上传重试记录，请恢复浏览器本地存储后重试。"); return; }
    setPending(next);
    const result = await uploadKnowledgeDocument(libraryID, file, next, signal);
    if (signal.aborted) return;
    clearPending(); setFile(null); if (input.current) input.current.value = "";
    setSelectedID(result.id); setSelected(result); setConfirmation(null); setCursors([""]);
    setNotice(result.state === "ready" ? "文件已入库，可以在对话中查询。" : "原文件已保存。入库进度见下方文档状态，可以关闭窗口，稍后继续查看。");
  }
  async function download(signal: AbortSignal) {
    if (!selected) return;
    const blob = await readKnowledgeDocument(selected, signal);
    if (signal.aborted) return;
    const url = URL.createObjectURL(blob), link = document.createElement("a");
    link.href = url; link.download = selected.filename; link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    setNotice(`已发起原文件下载：${selected.filename}`);
  }
  async function remove(signal: AbortSignal) {
    if (!confirmation) return;
    const result = await request<KnowledgeDocument>(`${documentPath(libraryID, confirmation.id)}?expected_revision=${confirmation.revision}`, "DELETE", undefined, signal);
    if (signal.aborted) return;
    setConfirmation(null); setSelected(result.state === "deleted" ? null : result); setSelectedID(result.state === "deleted" ? "" : result.id);
    setNotice(result.state === "deleted" ? "文档已删除。" : "文档已停止检索和下载，后台正在核对并清理原文件与索引。");
  }
  return <section className="library-documents" aria-label="资料库文档">
    <div className="attachment-list-heading"><h4>文档</h4><Button size="sm" variant="ghost" disabled={blocked || loading} onClick={() => { setError(""); setRefresh(n => n + 1); }}><RefreshCw size={14} />刷新文档</Button></div>
    {available && <>
      {library.archived ? <p className="subtle">资料库已归档，上传、下载和检索已暂停。编辑者仍可删除不需要的文档。</p> : !library.documents_configured ? <p className="subtle">此资料库尚未开放文档上传，请联系管理员连接支持入库的知识源。已经保存的原文件仍可按权限下载和删除。</p> : !writable ? <p className="subtle">你拥有阅读权限，可以查询和下载资料；上传与删除由编辑者或管理者操作。</p> : <div className="attachment-upload">
        <label htmlFor="library-document-file">选择资料文件</label><Input ref={input} id="library-document-file" type="file" accept={documentAccept} disabled={blocked || !uploadEnabled} onChange={event => { setFile(event.target.files?.[0] || null); setError(""); }} />
        <p>PDF、Word、Excel、TXT、Markdown、CSV、TSV、JSON · 单文件最多 {maxUploadBytes / (1024 * 1024)} MiB</p>
        <p>{library.kind === "shared" ? `上传到“${library.name}”后，此资料库的所有阅读成员都能访问文件。` : "文件仅自己可见，入库后可跨会话查询。"}</p>
        <Button disabled={blocked || !file || !uploadEnabled} onClick={() => void perform(upload)}><Upload size={16} />{busy ? "正在处理…" : pending ? "继续上次上传" : library.kind === "shared" ? "上传到共享资料库" : "保存到个人资料"}</Button>
      </div>}
      {pending && <div className="memory-operation"><p>上次上传待确认：{pending.filename}。可重新选择同一文件继续上传；浏览器仅保存重试编号和文件校验信息。</p><Button size="sm" variant="ghost" disabled={blocked} onClick={() => { try { clearPending(); setNotice("已结束这次重试。已经上传的文档仍保留在下方列表，请先核对后再创建新的上传。"); } catch { setError("无法清除浏览器上传重试记录。"); } }}>结束这次上传重试</Button></div>}
    </>}
    {loadError && <p role="alert" className="error-text">{loadError}</p>}{error && <p role="alert" className="error-text">{error}</p>}{notice && <p role="status" className="subtle">{notice}</p>}
    {!available && loading ? <p className="subtle">正在读取资料库文档…</p> : available && (page.items.length === 0 ? <p className="subtle">此页还没有可显示的文档。</p> : <div className="attachment-list">{page.items.map(item => <Button key={item.id} aria-pressed={item.id === selectedID} disabled={blocked} variant={item.id === selectedID ? "secondary" : "outline"} onClick={() => { if (item.id !== selectedID) setSelected(null); setConfirmation(null); setSelectedID(item.id); setError(""); setRefresh(n => n + 1); }}><FileText size={18} /><span><strong>{item.filename}</strong><small>{documentLabel(item)} · {(item.bytes / 1024).toFixed(1)} KiB</small></span></Button>)}</div>)}
    {(cursors.length > 1 || !page.complete) && <div className="interaction-actions"><Button size="sm" variant="outline" disabled={blocked || loading || cursors.length === 1} onClick={() => setCursors(values => values.slice(0, -1))}>上一页文档</Button><Button size="sm" variant="outline" disabled={blocked || loading || !page.next_after} onClick={() => setCursors(values => [...values, page.next_after!])}>下一页文档</Button></div>}
    {filePreview && selected && available && !library.archived && documentCanDownload(selected) && <FilePreviewDialog target={{ kind: "library", libraryID, id: selected.id }} title={selected.filename} onClose={() => setFilePreview(false)} />}{selected && available && <section className="artifact-detail" aria-label="文档详情"><h4>{selected.filename}</h4><p className="subtle">{documentLabel(selected)} · {library.kind === "shared" ? "继承本资料库成员权限" : "仅自己可见"}</p><p className="subtle">{documentStatusDetail(selected)}</p>{selected.error_code && <p className="error-text">{errorMessage(selected.error_code)}</p>}
      <div className="interaction-actions"><Button variant="outline" disabled={blocked || library.archived || !documentCanDownload(selected)} onClick={() => setFilePreview(true)}>预览原文件</Button><Button variant="outline" disabled={blocked || library.archived || !documentCanDownload(selected)} onClick={() => void perform(download)}><Download size={15} />下载原文件</Button><Button variant="outline" disabled={blocked || library.archived || !documentCanDownload(selected)} onClick={() => onTransfer(selected, library)}>复制／移动文档</Button>{writable && <Button variant="ghost" disabled={blocked || selected.state === "deleting" || selected.state === "deleted"} onClick={() => setConfirmation(selected)}><Trash2 size={15} />删除文档</Button>}</div>
      {confirmation && <div className="memory-operation"><p>删除“{confirmation.filename}”？{library.kind === "shared" ? "资料库所有成员都将无法再检索或下载此文档。" : "此文档将停止检索和下载。"}原文件和索引随后在后台清理。</p><div className="interaction-actions"><Button variant="destructive" disabled={blocked} onClick={() => void perform(remove)}>确认删除文档</Button><Button variant="outline" disabled={blocked} onClick={() => setConfirmation(null)}>保留文档</Button></div></div>}
    </section>}
  </section>;
}
