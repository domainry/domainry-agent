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

Bind the conversation to the authenticated principal and Workspace, append user/model/tool events through published operations, and fork only from a stable completed boundary. Every public model generation has a stable durable message ID from its first text delta. Subsequent tools, model steps, retries and refreshes must not erase or overwrite earlier response text. Interrupted generations remain visible with an explicit incomplete status; internal reasoning, planning JSON and provider continuation state are not public messages. Context compaction changes only the model's input projection, never the stored transcript.

Interactive conversations use the streaming model/tool loop by default. Enable
`ConversationOptions.RunPlanningEnabled` only when a separate, persisted plan
justifies an extra model call before execution. Model and tool progress events
remain available without that upfront planner.

For an upfront plan, configure a separate text-only model with
`AGENT_CONVERSATION_PLANNING_PROVIDER`, `_PROTOCOL`, `_BASE_URL`, `_MODEL`,
`_MAX_OUTPUT_TOKENS` and, when supported, `_REASONING_EFFORTS`/`_REASONING_EFFORT`
(each suffix uses the same `AGENT_CONVERSATION_PLANNING` prefix). Set
`AGENT_CONVERSATION_PLANNING_MODEL_API_KEY` for a dedicated credential; gateway
models otherwise use `AGENT_PROVIDER_API_KEY`. Programmatic hosts can set
`Options.ConversationPlanningModelConfig` or supply
`ConversationOptions.RunPlanningModel`. The planner receives the authorized
context and tool catalog, but only metadata for image/file attachments. Its
identity, protocol, reasoning effort and output ceiling never change execution's
model selection. A persisted plan is reused after tools, confirmation and resume.
`AGENT_CONVERSATION_PLANNING_TIMEOUT` bounds the complete planning/retry sequence
(default 10 seconds); timeout fails with `run_plan_timeout` before any business
effect, never by silently executing without a valid plan.

Set `AGENT_CONVERSATION_MAX_OUTPUT_TOKENS` for the conversation model and
`AGENT_TASK_MODEL_MAX_OUTPUT_TOKENS` for the separate task model. Both are
positive integer ceilings, not target answer lengths; an omitted value uses
4096. For reasoning models the provider can count hidden reasoning against this
ceiling, so size it from observed usage, not visible text alone. A provider
`length`/`max_tokens` termination is `model_output_truncated`, never a completed
answer or an executable partial tool call. Retrying with the same exhausted
budget is not an automatic recovery.

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
