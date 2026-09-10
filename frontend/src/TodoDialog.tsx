import { useEffect, useRef, useState } from "react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "./components/ui/dialog";
import { request } from "./api.ts";
import { ApiError, describeError } from "./errors.ts";
import { sessionScope } from "./session.ts";
import { parseTodoMutation, todoDeadline, type Todo, type TodoPage, type TodoMutation } from "./todo-state.ts";

export function TodoDialog({ conversationID, onClose, onSource }: { conversationID: string; onClose: () => void; onSource: (id: string) => void }) {
  const storageKey = `agent-todo-mutation:${sessionScope()}`;
  const [pending, setPending] = useState<TodoMutation | null>(() => { try { return parseTodoMutation(localStorage.getItem(storageKey)); } catch { return null; } });
  const [storageError, setStorageError] = useState(false);
  const [page, setPage] = useState<TodoPage>({ items: [], complete: true });
  const [query, setQuery] = useState("");
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("all");
  const [currentOnly, setCurrentOnly] = useState(false);
  const [cursors, setCursors] = useState([""]);
  const [refresh, setRefresh] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [listError, setListError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const locked = useRef(false);
  const [editing, setEditing] = useState<Todo | "new" | null>(null);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [date, setDate] = useState("");
  const [dateDirty, setDateDirty] = useState(false);
  const [timezone, setTimezone] = useState(Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC");
  const disabled = busy || !!pending;
  const cursor = cursors[cursors.length - 1];
  useEffect(() => {
    const controller = new AbortController();
    const params = new URLSearchParams({ query: search, status, limit: "20", cursor });
    if (currentOnly && conversationID) params.set("source_conversation_id", conversationID);
    setLoading(true); setListError("");
    request<TodoPage>(`/agent/todos?${params}`, "GET", undefined, controller.signal)
      .then(value => { if (!controller.signal.aborted) setPage(value); })
      .catch(failure => { if (!controller.signal.aborted) setListError(describeError(failure)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [search, status, cursor, currentOnly, conversationID, refresh]);
  function edit(todo: Todo | "new") {
    setEditing(todo); setTitle(todo === "new" ? "" : todo.title); setDescription(todo === "new" ? "" : todo.description || "");
    setDate(todo === "new" ? "" : todo.due_date || ""); setDateDirty(false);
    setTimezone(todo === "new" ? Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC" : todo.timezone);
  }
  async function apply(command: TodoMutation) {
    if (locked.current) return;
    locked.current = true; setBusy(true); setError(""); setNotice("");
    const mutation = pending || command;
    setPending(mutation);
    try { localStorage.setItem(storageKey, JSON.stringify(mutation)); } catch { setStorageError(true); }
    const clear = () => { setPending(null); try { localStorage.removeItem(storageKey); } catch { setStorageError(true); } };
    try {
      await request(mutation.path, mutation.method, mutation.body);
      clear(); setEditing(null); setNotice(`${mutation.label}已完成。`); setCursors([""]); setRefresh(value => value + 1);
    } catch (failure) {
      setError(describeError(failure));
      if (failure instanceof ApiError && [400, 403, 404, 409].includes(failure.status || 0)) {
        clear(); setCursors([""]); setRefresh(value => value + 1);
      }
    } finally { locked.current = false; setBusy(false); }
  }
  function change(todo: Todo, patch: Record<string, unknown>, label: string) {
    void apply({ path: `/agent/todos/${todo.id}`, method: "PATCH", body: { client_id: crypto.randomUUID(), expected_revision: todo.revision, patch }, label });
  }
  function save() {
    if (!title.trim() || new TextEncoder().encode(title).length > 128 || new TextEncoder().encode(description).length > 1024) { setError("标题最多 128 字节，说明最多 1024 字节，请缩短后保存。"); return; }
    if (editing === "new") void apply({ path: "/agent/todos", method: "POST", body: { client_id: crypto.randomUUID(), ...(conversationID ? { source_conversation_id: conversationID } : {}), items: [{ title: title.trim(), description, timezone, ...(date ? { due_date: date } : {}) }] }, label: "创建事项" });
    else if (editing) change(editing, { title: title.trim(), description, timezone, ...(dateDirty ? { due_date: date } : {}) }, "修改事项");
  }
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose(); }}><DialogContent className="todo-dialog">
    <DialogHeader><DialogTitle>个人待办</DialogTitle><DialogDescription>管理工作事项、截止日期和完成状态。批次内的项次会保留，方便继续讨论和修改。</DialogDescription></DialogHeader>
    <form className="todo-toolbar" onSubmit={event => { event.preventDefault(); setSearch(query); setCursors([""]); }}>
      <Input aria-label="搜索待办" placeholder="搜索标题或说明" value={query} onChange={event => setQuery(event.target.value)} />
      <select aria-label="待办状态" value={status} onChange={event => { setStatus(event.target.value); setCursors([""]); }}><option value="all">全部状态</option><option value="open">未完成</option><option value="completed">已完成</option></select>
      <Button type="submit" variant="outline" size="sm">搜索</Button><Button type="button" size="sm" disabled={disabled} onClick={() => edit("new")}>新增事项</Button>
    </form>
    {conversationID && <label className="memory-write-scope"><input type="checkbox" checked={currentOnly} onChange={event => { setCurrentOnly(event.target.checked); setCursors([""]); }} />只看来自当前会话的事项</label>}
    {(error || listError) && <p role="alert" className="text-destructive">{error || listError}</p>}{notice && <p role="status">{notice}</p>}
    {pending && <div className="composer-notice"><p>“{pending.label}”的结果尚未确认，重试会沿用原请求。</p><Button type="button" disabled={busy} variant="outline" onClick={() => void apply(pending)}>重试原操作</Button></div>}
    {storageError && <p className="subtle">浏览器无法保存请求回执，请在当前页面确认操作结果后再刷新。</p>}
    {editing && <form className="todo-editor" onSubmit={event => { event.preventDefault(); save(); }}>
      <strong>{editing === "new" ? "新增事项" : `修改第 ${editing.position} 项`}</strong>
      <label>标题<Input aria-label="待办标题" value={title} disabled={disabled} onChange={event => setTitle(event.target.value)} /></label>
      <label>说明<Textarea aria-label="待办说明" value={description} disabled={disabled} onChange={event => setDescription(event.target.value)} /></label>
      <div className="todo-toolbar"><label>截止日期<Input aria-label="截止日期" type="date" value={date} disabled={disabled} onChange={event => { setDate(event.target.value); setDateDirty(true); }} /></label><label>时区<Input aria-label="待办时区" value={timezone} disabled={disabled || editing !== "new" && !!editing.due_at && !dateDirty} onChange={event => setTimezone(event.target.value)} /></label></div>
      {editing !== "new" && editing.due_at && <small className="subtle">当前截止时间：{todoDeadline(editing)}。选择日期后将改为按日期截止；保持此栏不变会保留具体时间。</small>}
      <div className="interaction-actions"><Button type="submit" disabled={disabled || !title.trim()}>保存事项</Button><Button type="button" variant="ghost" disabled={busy} onClick={() => setEditing(null)}>关闭编辑</Button></div>
    </form>}
    <div className="todo-list" aria-label="待办列表">
      {loading ? <p className="subtle">正在读取待办…</p> : !page.items.length ? <p className="subtle">当前没有匹配的事项。</p> : page.items.map((todo, index) => <div className="todo-card" key={todo.id}>
        {(index === 0 || page.items[index - 1].batch_id !== todo.batch_id) && <small className="subtle">{new Date(todo.created_at).toLocaleString()} 创建的事项</small>}
        <div className="todo-heading"><input type="checkbox" aria-label={`完成事项 ${todo.title}`} checked={todo.status === "completed"} disabled={disabled} onChange={event => change(todo, { status: event.target.checked ? "completed" : "open" }, event.target.checked ? "完成事项" : "重新打开事项")} /><strong data-completed={todo.status === "completed"}>第 {todo.position} 项 · {todo.title}</strong></div>
        {todo.description && <p>{todo.description}</p>}<small>{todoDeadline(todo)}</small>
        <div className="interaction-actions"><Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={() => edit(todo)}>修改</Button>{todo.source_conversation_id && <Button type="button" variant="ghost" size="sm" onClick={() => onSource(todo.source_conversation_id!)}>来源会话</Button>}<Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={() => void apply({ path: `/agent/todos/${todo.id}`, method: "DELETE", body: { client_id: crypto.randomUUID(), expected_revision: todo.revision }, label: `删除“${todo.title}”` })}>删除</Button></div>
      </div>)}
    </div>
    <div className="todo-toolbar"><Button variant="ghost" disabled={loading || cursors.length === 1} onClick={() => setCursors(values => values.slice(0, -1))}>上一页</Button><small className="subtle">第 {cursors.length} 页{!page.complete ? " · 还有更多事项" : ""}</small><Button variant="ghost" disabled={loading || !page.next_cursor} onClick={() => setCursors(values => [...values, page.next_cursor!])}>下一页</Button><Button variant="ghost" disabled={loading} onClick={() => { setCursors([""]); setRefresh(value => value + 1); }}>刷新</Button></div>
  </DialogContent></Dialog>;
}
