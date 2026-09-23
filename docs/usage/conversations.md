# When should work use a persistent Agent conversation?

## Problems solved

- Keeps model dialogue, explicit memory, run state, confirmations, forks, and resumable evidence inside one owner-scoped conversation instead of scattering them across browser state or business Objects.

## Business scenarios

- A support copilot continues a customer investigation across several sessions while retaining only explicitly saved personal memory.
- An analyst forks a completed conversation at a stable boundary to compare a second approach without replaying earlier tool effects.
- A reimbursement assistant collects missing facts over several turns but does not treat conversation completeness as a submitted reimbursement.
- A confirmed proposal becomes a governed task/effect only after explicit confirmation and fresh authorization.

## Use when

Use a persistent conversation when natural-language work spans turns, needs resumable runs or confirmations, or must retain a user-owned history.

## Do not use when

Do not use a conversation as a Workflow engine, shared business record, audit ledger, or substitute for a deterministic Operation.

## How to use

Bind the conversation to the authenticated principal and Workspace, append user/model/tool events through published operations, keep attempt drafts out of durable history until accepted, and fork only from a stable completed boundary.

## Adaptation cookbook

| Conversation requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| A support copilot continues an investigation tomorrow | Persistent personal conversation | Reopen the same owner-scoped conversation, rebuild bounded context from durable messages and explicit memory, and revalidate current tool authority | Storing the transcript only in browser local storage or copying it into a customer record |
| A tool call needs user confirmation | Conversation confirmation state | Persist the exact proposed tool, typed arguments, authority snapshot, and expiry; execute only after a valid confirmation and fresh authorization | Treating a later model sentence such as “confirmed” as user approval |
| An analyst wants to compare an alternative approach | Conversation trajectory fork | Fork from a stable completed run boundary and start a new execution identity without replaying prior effects | Cloning an in-flight attempt or replaying historical writes to reconstruct context |
| The product only needs one deterministic eligibility check | Business Operation | Execute typed code with no model conversation lifecycle | Creating an Agent session for a fixed rule |

## Example

A reimbursement conversation collects amount, date, receipt attachment, and cost center over several turns. Until the user explicitly confirms the typed proposal, it remains conversation state and no reimbursement record exists. Confirmation creates the governed Task/Operation under fresh authority. An analyst may fork a completed boundary to compare a second approach: the fork copies only visible context, receives a new execution identity, and never replays prior effects. Trajectory records model/tool execution and evidence; it is not interchangeable with the user-visible message history or Workflow state.

## Permissions and scope

Conversation list/read/write/fork operations are owner-scoped. Resuming or forking rechecks current Identity and tool permissions; history never carries stale credentials forward.

## Boundaries

Agent owns conversational state and trajectories. Business owners own records and effects; Audit owns immutable evidence required outside the personal conversation history.
