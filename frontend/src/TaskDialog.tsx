import { useEffect, useState } from "react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { request } from "./api.ts";
import { describeError, errorMessage } from "./errors.ts";
import { taskCanCancel, taskCanResume, taskControlPath, taskListPath, taskNeedsRefresh, taskPath, taskStatusLabel, type ConversationTaskDetail, type ConversationTaskPage } from "./task-state.ts";

export function TaskDialog({ conversationID, onClose, onSource, onRun, onArtifact }: {
  conversationID: string; onClose: () => void; onSource: (conversationID: string) => void;
  onRun: (conversationID: string, runID: string) => void; onArtifact: (id: string, version: number) => void;
}) {
  const [page, setPage] = useState<ConversationTaskPage>({ items: [], complete: true });
  const [selectedID, setSelectedID] = useState("");
  const [detail, setDetail] = useState<ConversationTaskDetail | null>(null);
  const [query, setQuery] = useState(""), [search, setSearch] = useState("");
  const [status, setStatus] = useState(""), [currentOnly, setCurrentOnly] = useState(false), [cursors, setCursors] = useState([""]);
  const [refresh, setRefresh] = useState(0), [loading, setLoading] = useState(true), [error, setError] = useState("");
  const [confirmCancel, setConfirmCancel] = useState(false), [acting, setActing] = useState(false);
  const cursor = cursors[cursors.length - 1];
  useEffect(() => {
    const controller = new AbortController(); setLoading(true); setError("");
    request<ConversationTaskPage>(taskListPath({ query: search, status, sourceConversationID: currentOnly ? conversationID : "", cursor }), "GET", undefined, controller.signal)
      .then(value => { if (!controller.signal.aborted) { setPage(value); setSelectedID(current => value.items.some(item => item.id === current) ? current : value.items[0]?.id || ""); } })
      .catch(failure => { if (!controller.signal.aborted) { setPage({ items: [], complete: true }); setSelectedID(""); setError(describeError(failure)); } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [search, status, currentOnly, conversationID, cursor, refresh]);
  useEffect(() => {
    setConfirmCancel(false);
    if (!selectedID) { setDetail(null); return; }
    const controller = new AbortController();
    request<ConversationTaskDetail>(taskPath(selectedID), "GET", undefined, controller.signal)
      .then(value => { if (!controller.signal.aborted) setDetail(value); })
      .catch(failure => { if (!controller.signal.aborted) { setDetail(null); setError(describeError(failure)); } });
    return () => controller.abort();
  }, [selectedID, refresh]);
  async function control(action: "cancel" | "resume") {
    if (!detail || acting) return;
    setActing(true); setError("");
    try {
      const value = await request<ConversationTaskDetail>(taskControlPath(detail.id, action), "POST");
      setDetail(value); setConfirmCancel(false); setRefresh(current => current + 1);
    } catch (failure) {
      setError(describeError(failure));
    } finally {
      setActing(false);
    }
  }
  useEffect(() => {
    if (!page.items.some(taskNeedsRefresh) && (!detail || !taskNeedsRefresh(detail))) return;
    const timer = window.setTimeout(() => setRefresh(value => value + 1), 1000);
    return () => window.clearTimeout(timer);
  }, [page, detail, refresh]);
  return <Dialog open onOpenChange={open => { if (!open) onClose(); }}><DialogContent className="task-dialog">
    <DialogHeader><DialogTitle>后台任务</DialogTitle><DialogDescription>任务由服务端持续执行。关闭网页后，重新打开这里仍能查看进度、等待事项、结果和成果。</DialogDescription></DialogHeader>
    <form className="todo-toolbar" onSubmit={event => { event.preventDefault(); setSearch(query); setCursors([""]); }}>
      <Input aria-label="搜索后台任务" placeholder="搜索任务目标" value={query} onChange={event => setQuery(event.target.value)} />
      <select aria-label="后台任务状态" value={status} onChange={event => { setStatus(event.target.value); setCursors([""]); }}><option value="">全部状态</option><option value="queued">等待开始</option><option value="running">进行中</option><option value="completed">已完成</option><option value="failed">失败</option><option value="cancelled">已停止</option></select>
      <Button type="submit" variant="outline" size="sm">搜索</Button><Button type="button" variant="ghost" size="sm" onClick={() => setRefresh(value => value + 1)}>刷新任务</Button>
    </form>
    {conversationID && <label className="memory-write-scope"><input type="checkbox" checked={currentOnly} onChange={event => { setCurrentOnly(event.target.checked); setCursors([""]); }} />只看来自当前会话的任务</label>}
    {error && <p role="alert" className="text-destructive">{error}</p>}
    {loading && !page.items.length ? <p role="status">正在读取后台任务…</p> : !page.items.length ? <p className="subtle">暂无匹配的后台任务。</p> : <div className="task-list" aria-label="后台任务列表">{page.items.map(task => <button type="button" key={task.id} className={selectedID === task.id ? "selected" : ""} onClick={() => setSelectedID(task.id)}><span><strong>{task.goal}</strong><small>{taskStatusLabel(task)} · {new Date(task.updated_at).toLocaleString()}</small></span>{task.status === "running" && !task.waiting && <span className="live-dot" />}</button>)}</div>}
    {(cursors.length > 1 || !page.complete) && <div className="todo-toolbar"><Button size="sm" variant="outline" disabled={cursors.length < 2} onClick={() => setCursors(value => value.slice(0, -1))}>上一页任务</Button><Button size="sm" variant="outline" disabled={page.complete || !page.next_cursor} onClick={() => setCursors(value => [...value, page.next_cursor!])}>下一页任务</Button></div>}
    {detail && <section className="task-detail" aria-label="后台任务详情">
      <div className="task-detail-heading"><div><span className="agent-label">{taskStatusLabel(detail)}</span><h3>{detail.goal}</h3></div><small>{detail.id}</small></div>
      <p className="task-input">{detail.input}</p>
      <dl className="run-metadata"><dt>进度</dt><dd>{detail.progress.steps} 个步骤 · {detail.progress.tool_calls} 次工具调用{detail.progress.attempt ? ` · 第 ${detail.progress.attempt} 次处理` : ""}</dd><dt>可用工具</dt><dd>{detail.allowed_tools.length ? detail.allowed_tools.join("、") : "不使用工具"}</dd><dt>预算</dt><dd>{detail.budget.max_steps} 步 · {detail.budget.max_tool_calls} 次工具 · {detail.budget.timeout_seconds} 秒</dd><dt>更新时间</dt><dd>{new Date(detail.updated_at).toLocaleString()}</dd></dl>
      {detail.access_error && <p role="alert" className="subtle">{errorMessage(detail.access_error)}</p>}
      {detail.error_code && <p role="alert" className="text-destructive">{errorMessage(detail.error_code)}</p>}
      {detail.waiting && <div className="task-waiting"><strong>{taskStatusLabel(detail)}</strong><p>{detail.waiting.question}</p>{detail.waiting.tool && <small className="subtle">等待工具：{detail.waiting.tool}</small>}</div>}
      {detail.result && <div className="task-result"><strong>任务结果</strong><p>{detail.result.preview}</p>{!detail.result.complete && <small className="subtle">这里只显示结果预览；返回来源会话可查看完整回复。</small>}</div>}
      {!!detail.artifacts.length && <div className="task-artifacts"><strong>任务成果</strong><div className="artifact-list">{detail.artifacts.map(artifact => <Button key={artifact.id} variant="outline" onClick={() => onArtifact(artifact.id, artifact.version)}><span>{artifact.title}</span><small>版本 {artifact.version}</small></Button>)}</div></div>}
      {detail.artifacts_omitted && <p className="subtle">部分成果当前无权查看，已隐藏。</p>}
      {confirmCancel && <div className="task-waiting"><strong>停止这个后台任务？</strong><p>已完成的操作会保留；正在写入外部系统的结果可能需要后续核查。</p><div className="todo-toolbar"><Button variant="destructive" disabled={acting} onClick={() => void control("cancel")}>确认停止任务</Button><Button variant="ghost" disabled={acting} onClick={() => setConfirmCancel(false)}>继续执行</Button></div></div>}
      <div className="todo-toolbar"><Button variant="outline" onClick={() => onSource(detail.source_conversation_id)}>返回来源会话</Button>{detail.execution_run_id && <Button variant="ghost" onClick={() => onRun(detail.source_conversation_id, detail.execution_run_id!)}>查看处理记录</Button>}</div>
      <div className="todo-toolbar">{taskCanCancel(detail) && !confirmCancel && <Button variant="destructive" disabled={acting} onClick={() => setConfirmCancel(true)}>停止任务</Button>}{taskCanResume(detail) && <Button disabled={acting} onClick={() => void control("resume")}>继续任务</Button>}{detail.control.resume_blocker === "interaction_response_required" && <small className="subtle">请在来源会话完成当前等待事项。</small>}{detail.control.resume_blocker === "interaction_closed" && <small className="subtle">原等待事项已关闭，无法直接继续；请重新创建任务。</small>}</div>
    </section>}
  </DialogContent></Dialog>;
}
