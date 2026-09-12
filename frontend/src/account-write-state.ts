// Display validation only. Tool schemas, current authorization and execution
// rules remain in their owners. Unknown fields must never be hidden by a preview.
export type WriteAddress = { address: string; name?: string };
export type WriteAttendee = WriteAddress & { kind: "required" | "optional" | "resource" };
export type WriteMoment = { date?: string; date_time?: string; time_zone: string };
export type CalendarChange = { title?: string; description?: string; location?: string; start?: WriteMoment; end?: WriteMoment; attendees?: WriteAttendee[] };
export type OutgoingMail = { to: WriteAddress[]; cc: WriteAddress[]; bcc: WriteAddress[]; subject: string; text: string };
type AccountTarget = { accountKey: string; accountRevision: string };
export type AccountWritePreview = AccountTarget & (
  { kind: "calendar_create"; calendarID: string; changes: CalendarChange } |
  { kind: "calendar_update"; calendarID: string; eventID: string; expectedVersion: string; scope: "event" | "series"; changes: CalendarChange } |
  { kind: "mail_send"; message: OutgoingMail } |
  { kind: "mail_reply"; messageID: string; message: OutgoingMail }
);
export const accountWriteTools = ["calendar_event_create", "calendar_event_update", "mail_send", "mail_reply"];
export const isAccountWrite = (tool: string) => accountWriteTools.includes(tool);
type ObjectValue = Record<string, unknown>;
const object = (v: unknown): v is ObjectValue => !!v && typeof v === "object" && !Array.isArray(v);
const text = (v: unknown): v is string => typeof v === "string";
const named = (v: unknown): v is string => text(v) && !!v.trim();
const keys = (v: ObjectValue, allowed: string[]) => Object.keys(v).every(key => allowed.includes(key));
function address(v: unknown): v is WriteAddress {
  return object(v) && keys(v, ["address", "name"]) && named(v.address) && (v.name === undefined || text(v.name));
}
function attendees(v: unknown): v is WriteAttendee[] {
  return Array.isArray(v) && v.length <= 100 && v.every(a => object(a) && keys(a, ["address", "name", "kind"]) && named(a.address) && (a.name === undefined || text(a.name)) && typeof a.kind === "string" && ["required", "optional", "resource"].includes(a.kind));
}
function moment(v: unknown): v is WriteMoment {
  return object(v) && keys(v, ["date", "date_time", "time_zone"]) && named(v.time_zone) && (
    named(v.date) && /^\d{4}-\d{2}-\d{2}$/.test(v.date) && v.date_time === undefined ||
    named(v.date_time) && !Number.isNaN(Date.parse(v.date_time)) && /(Z|[+-]\d{2}:\d{2})$/.test(v.date_time) && v.date === undefined
  );
}
function changes(v: unknown, create: boolean): v is CalendarChange {
  if (!object(v) || !keys(v, ["title", "description", "location", "start", "end", "attendees"]) || !Object.keys(v).length) return false;
  if (v.title !== undefined && !named(v.title) || v.description !== undefined && !text(v.description) || v.location !== undefined && !text(v.location)) return false;
  if (v.attendees !== undefined && !attendees(v.attendees)) return false;
  if ((v.start === undefined) !== (v.end === undefined) || v.start !== undefined && (!moment(v.start) || !moment(v.end))) return false;
  return !create || named(v.title) && moment(v.start) && moment(v.end) && attendees(v.attendees);
}
function message(v: unknown): v is OutgoingMail {
  if (!object(v) || !keys(v, ["to", "cc", "bcc", "subject", "text"]) || !text(v.subject) || !text(v.text)) return false;
  const all: WriteAddress[] = [];
  for (const group of [v.to, v.cc, v.bcc]) {
    if (!Array.isArray(group) || !group.every(address)) return false;
    all.push(...group);
  }
  return all.length > 0 && all.length <= 50 && new Set(all.map(a => a.address.toLowerCase())).size === all.length;
}
export function accountWritePreview(tool: string, argumentsText: string): AccountWritePreview | null {
  if (!isAccountWrite(tool) || new TextEncoder().encode(argumentsText).length > (1 << 20)) return null;
  try {
    const v: unknown = JSON.parse(argumentsText);
    if (!object(v) || !keys(v, ["account_key", "account_updated_at", "request"]) || !named(v.account_key) || !named(v.account_updated_at) || !object(v.request)) return null;
    const q = v.request, account = { accountKey: v.account_key, accountRevision: v.account_updated_at };
    if (tool === "calendar_event_create" && keys(q, ["calendar_id", "event", "notifications"]) && named(q.calendar_id) && q.notifications === "notify_attendees" && changes(q.event, true)) return { ...account, kind: "calendar_create", calendarID: q.calendar_id, changes: q.event };
    if (tool === "calendar_event_update" && keys(q, ["calendar_id", "event_id", "expected_version", "scope", "changes", "notifications"]) && named(q.calendar_id) && named(q.event_id) && named(q.expected_version) && (q.scope === "event" || q.scope === "series") && q.notifications === "notify_attendees" && changes(q.changes, false)) return { ...account, kind: "calendar_update", calendarID: q.calendar_id, eventID: q.event_id, expectedVersion: q.expected_version, scope: q.scope as "event" | "series", changes: q.changes };
    if (tool === "mail_send" && keys(q, ["message"]) && message(q.message)) return { ...account, kind: "mail_send", message: q.message };
    if (tool === "mail_reply" && keys(q, ["message_id", "message"]) && named(q.message_id) && message(q.message)) return { ...account, kind: "mail_reply", messageID: q.message_id, message: q.message };
  } catch { /* Invalid or incomplete snapshots cannot be confirmed. */ }
  return null;
}
