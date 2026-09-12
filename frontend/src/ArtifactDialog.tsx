import { useEffect, useRef, useState } from "react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "./components/ui/dialog";
import { request } from "./api.ts";
import { ApiError, describeError } from "./errors.ts";
import { sessionScope } from "./session.ts";
import { ArtifactPreview } from "./ArtifactPreview.tsx";
import { artifactPath, downloadArtifact, parseArtifactMutation, type ArtifactPage, type ArtifactVersion, type ArtifactVersions, type ArtifactMutation, type ArtifactExport } from "./artifact-state.ts";

type ArtifactDialogProps = {
  conversationID?: string;
  initial?: { id: string; version: number };
  onClose: () => void;
  onSource?: (conversationID: string) => void;
  onRun?: (conversationID: string, runID: string) => void;
};

export function ArtifactDialog({ conversationID = "", initial, onClose, onSource, onRun }: ArtifactDialogProps) {
  const key = `agent-artifact-mutation:${sessionScope()}`;
  const [pending, setPending] = useState<ArtifactMutation | null>(() => { try { return parseArtifactMutation(localStorage.getItem(key)); } catch { return null; } });
  const [selected, setSelected] = useState<{ id: string; version: number } | null>(() => pending ? { id: pending.artifactID, version: pending.body.expected_version || pending.body.version || 0 } : initial || null);
  const [page, setPage] = useState<ArtifactPage>({ items: [], complete: true });
  const [value, setValue] = useState<ArtifactVersion | null>(null);
  const [versions, setVersions] = useState<ArtifactVersions>({ items: [], complete: true });
  const [query, setQuery] = useState(""), [search, setSearch] = useState("");
  const [currentOnly, setCurrentOnly] = useState(false), [cursors, setCursors] = useState([""]);
  const [refresh, setRefresh] = useState(0), [loading, setLoading] = useState(false);
  const [error, setError] = useState(""), [listError, setListError] = useState(""), [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false), [editing, setEditing] = useState(false), [storageError, setStorageError] = useState(false);
  const [title, setTitle] = useState(""), [markdown, setMarkdown] = useState("");
  const [row, setRow] = useState(1), [column, setColumn] = useState(""), [cell, setCell] = useState(""), [empty, setEmpty] = useState(false), [cellDirty, setCellDirty] = useState(false);
  const lock = useRef(false);
  const editingRef = useRef(editing); editingRef.current = editing;
  const selectionRef = useRef(selected); selectionRef.current = selected;
  const versionRequest = useRef<AbortController | null>(null);
  const cursor = cursors[cursors.length - 1];
  useEffect(() => { const reload = () => { if (!editingRef.current && !lock.current) setRefresh(n => n + 1); }; window.addEventListener("focus", reload); return () => { window.removeEventListener("focus", reload); versionRequest.current?.abort(); }; }, []);
  useEffect(() => {
    const controller = new AbortController();
    const params = new URLSearchParams({ query: search, limit: "20", cursor });
    if (currentOnly && conversationID) params.set("source_conversation_id", conversationID);
    setListError("");
    request<ArtifactPage>(`/agent/artifacts?${params}`, "GET", undefined, controller.signal).then(result => { if (!controller.signal.aborted) setPage(result); }).catch(failure => { if (!controller.signal.aborted) { setPage({ items: [], complete: true }); setListError(describeError(failure)); } });
    return () => controller.abort();
  }, [search, cursor, currentOnly, conversationID, refresh]);
  useEffect(() => {
    setValue(null); setVersions({ items: [], complete: true }); setError(""); setEditing(false);
    if (!selected) { setLoading(false); return; }
    const controller = new AbortController(); setLoading(true);
    request<ArtifactVersion>(`${artifactPath(selected.id)}?version=${selected.version}`, "GET", undefined, controller.signal).then(result => { if (!controller.signal.aborted) setValue(result); }).catch(failure => { if (!controller.signal.aborted) setError(describeError(failure)); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    request<ArtifactVersions>(`${artifactPath(selected.id)}/versions?limit=50`, "GET", undefined, controller.signal).then(result => { if (!controller.signal.aborted) setVersions(result); }).catch(() => { /* Read permission can exist without version-list permission. */ });
    return () => controller.abort();
  }, [selected?.id, selected?.version, refresh]);
  function clearPending() { setPending(null); try { localStorage.removeItem(key); } catch { setStorageError(true); } }
  function selectArtifact(next: { id: string; version: number }) { setNotice(""); setSelected(next); }
  async function loadVersions() {
    if (lock.current || !value || !versions.next_before) return;
    lock.current = true; setBusy(true);
    const selection = selected;
    const controller = new AbortController(); versionRequest.current = controller;
    try {
      const next = await request<ArtifactVersions>(`${artifactPath(value.artifact.id)}/versions?limit=50&before=${versions.next_before}`, "GET", undefined, controller.signal);
      if (!controller.signal.aborted && selectionRef.current?.id === selection?.id && selectionRef.current?.version === selection?.version) setVersions(previous => ({ ...next, items: [...previous.items, ...next.items.filter(item => !previous.items.some(old => old.version === item.version))] }));
    } catch (failure) { if (!controller.signal.aborted) setError(describeError(failure)); }
    finally { lock.current = false; if (!controller.signal.aborted) setBusy(false); }
  }
  async function apply(command: ArtifactMutation) {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError(""); setNotice("");
    const mutation = pending || command;
    setPending(mutation); try { localStorage.setItem(key, JSON.stringify(mutation)); } catch { setStorageError(true); }
    try {
      if (mutation.kind === "edit") {
        const result = await request<ArtifactVersion>(artifactPath(mutation.artifactID), "PATCH", mutation.body);
        clearPending(); setEditing(false); setSelected({ id: result.artifact.id, version: result.artifact.version }); setRefresh(n => n + 1); setNotice(`已保存为版本 ${result.artifact.version}。`);
      } else {
        const result = await request<ArtifactExport>(`${artifactPath(mutation.artifactID)}/exports`, "POST", mutation.body);
        clearPending(); await downloadArtifact(result); setNotice(`已下载版本 ${result.version} · ${result.filename}${result.formula_guarded ? "；公式形式的文本已作防护处理" : ""}`);
      }
    } catch (failure) {
      setError(describeError(failure));
      if (failure instanceof ApiError && [400, 403, 404, 409].includes(failure.status || 0)) {
        clearPending();
        if ([403, 404].includes(failure.status || 0)) { setValue(null); setVersions({ items: [], complete: true }); setEditing(false); setMarkdown(""); setTitle(""); setCell(""); }
      }
    } finally { lock.current = false; setBusy(false); }
  }
  function startEdit() {
    if (!value) return;
    setTitle(value.artifact.title); setMarkdown(value.content.markdown || ""); setRow(1); setColumn(value.content.table?.columns[0]?.key || "");
    const first = value.content.table?.rows[0]?.[0]; setCell(first || ""); setEmpty(first === null); setCellDirty(false); setEditing(true); setError("");
  }
  function chooseCell(nextRow: number, nextColumn: string) {
    setRow(nextRow); setColumn(nextColumn); setCellDirty(false);
    const index = value?.content.table?.columns.findIndex(c => c.key === nextColumn) ?? -1;
    const original = value?.content.table?.rows[nextRow - 1]?.[index]; setCell(original || ""); setEmpty(original === null);
  }
  function save() {
    if (!value || !title.trim()) return;
    const patch: Record<string, unknown> = {};
    if (title !== value.artifact.title) patch.title = title;
    if (value.content.kind === "markdown" && markdown !== (value.content.markdown || "")) patch.content = { kind: "markdown", markdown };
    if (value.content.table && cellDirty) {
      if (!Number.isInteger(row) || row < 1 || row > value.content.table.rows.length) { setError("请选择实际存在的行号。"); return; }
      patch.cells = [{ row: row - 1, column, value: empty ? null : cell }];
    }
    if (!Object.keys(patch).length) { setEditing(false); return; }
    void apply({ kind: "edit", artifactID: value.artifact.id, body: { client_id: crypto.randomUUID(), expected_version: value.artifact.version, patch } });
  }
  const disabled = busy || !!pending;
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose(); }}><DialogContent className="artifact-dialog">
    <DialogHeader><DialogTitle>我的成果</DialogTitle><DialogDescription>预览周报、表格和图表，修改内容并下载指定版本。</DialogDescription></DialogHeader>
    <form className="todo-toolbar" onSubmit={event => { event.preventDefault(); setSearch(query); setCursors([""]); }}><Input aria-label="搜索成果" placeholder="搜索成果标题" value={query} onChange={event => setQuery(event.target.value)} /><Button type="submit" variant="outline" size="sm">搜索</Button><Button type="button" variant="ghost" size="sm" disabled={busy || editing} onClick={() => setRefresh(n => n + 1)}>刷新成果</Button></form>
    {conversationID && <label className="memory-write-scope"><input type="checkbox" checked={currentOnly} onChange={event => { setCurrentOnly(event.target.checked); setCursors([""]); }} />只看来自当前会话的成果</label>}
    {listError && <p role="alert" className="text-destructive">{listError}</p>}
    {!!page.items.length && <div className="artifact-list">{page.items.map(item => <Button key={item.id} variant={selected?.id === item.id ? "secondary" : "ghost"} disabled={disabled || editing} onClick={() => selectArtifact({ id: item.id, version: 0 })}><span>{item.title}</span><small>版本 {item.version} · {item.kind === "markdown" ? "文档" : item.kind === "table" ? "表格" : "图表"}</small></Button>)}</div>}
    {!page.items.length && !listError && <p className="subtle">暂无匹配成果。可以在对话中让 Agent 生成一份周报或表格。</p>}
    {page.omitted && <p className="subtle">部分成果的来源当前不可读取，已从列表隐藏。</p>}
    {(cursors.length > 1 || !page.complete) && <div className="todo-toolbar"><Button size="sm" variant="outline" disabled={cursors.length < 2} onClick={() => setCursors(v => v.slice(0, -1))}>上一页成果</Button><Button size="sm" variant="outline" disabled={page.complete || !page.next_cursor} onClick={() => setCursors(v => [...v, page.next_cursor!])}>下一页成果</Button></div>}
    {notice && <p role="status">{notice}</p>}{error && <p role="alert" className="text-destructive">{error}</p>}
    {pending && <div className="composer-notice"><p>上次成果操作的结果尚未确认。重试会保留原目标、版本和修改内容。</p><Button disabled={busy} variant="outline" onClick={() => void apply(pending)}>重试原操作</Button></div>}
    {storageError && <p className="subtle">浏览器无法保留请求，请在当前页面核对结果后再刷新。</p>}
    {loading && <p role="status">正在读取成果…</p>}
    {value && <section className="artifact-detail" aria-label="成果内容">
      <div className="todo-toolbar"><h3>{value.artifact.title}</h3><select aria-label="成果版本" disabled={disabled || editing} value={selected?.version || 0} onChange={event => selectArtifact({ id: value.artifact.id, version: Number(event.target.value) })}><option value={0}>最新版本</option>{!versions.items.some(item => item.version === value.artifact.version) && <option value={value.artifact.version}>版本 {value.artifact.version}</option>}{versions.items.map(item => <option key={item.version} value={item.version}>版本 {item.version}</option>)}</select><span className="subtle">当前显示版本 {value.artifact.version}</span></div>
      <p className="subtle">更新于 {new Date(value.artifact.updated_at).toLocaleString()} · {(value.artifact.bytes / 1024).toFixed(1)} KB</p>
      {value.artifact.source_conversation_id && <div className="todo-toolbar" aria-label="成果来源关联">
        <span className="subtle">来源关联</span>
        {onSource && <Button type="button" variant="ghost" size="sm" disabled={disabled || editing} onClick={() => onSource(value.artifact.source_conversation_id!)}>来源会话</Button>}
        {value.artifact.source_run_id && onRun && <Button type="button" variant="ghost" size="sm" disabled={disabled || editing} onClick={() => onRun(value.artifact.source_conversation_id!, value.artifact.source_run_id!)}>来源消息与处理记录</Button>}
      </div>}
      {versions.omitted && <p className="subtle">部分版本的来源当前不可读取。</p>}
      {!versions.complete && versions.next_before && <Button size="sm" variant="ghost" disabled={busy} onClick={() => void loadVersions()}>加载更早版本</Button>}
      {!editing ? <><div className="todo-toolbar"><Button size="sm" variant="outline" disabled={disabled} onClick={startEdit}>修改内容</Button><Button size="sm" disabled={disabled} onClick={() => void apply({ kind: "export", artifactID: value.artifact.id, body: { client_id: crypto.randomUUID(), version: value.artifact.version, format: value.content.kind === "markdown" ? "markdown" : "csv" } })}>下载此版本{value.content.kind === "markdown" ? " Markdown" : " CSV"}</Button></div><ArtifactPreview key={`${value.artifact.id}:${value.artifact.version}`} content={value.content} /></> : <form className="todo-editor" onSubmit={event => { event.preventDefault(); save(); }}>
        <label>标题<Input aria-label="成果标题" value={title} disabled={disabled} onChange={event => setTitle(event.target.value)} /></label>
        {value.content.kind === "markdown" ? <label>Markdown 正文<Textarea aria-label="成果正文" className="artifact-editor" value={markdown} disabled={disabled} onChange={event => setMarkdown(event.target.value)} /></label> : value.content.table && !!value.content.table.rows.length && <><div className="todo-toolbar"><label>行号<Input aria-label="编辑行号" type="number" min={1} max={value.content.table.rows.length} value={row} disabled={disabled} onChange={event => chooseCell(Number(event.target.value), column)} /></label><label>列<select aria-label="编辑列" value={column} disabled={disabled} onChange={event => chooseCell(row, event.target.value)}>{value.content.table.columns.map(c => <option key={c.key} value={c.key}>{c.label}</option>)}</select></label></div><label>单元格值<Textarea aria-label="单元格值" value={cell} disabled={disabled || empty} onChange={event => { setCell(event.target.value); setCellDirty(true); }} /></label><label className="memory-write-scope"><input type="checkbox" checked={empty} disabled={disabled} onChange={event => { setEmpty(event.target.checked); setCellDirty(true); }} />清空这个单元格</label></>}
        <div className="todo-toolbar"><Button type="submit" disabled={disabled}>保存新版本</Button><Button type="button" variant="ghost" disabled={busy} onClick={() => setEditing(false)}>取消修改</Button></div>
      </form>}
    </section>}
  </DialogContent></Dialog>;
}
