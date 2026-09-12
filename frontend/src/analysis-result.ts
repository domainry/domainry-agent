import type { ArtifactContent } from "./artifact-state.ts";

type AnalysisReference = { kind: string; id: string; label?: string; version?: string; subresource?: string };
type AnalysisMissing = { column: string; code: string; count: string };
export type AnalysisResultPreview = {
  content: ArtifactContent;
  datasetKey: string;
  complete: boolean;
  truncated: boolean;
  returnedRows: number;
  requestedMaxRows: number;
  missing: AnalysisMissing[];
  references: AnalysisReference[];
  chartOmittedReason?: string;
};

const numeric = new Set(["integer", "decimal", "currency", "percent", "number"]);
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);
const cleanText = (value: unknown, max = 256): string | undefined => typeof value === "string" && value.length > 0 && value.length <= max && !value.includes("\0") ? value : undefined;

// Parse only the closed presentation projection needed by the UI. The raw,
// hash-checked JSON remains the source of truth and is still shown below it.
export function parseAnalysisResult(raw: string): AnalysisResultPreview | null {
  let receipt: unknown;
  try { receipt = JSON.parse(raw); } catch { return null; }
  if (!object(receipt) || receipt.status !== "completed" || !object(receipt.content)) return null;
  const envelope = receipt.content;
  if (!object(envelope) || envelope.operation !== "run" || !object(envelope.result)) return null;
  const result = envelope.result;
  if (!object(result.spec) || !object(result.coverage) || !object(result.visualization) || !object(result.source) || !Array.isArray(result.columns) || !Array.isArray(result.rows) || !Array.isArray(result.references)) return null;
  const datasetKey = cleanText(result.spec.dataset_key, 128);
  const coverage = result.coverage;
  if (!datasetKey || result.source.dataset_key !== datasetKey || coverage.complete !== true || typeof coverage.truncated !== "boolean" || typeof coverage.returned_rows !== "number" || typeof coverage.requested_max_rows !== "number" || !Number.isSafeInteger(coverage.returned_rows) || !Number.isSafeInteger(coverage.requested_max_rows) || coverage.returned_rows !== result.rows.length || coverage.returned_rows < 0 || coverage.requested_max_rows < 1 || !Array.isArray(coverage.missing)) return null;

  const keys = new Set<string>();
  const columns = result.columns.map(column => {
    if (!object(column)) return null;
    const key = cleanText(column.key, 128), sourceType = cleanText(column.type, 32);
    if (!key || !sourceType || keys.has(key)) return null;
    keys.add(key);
    const label = cleanText(column.name) || key;
    const type = numeric.has(sourceType) ? "number" : sourceType === "date" || sourceType === "datetime" ? "date" : "text";
    return { key, label, type } as const;
  });
  if (!columns.length || columns.some(column => !column)) return null;
  const tableRows = result.rows.map(row => {
    if (!object(row) || !object(row.values)) return null;
    const values = row.values;
    const cells = columns.map(column => {
      const value = values[column!.key];
      return value === null || typeof value === "string" && value.length <= 16384 ? value : undefined;
    });
    return cells.some(cell => cell === undefined) ? null : cells as (string | null)[];
  });
  if (tableRows.some(row => !row)) return null;

  const references = result.references.map(reference => {
    if (!object(reference)) return null;
    const kind = cleanText(reference.kind, 64), id = cleanText(reference.id);
    if (!kind || !id) return null;
    return { kind, id, label: cleanText(reference.label), version: cleanText(reference.version), subresource: cleanText(reference.subresource) };
  });
  if (!references.length || references.length > 16 || references.some(reference => !reference)) return null;
  const missing = coverage.missing.map(item => {
    if (!object(item)) return null;
    const column = cleanText(item.column, 128), code = cleanText(item.code, 128);
    return column && code && typeof item.count === "string" && /^[1-9][0-9]{0,63}$/.test(item.count) ? { column, code, count: item.count } : null;
  });
  if (missing.some(item => !item)) return null;

  let chart: ArtifactContent["chart"];
  const rawChart = result.visualization.chart;
  if (rawChart !== null) {
    if (!object(rawChart) || !["bar", "line"].includes(String(rawChart.type)) || !cleanText(rawChart.x_column, 128) || !Array.isArray(rawChart.y_columns)) return null;
    const y = rawChart.y_columns.map(value => cleanText(value, 128));
    if (!keys.has(String(rawChart.x_column)) || !y.length || y.length > 8 || y.some(value => !value || !keys.has(value))) return null;
    chart = { type: rawChart.type as "bar" | "line", x_column: String(rawChart.x_column), y_columns: y as string[] };
  }
  const omitted = cleanText(result.visualization.omitted_reason, 128);
  if (!!chart === !!omitted) return null;
  return {
    content: { kind: chart ? "chart" : "table", table: { columns: columns as NonNullable<typeof columns[number]>[], rows: tableRows as (string | null)[][] }, chart },
    datasetKey, complete: true, truncated: coverage.truncated as boolean,
    returnedRows: Number(coverage.returned_rows), requestedMaxRows: Number(coverage.requested_max_rows),
    missing: missing as AnalysisMissing[], references: references as AnalysisReference[], chartOmittedReason: omitted,
  };
}
