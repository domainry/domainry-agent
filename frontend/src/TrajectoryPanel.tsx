import { useEffect, useState } from "react";
import { formatLocalDateTime } from "./time.ts";
import { Download, GitBranch, RefreshCw, Scale } from "lucide-react";
import {
  exportConversationTrajectory,
  request,
  trajectoryPath,
  type ConversationRecord,
  type ConversationTrajectory,
  type ContentBlock,
  type TrajectoryComparison,
  type TrajectoryReplay,
} from "./api";
import { describeError } from "./errors";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const shortHash = (value: string) => value ? `${value.slice(0, 12)}…` : "—";

function TrajectoryContent({ blocks }: { blocks?: ContentBlock[] }) {
  const images = blocks?.filter((block): block is Extract<ContentBlock, { type: "image" }> => block.type === "image") || [];
  if (!images.length) return null;
  return <ul className="trajectory-images">{images.map((block, index) => <li key={`${block.image.attachment_id}:${index}`}>
    图片：{block.image.filename} · {block.image.content_type} · {block.image.bytes} 字节
    {block.image.source && <> · 来源运行 {block.image.source.run_id}</>}
  </li>)}</ul>;
}

export function TrajectoryPanel({ conversationID, runID, onFork }: { conversationID: string; runID: string; onFork?: (conversation: ConversationRecord) => void }) {
  const [trajectory, setTrajectory] = useState<ConversationTrajectory | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState("");
  const [otherConversation, setOtherConversation] = useState(conversationID);
  const [otherRun, setOtherRun] = useState("");
  const [comparison, setComparison] = useState<TrajectoryComparison | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setTrajectory(null); setError(""); setNotice(""); setComparison(null);
    request<ConversationTrajectory>(trajectoryPath(conversationID, runID), "GET", undefined, controller.signal)
      .then(value => { if (!controller.signal.aborted) setTrajectory(value); })
      .catch(reason => { if (!controller.signal.aborted) setError(describeError(reason)); });
    return () => controller.abort();
  }, [conversationID, runID]);

  async function action(name: string, work: () => Promise<void>) {
    if (busy) return;
    setBusy(name); setError(""); setNotice("");
    try { await work(); } catch (reason) { setError(describeError(reason)); }
    finally { setBusy(""); }
  }

  return <section className="trajectory-panel" aria-label="请求与工具轨迹">
    <header><div><h3>请求与工具轨迹</h3><p className="subtle">展示和录制夹具回放只读取已保存记录，不调用模型或工具。创建分叉后由你发送新输入，才会开始新的独立运行。</p></div>{trajectory && <code title={trajectory.sha256}>{shortHash(trajectory.sha256)}</code>}</header>
    {error && <p role="alert" className="text-destructive text-sm">{error}</p>}
    {!trajectory && !error && <p role="status" className="subtle">正在读取轨迹…</p>}
    {trajectory && <>
      <dl className="trajectory-summary">
        <dt>稳定边界</dt><dd>事件序号 {trajectory.boundary_event_seq}</dd>
        <dt>录制时间</dt><dd>{formatLocalDateTime(trajectory.recorded_at)}</dd>
        <dt>内容</dt><dd>{trajectory.requests.length} 次模型请求 · {trajectory.responses.length} 次录制响应 · {trajectory.tools.length} 个工具调用</dd>
      </dl>
      <div className="trajectory-actions">
        <Button variant="outline" size="sm" disabled={!!busy} onClick={() => void action("export", async () => {
          const download = await exportConversationTrajectory(conversationID, runID);
          const url = URL.createObjectURL(download.blob);
          const link = document.createElement("a"); link.href = url; link.download = download.filename; link.click();
          setTimeout(() => URL.revokeObjectURL(url), 0);
          setNotice(`轨迹已导出${download.sha256 ? ` · ${shortHash(download.sha256)}` : ""}`);
        })}><Download size={14}/>{busy === "export" ? "正在导出…" : "导出 JSON"}</Button>
        <Button variant="outline" size="sm" disabled={!!busy} onClick={() => void action("fixture", async () => {
          const replay = await request<TrajectoryReplay>(`${trajectoryPath(conversationID, runID)}/replay`, "POST", { mode: "model_fixture" });
          if (replay.effects_executed || replay.recorded_responses?.length !== trajectory.responses.length) throw new Error("录制夹具不完整");
          setNotice(`录制夹具可用：${replay.consumed_requests} 次请求，未执行外部操作。`);
        })}><RefreshCw size={14}/>{busy === "fixture" ? "正在核对…" : "核对录制夹具"}</Button>
        {onFork && <Button size="sm" disabled={!!busy} onClick={() => void action("fork", async () => {
          const replay = await request<TrajectoryReplay>(`${trajectoryPath(conversationID, runID)}/replay`, "POST", { mode: "live_rerun", fork: { client_id: crypto.randomUUID(), title: "分叉探索" } });
          if (!replay.fork || replay.effects_executed || !replay.ready_for_input) throw new Error("分叉创建结果不完整");
          onFork(replay.fork);
        })}><GitBranch size={14}/>{busy === "fork" ? "正在创建…" : "创建独立分叉"}</Button>}
      </div>
      {notice && <p role="status" className="trajectory-notice">{notice}</p>}

      <div className="trajectory-records">
        {trajectory.requests.map((item, index) => <details key={item.sha256}>
          <summary>请求 {index + 1} · {item.step < 0 ? "回复" : `步骤 ${item.step + 1}`} <code>{shortHash(item.sha256)}</code></summary>
          <dl><dt>模型</dt><dd>{item.model.provider} / {item.model.model || "未记录"}</dd><dt>用途</dt><dd>{item.purpose}</dd>{item.reasoning_effort && <><dt>推理强度</dt><dd>{item.reasoning_effort}</dd></>}</dl>
          <h4>模型可见消息</h4>
          {item.messages.map((message, messageIndex) => <article key={`${messageIndex}:${message.role}`} className="trajectory-message"><strong>{message.role}</strong><pre>{message.content || (message.content_blocks?.length ? "（图片输入）" : "（空内容）")}</pre><TrajectoryContent blocks={message.content_blocks}/>{message.tool_calls?.length ? <pre>{JSON.stringify(message.tool_calls, null, 2)}</pre> : null}</article>)}
          {item.tools?.length ? <><h4>冻结工具定义</h4><pre>{JSON.stringify(item.tools, null, 2)}</pre></> : null}
          {item.context && <><h4>上下文边界</h4><pre>{JSON.stringify(item.context, null, 2)}</pre></>}
          {trajectory.responses.filter(response => response.request_index === item.index).map(response => <div key={response.sha256} className="trajectory-response"><h4>录制响应 · {response.finish_reason}</h4><pre>{response.message.content || JSON.stringify(response.message.tool_calls || [], null, 2)}</pre><small className="subtle">{shortHash(response.sha256)}</small></div>)}
        </details>)}
        {!!trajectory.tools.length && <details><summary>工具轨迹 · {trajectory.tools.length} 项</summary>{trajectory.tools.map(tool => <article key={tool.sha256} className="trajectory-tool"><strong>{tool.call.name} · {tool.state}</strong><pre>{tool.call.arguments}</pre><pre>{JSON.stringify(tool.result || null, null, 2)}</pre><small className="subtle">步骤 {tool.step + 1} · {shortHash(tool.sha256)}</small></article>)}</details>}
      </div>

      <form className="trajectory-compare" onSubmit={event => { event.preventDefault(); void action("compare", async () => {
        const result = await request<TrajectoryComparison>(`${trajectoryPath(conversationID, runID)}/compare`, "POST", { other: { conversation_id: otherConversation.trim(), run_id: otherRun.trim() } });
        setComparison(result);
      }); }}>
        <h4><Scale size={15}/>对照另一条轨迹</h4>
        <label>会话 ID<Input value={otherConversation} onChange={event => setOtherConversation(event.target.value)} required /></label>
        <label>运行 ID<Input value={otherRun} onChange={event => setOtherRun(event.target.value)} required /></label>
        <Button variant="outline" size="sm" disabled={!!busy || !otherConversation.trim() || !otherRun.trim()} type="submit">{busy === "compare" ? "正在对照…" : "开始对照"}</Button>
      </form>
      {comparison && <div className="trajectory-comparison" role="status"><strong>{comparison.equal ? "两条轨迹完全一致" : `发现 ${comparison.differences.length} 处差异`}</strong><p className="subtle">左 {shortHash(comparison.left_sha256)} · 右 {shortHash(comparison.right_sha256)}</p>{comparison.differences.length ? <ul>{comparison.differences.map(item => <li key={`${item.kind}:${item.index}`}>{item.kind} {item.index + 1} · {shortHash(item.left_sha256 || "")} / {shortHash(item.right_sha256 || "")}</li>)}</ul> : null}</div>}
    </>}
  </section>;
}
