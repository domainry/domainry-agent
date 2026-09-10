import { useEffect } from "react";

export function BusinessOperationPreview({ argumentsText, onReady }: { argumentsText: string; onReady: (ready: boolean) => void }) {
  let args: { object_key?: string; action_key?: string; action_version?: string; record_id?: string; data?: Record<string, unknown> } = {};
  try { args = JSON.parse(argumentsText) || {}; } catch { /* Keep invalid snapshots unconfirmable. */ }
  const valid = typeof args.object_key === "string" && !!args.object_key && typeof args.action_key === "string" && !!args.action_key
    && typeof args.action_version === "string" && !!args.action_version && !!args.data && typeof args.data === "object" && !Array.isArray(args.data)
    && (args.record_id === undefined || typeof args.record_id === "string" && !!args.record_id);
  useEffect(() => { onReady(valid); }, [valid, argumentsText, onReady]);
  if (!valid) return <p role="alert">业务操作内容不完整，暂时无法确认。请停止本次处理后重新指定。</p>;
  const entries = Object.entries(args.data!);
  const business = entries.filter(([key]) => !["expected_updated_at", "expected_version"].includes(key));
  const versions = entries.filter(([key]) => ["expected_updated_at", "expected_version"].includes(key));
  const display = (value: unknown) => typeof value === "string" ? value : JSON.stringify(value, null, 2);
  return <div className="todo-operation">
    <strong>业务动作：{args.action_key}</strong>
    <p>目标对象：{args.object_key}</p>
    <p>{args.record_id ? `目标记录：${args.record_id}` : "对象级操作"}</p>
    <strong>本次提交的内容</strong>
    {business.length ? <dl>{business.map(([key, value]) => <div key={key}><dt>{key}</dt><dd><pre>{display(value)}</pre></dd></div>)}</dl> : <p>未提供额外业务参数。</p>}
    {!!versions.length && <p className="subtle">按已读取的记录版本执行。记录发生变化时，需要重新读取并确认。</p>}
    <details><summary>查看完整操作参数</summary><pre>{JSON.stringify(args, null, 2)}</pre></details>
  </div>;
}
