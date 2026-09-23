# How should durable Agent work be recovered or cancelled?

## Problems solved

- Keeps long-running Agent work recoverable and auditable across request boundaries, tool failures, cancellation, and operator intervention.

## Business scenarios

- A portfolio analysis resumes from a durable checkpoint after a model or tool outage.
- An operator cancels or recovers background case triage without repeating an already proposed business effect.
- A response is lost after a tool effect was persisted, so recovery returns recorded evidence rather than calling the tool again.

## Use when

Use durable task state for background work, multi-turn conversations, retries after tool calls, operator inspection, or resumable execution.

## Do not use when

Do not create a durable task for a bounded synchronous answer with no tools, proposal, or continuation.

## How to use

Persist conversation/task identity, checkpoints, tool-call idempotency, cancellation state, evidence, and terminal outcome. Operators may inspect, cancel, resume, or reconcile only through published Agent operations.

## Adaptation cookbook

| Failure/recovery requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| A long analysis fails after two successful reads. | Durable checkpoint and resumable task | Resume from the last fenced checkpoint with the original principal/tool intersection and retain execution evidence. | Starting a new unrestricted task or silently discarding prior evidence. |
| An operator cancels case triage while a proposal is pending. | Governed cancellation | Stop future tool steps, preserve the proposal/evidence state, and ensure no unapproved Action runs. | Deleting the task row or treating cancellation as rollback of an already committed external effect. |
| A retry follows an uncertain provider or Action result. | Owner-specific idempotency and reconciliation | Reconcile through the Action/Integration owner before retrying and reuse the durable execution identity. | Blindly repeating a write or provider call from model reasoning. |

## Example

Before any tool call, a failed run may reacquire a fenced Lease and resume from the last Checkpoint under the original Task/Conversation identity, current authority intersection, and remaining Budget. After a tool call with uncertain outcome, it must query the Action/Provider idempotency receipt before retrying. If the tool result was durably recorded but the response was lost, recovery reads Execution Evidence and returns the original result. Cancellation fences future steps but does not roll back committed effects. Operator recovery requires separate permission, records who intervened, and cannot reset budget, authority, or business idempotency by starting an unrestricted task.

## Permissions and scope

Task owners see their own work; operator recovery requires separate operational permission. Resumption revalidates current authority instead of replaying stale credentials.

## Boundaries

Agent owns execution state. Workflow owns business process state, and Scheduler owns independent recurrence.
