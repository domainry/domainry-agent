import { useEffect, useRef, useState } from "react";
import { Button } from "./components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { request } from "./api.ts";
import { ApiError, describeError } from "./errors.ts";
import { sessionScope } from "./session.ts";
import { documentLabel, documentPath, type KnowledgeDocument, type KnowledgeLibrary } from "./knowledge-document-state.ts";
import { canTransferToLibrary, loadPendingTransfer, parsePendingTransfer, transferReceiptKey, type PendingTransfer, type TransferDraft } from "./document-transfer-state.ts";

type LibraryPage = { items: KnowledgeLibrary[]; complete: boolean; next_after?: string };
export function KnowledgeDocumentTransferDialog({ draft, onClose, onReceipt, onOpenTarget }: { draft: TransferDraft; onClose: () => void; onReceipt: (value: PendingTransfer | null) => void; onOpenTarget: (id: string) => void }) {
  const [pending, setPending] = useState(loadPendingTransfer);
  const source = pending?.source || draft.source, key = transferReceiptKey();
  const [mode, setMode] = useState<"copy" | "move">(pending?.mode || "copy");
  const [page, setPage] = useState<LibraryPage>({ items: [], complete: true }), [cursors, setCursors] = useState([""]);
  const [target, setTarget] = useState<KnowledgeLibrary | null>(null), [result, setResult] = useState<KnowledgeDocument | null>(null);
  const [loading, setLoading] = useState(true), [busy, setBusy] = useState(false), [error, setError] = useState("");
  const lock = useRef(false), action = useRef<AbortController | null>(null);
  const cursor = cursors.at(-1)!;
  useEffect(() => () => action.current?.abort(), []);
  useEffect(() => {
    const abort = new AbortController(); setLoading(true);
    const signal = AbortSignal.any([abort.signal, AbortSignal.timeout(15000)]);
    void Promise.all([
      request<LibraryPage>(`/agent/knowledge-libraries?${new URLSearchParams({ after: cursor, limit: "20" })}`, "GET", undefined, signal),
      pending ? request<KnowledgeLibrary>(`/agent/knowledge-libraries/${encodeURIComponent(pending.targetID)}`, "GET", undefined, signal) : Promise.resolve(null),
    ]).then(([value, previous]) => { if (!abort.signal.aborted) { setPage(value); if (previous) setTarget(previous); } }).catch(failure => { if (!abort.signal.aborted) { setTarget(null); setPage({ items: [], complete: true }); setError(describeError(failure)); } }).finally(() => { if (!abort.signal.aborted) setLoading(false); });
    return () => abort.abort();
  }, [cursor, pending]);
  const usable = (library: KnowledgeLibrary) => canTransferToLibrary(library, source.library_id, pending?.targetID);
  function clearReceipt(receipt: PendingTransfer) {
    if (parsePendingTransfer(localStorage.getItem(key))?.clientID === receipt.clientID) localStorage.removeItem(key);
    setPending(null); onReceipt(loadPendingTransfer());
  }
  async function transfer() {
    if (lock.current || !target || !usable(target)) return;
    lock.current = true; setBusy(true); setError("");
    const abort = new AbortController(); action.current = abort;
    const signal = AbortSignal.any([abort.signal, AbortSignal.timeout(60000)]), scope = sessionScope();
    try {
      const receipt: PendingTransfer = pending || { source, targetID: target.id, mode, clientID: crypto.randomUUID() };
      const persist = () => {
        const existing = parsePendingTransfer(localStorage.getItem(key));
        if (existing && existing.clientID !== receipt.clientID) throw new ApiError("agent.conversation.document_transfer_pending");
        localStorage.setItem(key, JSON.stringify(receipt));
      };
      if (navigator.locks) await navigator.locks.request(`domainry-agent:transfer:${scope}`, persist); else persist();
      if (signal.aborted || scope !== sessionScope()) return;
      setPending(receipt); onReceipt(receipt);
      const doc = await request<KnowledgeDocument>(`${documentPath(receipt.targetID)}/from-document`, "POST", { client_id: receipt.clientID, source_library_id: source.library_id, source_document_id: source.id, expected_revision: source.revision, mode: receipt.mode }, signal);
      if (signal.aborted) throw new DOMException("Aborted", "AbortError");
      if (scope !== sessionScope()) throw new ApiError("agent.web.identity_changed", 409);
      if (doc.library_id !== receipt.targetID || !/^kdoc_[a-f0-9]{32}$/.test(doc.id) || doc.filename !== source.filename || doc.bytes !== source.bytes || doc.sha256 !== source.sha256) throw new ApiError("agent.conversation.document_response_invalid");
      clearReceipt(receipt); setResult(doc);
    } catch (failure) { if (!abort.signal.aborted) setError(describeError(failure)); }
    finally { lock.current = false; if (!abort.signal.aborted) setBusy(false); }
  }
  return <Dialog open onOpenChange={open => { if (!open) onClose(); }}><DialogContent className="attachment-dialog"><DialogHeader><DialogTitle>复制或移动文档</DialogTitle><DialogDescription>选择目标资料库并确认可见范围；目标保存独立原文件，索引由后台处理。</DialogDescription></DialogHeader>
    <p><strong>{source.filename}</strong></p>
    {error && <p role="alert" className="error-text">{error}</p>}
    {result ? <><p role="status">{mode === "move" ? "目标原文件已保存，原位置已停止访问。目标索引与原位置清理会在后台继续。" : "独立副本已保存，原位置保留。"}当前目标状态：{documentLabel(result)}。</p><Button onClick={() => onOpenTarget(result.library_id)}>查看目标资料库</Button></> : <>
      {pending ? <p className="subtle">上次{pending.mode === "move" ? "移动" : "复制"}结果待确认。即使原文档已经消失，也可用原编号核对结果；不会重复创建目标文档。</p> : <><label htmlFor="document-transfer-mode">操作方式</label><select id="document-transfer-mode" disabled={busy} value={mode} onChange={event => setMode(event.target.value as "copy" | "move")}><option value="copy">复制，保留原文档</option><option value="move" disabled={!draft.canMove}>移动，原位置停止访问</option></select>{!draft.canMove && <p className="subtle">移动需要原资料库的编辑或管理权限。</p>}
        {loading ? <p className="subtle">正在读取目标资料库…</p> : <div className="attachment-list">{page.items.map(library => <Button key={library.id} variant={target?.id === library.id ? "secondary" : "outline"} disabled={busy || !usable(library)} onClick={() => setTarget(library)}><span><strong>{library.name}</strong><small>{library.kind === "personal" ? "个人 · 仅自己可见" : "共享 · 阅读成员可见"}{!usable(library) ? " · 不可选择" : ""}</small></span></Button>)}</div>}
        {(cursors.length > 1 || !page.complete) && <div className="interaction-actions"><Button disabled={busy || loading || cursors.length === 1} onClick={() => setCursors(v => v.slice(0, -1))}>上一页目标库</Button><Button disabled={busy || loading || !page.next_after} onClick={() => setCursors(v => [...v, page.next_after!])}>下一页目标库</Button></div>}
        {!loading && !page.items.some(usable) && <p className="subtle">本页没有可用目标；需要另一个可编辑且已连接入库知识源的资料库。</p>}
      </>}
      {target && <div className="memory-operation"><p>{mode === "move" ? "移动" : "复制"}“{source.filename}”到“{target.name}”？{target.kind === "shared" ? "目标资料库的所有阅读成员都能访问该文件。" : "目标文件仅自己可见。"}{mode === "move" ? "操作受理后，原位置立即停止检索和下载；索引及远端清理随后进行。" : "原位置的文件和权限保持不变。"}</p><Button disabled={busy || loading || !usable(target)} onClick={() => void transfer()}>{busy ? "正在处理…" : pending ? "继续核对上次操作" : mode === "move" ? "确认移动文档" : "确认复制文档"}</Button></div>}
      {pending && <Button variant="ghost" disabled={busy} onClick={() => { try { clearReceipt(pending); onClose(); } catch { setError("无法清除本地重试记录。"); } }}>结束重试，不撤销已受理操作</Button>}
    </>}
  </DialogContent></Dialog>;
}
