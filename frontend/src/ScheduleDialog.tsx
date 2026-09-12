import { useEffect, useMemo, useRef, useState } from "react";
import { CalendarClock, RefreshCw, Sparkles, Trash2 } from "lucide-react";
import { request } from "./api.ts";
import { ApiError } from "./errors.ts";
import { sessionFetch } from "./session.ts";
import { scheduleStatusLabel, scheduleTriggerLabel, scheduleUpdateBody, type ScheduleOutput, type SchedulePlan } from "./schedule-state.ts";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";

const weekdays = [["monday", "周一"], ["tuesday", "周二"], ["wednesday", "周三"], ["thursday", "周四"], ["friday", "周五"], ["saturday", "周六"], ["sunday", "周日"]];

function scheduleError(error: unknown) {
  if (error instanceof ApiError) {
    if (error.status === 403) return "当前账号没有管理计划的权限，或计划工具已关闭。";
    if (error.status === 404) return error.code === "request_failed" ? "当前产品尚未连接计划服务。" : "计划不存在，可能已被删除。";
    if (error.status === 409) return "计划已有新版本，请刷新后再修改。";
    if (error.status === 400) return "计划内容不符合要求，请检查时间、时区和必填内容。";
  }
  return "暂时无法确认计划状态，请刷新后核对。";
}

async function writeSchedule(path: string, method: string, body: unknown, key: string): Promise<ScheduleOutput> {
  let response: Response;
  try {
    response = await sessionFetch(path, { method, credentials: "same-origin", headers: { "Content-Type": "application/json", "Idempotency-Key": key }, body: JSON.stringify(body) });
  } catch (error) {
    if (error instanceof ApiError) throw error;
    throw new ApiError("network_error");
  }
  const value = await response.json().catch(() => ({}));
  if (!response.ok) throw new ApiError(typeof value?.code === "string" ? value.code : "request_failed", response.status);
  return value as ScheduleOutput;
}

