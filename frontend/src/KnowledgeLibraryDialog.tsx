import { useCallback, useEffect, useRef, useState } from "react";
import { BookOpen, Plus, RefreshCw } from "lucide-react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { request } from "./api.ts";
import { ApiError, describeError } from "./errors.ts";
import { KnowledgeDocumentsPanel } from "./KnowledgeDocumentsPanel";
import { KnowledgeDocumentTransferDialog } from "./KnowledgeDocumentTransferDialog";
import { loadPendingTransfer, type TransferDraft } from "./document-transfer-state.ts";
import { documentCanWrite } from "./knowledge-document-state.ts";
import type { KnowledgeLibrary as Library } from "./knowledge-document-state.ts";

type Page = { items: Library[]; next_after?: string; complete: boolean };
type Member = { user_id: string; role: string };
type Members = { items: Member[]; revision: number; next_after?: string; complete: boolean };
const base = "/agent/knowledge-libraries";
const roles: Record<string, string> = { reader: "阅读", editor: "编辑", manager: "管理" };

export function KnowledgeLibraryDialog({ onClose }: { onClose: () => void }) {
  const [transfer, setTransfer] = useState<TransferDraft | null>(null), [pendingTransfer, setPendingTransfer] = useState(loadPendingTransfer);
  const [page, setPage] = useState<Page>({ items: [], complete: true });
  const [cursors, setCursors] = useState([""]), [refresh, setRefresh] = useState(0);
  const [selectedID, setSelectedID] = useState(""), [selected, setSelected] = useState<Library | null>(null);
  const [members, setMembers] = useState<Members | null>(null), [memberCursors, setMemberCursors] = useState([""]);
  const [createName, setCreateName] = useState(""), [name, setName] = useState(""), [description, setDescription] = useState("");
  const [user, setUser] = useState(""), [role, setRole] = useState("reader"), [removeUser, setRemoveUser] = useState("");
  const [loading, setLoading] = useState(true), [busy, setBusy] = useState(false), [error, setError] = useState(""), [notice, setNotice] = useState("");
  const [documentBusy, setDocumentBusy] = useState(false);
  const documentLock = useRef(false);
  const onDocumentBusy = useCallback((value: boolean) => { documentLock.current = value; setDocumentBusy(value); }, []);
  const blocked = busy || documentBusy;
  const lock = useRef(false), controller = useRef<AbortController | null>(null);
  const createReceipt = useRef<{ client_id: string; kind: string; name: string } | null>(null);
  const cursor = cursors.at(-1)!, memberCursor = memberCursors.at(-1)!;

  useEffect(() => {
    const reload = () => { if (!lock.current && !documentLock.current) setRefresh(value => value + 1); };
    window.addEventListener("focus", reload);
    return () => { window.removeEventListener("focus", reload); controller.current?.abort(); };
  }, []);
  useEffect(() => {
    const abort = new AbortController(); setLoading(true);
    request<Page>(`${base}?${new URLSearchParams({ after: cursor, limit: "20" })}`, "GET", undefined, abort.signal)
      .then(value => { if (!abort.signal.aborted) setPage(value); })
      .catch(failure => { if (!abort.signal.aborted) { setPage({ items: [], complete: true }); setError(describeError(failure)); } })
      .finally(() => { if (!abort.signal.aborted) setLoading(false); });
    return () => abort.abort();
  }, [cursor, refresh]);
  useEffect(() => { setSelected(null); setMembers(null); setRemoveUser(""); }, [selectedID]);
  useEffect(() => {
    if (!selectedID) return;
    const abort = new AbortController();
    request<Library>(`${base}/${selectedID}`, "GET", undefined, abort.signal).then(async value => {
      if (abort.signal.aborted) return;
      setSelected(value); setName(value.name); setDescription(value.description);
      if (value.role === "manager") {
        try {
          const result = await request<Members>(`${base}/${value.id}/members?${new URLSearchParams({ after: memberCursor, limit: "20" })}`, "GET", undefined, abort.signal);
          if (!abort.signal.aborted) setMembers(result);
        } catch (failure) { if (!abort.signal.aborted) { setMembers(null); setError(describeError(failure)); } }
      }
    }).catch(failure => { if (!abort.signal.aborted) { setSelected(null); setMembers(null); setError(describeError(failure)); } });
    return () => abort.abort();
  }, [selectedID, memberCursor, refresh]);

  async function perform(action: (signal: AbortSignal) => Promise<void>) {
    if (lock.current || documentLock.current) return;
    lock.current = true; setBusy(true); setError(""); setNotice("");
    const abort = new AbortController(); controller.current = abort;
    try { await action(abort.signal); }
    catch (failure) { if (!abort.signal.aborted) setError(describeError(failure)); }
    finally { lock.current = false; if (!abort.signal.aborted) setBusy(false); }
  }
  function updated(value: Library, message: string) {
    setSelectedID(value.role ? value.id : ""); setSelected(value.role ? value : null);
    setMemberCursors([""]); setRefresh(n => n + 1); setNotice(message);
  }
  async function create(kind: "personal" | "shared", signal: AbortSignal) {
    const title = kind === "personal" ? "个人资料" : createName.trim();
    if (!title) return;
    const pending = createReceipt.current;
    if (pending && (pending.kind !== kind || pending.name !== title)) { setError("上次创建结果待确认，请先刷新列表核对；重试时保持资料库名称不变。"); return; }
    const input = pending || { client_id: crypto.randomUUID(), kind, name: title };
    createReceipt.current = input;
    let value: Library;
    try { value = await request<Library>(base, "POST", input, signal); }
    catch (failure) {
      if (failure instanceof ApiError && [400, 401, 403, 404, 409].includes(failure.status || 0)) createReceipt.current = null;
      throw failure;
    }
    if (signal.aborted) return;
    createReceipt.current = null; setCreateName(""); setCursors([""]); updated(value, kind === "personal" ? "已打开个人资料。" : "共享资料库已创建，当前只有你一位成员。");
  }
  async function save(signal: AbortSignal, archive = selected?.archived) {
    if (!selected) return;
    const value = await request<Library>(`${base}/${selected.id}`, "PATCH", { name: name.trim(), description, archived: archive, expected_revision: selected.revision }, signal);
    if (!signal.aborted) updated(value, archive ? "资料库已归档。成员关系保留，可随时恢复。" : "资料库设置已保存。");
  }
  async function setMember(signal: AbortSignal) {
    if (!selected || !user.trim()) return;
    const value = await request<Library>(`${base}/${selected.id}/members/${encodeURIComponent(user.trim())}`, "PUT", { role, expected_revision: members?.revision ?? selected.revision }, signal);
    if (!signal.aborted) { setUser(""); updated(value, "成员角色已保存，后续访问使用新权限。"); }
  }
  async function removeMember(signal: AbortSignal) {
    if (!selected || !removeUser) return;
    const value = await request<Library>(`${base}/${selected.id}/members/${encodeURIComponent(removeUser)}?expected_revision=${members?.revision ?? selected.revision}`, "DELETE", undefined, signal);
    if (!signal.aborted) updated(value, "成员已移出资料库。");
  }
  return <><Dialog open onOpenChange={open => { if (!open) onClose(); }}><DialogContent className="attachment-dialog library-dialog"><DialogHeader><DialogTitle>资料库</DialogTitle><DialogDescription>个人资料仅自己可见。共享资料库按成员授权，文档继承库权限。</DialogDescription></DialogHeader>
    <p className="subtle">资料库可由管理员连接知识源，连接后可在对话中检索。文档默认继承所在资料库权限，上传入库完成后可跨会话查询。</p>
    {pendingTransfer && <div className="memory-operation"><p>上次{pendingTransfer.mode === "move" ? "移动" : "复制"}结果待确认：{pendingTransfer.source.filename}。原文档不再显示时，仍可核对目标结果。</p><Button disabled={blocked} onClick={() => setTransfer({ source: pendingTransfer.source, canMove: true })}>核对上次文档操作</Button></div>}
    <div className="library-create"><Button variant="outline" disabled={blocked} onClick={() => void perform(signal => create("personal", signal))}><BookOpen size={16} />打开个人资料</Button><label htmlFor="library-create-name">新共享资料库名称</label><Input id="library-create-name" maxLength={128} value={createName} disabled={blocked} onChange={event => setCreateName(event.target.value)} placeholder="例如：项目协作资料" /><Button disabled={blocked || !createName.trim()} onClick={() => void perform(signal => create("shared", signal))}><Plus size={16} />创建共享资料库</Button></div>
    {error && <p role="alert" className="error-text">{error}</p>}{notice && <p role="status" className="subtle">{notice}</p>}
    <div className="attachment-list-heading"><strong>我的资料库</strong><Button variant="ghost" disabled={blocked || loading} onClick={() => { setError(""); setRefresh(n => n + 1); }}><RefreshCw size={14} />刷新资料库</Button></div>
    {loading ? <p>正在读取资料库…</p> : page.items.length === 0 ? <p className="subtle">还没有可访问的资料库。</p> : <div className="attachment-list">{page.items.map(item => <Button variant={item.id === selectedID ? "secondary" : "outline"} key={item.id} disabled={blocked} onClick={() => { setError(""); setMemberCursors([""]); setSelectedID(item.id); }}><BookOpen size={18} /><span><strong>{item.name}</strong><small>{item.kind === "personal" ? "个人" : "共享"} · {roles[item.role]}{item.archived ? " · 已归档" : ""}</small></span></Button>)}</div>}
    {(cursors.length > 1 || !page.complete) && <div className="interaction-actions"><Button disabled={blocked || loading || cursors.length === 1} variant="outline" onClick={() => setCursors(values => values.slice(0, -1))}>上一页</Button><Button disabled={blocked || loading || !page.next_after} variant="outline" onClick={() => setCursors(values => [...values, page.next_after!])}>下一页</Button></div>}
    {selected && <section className="library-detail" aria-label="资料库详情"><h3>{selected.name}</h3><p className="subtle">{selected.kind === "personal" ? "仅自己可见" : `我的角色：${roles[selected.role]}`} · {selected.knowledge_configured ? selected.archived ? "已配置知识源 · 归档期间停止检索" : "已配置知识源，可通过对话检索" : "尚未连接知识源"}</p>
      <KnowledgeDocumentsPanel key={selected.id} library={selected} disabled={busy} onBusyChange={onDocumentBusy} onTransfer={(doc, library) => { if (pendingTransfer) { setError("请先核对上次文档操作，或结束其重试。"); return; } setTransfer({ source: { id: doc.id, library_id: doc.library_id, filename: doc.filename, bytes: doc.bytes, sha256: doc.sha256, revision: doc.revision }, canMove: documentCanWrite(library) }); }} />
      {selected.role === "manager" ? <><label htmlFor="library-name">资料库名称</label><Input id="library-name" value={name} maxLength={128} disabled={blocked} onChange={event => setName(event.target.value)} /><label htmlFor="library-description">资料库说明</label><Input id="library-description" value={description} maxLength={1024} disabled={blocked} onChange={event => setDescription(event.target.value)} /><div className="interaction-actions"><Button disabled={blocked || !name.trim()} onClick={() => void perform(signal => save(signal))}>保存设置</Button><Button variant="outline" disabled={blocked} onClick={() => void perform(signal => save(signal, !selected.archived))}>{selected.archived ? "恢复资料库" : "归档资料库"}</Button></div></> : <p>{selected.description || "暂无说明"}</p>}
      {selected.kind === "shared" && selected.role === "manager" && <div className="library-members"><h4>成员与角色</h4><p className="subtle">阅读者可查看资料；编辑者可维护文档；管理者可维护成员及设置。至少保留一位管理者。</p>{members?.items.map(member => <div className="library-member" key={member.user_id}><span>{member.user_id}<small>{roles[member.role]}</small></span><Button size="sm" variant="ghost" disabled={blocked} onClick={() => { setUser(member.user_id); setRole(member.role); setRemoveUser(""); }}>调整角色</Button><Button size="sm" variant="ghost" disabled={blocked} onClick={() => setRemoveUser(member.user_id)}>移除成员</Button></div>)}
        {members && (memberCursors.length > 1 || !members.complete) && <div className="interaction-actions"><Button size="sm" variant="outline" disabled={blocked || memberCursors.length === 1} onClick={() => setMemberCursors(values => values.slice(0, -1))}>上一页成员</Button><Button size="sm" variant="outline" disabled={blocked || !members.next_after} onClick={() => setMemberCursors(values => [...values, members.next_after!])}>下一页成员</Button></div>}
        <label htmlFor="library-member-user">当前工作区的成员用户 ID</label><Input id="library-member-user" value={user} maxLength={255} disabled={blocked} onChange={event => setUser(event.target.value)} /><label htmlFor="library-member-role">成员角色</label><select id="library-member-role" value={role} disabled={blocked} onChange={event => setRole(event.target.value)}>{Object.entries(roles).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select><Button disabled={blocked || !user.trim()} onClick={() => void perform(setMember)}>保存成员角色</Button>
        {removeUser && <div className="memory-operation"><p>将“{removeUser}”移出“{selected.name}”？该成员将失去此资料库访问权限。</p><div className="interaction-actions"><Button variant="destructive" disabled={blocked} onClick={() => void perform(removeMember)}>确认移除成员</Button><Button variant="outline" disabled={blocked} onClick={() => setRemoveUser("")}>保留成员</Button></div></div>}
      </div>}
    </section>}
  </DialogContent></Dialog>{transfer && <KnowledgeDocumentTransferDialog draft={transfer} onReceipt={setPendingTransfer} onClose={() => { setTransfer(null); setPendingTransfer(loadPendingTransfer()); setRefresh(n => n + 1); }} onOpenTarget={id => { setTransfer(null); setSelectedID(id); setRefresh(n => n + 1); }} />}</>;
}
