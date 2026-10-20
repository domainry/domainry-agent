import type { Run } from "./api.ts";

export type Citation = { conversation_id?: string; library_id?: string; id: string; provider: string; kb_id: string; operation: "search" | "fetch"; doc_id: string; title?: string; url?: string; excerpt?: string; excerpt_truncated?: boolean; location?: { page?: number; slide?: number; section?: string; chunk_id?: string; sheet?: string; row?: number; row_end?: number; cell?: string; line?: number; line_end?: number; paragraph?: number; json_path?: string; html_id?: string; start_ms?: number; end_ms?: number; media_kind?: "audio" | "video" } };
export function validCitations(items?: Citation[]): Citation[] {
  const seen = new Set<string>();
  return (items || []).filter(item => {
    if (!item || !/^kc_[0-9a-f]{32}$/.test(item.id) || typeof item.doc_id !== "string" || !item.doc_id || !["search", "fetch"].includes(item.operation) || seen.has(item.id)) return false;
    seen.add(item.id); return true;
  });
}
export function runCitations(run: Run): Citation[] {
  if (run.access_error) return [];
  return validCitations(run.steps?.flatMap(step => step.calls.filter(call => call.status === "completed").flatMap(call => call.citations || [])));
}
export function citationMarkdown(text: string, items: Citation[]): string {
  const positions = new Map(validCitations(items).map((item, index) => [item.id, index + 1]));
  return text.replace(/\[\[cite:([^\]\r\n]{1,128})\]\]/g, (_, id: string) => positions.has(id) ? `[来源 ${positions.get(id)}](#knowledge-${id})` : "【引用未验证】");
}
export function safeCitationURL(value?: string): string | undefined {
  try { const url = new URL(value || ""); return ["https:", "http:"].includes(url.protocol) && !url.username && !url.password && !/[\r\n\t]/.test(value || "") ? url.href : undefined; } catch { return undefined; }
}

export function citationLocation(citation: Citation): string {
  const location = citation.location;
  if (!location) return "";
  const parts: string[] = [];
  if (Number.isInteger(location.page) && location.page! > 0) parts.push(`第 ${location.page} 页`);
  if (Number.isInteger(location.slide) && location.slide! > 0) parts.push(`第 ${location.slide} 张幻灯片`);
  if (typeof location.section === "string" && location.section.length <= 256 && location.section) parts.push(location.section);
  if (typeof location.chunk_id === "string" && location.chunk_id.length <= 128 && location.chunk_id) parts.push(`分块 ${location.chunk_id}`);
  if (typeof location.sheet === "string" && location.sheet.length <= 128 && location.sheet) parts.push(`工作表：${location.sheet}`);
  if (typeof location.cell === "string" && /^[A-Z]{1,3}[1-9][0-9]{0,6}$/.test(location.cell)) parts.push(`单元格 ${location.cell}`);
  else if (Number.isInteger(location.row) && location.row! > 0) parts.push(Number.isInteger(location.row_end) && location.row_end! >= location.row! ? `第 ${location.row}–${location.row_end} 行` : `第 ${location.row} 行`);
  if (Number.isInteger(location.line) && location.line! > 0) parts.push(Number.isInteger(location.line_end) && location.line_end! >= location.line! ? `第 ${location.line}–${location.line_end} 行` : `第 ${location.line} 行`);
  if (Number.isInteger(location.paragraph) && location.paragraph! >= 0) parts.push(`第 ${location.paragraph! + 1} 段`);
  if (typeof location.json_path === "string" && location.json_path.length <= 512 && location.json_path) parts.push(`JSON ${location.json_path}`);
  if (typeof location.html_id === "string" && location.html_id.length <= 256 && location.html_id) parts.push(`#${location.html_id}`);
  if (Number.isInteger(location.start_ms) && location.start_ms! >= 0) parts.push(`${location.media_kind === "video" ? "视频" : location.media_kind === "audio" ? "音频" : "媒体"} ${formatMediaTime(location.start_ms!)}${Number.isInteger(location.end_ms) && location.end_ms! > location.start_ms! ? `–${formatMediaTime(location.end_ms!)}` : ""}`);
  return parts.join(" · ");
}

function formatMediaTime(milliseconds: number) {
  const seconds = Math.floor(milliseconds / 1000);
  const hours = Math.floor(seconds / 3600), minutes = Math.floor(seconds % 3600 / 60), rest = seconds % 60;
  return hours ? `${hours}:${String(minutes).padStart(2, "0")}:${String(rest).padStart(2, "0")}` : `${minutes}:${String(rest).padStart(2, "0")}`;
}
