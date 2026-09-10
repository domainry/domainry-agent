import { useEffect, useState } from "react";
import { request } from "./api.ts";
import { describeError } from "./errors.ts";
import { todoDeadline, type Todo } from "./todo-state.ts";

export function TodoOperationPreview({ tool, argumentsText, onReady }: { tool: string; argumentsText: string; onReady: (ready: boolean) => void }) {
  const [item, setItem] = useState<Todo | null>(null);
  const [error, setError] = useState("");
  let args: { items?: Todo[]; id?: string; expected_revision?: number; patch?: Partial<Todo> } = {};
  try { args = JSON.parse(argumentsText); } catch { /* Already validated by the executor. */ }
  const id = args.id;
  useEffect(() => {
    if (tool === "todo_create") { onReady(true); return; }
    onReady(false); setItem(null); setError("");
    if (!id) return;
    const controller = new AbortController();
    request<Todo>(`/agent/todos/${id}`, "GET", undefined, controller.signal)
      .then(value => { if (!controller.signal.aborted) { setItem(value); onReady(value.revision === args.expected_revision); } })
      .catch(failure => { if (!controller.signal.aborted) setError(describeError(failure)); });
    return () => controller.abort();
  }, [tool, id, args.expected_revision, onReady]);
  if (tool === "todo_create") return <div className="todo-operation"><strong>创建 {args.items?.length || 0} 项待办</strong><ol>{args.items?.map((todo, index) => <li key={index}><strong>{todo.title}</strong>{todo.description && <p>{todo.description}</p>}<small>{todoDeadline(todo)}</small></li>)}</ol></div>;
  const patch = args.patch || {};
  return <div className="todo-operation"><strong>{tool === "todo_delete" ? "删除" : "修改"}待办{item ? `：${item.title}` : ""}</strong>
    {error ? <p role="alert">{error}</p> : !item ? <p>正在读取事项…</p> : <>
      <p>原批次第 {item.position} 项 · {item.status === "completed" ? "已完成" : "未完成"}</p><small>当前截止：{todoDeadline(item)}</small>
      {item.revision !== args.expected_revision && <p role="alert">事项已在其他地方修改，请停止本次处理并重新指定。</p>}
      {tool === "todo_update" && <dl>
        {patch.title !== undefined && <><dt>新标题</dt><dd>{patch.title}</dd></>}
        {patch.description !== undefined && <><dt>新说明</dt><dd>{patch.description || "清空说明"}</dd></>}
        {patch.status !== undefined && <><dt>状态</dt><dd>{patch.status === "completed" ? "标记完成" : "重新打开"}</dd></>}
        {(patch.due_date !== undefined || patch.due_at !== undefined) && <><dt>新截止</dt><dd>{todoDeadline({ ...item, due_date: patch.due_date ?? "", due_at: patch.due_at ?? "", timezone: patch.timezone || item.timezone })}</dd></>}
        {patch.timezone !== undefined && <><dt>时区</dt><dd>{patch.timezone}</dd></>}
      </dl>}
    </>}
  </div>;
}
