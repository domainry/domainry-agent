import assert from "node:assert/strict";
import test from "node:test";
import { parseAnalysisResult } from "./analysis-result.ts";

const base = {
  status: "completed",
  content: {
    operation: "run",
    result: {
    spec: { dataset_key: "sales", max_rows: 10 },
    columns: [{ key: "region", name: "区域", type: "text" }, { key: "total", name: "金额", type: "decimal" }],
    rows: [{ values: { region: "east", total: "9007199254740993.20" } }, { values: { region: "west", total: null } }],
    visualization: { chart: { type: "bar", x_column: "region", y_columns: ["total"] } },
    coverage: { complete: true, truncated: false, returned_rows: 2, requested_max_rows: 10, missing: [{ column: "total", code: "null_value", count: "1" }] },
    references: [{ kind: "knowledge_document", id: "doc-1", label: "Revenue", version: "gen-1", subresource: "sheet-1" }],
    source: { dataset_key: "sales" },
    },
  },
};

test("parses a closed analysis projection without rounding decimal strings", () => {
  const parsed = parseAnalysisResult(JSON.stringify(base));
  assert.equal(parsed?.content.kind, "chart");
  assert.equal(parsed?.content.table?.rows[0][1], "9007199254740993.20");
  assert.deepEqual(parsed?.content.chart, { type: "bar", x_column: "region", y_columns: ["total"] });
  assert.deepEqual(parsed?.missing, [{ column: "total", code: "null_value", count: "1" }]);
  assert.equal(parsed?.references[0].id, "doc-1");
});

test("requires explicit complete coverage, source references and safe chart columns", () => {
  for (const change of ["partial", "count", "reference", "axis", "executable", "missing-reason"]) {
    const value = structuredClone(base) as any;
    if (change === "partial") value.content.result.coverage.complete = false;
    if (change === "count") value.content.result.coverage.returned_rows = 1;
    if (change === "reference") value.content.result.references = [];
    if (change === "axis") value.content.result.visualization.chart.y_columns = ["secret"];
    if (change === "executable") value.content.result.visualization.chart = { type: "javascript", x_column: "region", y_columns: ["total"], code: "fetch('/secret')" };
    if (change === "missing-reason") value.content.result.visualization = { chart: null };
    assert.equal(parseAnalysisResult(JSON.stringify(value)), null, change);
  }
});

test("accepts a declared chart omission and keeps the result table", () => {
  const value = structuredClone(base) as any;
  value.content.result.visualization = { chart: null, omitted_reason: "single_value_or_multiple_dimensions" };
  const parsed = parseAnalysisResult(JSON.stringify(value));
  assert.equal(parsed?.content.kind, "table");
  assert.equal(parsed?.chartOmittedReason, "single_value_or_multiple_dimensions");
});
