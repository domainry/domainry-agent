# When must an Agent proposal wait for human approval?

## Problems solved

- Separates model-generated recommendations from authoritative business writes so approval remains explicit, attributable, and revocable.

## Business scenarios

- An Agent drafts a customer follow-up action that a manager must accept before the customer Handler executes it.
- A risk Agent proposes a refund, account restriction, or remediation plan without directly applying the change.
- Approved proposal data may become stale before execution, so the target Operation rechecks state, version, permission, and business invariants.
- Replaying one accepted proposal returns the original execution evidence rather than applying the write twice.

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

Agent proposal `credit-limit-change:p-88` recommends raising customer `c-42` to 100,000 and stores source evidence, target Operation, typed input, expiry, and proposal identity. An authorized manager accepts it through the proposal-decision contract; execution then re-resolves current customer state, version, row scope, and `customer.credit_limit_change` permission. If the customer became suspended or its version changed, the Operation refuses or requires a new proposal—approval does not freeze an obsolete world. Repeating the same accepted proposal reuses its execution identity and returns the linked Operation result, approver, and evidence instead of a second write. Read-only advice needs no Proposal; deterministic policy stays in code.

## Permissions and scope

The approver needs the exact proposal-decision permission and the target operation permission. Approval never expands row scope beyond what the target operation authorizes.

## Boundaries

Agent owns proposal state; the business owner owns mutation semantics; Audit owns durable decision/effect evidence.
