function sourceLink(raw: unknown, title?: string) {
  if (typeof raw !== "string") return null;
  try {
    const url = new URL(raw);
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) return null;
    return <a href={url.href} target="_blank" rel="noopener noreferrer" className="break-all underline">{title || raw}</a>;
  } catch { return null; }
}

// Render text as data. A page's HTML or Markdown never becomes executable UI.
export function webResult(key: string, text?: string) {
  try {
    const {data, read_at} = JSON.parse(text || "");
    if (!data || typeof read_at !== "string") return null;
    const retrieved = <small className="subtle">读取时间：{read_at}（并非网页发布时间）</small>;
    if (key === "web_search" && Array.isArray(data.items) && data.scope === "ranked_results") return <div className="web-result space-y-3">
      <p>搜索：{typeof data.query === "string" ? data.query : ""}</p>{retrieved}
      <p className="subtle">排名结果与片段，未覆盖全部来源。{data.truncated === true ? "结果已裁剪。" : ""}</p>
      {!data.items.length && <p>没有返回匹配来源。</p>}
      {data.items.map((item: {url?: unknown; title?: unknown; excerpts?: unknown; truncated?: boolean}, i: number) => <div key={i} className="space-y-1">
        {sourceLink(item.url, typeof item.title === "string" ? item.title : undefined)}
        <small className="block break-all subtle">{typeof item.url === "string" ? item.url : ""}</small>
        {Array.isArray(item.excerpts) && item.excerpts.filter((v): v is string => typeof v === "string").map((v, j) => <p className="whitespace-pre-wrap break-words" key={j}>{v}</p>)}
        {item.truncated === true && <small className="subtle">片段已裁剪</small>}
      </div>)}
    </div>;
    if (key === "web_fetch" && typeof data.content === "string") return <div className="web-result space-y-3">
      {sourceLink(data.url, typeof data.title === "string" ? data.title : undefined)}
      <p className="break-all">请求来源：{sourceLink(data.requested_url)}</p><p className="break-all">服务返回来源：{sourceLink(data.url)}</p>{retrieved}
      <p className="subtle">{data.source_completeness === "partial" ? "仅取得部分页面内容。" : "页面完整性未知。"}{data.truncated === true ? "正文已裁剪。" : ""}</p>
      {Array.isArray(data.warnings) && (data.warnings as unknown[]).filter((v): v is string => typeof v === "string").map((v, i) => <p className="subtle" key={i}>{v}</p>)}
      <p className="whitespace-pre-wrap break-words">{data.content || "没有返回可读正文，不能据此判断网页为空。"}</p>
    </div>;
  } catch { /* Incomplete previews retain the generic bounded fallback. */ }
  return null;
}
