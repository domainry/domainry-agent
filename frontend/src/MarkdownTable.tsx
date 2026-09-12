import { Children, cloneElement, isValidElement, useRef, useState, type ComponentProps, type ReactNode } from "react";
import type { ExtraProps } from "streamdown";
import { extractTableDataFromElement, tableDataToMarkdown, tableDataToTSV } from "streamdown";
import { Button } from "./components/ui/button";

type SectionProps = { children?: ReactNode; node?: { tagName?: string } };
const pageSize = 20;

// Paginate the already-authorized rendered message. This never fetches more
// source content or treats a Markdown table as a complete original document.
export function MarkdownTable({ children, node: _node, ...props }: ComponentProps<"table"> & ExtraProps) {
  const [page, setPage] = useState(0);
  const [feedback, setFeedback] = useState("");
  const table = useRef<HTMLTableElement>(null);
  const sections = Children.toArray(children);
  const body = sections.find(child => isValidElement<SectionProps>(child) && (child.type === "tbody" || child.props.node?.tagName === "tbody"));
  const rows = isValidElement<SectionProps>(body) ? Children.toArray(body.props.children) : [];
  const pages = Math.max(1, Math.ceil(rows.length / pageSize));
  const current = Math.min(page, pages - 1);
  const copy = async () => {
    if (!table.current) return;
    try { await navigator.clipboard.writeText(tableDataToTSV(extractTableDataFromElement(table.current))); setFeedback("已复制本页表格"); }
    catch { setFeedback("无法复制，请选择表格文本后手动复制。"); }
  };
  const download = () => {
    if (!table.current) return;
    const url = URL.createObjectURL(new Blob([tableDataToMarkdown(extractTableDataFromElement(table.current))], { type: "text/markdown;charset=utf-8" }));
    const link = document.createElement("a"); link.href = url; link.download = `表格-第${current + 1}页.md`; link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000); setFeedback("已下载本页表格");
  };
  return <section className="markdown-table" aria-label="回复表格" data-static-ui-list>
    <div className="markdown-table-actions"><Button type="button" variant="ghost" size="sm" onClick={copy}>复制本页</Button><Button type="button" variant="ghost" size="sm" onClick={download}>下载本页</Button></div>
    <div className="markdown-table-scroll"><table {...props} ref={table}>{sections.map(child => child === body && isValidElement<SectionProps>(child) ? cloneElement(child, { children: rows.slice(current * pageSize, (current + 1) * pageSize) }) : child)}</table></div>
    <nav className="markdown-table-pagination" aria-label="回复表格分页" data-static-ui-pagination>
      <Button type="button" variant="ghost" size="sm" disabled={current === 0} onClick={() => { setPage(current - 1); setFeedback(""); }}>上一页</Button>
      <span>第 {current + 1} / {pages} 页 · 共 {rows.length} 行</span>
      <Button type="button" variant="ghost" size="sm" disabled={current + 1 >= pages} onClick={() => { setPage(current + 1); setFeedback(""); }}>下一页</Button>
    </nav>
    {feedback && <p className="subtle" role="status">{feedback}</p>}
  </section>;
}
