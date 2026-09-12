import { useEffect, useState } from "react";
import { request, type Memory } from "./api.ts";
import { describeError } from "./errors.ts";

export function MemoryOperationPreview({ tool, argumentsText, onReady }: { tool: string; argumentsText: string; onReady?: (ready: boolean) => void }) {
  const [memory, setMemory] = useState<Memory | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(tool === "memory_forget");
  let args: Partial<Memory> & { expected_revision?: number } = {};
  try { args = JSON.parse(argumentsText); } catch { /* The executor validates the frozen arguments. */ }
  const id = args.id;
  const ready = tool === "memory_save" ? typeof args.title === "string" && !!args.title && typeof args.content === "string" && !!args.content
    : !loading && !error && !!memory && memory.revision === args.expected_revision;
  useEffect(() => { onReady?.(ready); }, [ready, onReady]);
  useEffect(() => {
    if (tool !== "memory_forget" || !id) return;
    const controller = new AbortController();
    setLoading(true); setError(""); setMemory(null);
    request<Memory[]>("/agent/conversations/memories", "GET", undefined, controller.signal)
      .then(items => { if (!controller.signal.aborted) setMemory(items.find(item => item.id === id) || null); })
      .catch(failure => { if (!controller.signal.aborted) setError(describeError(failure)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [tool, id]);
  if (tool === "memory_save") return <div className="memory-operation">
    <strong>{args.id ? "更新个人记忆" : "新增个人记忆"}：{args.title}</strong>
    <p>{args.content}</p><small className="subtle">保存后{args.enabled ? "启用" : "停用"}此记忆。</small>
  </div>;
  return <div className="memory-operation">
    <strong>删除个人记忆{memory ? `：${memory.title}` : ""}</strong>
    {loading ? <p>正在读取待删除的内容…</p> : error ? <p role="alert">{error}</p> : memory ? <>
      <p>{memory.content}</p>
      {memory.revision !== args.expected_revision && <p role="alert">该记忆已发生修改，当前确认无法删除新版本；请停止本次处理并重新指定。</p>}
    </> : <p>该记忆已不存在，本次操作无法继续删除。</p>}
    <small className="subtle">删除后不再作为个人记忆使用。原始对话记录仍保留。</small>
  </div>;
}
