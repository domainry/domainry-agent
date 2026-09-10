export type Todo = {
  id: string; title: string; description?: string; status: "open" | "completed";
  due_date?: string; due_at?: string; timezone: string; revision: number;
  batch_id: string; position: number; source_conversation_id?: string; source_run_id?: string;
  created_at: string; updated_at: string; completed_at?: string;
};
export type TodoPage = { items: Todo[]; next_cursor?: string; complete: boolean };
export type TodoBatch = { batch_id: string; items: Todo[] };
export function todoDeadline(todo: Pick<Todo, "due_date" | "due_at" | "timezone">) {
  if (todo.due_date) return `${todo.due_date} · ${todo.timezone}`;
  if (todo.due_at) {
    try { return new Date(todo.due_at).toLocaleString(undefined, { timeZone: todo.timezone, timeZoneName: "short" }); } catch { return `${todo.due_at} · ${todo.timezone}`; }
  }
  return "未设截止日期";
}
export type TodoMutation = { path: string; method: "POST" | "PATCH" | "DELETE"; body: Record<string, unknown>; label: string };
// Only todo endpoints are eligible for a persisted retry. The body, including
// its client_id and expected revision, must be reused without reconstruction.
export function parseTodoMutation(raw: string | null): TodoMutation | null {
  try {
    const value = JSON.parse(raw || "null") as TodoMutation | null;
    if (!value || typeof value.path !== "string" || typeof value.label !== "string" || !value.body || typeof value.body !== "object" || Array.isArray(value.body) || typeof value.body.client_id !== "string" || !value.body.client_id.trim()) return null;
    if (value.method === "POST") {
      if (value.path !== "/agent/todos") return null;
    } else if (value.method === "PATCH" || value.method === "DELETE") {
      if (!/^\/agent\/todos\/[A-Za-z0-9_.:-]+$/.test(value.path) || !Number.isSafeInteger(value.body.expected_revision) || (value.body.expected_revision as number) < 1) return null;
    } else return null;
    return value;
  } catch { return null; }
}
