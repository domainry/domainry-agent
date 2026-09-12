import test from "node:test";
import assert from "node:assert/strict";
import { accountWritePreview } from "./account-write-state.ts";

const target = { account_key: "chosen-account", account_updated_at: "revision-1" };
const addresses = (prefix: string, n = 1) => Array.from({ length: n }, (_, i) => ({ address: `${prefix}${i}@example.test`, name: `名字 ${i}` }));
const message = { to: addresses("to"), cc: addresses("cc"), bcc: addresses("hidden"), subject: "完整主题", text: "完整正文\n<script>正文是文本</script>" };
const preview = (tool: string, request: unknown) => accountWritePreview(tool, JSON.stringify({ ...target, request }));
const event = { title: "全天评审", start: { date: "2026-11-01", time_zone: "America/New_York" }, end: { date: "2026-11-02", time_zone: "America/New_York" }, attendees: [{ address: "one@example.test", kind: "required" }] };
const create = { calendar_id: "calendar", event, notifications: "notify_attendees" };
const update = { calendar_id: "calendar", event_id: "exact-event", expected_version: '"version-1"', scope: "series", changes: { description: "", location: "", attendees: [] }, notifications: "notify_attendees" };

test("calendar preview retains exact target, exclusive all-day dates, series and explicit clears", () => {
  assert.deepEqual(preview("calendar_event_create", create), { accountKey: target.account_key, accountRevision: target.account_updated_at, kind: "calendar_create", calendarID: "calendar", changes: event });
  const value = preview("calendar_event_update", update);
  assert.ok(value?.kind === "calendar_update");
  assert.equal(value.scope, "series"); assert.equal(value.eventID, "exact-event");
  assert.deepEqual(value.changes, update.changes);
  assert.equal(Object.hasOwn(value.changes, "title"), false);
});
test("mail preview retains every recipient, original ID and complete long text", () => {
  const full = { ...message, to: addresses("to", 25), cc: addresses("cc", 15), bcc: addresses("bcc", 10), text: "字".repeat(18000) + "末尾确认内容" };
  const value = preview("mail_reply", { message_id: "original-id", message: full });
  assert.ok(value?.kind === "mail_reply"); assert.equal(value.messageID, "original-id"); assert.deepEqual(value.message, full);
  assert.deepEqual(preview("mail_send", { message })?.kind, "mail_send");
});
test("unknown or omitted content cannot disappear from a confirmable preview", () => {
  for (const request of [
    { message: { ...message, from: "hidden@example.test" } },
    { message: { ...message, bcc: undefined } },
    { message: { ...message, bcc: null } },
    { message: { ...message, cc: message.to } },
    { message: { ...message, to: [{ ...message.to[0], token: "hidden" }] } },
    { message, headers: { Bcc: "hidden@example.test" } },
  ]) assert.equal(preview("mail_send", request), null);
  for (const request of [
    { ...update, scope: ["series"] },
    { ...update, changes: {} },
    { ...update, changes: { description: null } },
    { ...update, changes: { start: event.start } },
    { ...create, event: { ...event, recurrence: ["RRULE:FREQ=DAILY"] } },
    { ...create, event: { ...event, attendees: [{ address: "one@example.test", kind: ["required"] }] } },
  ]) assert.equal(preview("changes" in request ? "calendar_event_update" : "calendar_event_create", request), null);
  assert.equal(accountWritePreview("mail_send", JSON.stringify({ ...target, request: { message }, access: "approved" })), null);
  assert.equal(accountWritePreview("mail_send", "{"), null);
});
test("timed preview preserves both DST offsets instead of converting to the browser zone", () => {
  const timed = { ...event, start: { date_time: "2026-11-01T01:30:00-04:00", time_zone: "America/New_York" }, end: { date_time: "2026-11-01T01:30:00-05:00", time_zone: "America/New_York" } };
  const value = preview("calendar_event_create", { ...create, event: timed });
  assert.ok(value?.kind === "calendar_create"); assert.deepEqual(value.changes, timed);
  assert.equal(preview("calendar_event_create", { ...create, event: { ...timed, start: { ...timed.start, date: "2026-11-01" } } }), null);
});