export function ScheduleDialog({ onClose, onAsk }: { onClose: () => void; onAsk?: (prompt: string) => void | Promise<void> }) {
  const [plans, setPlans] = useState<SchedulePlan[]>([]), [selectedID, setSelectedID] = useState("");
  const [status, setStatus] = useState(""), [cursor, setCursor] = useState(""), [nextCursor, setNextCursor] = useState("");
  const [loading, setLoading] = useState(true), [busy, setBusy] = useState(false), [error, setError] = useState(""), [notice, setNotice] = useState("");
  const [deleteArmed, setDeleteArmed] = useState(false);
  const [name, setName] = useState(""), [timezone, setTimezone] = useState(""), [time, setTime] = useState(""), [day, setDay] = useState("");
  const [goal, setGoal] = useState(""), [input, setInput] = useState(""), [allowedTools, setAllowedTools] = useState("");
	const [completionCondition, setCompletionCondition] = useState("");
  const [title, setTitle] = useState(""), [message, setMessage] = useState("");
  const mutation = useRef(false);
  const selected = useMemo(() => plans.find(plan => plan.id === selectedID) || null, [plans, selectedID]);

  function edit(plan: SchedulePlan | null) {
    if (!plan) return;
    setName(plan.name); setTimezone(plan.timezone);
    if (plan.trigger.type === "once") { setTime(plan.trigger.at); setDay(""); }
    else { setTime(plan.trigger.schedule.time_of_day); setDay(plan.trigger.schedule.day_of_week || String(plan.trigger.schedule.day_of_month || 1)); }
    setGoal(plan.details.goal || ""); setInput(plan.details.input || ""); setAllowedTools((plan.details.allowed_tools || []).join(", "));
		setCompletionCondition(plan.details.completion_condition || "");
    setTitle(plan.details.title || ""); setMessage(plan.details.message || "");
  }

  async function reload(signal?: AbortSignal, next = cursor) {
    setLoading(true); setError("");
    try {
      const query = new URLSearchParams(); if (status) query.set("status", status); if (next) query.set("cursor", next); query.set("limit", "50");
      const result = await request<ScheduleOutput>(`/app/product/plans?${query}`, "GET", undefined, signal);
      if (signal?.aborted) return;
      const items = result.items || []; setPlans(items); setNextCursor(result.next_cursor || "");
      setSelectedID(current => items.some(plan => plan.id === current) ? current : items[0]?.id || "");
    } catch (failure) { if (!signal?.aborted) { setPlans([]); setSelectedID(""); setError(scheduleError(failure)); } }
    finally { if (!signal?.aborted) setLoading(false); }
  }
  useEffect(() => { const controller = new AbortController(); void reload(controller.signal); return () => controller.abort(); }, [status, cursor]);
  useEffect(() => { edit(selected); setDeleteArmed(false); }, [selectedID, selected?.revision]);

  async function act(operation: "update" | "pause" | "resume" | "delete") {
    if (!selected || mutation.current) return;
    mutation.current = true; setBusy(true); setError(""); setNotice("");
    const key = crypto.randomUUID();
    try {
      let result: ScheduleOutput;
      if (operation === "update") {
        const details = selected.kind !== "reminder"
          ? { goal: goal.trim(), input: input.trim(), allowed_tools: allowedTools.split(",").map(value => value.trim()).filter(Boolean), ...(selected.kind === "follow_up" ? { completion_condition: completionCondition.trim() } : {}) }
          : { title: title.trim(), message: message.trim() };
        result = await writeSchedule(`/app/product/plans/${encodeURIComponent(selected.id)}`, "PUT", scheduleUpdateBody(selected, { name, timezone, time, day, details }), key);
      } else {
        const suffix = operation === "delete" ? "" : `/${operation}`;
        result = await writeSchedule(`/app/product/plans/${encodeURIComponent(selected.id)}${suffix}`, operation === "delete" ? "DELETE" : "POST", { expected_revision: selected.revision }, key);
      }
      if (result.plan) {
        setPlans(current => current.map(plan => plan.id === result.plan!.id ? result.plan! : plan));
        setNotice(operation === "pause" ? "计划已暂停。" : operation === "resume" ? "计划已恢复。" : "计划已保存。除非再次修改，否则会按新版本执行。");
      } else if (result.deleted) {
        setPlans(current => current.filter(plan => plan.id !== selected.id)); setSelectedID(""); setNotice("计划已删除，重启后也不会恢复执行。");
      }
    } catch (failure) { setError(scheduleError(failure)); }
    finally { mutation.current = false; setBusy(false); setDeleteArmed(false); }
  }

  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose(); }}><DialogContent className="task-dialog schedule-dialog">
    <DialogHeader><DialogTitle>计划与提醒</DialogTitle><DialogDescription>查看并管理当前账号的定时计划和后台跟进。跟进只在变化、完成、失败或需要你处理时通知。</DialogDescription></DialogHeader>
    <div className="todo-toolbar"><select aria-label="计划状态" value={status} onChange={event => { setStatus(event.target.value); setCursor(""); }}><option value="">全部状态</option><option value="enabled">运行中</option><option value="paused">已暂停</option><option value="disabled">已停用</option></select><Button variant="outline" size="sm" disabled={busy || loading} onClick={() => void reload()}><RefreshCw size={15}/>刷新计划</Button>{onAsk && <Button variant="ghost" size="sm" disabled={busy} onClick={() => onAsk("请帮我创建或管理一个定时计划。先查看我当前的计划，再询问缺少的时间或内容。可参考：每周一整理待办；周五提醒我提交周报。") }><Sparkles size={15}/>交给 Agent</Button>}</div>
    {error && <p role="alert" className="text-destructive">{error}</p>}{notice && <p role="status" className="subtle">{notice}</p>}
    {loading && !plans.length ? <p role="status">正在读取计划…</p> : !plans.length ? <div className="product-empty"><CalendarClock size={30}/><h3>还没有计划</h3><p>在对话中说明时间、频率和要做的事，Agent 会创建受限计划。</p></div> : <div className="task-list" aria-label="计划列表">{plans.map(plan => <button type="button" key={plan.id} className={selectedID === plan.id ? "selected" : ""} onClick={() => setSelectedID(plan.id)}><span><strong>{plan.name}</strong><small>{scheduleTriggerLabel(plan)} · {plan.timezone}</small></span><span className="agent-label">{scheduleStatusLabel(plan.status)}</span></button>)}</div>}
    {(cursor || nextCursor) && <div className="todo-toolbar"><Button variant="outline" size="sm" disabled={!cursor || loading} onClick={() => setCursor("")}>第一页</Button><Button variant="outline" size="sm" disabled={!nextCursor || loading} onClick={() => setCursor(nextCursor)}>下一页</Button></div>}
    {selected && <section className="task-detail" aria-label="计划详情"><div className="task-detail-heading"><div><span className="agent-label">{selected.kind === "reminder" ? "提醒" : selected.kind === "follow_up" ? "后台跟进" : "后台任务"} · 版本 {selected.revision}</span><h3>{selected.name}</h3></div><small>{selected.id}</small></div>
      <div className="schedule-form"><label>名称<Input value={name} maxLength={500} disabled={busy} onChange={event => setName(event.target.value)}/></label><label>时区<Input value={timezone} maxLength={128} disabled={busy} onChange={event => setTimezone(event.target.value)}/></label>
        {selected.trigger.type === "once" ? <label className="schedule-wide">执行时间（RFC 3339）<Input value={time} disabled={busy} onChange={event => setTime(event.target.value)}/></label> : <><label>执行时间<Input type="time" step="60" value={time.slice(0,5)} disabled={busy} onChange={event => setTime(event.target.value)}/></label>{selected.trigger.schedule.type === "weekly_at" && <label>星期<select value={day} disabled={busy} onChange={event => setDay(event.target.value)}>{weekdays.map(([value,label]) => <option key={value} value={value}>{label}</option>)}</select></label>}{selected.trigger.schedule.type === "monthly_at" && <label>每月日期<Input type="number" min={1} max={31} value={day} disabled={busy} onChange={event => setDay(event.target.value)}/></label>}</>}
        {selected.kind !== "reminder" ? <><label className="schedule-wide">任务目标<textarea rows={3} value={goal} disabled={busy} onChange={event => setGoal(event.target.value)}/></label><label className="schedule-wide">补充输入<textarea rows={3} value={input} disabled={busy} onChange={event => setInput(event.target.value)}/></label><label className="schedule-wide">允许工具（逗号分隔）<Input value={allowedTools} disabled={busy} onChange={event => setAllowedTools(event.target.value)}/></label>{selected.kind === "follow_up" && <label className="schedule-wide">完成条件<textarea rows={3} value={completionCondition} disabled={busy} onChange={event => setCompletionCondition(event.target.value)}/><small>首次运行建立基线；之后只有状态变化、完成、失败或需要操作时才通知。</small></label>}</> : <><label>提醒标题<Input value={title} disabled={busy} onChange={event => setTitle(event.target.value)}/></label><label className="schedule-wide">提醒内容<textarea rows={4} value={message} disabled={busy} onChange={event => setMessage(event.target.value)}/></label></>}
      </div><dl className="run-metadata"><dt>最近更新</dt><dd>{new Date(selected.updated_at).toLocaleString()}</dd>{selected.conversation_id && <><dt>来源会话</dt><dd>{selected.conversation_id}</dd></>}</dl>
      <div className="todo-toolbar"><Button disabled={busy || !name.trim() || !timezone.trim() || (selected.kind === "follow_up" && !completionCondition.trim())} onClick={() => void act("update")}>保存修改</Button>{selected.status === "enabled" && <Button variant="outline" disabled={busy} onClick={() => void act("pause")}>{selected.kind === "follow_up" ? "停止跟进" : "暂停"}</Button>}{selected.status !== "enabled" && <Button variant="outline" disabled={busy} onClick={() => void act("resume")}>恢复</Button>}{deleteArmed ? <><span role="status" className="text-destructive">确定删除这个计划？</span><Button variant="destructive" disabled={busy} onClick={() => void act("delete")}><Trash2 size={15}/>确认删除</Button><Button variant="outline" disabled={busy} onClick={() => setDeleteArmed(false)}>取消</Button></> : <Button variant="destructive" disabled={busy} onClick={() => setDeleteArmed(true)}><Trash2 size={15}/>删除</Button>}{onAsk && <Button variant="ghost" disabled={busy} onClick={() => onAsk(`请查看并管理计划 ${selected.id}（当前版本 ${selected.revision}，名称：${selected.name}）。先读取最新版本再修改。`)}><Sparkles size={15}/>交给 Agent 修改</Button>}</div>
    </section>}
  </DialogContent></Dialog>;
}
