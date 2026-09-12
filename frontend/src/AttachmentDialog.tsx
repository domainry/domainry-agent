import { useCallback, useEffect, useRef, useState } from "react";
import { Download, FileText, RefreshCw, Trash2, Upload } from "lucide-react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { FilePreviewDialog } from "./FilePreviewDialog";
import { AttachmentLibrarySave } from "./AttachmentLibrarySave";
import { AttachmentIndexPanel } from "./AttachmentIndexPanel";
import { request } from "./api.ts";
import { describeError } from "./errors.ts";
import { sessionScope } from "./session.ts";
import { attachmentAccept, attachmentHash, attachmentLabel, attachmentMaxBytes, attachmentPath, parsePendingAttachment, readAttachment, updateAttachmentPage, uploadAttachment, type Attachment, type AttachmentPage, type PendingAttachment } from "./attachment-state.ts";

export function AttachmentDialog({ conversationID, archived, onClose, onOpenLibrary }: { conversationID: string; archived: boolean; onClose: () => void; onOpenLibrary: () => void }) {
  const storageKey = `agent-attachment-upload:${sessionScope()}:${conversationID}`;
  const [pending, setPending] = useState<PendingAttachment | null>(() => { try { return parsePendingAttachment(localStorage.getItem(storageKey)); } catch { return null; } });
  const [file, setFile] = useState<File | null>(null), [page, setPage] = useState<AttachmentPage>({ items: [], complete: true });
  const [cursors, setCursors] = useState([""]), [refresh, setRefresh] = useState(0);
  const [detailRefresh,setDetailRefresh]=useState(0),[indexBusy,setIndexBusy]=useState(false);
  const [selectedID, setSelectedID] = useState(""), [selected, setSelected] = useState<Attachment | null>(null);
  const [busy, setBusy] = useState(false), [loading, setLoading] = useState(true);
  const [error, setError] = useState(""), [notice, setNotice] = useState(""), [preview, setPreview] = useState<string | null>(null);
  const [filePreview, setFilePreview] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const requestController = useRef<AbortController | null>(null), fileInput = useRef<HTMLInputElement>(null), lock = useRef(false);
  const [saving, setSaving] = useState(false), saveLock = useRef(false);
  const indexLock=useRef(false), selection=useRef(""), selectedFromResponse=useRef<Attachment|null>(null);
  selection.current=selectedID;
  const onIndexBusy=useCallback((value:boolean)=>{indexLock.current=value;setIndexBusy(value);},[]);
  const onSaveBusy = useCallback((value: boolean) => { saveLock.current = value; setSaving(value); }, []);
  const blocked = busy || saving || indexBusy;
  const cursor = cursors[cursors.length - 1];
  const pageCursor=useRef(cursor);pageCursor.current=cursor;
  const applyAttachment=useCallback((result:Attachment,add=false)=>{
    setSelected(result);
    setPage(previous=>updateAttachmentPage(previous,result,pageCursor.current,add));
  },[]);
  useEffect(() => {
    const reload = () => { if (!lock.current && !saveLock.current && !indexLock.current) { setPreview(null); if(selection.current)setDetailRefresh(n=>n+1);else setRefresh(n => n + 1); } };
    window.addEventListener("focus", reload);
    return () => { window.removeEventListener("focus", reload); requestController.current?.abort(); };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError("");
    request<AttachmentPage>(`${attachmentPath(conversationID)}?${new URLSearchParams({ after: cursor, limit: "20" })}`, "GET", undefined, controller.signal).then(result => { if (!controller.signal.aborted) setPage(result); }).catch(failure => { if (!controller.signal.aborted) { setPage({ items: [], complete: true }); setError(describeError(failure)); } }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [conversationID, cursor, refresh]);
  useEffect(() => {
    setSelected(value=>value?.id===selectedID?value:null);
    if (!selectedID) return;
    if(selectedFromResponse.current?.id===selectedID){selectedFromResponse.current=null;return;}
    const controller = new AbortController();
    request<Attachment>(attachmentPath(conversationID, selectedID), "GET", undefined, controller.signal).then(result => { if (!controller.signal.aborted) applyAttachment(result); }).catch(failure => { if (!controller.signal.aborted) {setSelected(null);setPreview(null);setFilePreview(false);setError(describeError(failure));} });
    return () => controller.abort();
  }, [conversationID, selectedID, detailRefresh,applyAttachment]);
  useEffect(()=>setDeleting(false),[selectedID]);
  useEffect(() => {
    if(blocked)return;
    const watchingSelected=selected?.indexing?.requested&&["indexing","failed","needs_reconcile","deleting"].includes(selected.state);
    if (!watchingSelected&&!page.items.some(item => ["uploading","indexing","needs_reconcile"].includes(item.state))) return;
    const timer = setTimeout(() => { if (!lock.current && !saveLock.current && !indexLock.current) {if(watchingSelected)setDetailRefresh(n=>n+1);else setRefresh(n => n + 1);} }, 5000);
    return () => clearTimeout(timer);
  }, [page, selected,refresh,detailRefresh,blocked]);
  function clearPending() { localStorage.removeItem(storageKey); setPending(null); }
  async function perform(action: (signal: AbortSignal) => Promise<void>) {
    if (lock.current || saveLock.current || indexLock.current) return;
    lock.current = true; setBusy(true); setError(""); setNotice("");
    const controller = new AbortController(); requestController.current = controller;
    try { await action(controller.signal); } catch (failure) { if (!controller.signal.aborted) setError(describeError(failure)); }
    finally { lock.current = false; if (!controller.signal.aborted) setBusy(false); }
  }
  async function upload(signal: AbortSignal) {
    if (!file) return;
    if (!file.size || file.size > attachmentMaxBytes) { setError("请选择不超过 16 MiB 的非空文件。"); return; }
    if (!attachmentAccept.split(",").some(extension => file.name.toLowerCase().endsWith(extension))) { setError("请选择 PDF、Word、Excel 或支持的文本文件。"); return; }
    const sha256 = await attachmentHash(await file.arrayBuffer());
    if (signal.aborted) return;
    if (pending && (pending.filename !== file.name || pending.bytes !== file.size || pending.sha256 !== sha256)) { setError(`请重新选择上次的“${pending.filename}”以重试，或先结束这次上传重试。`); return; }
    const next = pending || { clientID: crypto.randomUUID(), filename: file.name, bytes: file.size, sha256 };
    // Keep only a bounded retry receipt locally; never persist the file bytes.
    try { localStorage.setItem(storageKey, JSON.stringify(next)); } catch { setError("浏览器无法保存上传重试记录，请恢复本地存储后重试。"); return; }
    setPending(next);
    const result = await uploadAttachment(conversationID, file, next, signal);
    if (signal.aborted) return;
    clearPending(); setFile(null); if (fileInput.current) fileInput.current.value = "";
    selectedFromResponse.current=selectedID===result.id?null:result;setSelectedID(result.id);applyAttachment(result,true);
    setNotice("文件已私有保存，可预览、下载或另存到资料库。");
  }
  async function openFile(signal: AbortSignal, previewOnly: boolean) {
    if (!selected) return;
    setPreview(null);
    const blob = await readAttachment(selected, signal);
    if (signal.aborted) return;
    if (previewOnly) {
      let text: string;
      try { text = new TextDecoder("utf-8", { fatal: true }).decode(await blob.arrayBuffer()); }
      catch { setError("此文件不是 UTF-8 文本，请下载原文件查看。"); return; }
      if (/[\x00-\x08\x0b\x0c\x0e-\x1f]/.test(text)) { setError("此文件含二进制内容，请下载原文件查看。"); return; }
      setPreview(text.slice(0, 65536));
      if (text.length > 65536) setNotice("预览显示前 65,536 个字符，完整内容请下载查看。");
    } else {
      const url = URL.createObjectURL(blob), link = document.createElement("a");
      link.href = url; link.download = selected.filename; link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setNotice(`已发起原文件下载：${selected.filename}`);
    }
  }
  async function remove(signal: AbortSignal) {
    if (!selected) return;
    const result = await request<Attachment>(`${attachmentPath(conversationID, selected.id)}?expected_revision=${selected.revision}`, "DELETE", undefined, signal);
    if (signal.aborted) return;
    if(result.state==="deleted"){setSelectedID("");setSelected(null);}else applyAttachment(result);
    setPage(previous=>({...previous,items:previous.items.filter(item=>item.id!==result.id)}));setPreview(null);setDeleting(false);
    setNotice(result.state === "deleted" ? "附件已删除。" : "附件已停止访问，原文件正在后台清理。");
  }
  const textPreview = selected && (selected.content_type.startsWith("text/") || selected.content_type === "application/json");
  const readable = selected && ["stored", "indexing", "ready", "failed", "needs_reconcile"].includes(selected.state);
  return <Dialog open onOpenChange={open => { if (!open) onClose(); }}><DialogContent className="attachment-dialog"><DialogHeader><DialogTitle>会话附件</DialogTitle><DialogDescription>文件仅属于当前用户的这段会话。关闭窗口不删除附件；上传成功不代表已经可以检索。</DialogDescription></DialogHeader>
    <div className="attachment-upload"><label htmlFor="attachment-file">选择附件</label><Input ref={fileInput} id="attachment-file" type="file" accept={attachmentAccept} disabled={blocked || archived} onChange={event => { setFile(event.target.files?.[0] || null); setError(""); }} /><p>PDF、Word、Excel、TXT、Markdown、CSV、TSV、JSON · 每个文件最多 16 MiB</p><Button disabled={!file || blocked || archived} onClick={() => void perform(upload)}><Upload size={16} />{busy ? "正在处理…" : pending ? "重试上传" : "上传并私有保存"}</Button>{archived && <p>会话已归档，恢复会话后可以上传。</p>}</div>
    {pending && <div className="memory-operation"><p>上次上传待确认：{pending.filename}。刷新后请重新选择同一个文件重试。</p><Button variant="ghost" size="sm" disabled={blocked} onClick={() => { try { clearPending(); setNotice("已结束重试。已经传到服务器的附件仍可在下方管理。"); } catch { setError("无法清除本地重试记录。"); } }}>结束这次上传重试</Button></div>}
    {error && <p role="alert" className="error-text">{error}</p>}{notice && <p role="status" className="subtle">{notice}</p>}
    <div className="attachment-list-heading"><strong>已保存的附件</strong><Button variant="ghost" size="sm" disabled={blocked || loading} onClick={() => setRefresh(n => n + 1)}><RefreshCw size={14} />刷新列表</Button></div>
    {loading ? <p className="subtle">正在读取附件…</p> : page.items.length === 0 ? <p className="subtle">这段会话还没有可显示的附件。</p> : <div className="attachment-list">{page.items.map(item => <Button key={item.id} variant={selectedID === item.id ? "secondary" : "outline"} disabled={blocked} onClick={() => { selectedFromResponse.current=null;setSelectedID(item.id);setDetailRefresh(n=>n+1);setPreview(null);setError(""); }}><FileText size={18} /><span><strong>{item.filename}</strong><small>{attachmentLabel(item.state)} · {(item.bytes / 1024).toFixed(1)} KiB</small></span></Button>)}</div>}
    {(cursors.length > 1 || !page.complete) && <div className="interaction-actions"><Button variant="outline" size="sm" disabled={blocked || loading || cursors.length === 1} onClick={() => setCursors(values => values.slice(0, -1))}>上一页</Button><Button variant="outline" size="sm" disabled={blocked || loading || !page.next_after} onClick={() => setCursors(values => [...values, page.next_after!])}>下一页</Button></div>}
    {filePreview && selected && readable && <FilePreviewDialog target={{ kind: "attachment", conversationID, id: selected.id }} title={selected.filename} onClose={() => setFilePreview(false)} />}{selected && <section className="artifact-detail" aria-label="附件详情"><h3>{selected.filename}</h3><p className="subtle">仅当前会话可见 · {attachmentLabel(selected.state)}</p>{selected.state === "stored" && <p>原文件仅限当前会话，可在线预览、下载或另存到资料库。</p>}{selected.error_code && !selected.indexing?.requested && <p className="error-text">处理未完成；原文件如已保存，可尝试下载。</p>}<AttachmentIndexPanel key={`index:${selected.id}`} attachment={selected} disabled={busy||saving} onChange={applyAttachment} onBusyChange={onIndexBusy} /><div className="interaction-actions">{["pdf", "docx", "xlsx"].includes(selected.filename.split(".").at(-1)?.toLowerCase() || "") && <Button variant="outline" disabled={blocked || !readable} onClick={() => setFilePreview(true)}>预览原文件</Button>}{textPreview && <Button variant="outline" disabled={blocked || !readable} onClick={() => void perform(signal => openFile(signal, true))}>预览文本</Button>}<Button variant="outline" disabled={blocked || !readable} onClick={() => void perform(signal => openFile(signal, false))}><Download size={15} />下载原文件</Button><Button variant="ghost" disabled={blocked || selected.state === "deleting"} onClick={() => setDeleting(true)}><Trash2 size={15} />删除附件</Button></div>{readable && <AttachmentLibrarySave key={`library:${selected.id}`} attachment={selected} disabled={busy||indexBusy} onBusyChange={onSaveBusy} onOpenLibrary={onOpenLibrary} />}{!textPreview && !["pdf", "docx", "xlsx"].includes(selected.filename.split(".").at(-1)?.toLowerCase() || "") && <p className="subtle">此格式暂不支持在线预览，可下载原文件查看。</p>}{deleting && <div className="memory-operation"><p>删除“{selected.filename}”？附件会立即停止访问，并清理原文件。</p><div className="interaction-actions"><Button variant="destructive" disabled={blocked} onClick={() => void perform(remove)}>确认删除附件</Button><Button variant="outline" disabled={blocked} onClick={() => setDeleting(false)}>保留附件</Button></div></div>}{preview !== null && <pre className="attachment-text" aria-label="附件文本预览">{preview}</pre>}</section>}
  </DialogContent></Dialog>;
}
