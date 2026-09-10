import { useState } from "react";
import { MessageResponse } from "./components/ai-elements/message";
import { Button } from "./components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { citationMarkdown, safeCitationURL, validCitations, type Citation } from "./knowledge-state.ts";

function CitationDialog({ citation, onClose }: { citation: Citation; onClose: () => void }) {
  const link = safeCitationURL(citation.url);
  return <Dialog open onOpenChange={open => { if (!open) onClose(); }}><DialogContent className="knowledge-dialog"><DialogHeader><DialogTitle>{citation.title || citation.doc_id}</DialogTitle><DialogDescription>查看本次取得的资料片段，核对回复中的依据。</DialogDescription></DialogHeader>
    {citation.library_id && <p className="subtle">资料库编号：{citation.library_id}</p>}
    <p className="subtle">文档编号：{citation.doc_id}{citation.kb_id ? ` · 知识库：${citation.kb_id}` : ""}</p>
    <p className="subtle">{citation.operation === "search" ? "搜索命中的片段" : "文档读取结果中的片段"}{citation.excerpt_truncated ? " · 仅展示部分内容" : ""}</p>
    {citation.excerpt ? <blockquote className="knowledge-excerpt">{citation.excerpt}</blockquote> : <p className="subtle">上游没有提供可展示的正文片段。</p>}
    {link && <a className="knowledge-source-link" href={link} target="_blank" rel="noopener noreferrer">打开来源链接 ↗</a>}
  </DialogContent></Dialog>;
}

export function KnowledgeSources({ citations }: { citations: Citation[] }) {
  const items = validCitations(citations);
  const [selectedID, setSelectedID] = useState("");
  const selected = items.find(item => item.id === selectedID);
  return <div className="knowledge-sources" aria-label="本次查阅资料">{items.map(item => <Button key={item.id} size="sm" variant="outline" onClick={() => setSelectedID(item.id)}><span>{item.title || item.doc_id}</span><small>{item.operation === "search" ? "检索片段" : "读取依据"}</small></Button>)}{selected && <CitationDialog citation={selected} onClose={() => setSelectedID("")} />}</div>;
}

export function KnowledgeResponse({ text, citations = [], isAnimating = false }: { text: string; citations?: Citation[]; isAnimating?: boolean }) {
  const items = validCitations(citations);
  const [selectedID, setSelectedID] = useState("");
  const selected = items.find(item => item.id === selectedID);
  return <><MessageResponse isAnimating={isAnimating} components={{ a: ({ href, children }) => {
    if (href?.startsWith("#knowledge-")) {
      const citation = items.find(item => href === `#knowledge-${item.id}`);
      return citation ? <button className="knowledge-citation" type="button" aria-label={`查看来源：${citation.title || citation.doc_id}`} onClick={() => setSelectedID(citation.id)}>{children}</button> : <span>【引用未验证】</span>;
    }
    const link = safeCitationURL(href);
    return link ? <a href={link} target="_blank" rel="noopener noreferrer">{children}</a> : <span>{children}</span>;
  } }}>{citationMarkdown(text, items)}</MessageResponse>{selected && <CitationDialog citation={selected} onClose={() => setSelectedID("")} />}</>;
}
