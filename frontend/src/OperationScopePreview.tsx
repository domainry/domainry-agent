import { useCallback, useEffect, useState } from "react";
import type { InteractionOperation } from "./interaction-state.ts";
import { BusinessOperationPreview } from "./BusinessOperationPreview.tsx";
import { WorkflowOperationPreview } from "./WorkflowOperationPreview.tsx";
import { TodoOperationPreview } from "./TodoOperationPreview.tsx";
import { ArtifactOperationPreview } from "./ArtifactOperationPreview.tsx";
import { MemoryOperationPreview } from "./MemoryOperationPreview.tsx";
import { AccountWriteOperationPreview } from "./AccountWriteOperationPreview.tsx";
import { isAccountWrite } from "./account-write-state.ts";

function GenericOperation({ item, onReady }: { item: InteractionOperation; onReady: (ready: boolean) => void }) {
  let text = item.arguments, valid = false;
  try { const value: unknown = JSON.parse(text); valid = !!value && typeof value === "object" && !Array.isArray(value); text = JSON.stringify(value, null, 2); } catch { /* Never approve incomplete arguments. */ }
  useEffect(() => { onReady(valid); }, [valid, onReady]);
  return <><strong>{item.tool}</strong><pre>{text}</pre>{!valid && <p role="alert">操作参数不完整，请停止后重新指定。</p>}</>;
}

function ScopeEntry({ item, onReady }: { item: InteractionOperation; onReady: (id: string, ready: boolean) => void }) {
  const update = useCallback((ready: boolean) => onReady(item.call_id, ready), [item.call_id, onReady]);
  if (isAccountWrite(item.tool)) return <AccountWriteOperationPreview tool={item.tool} argumentsText={item.arguments} onReady={update} />;
  if (item.tool === "invoke_action") return <BusinessOperationPreview argumentsText={item.arguments} onReady={update} />;
  if (item.tool === "workflow_start") return <WorkflowOperationPreview argumentsText={item.arguments} onReady={update} />;
  if (item.tool.startsWith("todo_")) return <TodoOperationPreview tool={item.tool} argumentsText={item.arguments} onReady={update} />;
  if (item.tool.startsWith("artifact_")) return <ArtifactOperationPreview tool={item.tool} argumentsText={item.arguments} onReady={update} />;
  if (item.tool.startsWith("memory_")) return <MemoryOperationPreview tool={item.tool} argumentsText={item.arguments} onReady={update} />;
  return <GenericOperation item={item} onReady={update} />;
}

export function OperationScopePreview({ operations, onReady, onFirstReady }: { operations: InteractionOperation[]; onReady: (ready: boolean) => void; onFirstReady: (ready: boolean) => void }) {
  const [ready, setReady] = useState<Record<string, boolean>>({});
  const update = useCallback((id: string, value: boolean) => setReady(old => old[id] === value ? old : { ...old, [id]: value }), []);
  useEffect(() => {
    onFirstReady(!!ready[operations[0]?.call_id]);
    onReady(operations.length >= 2 && operations.length <= 20 && new Set(operations.map(item => item.call_id)).size === operations.length && operations.every(item => ready[item.call_id]));
  }, [operations, ready, onReady, onFirstReady]);
  return <div className="space-y-3" aria-label="本次操作授权范围">
    <p>以下 {operations.length} 项操作各执行一次。你可以先确认第一项，或一次授权全部列出的操作；新增目标或修改参数后会重新确认。</p>
    {operations.map((item, index) => <article key={item.call_id} className="rounded-lg border p-3">
      <p className="subtle">第 {index + 1} 项</p><ScopeEntry item={item} onReady={update} />
    </article>)}
  </div>;
}
