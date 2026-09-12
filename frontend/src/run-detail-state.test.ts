import assert from "node:assert/strict";
import test from "node:test";
import { auditEventLabel, durationLabel, usageItems } from "./run-detail-state.ts";

test("run detail formats bounded duration and common provider usage", () => {
  assert.equal(durationLabel(0), "小于 1 毫秒");
  assert.equal(durationLabel(1250), "1.25 秒");
  assert.equal(durationLabel(61_000), "1 分 1 秒");
  assert.equal(durationLabel(undefined), "未报告");
  assert.deepEqual(usageItems({prompt_tokens: 7, completion_tokens: 3, secret_vendor_field: "hidden"}), [
    {label: "输入 tokens", value: 7}, {label: "输出 tokens", value: 3}, {label: "总 tokens", value: 10},
  ]);
  assert.deepEqual(usageItems({total_tokens: 12}), [{label: "总 tokens", value: 12}]);
});

test("audit labels expose safe execution facts", () => {
  assert.equal(auditEventLabel({seq: 3, type: "authorization", status: "granted", step: 0, tool: "todo_create", occurred_at: "2026-09-12T00:00:00Z"}), "授权检查 · 已授权 · todo_create");
});
