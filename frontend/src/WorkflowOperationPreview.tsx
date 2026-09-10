import { useEffect } from "react";

export function WorkflowOperationPreview({ argumentsText, onReady }: { argumentsText: string; onReady: (ready: boolean) => void }) {
  let args: { workflow_key?: string; workflow_version?: string; data?: Record<string, unknown> } = {};
  try { args = JSON.parse(argumentsText) || {}; } catch { /* Invalid snapshots cannot be confirmed. */ }
  const valid = typeof args.workflow_key === "string" && !!args.workflow_key && typeof args.workflow_version === "string" && !!args.workflow_version
    && !!args.data && typeof args.data === "object" && !Array.isArray(args.data);
  useEffect(() => { onReady(valid); }, [valid, argumentsText, onReady]);
  if (!valid) return <p role="alert">流程及输入内容不完整，暂时无法确认。请停止本次处理后重新指定。</p>;
  return <div className="todo-operation">
    <strong>启动流程：{args.workflow_key}</strong>
    <p>确认后会提交这次流程。提交成功后可继续查询进度；需要审批或等待时，流程会保持处理中。</p>
    <strong>本次提交的内容</strong>
    {Object.keys(args.data!).length ? <dl>{Object.entries(args.data!).map(([key, value]) => <div key={key}><dt>{key}</dt><dd><pre>{typeof value === "string" ? value : JSON.stringify(value, null, 2)}</pre></dd></div>)}</dl> : <p>未提供额外业务参数。</p>}
    <details><summary>查看完整流程参数</summary><pre>{JSON.stringify(args, null, 2)}</pre></details>
  </div>;
}
