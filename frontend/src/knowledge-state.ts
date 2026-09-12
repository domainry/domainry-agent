import type { Run } from "./api.ts";

export type Citation = { conversation_id?: string; library_id?: string; id: string; provider: string; kb_id: string; operation: "search" | "fetch"; doc_id: string; title?: string; url?: string; excerpt?: string; excerpt_truncated?: boolean; location?: { sheet?: string; row?: number; cell?: string } };
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
  if (typeof location.sheet === "string" && location.sheet.length <= 128 && location.sheet) parts.push(`工作表：${location.sheet}`);
  if (typeof location.cell === "string" && /^[A-Z]{1,3}[1-9][0-9]{0,6}$/.test(location.cell)) parts.push(`单元格 ${location.cell}`);
  else if (Number.isInteger(location.row) && location.row! > 0) parts.push(`第 ${location.row} 行`);
  return parts.join(" · ");
}
