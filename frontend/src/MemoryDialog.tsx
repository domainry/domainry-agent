import { useEffect, useRef, useState } from "react";
import { Brain, Pencil, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { request, type Memory, type MemoryKind, type MemoryScopeKind } from "./api";
import { describeError } from "./errors";

export type MemorySource = { title: string; content: string; conversationID: string; messageID: string; runID: string; taskID?: string };

const kindLabels: Record<MemoryKind, string> = { user_preference: "用户偏好", project_fact: "项目事实", task_context: "任务上下文" };
const scopeLabels: Record<MemoryScopeKind, string> = { workspace: "当前工作区", conversation: "当前会话", task: "当前任务" };

export function MemoryDialog({ source, conversationID, onClose, onSaved }: { source: MemorySource | null; conversationID: string; onClose: () => void; onSaved: () => void }) {
  const [items, setItems] = useState<Memory[]>([]);
  const [editing, setEditing] = useState<Memory | null>(null);
  const [fromMessage, setFromMessage] = useState(!!source);
  const [savedNotice, setSavedNotice] = useState("");
  const [kind, setKind] = useState<MemoryKind>("user_preference");
  const [scope, setScope] = useState<MemoryScopeKind>(source ? "conversation" : "workspace");
  const [title, setTitle] = useState(source?.title || "");
  const [content, setContent] = useState(source?.content || "");
  const [topics, setTopics] = useState("");
  const [uncertainty, setUncertainty] = useState("");
  const [correctionReason, setCorrectionReason] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const newID = useRef(crypto.randomUUID());
  const titleBytes = new TextEncoder().encode(title.trim()).length;
  const contentBytes = new TextEncoder().encode(content.trim()).length;
  const topicValues = topics.split(/[，,]/).map((value) => value.trim()).filter(Boolean);
  const scopedConversationID = editing?.scope.conversation_id || conversationID;
  const scopedTaskID = editing?.scope.task_id || source?.taskID;
  const scopeAvailable = scope === "workspace" || !!scopedConversationID && (scope !== "task" || !!scopedTaskID);
  const valid = !!title.trim() && !!content.trim() && titleBytes <= 128 && contentBytes <= 512 && topicValues.length <= 16 && topicValues.every((value) => new TextEncoder().encode(value).length <= 64) && new TextEncoder().encode(uncertainty.trim()).length <= 512 && (!editing || !!correctionReason.trim()) && scopeAvailable && !(kind === "task_context" && scope === "workspace");

  useEffect(() => {
    const controller = new AbortController();
    request<Memory[]>("/agent/conversations/memories", "GET", undefined, controller.signal)
      .then((value) => setItems(value || []))
      .catch((failure) => { if (!controller.signal.aborted) setError(describeError(failure)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, []);

  function reset() {
    setFromMessage(false); setEditing(null); setKind("user_preference"); setScope("workspace"); setTitle(""); setContent(""); setTopics(""); setUncertainty(""); setCorrectionReason(""); newID.current = crypto.randomUUID();
  }
  function edit(memory: Memory) {
    setEditing(memory); setFromMessage(false); setKind(memory.kind); setScope(memory.scope.kind); setTitle(memory.title); setContent(memory.content); setTopics(memory.applies_to.join("，")); setUncertainty(memory.uncertainty || ""); setCorrectionReason(""); setError("");
  }
  function writeBody(memory: Memory, enabled: boolean) {
    return { kind: memory.kind, title: memory.title, content: memory.content, enabled, scope: memory.scope, applies_to: memory.applies_to, uncertainty: memory.uncertainty || "", expected_revision: memory.revision };
  }
  async function mutate(action: () => Promise<void>) {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError("");
    try { await action(); } catch (failure) { setError(describeError(failure)); } finally { lock.current = false; setBusy(false); }
  }

  return <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose(); }}><DialogContent className="memory-dialog">
    <DialogHeader><DialogTitle>个人记忆</DialogTitle><DialogDescription>分别保存偏好、项目事实和任务上下文。Agent 只召回当前范围内与请求相关的内容，并保留来源、更新时间和限制。</DialogDescription></DialogHeader>
    {error && <p role="alert" className="text-destructive text-sm">{error}</p>}
    {savedNotice && <p role="status" className="composer-notice">{savedNotice}</p>}
    <div className="memory-list">{loading ? <p className="subtle">正在读取记忆…</p> : items.length ? items.map((memory) => <div className="memory-card" key={memory.id}>
      <div><div className="memory-card-heading"><strong>{memory.title}</strong><span>{kindLabels[memory.kind]} · {scopeLabels[memory.scope.kind]}</span></div><p>{memory.content}</p><small className="subtle">更新于 {new Date(memory.updated_at).toLocaleString()}{memory.applies_to.length ? ` · 适用于 ${memory.applies_to.join("、")}` : ""}{memory.uncertainty ? ` · 限制：${memory.uncertainty}` : ""}{memory.source?.message_id ? ` · 来源消息 ${memory.source.message_id}` : ""}{memory.correction ? ` · 修正自 v${memory.correction.previous_revision}：${memory.correction.reason}` : ""}</small></div>
      <Switch aria-label={`启用记忆 ${memory.title}`} checked={memory.enabled} disabled={busy} onCheckedChange={(enabled) => void mutate(async () => { const updated = await request<Memory>(`/agent/conversations/memories/${memory.id}`, "PUT", writeBody(memory, enabled)); setItems((value) => value.map((item) => item.id === updated.id ? updated : item)); if (editing?.id === updated.id) setEditing(updated); })}/>
      <Button variant="ghost" size="icon-sm" aria-label={`编辑记忆 ${memory.title}`} disabled={busy} onClick={() => edit(memory)}><Pencil size={15}/></Button>
      <Button variant="ghost" size="icon-sm" aria-label={`删除记忆 ${memory.title}`} disabled={busy} onClick={() => void mutate(async () => { await request(`/agent/conversations/memories/${memory.id}?expected_revision=${memory.revision}`, "DELETE"); setItems((value) => value.filter((item) => item.id !== memory.id)); if (editing?.id === memory.id) reset(); })}><Trash2 size={15}/></Button>
    </div>) : <p className="subtle">还没有已保存记忆。会话历史仍单独保留。</p>}</div>
    <form className="dialog-form" onSubmit={(event) => { event.preventDefault(); if (!valid) return; void mutate(async () => {
      const saved = await request<Memory>(`/agent/conversations/memories/${editing?.id || newID.current}`, "PUT", { kind, title: title.trim(), content: content.trim(), enabled: editing?.enabled ?? true, scope: { kind: scope, ...(scope !== "workspace" ? { conversation_id: scopedConversationID } : {}), ...(scope === "task" && scopedTaskID ? { task_id: scopedTaskID } : {}) }, applies_to: topicValues, uncertainty: uncertainty.trim(), correction_reason: editing ? correctionReason.trim() : "", expected_revision: editing?.revision || 0, ...(!editing && source ? { source: { kind: "manual", conversation_id: source.conversationID, message_id: source.messageID, run_id: source.runID, ...(source.taskID ? { task_id: source.taskID } : {}) } } : {}) });
      setItems((value) => editing ? value.map((item) => item.id === saved.id ? saved : item) : [...value, saved]); reset(); setSavedNotice("记忆已保存；后续只在适用范围和相关请求中召回最新版本。"); onSaved();
    }); }}>
      <div className="memory-editor-heading"><span><Brain size={15}/>{editing ? `编辑「${editing.title}」` : fromMessage ? "从消息保存为记忆" : "新增记忆"}</span>{editing && <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={reset}>取消编辑</Button>}</div>
      {fromMessage && !editing && <p className="subtle">已填入消息原文。保存前请确认类型、范围和适用主题；系统不会自动推广到其他任务。</p>}
      <div className="memory-classification"><label>类型<select aria-label="记忆类型" value={kind} disabled={busy} onChange={(event) => { const value = event.target.value as MemoryKind; setKind(value); if (value === "task_context" && scope === "workspace") setScope("conversation"); }}>{Object.entries(kindLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label><label>范围<select aria-label="记忆范围" value={scope} disabled={busy} onChange={(event) => setScope(event.target.value as MemoryScopeKind)}><option value="workspace" disabled={kind === "task_context"}>当前工作区</option><option value="conversation" disabled={!scopedConversationID}>当前会话</option><option value="task" disabled={!scopedTaskID}>当前任务</option></select></label></div>
      <label htmlFor="memory-title">名称</label><Input id="memory-title" value={title} onChange={(event) => setTitle(event.target.value)} disabled={busy} required placeholder="例如：周报表达偏好"/>
      <label htmlFor="memory-content">内容</label><Textarea id="memory-content" rows={4} value={content} onChange={(event) => setContent(event.target.value)} disabled={busy} required placeholder="例如：周报先列结论，再按项目说明进展。"/>
      <label htmlFor="memory-topics">适用主题</label><Input id="memory-topics" value={topics} onChange={(event) => setTopics(event.target.value)} disabled={busy} placeholder="可选，用逗号分隔，例如：周报，项目复盘"/>
      <label htmlFor="memory-uncertainty">适用限制或未确认信息</label><Input id="memory-uncertainty" value={uncertainty} onChange={(event) => setUncertainty(event.target.value)} disabled={busy} placeholder="可选，例如：仅适用于 2026 年第三季度"/>
      {editing && <><label htmlFor="memory-correction">本次修正原因</label><Input id="memory-correction" value={correctionReason} onChange={(event) => setCorrectionReason(event.target.value)} disabled={busy} required placeholder="说明旧内容哪里不再适用"/></>}
      <p className={titleBytes > 128 || contentBytes > 512 ? "text-destructive text-xs" : "subtle"} aria-live="polite">名称 {titleBytes}/128 字节 · 内容 {contentBytes}/512 字节 · 主题 {topicValues.length}/16{contentBytes > 512 ? "，请提炼后保存" : ""}</p>
      <Button type="submit" disabled={busy || loading || !valid || (!editing && items.length >= 32)}>{editing ? "保存修改" : "保存记忆"}</Button>
    </form>
  </DialogContent></Dialog>;
}
