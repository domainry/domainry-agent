import { useState } from "react";
import { MessageResponse } from "./components/ai-elements/message";
import { Button } from "./components/ui/button";
import type { ArtifactContent } from "./artifact-state.ts";

export function ArtifactPreview({ content }: { content: ArtifactContent }) {
  const [page, setPage] = useState(0);
  if (content.kind === "markdown") return <div className="artifact-markdown"><MessageResponse skipHtml plugins={{}} controls={false} components={{
    img: ({ alt }) => <span className="subtle">[图片{alt ? `：${alt}` : ""}]</span>,
    a: ({ href, children }) => href && /^https?:\/\//i.test(href) ? <a href={href} target="_blank" rel="noopener noreferrer">{children}</a> : <span>{children}</span>,
  }}>{content.markdown || ""}</MessageResponse></div>;
  const table = content.table;
  if (!table) return <p>内容格式无法预览。</p>;
  const start = Math.min(page * 25, Math.max(0, Math.floor((table.rows.length - 1) / 25) * 25));
  return <div>
    {content.kind === "chart" && content.chart && <ArtifactChart content={content} />}
    <p className="subtle">{table.rows.length} 行 · {table.columns.length} 列 · 数值保留原始精度</p>
    <div className="artifact-table-scroll"><table className="artifact-table"><thead><tr><th>行</th>{table.columns.map(column => <th key={column.key}>{column.label}</th>)}</tr></thead><tbody>
      {table.rows.slice(start, start + 25).map((row, i) => <tr key={start + i}><th>{start + i + 1}</th>{row.map((cell, j) => <td key={j}>{cell === null ? <span className="subtle">—</span> : cell}</td>)}</tr>)}
    </tbody></table></div>
    {!table.rows.length && <p className="subtle">表格暂无数据。</p>}
    {table.rows.length > 25 && <div className="todo-toolbar"><Button size="sm" variant="outline" disabled={start === 0} onClick={() => setPage(Math.max(0, page - 1))}>上一页数据</Button><span>{start + 1}–{Math.min(start + 25, table.rows.length)} / {table.rows.length}</span><Button size="sm" variant="outline" disabled={start + 25 >= table.rows.length} onClick={() => setPage(page + 1)}>下一页数据</Button></div>}
  </div>;
}

function ArtifactChart({ content }: { content: ArtifactContent }) {
  const table = content.table!, chart = content.chart!;
  const x = table.columns.findIndex(column => column.key === chart.x_column);
  const series = chart.y_columns.map(key => table.columns.findIndex(column => column.key === key)).filter(index => index >= 0);
  const values = table.rows.flatMap(row => series.map(index => row[index] === null ? null : Number(row[index]))).filter((v): v is number => v !== null && Number.isFinite(v));
  if (x < 0 || !series.length || !table.rows.length || !values.length) return <p className="subtle">当前数据不足以绘制图表，完整数据见下表。</p>;
  const low = Math.min(0, ...values), high = Math.max(0, ...values), scale = Math.max(Math.abs(low), Math.abs(high)) || 1;
  const span = (high / scale - low / scale) || 1;
  const width = Math.max(640, table.rows.length * 28), height = 260, left = 64, inner = width - left - 20;
  const y = (n: number) => 20 + (high / scale - n / scale) / span * 180;
  const step = inner / table.rows.length;
  const colors = ["#2563eb", "#0f766e", "#9333ea", "#b45309", "#dc2626", "#475569", "#be185d", "#4d7c0f"];
  return <figure><div className="artifact-table-scroll"><svg width={width} height={height} role="img" aria-label={chart.type === "bar" ? "柱状图，精确数据见下表" : "折线图，精确数据见下表"}>
    <line x1={left} x2={width - 20} y1={y(0)} y2={y(0)} stroke="currentColor" opacity=".25" />
    <text x={6} y={24} fontSize="11" fill="currentColor">{high.toPrecision(4)}</text><text x={6} y={204} fontSize="11" fill="currentColor">{low.toPrecision(4)}</text>
    {series.map((index, group) => {
      const parts: string[] = []; let continuous = false;
      table.rows.forEach((row, i) => { const n = row[index] === null ? NaN : Number(row[index]); if (!Number.isFinite(n)) { continuous = false; return; } parts.push(`${continuous ? "L" : "M"}${left + step * (i + .5)},${y(n)}`); continuous = true; });
      return <g key={index} fill={colors[group % colors.length]}>{chart.type === "line" && <path d={parts.join(" ")} fill="none" stroke={colors[group % colors.length]} strokeWidth="2" />}{table.rows.map((row, i) => {
        const n = row[index] === null ? NaN : Number(row[index]); if (!Number.isFinite(n)) return null;
        const title = `${row[x] ?? "未填写"} · ${table.columns[index].label}：${row[index]}`;
        return chart.type === "bar" ? <rect key={i} x={left + step * i + group * step * .8 / series.length} y={Math.min(y(0), y(n))} width={Math.max(1, step * .8 / series.length - 1)} height={Math.max(1, Math.abs(y(n) - y(0)))}><title>{title}</title></rect> : <circle key={i} cx={left + step * (i + .5)} cy={y(n)} r="2.5"><title>{title}</title></circle>;
      })}</g>;
    })}
    {table.rows.map((row, i) => i % Math.max(1, Math.ceil(table.rows.length / 20)) === 0 && <text key={i} x={left + step * (i + .5)} y={224} textAnchor="middle" fontSize="11" fill="currentColor">{String(row[x] ?? "—").slice(0, 14)}</text>)}
  </svg></div><figcaption className="todo-toolbar">{series.map((index, group) => <span key={index} style={{ color: colors[group % colors.length] }}>● {table.columns[index].label}</span>)}<small className="subtle">图表用于观察趋势，精确数值见表格。</small></figcaption></figure>;
}
