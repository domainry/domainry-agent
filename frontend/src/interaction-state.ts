import type { Run } from "./api.ts";
import type { ExecutionEvent } from "./execution-state.ts";

export type Interaction = {
  id: string; conversation_id: string; run_id: string; step: number; call_id: string;
  kind: "input" | "confirmation" | "reconciliation";
  status: "pending" | "answered" | "approved" | "rejected" | "cancelled" | "expired" | "resolved";
  question: string; choices?: string[]; tool: string; tool_version: string;
  action_key: string; arguments: string; arguments_hash: string; definition_hash: string;
  revision: number; answer?: string; responded_by?: string; expires_at: string;
};
export type InteractionResponse = {
  interaction_id: string; client_id: string; expected_revision: number;
  decision: "answer" | "approve" | "reject"; answer?: string;
};
export const interactionEventNames = [
  "run.waiting_user", "run.waiting_confirmation", "run.needs_reconciliation",
  "interaction.responded", "interaction.expired", "interaction.cancelled", "interaction.resolved",
];
export function applyInteractionEvent(run: Run, event: ExecutionEvent): Run {
  if (!interactionEventNames.includes(event.type)) return run;
  const interaction = event.data?.interaction;
  if (!interaction || interaction.run_id !== run.id || interaction.conversation_id !== run.conversation_id || !interaction.id || !Number.isInteger(interaction.revision) || interaction.revision < 1) throw new Error("Invalid interaction event");
  if (interaction.id === run.interaction?.id && interaction.revision < run.interaction.revision) throw new Error("Outdated interaction event");
  const status = event.type.startsWith("run.") ? event.type.slice(4) as Run["status"] : run.status;
  return {...run, status, interaction};
}
export const waiting = (run: Run | null) => !!run && ["waiting_user", "waiting_confirmation", "needs_reconciliation"].includes(run.status);
export const resumable = (run: Run | null) => !!run && ["failed", "cancelled", "needs_reconciliation"].includes(run.status) && !(run.interaction && run.interaction.kind !== "reconciliation" && ["cancelled", "expired", "rejected"].includes(run.interaction.status));
