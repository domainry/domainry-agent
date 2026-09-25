import assert from "node:assert/strict";
import test from "node:test";
import { browserTimeZone, formatLocalDate, formatLocalDateTime, formatLocalTime } from "./time.ts";

test("instant formatting follows the browser locale and timezone", () => {
  const instant = "2026-09-25T00:05:06.789Z";
  const date = new Date(instant);
  assert.equal(formatLocalDateTime(instant), new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(date));
  assert.equal(formatLocalDate(instant), new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(date));
  assert.equal(formatLocalTime(instant), new Intl.DateTimeFormat(undefined, { timeStyle: "medium" }).format(date));
  assert.equal(browserTimeZone(), Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC");
});

test("numeric instants are interpreted as Unix milliseconds", () => {
  const millis = 1790294706789;
  assert.equal(formatLocalDateTime(millis), formatLocalDateTime(new Date(millis)));
});
