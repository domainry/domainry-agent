import type { Run } from "./api.ts";

export type Citation = { library_id?: string; id: string; provider: string; kb_id: string; operation: "search" | "fetch"; doc_id: string; title?: string; url?: string; excerpt?: string; excerpt_truncated?: boolean };
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
