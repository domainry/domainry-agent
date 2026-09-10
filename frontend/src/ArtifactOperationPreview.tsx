import { useEffect, useState } from "react";
import { request } from "./api.ts";
import { describeError } from "./errors.ts";
import { artifactPath, type ArtifactContent, type ArtifactVersion } from "./artifact-state.ts";
import { ArtifactPreview } from "./ArtifactPreview.tsx";

type Arguments = { id?: string; title?: string; content?: ArtifactContent; expected_version?: number; version?: number; format?: string; patch?: { title?: string; content?: ArtifactContent; text?: { find: string; replace: string }[]; cells?: { row: number; column: string; value: string | null }[] } };
export function ArtifactOperationPreview({ tool, argumentsText, onReady }: { tool: string; argumentsText: string; onReady: (ready: boolean) => void }) {
  const [value, setValue] = useState<ArtifactVersion | null>(null);
  const [error, setError] = useState("");
  let args: Arguments = {};
  try { args = JSON.parse(argumentsText); } catch { /* The executor validates the frozen call. */ }
  const version = args.expected_version || args.version;
  useEffect(() => {
    setValue(null); setError(""); onReady(false);
    if (tool === "artifact_create") { onReady(!!args.title && !!args.content); return; }
    if (!args.id || !version) return;
    const controller = new AbortController();
    request<ArtifactVersion>(`${artifactPath(args.id)}?version=${version}`, "GET", undefined, controller.signal)
      .then(item => { if (!controller.signal.aborted) { setValue(item); onReady(item.artifact.version === version); } })
      .catch(failure => { if (!controller.signal.aborted) setError(describeError(failure)); });
    return () => controller.abort();
  }, [tool, argumentsText, onReady]);
  if (tool === "artifact_create") return <div className="memory-operation"><strong>创建成果：{args.title}</strong>{args.content && <ArtifactPreview content={args.content} />}</div>;
  const patch = args.patch || {};
  return <div className="memory-operation"><strong>{tool === "artifact_export" ? "导出" : "修改"}成果{value ? `：${value.artifact.title}` : ""}</strong>
    {error ? <p role="alert">{error}</p> : !value ? <p>正在读取目标版本…</p> : <>
      <p>目标版本 {version}{tool === "artifact_export" ? ` · ${args.format === "csv" ? "CSV 表格" : "Markdown 文档"}` : " · 保存时检测版本冲突"}</p>
      {tool === "artifact_edit" && <>
        {patch.title !== undefined && <p>新标题：{patch.title}</p>}
        {patch.content && <><p>替换为以下完整内容：</p><ArtifactPreview content={patch.content} /></>}
        {patch.text?.map((edit, i) => <div key={i}><small>替换原文</small><pre>{edit.find}</pre><small>修改为</small><pre>{edit.replace || "（删除这段内容）"}</pre></div>)}
        {patch.cells?.map((edit, i) => {
          const index = value.content.table?.columns.findIndex(column => column.key === edit.column) ?? -1;
          const label = value.content.table?.columns[index]?.label || edit.column;
          const original = value.content.table?.rows[edit.row]?.[index];
          return <div key={i}><p>第 {edit.row + 1} 行 · {label}</p><small>原值</small><pre>{original ?? "（空）"}</pre><small>新值</small><pre>{edit.value ?? "（清空单元格）"}</pre></div>;
        })}
      </>}
      {tool === "artifact_export" && <ArtifactPreview content={value.content} />}
    </>}
  </div>;
}
