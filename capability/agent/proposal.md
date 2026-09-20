# When must an Agent proposal wait for human approval?

## Problems solved

- Separates model-generated recommendations from authoritative business writes so approval remains explicit, attributable, and revocable.

## Business scenarios

- An Agent drafts a customer follow-up action that a manager must accept before the customer Handler executes it.
- A risk Agent proposes a refund, account restriction, or remediation plan without directly applying the change.

## Use when

Require approval when model reasoning proposes a business mutation, external delivery, or other consequential action.

## Do not use when

Do not add approval to read-only analysis. Do not let approval replace deterministic validation inside the target Handler.

## How to use

Persist the proposal, input evidence, intended operation, and expiry. An authorized human accepts or rejects it. Only acceptance may invoke the guarded target operation, which validates again.

## Adaptation cookbook

| Agent outcome | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Recommend a customer follow-up. | `proposal_only` Agent task | Return the declared Action key and typed input as a proposal; a manager explicitly accepts it before the Action's own authorization runs. | Treating model text such as “approved” as an approval decision. |
| Recommend a refund or account restriction. | Human approval plus exact Business Operation | Preserve reasoning and evidence, require an authorized reviewer, then execute the bounded Operation idempotently. | Giving the Agent a generic write tool or bypassing the Operation's validation. |
| Summarize a record with no effect. | `analysis_only` task | Publish typed read-only output and no allowed Action. | Creating approval state for a result that has no authoritative effect. |

## Example

An Agent drafts a customer follow-up action. The manager reviews the proposal, accepts it, and the customer-owned Handler performs the write. A model message saying “approved” is not an approval decision.

## Permissions and scope

The approver needs the exact proposal-decision permission and the target operation permission. Approval never expands row scope beyond what the target operation authorizes.

## Boundaries

Agent owns proposal state; the business owner owns mutation semantics; Audit owns durable decision/effect evidence.
