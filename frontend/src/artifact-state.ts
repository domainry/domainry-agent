import { ApiError } from "./errors.ts";
import { sessionFetch, sessionScope } from "./session.ts";

export type Artifact = { id: string; version: number; title: string; kind: "markdown" | "table" | "chart"; sha256: string; bytes: number; source_conversation_id?: string; source_run_id?: string; created_at: string; updated_at: string };
export type ArtifactColumn = { key: string; label: string; type: "text" | "number" | "date" };
export type ArtifactContent = { kind: Artifact["kind"]; markdown?: string; table?: { columns: ArtifactColumn[]; rows: (string | null)[][] }; chart?: { type: "bar" | "line"; x_column: string; y_columns: string[] } };
export type ArtifactVersion = { artifact: Artifact; content: ArtifactContent };
export type ArtifactPage = { items: Artifact[]; complete: boolean; next_cursor?: string; omitted?: boolean };
export type ArtifactVersions = { items: Artifact[]; complete: boolean; next_before?: number; omitted?: boolean };
export type ArtifactExport = { id: string; artifact_id: string; version: number; format: "markdown" | "csv"; filename: string; content_type: string; sha256: string; bytes: number; expires_at: string; formula_guarded?: boolean };
export type ArtifactMutation = { kind: "edit" | "export"; artifactID: string; body: { client_id: string; expected_version?: number; patch?: Record<string, unknown>; version?: number; format?: "markdown" | "csv" } };
export const artifactPath = (id: string) => `/agent/artifacts/${encodeURIComponent(id)}`;

export function parseArtifactMutation(raw: string | null): ArtifactMutation | null {
  try {
    const v = JSON.parse(raw || "null") as ArtifactMutation | null;
    if (!v || !["edit", "export"].includes(v.kind) || typeof v.artifactID !== "string" || !/^[A-Za-z0-9_.:-]{1,96}$/.test(v.artifactID) || !v.body || typeof v.body.client_id !== "string" || !/^[A-Za-z0-9_.:-]{1,96}$/.test(v.body.client_id)) return null;
    if (v.kind === "edit" && (!Number.isSafeInteger(v.body.expected_version) || (v.body.expected_version || 0) < 1 || !v.body.patch || typeof v.body.patch !== "object" || Array.isArray(v.body.patch))) return null;
    if (v.kind === "export" && (!Number.isSafeInteger(v.body.version) || (v.body.version || 0) < 1 || !["markdown", "csv"].includes(v.body.format || ""))) return null;
    return v;
  } catch { return null; }
}

export async function downloadArtifact(value: ArtifactExport): Promise<void> {
  const scope = sessionScope();
  const response = await sessionFetch(`/agent/artifact-exports/${encodeURIComponent(value.id)}/download`, { method: "GET", credentials: "same-origin", cache: "no-store" });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new ApiError(typeof data.code === "string" ? data.code : "request_failed", response.status);
  }
  const blob = await response.blob();
  const hash = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", await blob.arrayBuffer())), n => n.toString(16).padStart(2, "0")).join("");
  if (scope !== sessionScope()) throw new ApiError("agent.web.identity_changed", 409);
  if (blob.size !== value.bytes || hash !== value.sha256) throw new ApiError("agent.conversation.artifact_export_mismatch");
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url; link.download = value.filename; link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
