import { useRef, useState } from "react";
import { request, runPath, type Run } from "./api.ts";
import { ApiError, describeError } from "./errors.ts";
import { sessionScope } from "./session.ts";
import type { Interaction, InteractionResponse } from "./interaction-state.ts";
import { Button } from "./components/ui/button";
import { MemoryOperationPreview } from "./MemoryOperationPreview.tsx";
import { TodoOperationPreview } from "./TodoOperationPreview.tsx";
import { ArtifactOperationPreview } from "./ArtifactOperationPreview.tsx";
import { BusinessOperationPreview } from "./BusinessOperationPreview.tsx";
import { WorkflowOperationPreview } from "./WorkflowOperationPreview.tsx";
import { OperationScopePreview } from "./OperationScopePreview.tsx";
import { AccountWriteOperationPreview } from "./AccountWriteOperationPreview.tsx";
import { isAccountWrite } from "./account-write-state.ts";

export function InteractionCard({ interaction, onRun, onRefresh }: { interaction: Interaction; onRun: (run: Run) => void; onRefresh: () => void }) {
  const storageKey = `agent-interaction:${sessionScope()}:${interaction.id}:${interaction.revision}`;
  const [pending, setPending] = useState<InteractionResponse | null>(() => {
    try {
      const item = JSON.parse(localStorage.getItem(storageKey) || "null") as InteractionResponse | null;
      return item?.interaction_id === interaction.id && item.expected_revision === interaction.revision ? item : null;
    } catch { return null; }
  });
  const [answer, setAnswer] = useState(pending?.answer || "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [storageUnavailable, setStorageUnavailable] = useState(false);
  const [scopeReady, setScopeReady] = useState(false);
  const grouped = interaction.kind === "confirmation" && (interaction.operations?.length || 0) >= 2;
  const [previewReady, setPreviewReady] = useState(!isAccountWrite(interaction.tool) && !["invoke_action", "workflow_start"].includes(interaction.tool) && !interaction.tool.startsWith("artifact_") && (!interaction.tool.startsWith("todo_") || interaction.tool === "todo_create"));
  const locked = useRef(false);
  if (interaction.status !== "pending") return null;
  const path = runPath(interaction.conversation_id, interaction.run_id);
  async function submit(decision: InteractionResponse["decision"], scope?: InteractionResponse["scope"]) {
    if (locked.current) return;
    if (decision === "answer" && (!answer.trim() || new TextEncoder().encode(answer).length > 16384)) {
      setError("请填写补充信息，最多 16 KB。"); return;
    }
    locked.current = true; setBusy(true); setError("");
    const response: InteractionResponse = pending || { interaction_id: interaction.id, client_id: crypto.randomUUID(), expected_revision: interaction.revision, decision, ...(scope ? { scope } : {}), ...(decision === "answer" ? {answer} : {}) };
    setPending(response);
    try { localStorage.setItem(storageKey, JSON.stringify(response)); } catch { setStorageUnavailable(true); }
    try {
      const run = await request<Run>(`${path}/respond`, "POST", response);
      try { localStorage.removeItem(storageKey); } catch { /* Current page can still complete. */ }
      onRun(run); onRefresh();
    } catch (failure) {
      setError(describeError(failure));
      if (failure instanceof ApiError && [400, 409].includes(failure.status || 0)) {
        if (failure.status === 400) {
          setPending(null);
          try { localStorage.removeItem(storageKey); } catch { /* Storage is optional. */ }
        }
        onRefresh();
      }
    } finally { locked.current = false; setBusy(false); }
  }
  async function transition(operation: "cancel" | "resume") {
    if (locked.current) return;
    locked.current = true; setBusy(true); setError("");
    try { onRun(await request<Run>(`${path}/${operation}`, "POST")); onRefresh(); }
    catch (failure) { setError(describeError(failure)); }
    finally { locked.current = false; setBusy(false); }
  }
  let argumentsText = interaction.arguments;
  try { argumentsText = JSON.stringify(JSON.parse(argumentsText), null, 2); } catch { /* Preserve the displayed snapshot. */ }
  return <section className="interaction-card" aria-label={interaction.kind === "input" ? "补充信息" : interaction.kind === "confirmation" ? "操作确认" : "结果核查"}>
    <strong>{interaction.kind === "input" ? "需要你补充信息" : interaction.kind === "confirmation" ? grouped ? "请确认操作范围" : "请确认这项操作" : "操作结果待核查"}</strong>
    <p>{interaction.question}</p>
    {interaction.kind === "confirmation" && (grouped
      ? <OperationScopePreview operations={interaction.operations!} onReady={setScopeReady} onFirstReady={setPreviewReady} />
      : isAccountWrite(interaction.tool)
      ? <AccountWriteOperationPreview tool={interaction.tool} argumentsText={interaction.arguments} onReady={setPreviewReady} />
      : interaction.tool.startsWith("todo_")
      ? <TodoOperationPreview tool={interaction.tool} argumentsText={interaction.arguments} onReady={setPreviewReady} />
      : interaction.tool.startsWith("artifact_")
      ? <ArtifactOperationPreview tool={interaction.tool} argumentsText={interaction.arguments} onReady={setPreviewReady} />
      : ["memory_save", "memory_forget"].includes(interaction.tool)
      ? <MemoryOperationPreview tool={interaction.tool} argumentsText={interaction.arguments} />
      : interaction.tool === "invoke_action"
      ? <BusinessOperationPreview argumentsText={interaction.arguments} onReady={setPreviewReady} />
      : interaction.tool === "workflow_start"
      ? <WorkflowOperationPreview argumentsText={interaction.arguments} onReady={setPreviewReady} />
      : <><small className="subtle">{interaction.tool} · 版本 {interaction.tool_version}</small><pre>{argumentsText}</pre></>)}
    {interaction.kind === "input" && <>
      {!!interaction.choices?.length && <div className="interaction-actions">{interaction.choices.map(choice => <Button type="button" variant={answer === choice ? "default" : "outline"} size="sm" key={choice} disabled={busy || !!pending} onClick={() => setAnswer(choice)}>{choice}</Button>)}</div>}
      <label>补充信息<textarea aria-label="补充信息" value={answer} disabled={busy || !!pending} onChange={event => setAnswer(event.target.value)} rows={3} placeholder="选择建议或输入你的回答" /></label>
    </>}
    {interaction.kind !== "reconciliation" && <small className="subtle">有效期至 {new Date(interaction.expires_at).toLocaleString(undefined, {timeZoneName: "short"})}；提交后从当前步骤继续。</small>}
    {pending && !busy && <small className="subtle">上次提交的结果尚未确认。重试将提交相同内容。</small>}
    {storageUnavailable && <small className="subtle">浏览器存储不可用，请在当前页面重试；刷新后先核对服务端状态。</small>}
    {error && <p role="alert" className="text-destructive">{error}</p>}
    <div className="interaction-actions">
      {interaction.kind === "input" && <Button type="button" disabled={busy || !answer.trim()} onClick={() => void submit("answer")}>{pending ? "重试提交" : "提交并继续"}</Button>}
      {interaction.kind === "confirmation" && (pending ? <Button type="button" disabled={busy} onClick={() => void submit(pending.decision)}>{pending.scope ? `重试授权这 ${interaction.operations?.length} 项` : `重试${pending.decision === "approve" ? "确认" : "拒绝"}`}</Button> : <>
        <Button type="button" disabled={busy || !previewReady} onClick={() => void submit("approve")}>{grouped ? "仅确认第一项" : "确认执行"}</Button>
        {grouped && <Button type="button" variant="outline" disabled={busy || !scopeReady} onClick={() => void submit("approve", "listed_operations")}>授权并执行这 {interaction.operations!.length} 项</Button>}
        <Button type="button" variant="outline" disabled={busy} onClick={() => void submit("reject")}>拒绝执行</Button>
      </>)}
      {interaction.kind === "reconciliation" && <Button type="button" disabled={busy} onClick={() => void transition("resume")}>查询实际结果并继续</Button>}
      <Button type="button" variant="ghost" disabled={busy} onClick={() => void transition("cancel")}>停止本次处理</Button>
    </div>
  </section>;
}
