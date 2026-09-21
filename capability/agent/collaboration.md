# How should one Agent delegate work to another Agent?

## Problems solved

- Makes peer delegation, independently hosted Agent execution, ordered messages, and source publication explicit and auditable without sharing hidden prompts, credentials, or ambient authority.

## Business scenarios

- A case-triage Agent delegates a versioned fraud-analysis task to a specialist Agent and waits for a typed outcome.
- An independently hosted partner Agent claims one admitted execution and reports ordered progress without gaining access to the source conversation.
- A delegated specialist times out, so the parent recovers the still-open responsibility or routes it to a human instead of declaring success from message delivery.

## Use when

Use Agent collaboration when independently configured Agents must exchange bounded work or an external Agent must execute an explicitly admitted assignment.

## Do not use when

Do not delegate merely to split deterministic code, bypass a Workflow approval, or share an entire private conversation with another principal.

## How to use

Publish a stable task/version and exact input contract, admit one execution, grant the target Agent only the required tools/data, exchange durable ordered messages, and publish only the source references explicitly approved for that delegation.

## Adaptation cookbook

| Delegation requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Triage delegates fraud analysis to a specialist | Peer Agent delegation | Bind the exact task version, typed case reference, deadline, and allowed tools; correlate messages and terminal outcome to one admitted execution | Sending an unstructured prompt plus the caller's full tool token |
| A partner-hosted Agent performs one assignment | External Agent claim protocol | Let the external principal discover and claim only admitted work, report ordered events, and close delivery with idempotent execution identity | Exposing the internal queue or allowing the partner to enumerate all conversations |
| Specialist needs evidence from the source conversation | Explicit source publication | Preview and approve the precise message/artifact/reference set, then publish immutable references with publisher audit | Sharing the entire transcript, personal memory, or unrelated attachments |
| Two steps are fixed and deterministic | Workflow or code composition | Keep exact branching and retry in deterministic orchestration | Introducing multiple Agents where no model specialization exists |

## Example

A triage Agent delegates `fraud.review@2` with one case ID, immutable evidence references, deadline, read-only transaction tool, and expected typed risk result. The specialist gets only the intersection of its authority and the admitted assignment; a published capability claim is discovery, not permission. Ordered messages and terminal evidence stay correlated to one execution. If the claim or delivery times out, the parent resumes ownership, applies bounded retry or routes to a human, and does not mark the case complete merely because a message was sent. A deterministic Handler or fixed Workflow stays with that owner rather than being fragmented into Agent collaboration.

## Permissions and scope

Delegation creation, external claim, source publication, message exchange, and delivery inspection are separate permissions. Effective authority is the intersection of both Agents, the delegated task, the principal, and current source authorization.

## Boundaries

Agent owns delegation state and delivery. Workflow owns business approval/process state, while the target Business Operation owns any resulting authoritative effect.
