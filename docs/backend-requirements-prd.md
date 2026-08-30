# Agent Ownership Migration PRD

Project: `domainry-agent`

Requirement authority: explicit user decision on 2026-08-30; Runtime repository code and tests

Batch: light existing-project ownership migration

Status: confirmed

## 1. Scope and ownership

`domainry-agent` owns Agent definitions and provider-execution runtime state in both Module and SaaS topologies. Runtime owns workflow orchestration, business authorization decisions, host worker admission, and the host database/transaction/migration facilities exposed to an embedded Module.

No frontend source is present or in scope.

## 2. Requirements

| ID | Requirement | Owner | Acceptance evidence |
| --- | --- | --- | --- |
| AGENT-OWN-001 | Public Agent, Skill, task, route, context, handoff, and execution contracts have one declaration in `domainry-agent-sdk`. | Agent SDK | declaration-boundary tests and repository search |
| AGENT-OWN-002 | Agent task runs, interactive runs, dialog state, transitions, claims, leases, tool-call ledger, and provider-run correlation are Agent-owned aggregates/repositories. | Agent | package-boundary tests and Runtime consumer compilation |
| AGENT-OWN-003 | Embedded Module persistence uses the host database, SQL dialect, transaction boundary, migration lock, and sole `_schema_migrations` ledger through the host migration registrar. | Agent Module + Runtime host | migration/store integration tests |
| AGENT-OWN-004 | Agent SaaS persists the same Agent-owned state in its own database and never accesses the Runtime database. | Agent SaaS | restart/idempotency and isolation tests |
| AGENT-OWN-005 | Persistence DDL/DML uses `github.com/domainry/domainry-orm`; raw SQL is allowed only where the ORM lacks an equivalent and carries local justification plus dialect tests. | Agent | dependency and dialect tests |
| AGENT-OWN-006 | Runtime retains authorization evidence, workflow terminal commit, host callbacks, and composition only; it must not redeclare or persist Agent-owned state. | Runtime | ownership-boundary tests and full Runtime tests |

## 3. State and invariants

Agent task and interactive-run identifiers remain opaque and workspace-scoped. Start is idempotent within the application/workspace namespace. State transitions use optimistic revision or lease fencing; terminal states cannot regress. Tool-call attempts are replay-safe. Module/SaaS topology switching must not allow both bindings to accept the same idempotency namespace concurrently.

## 4. Transaction and failure behavior

Each state transition and its Agent-owned evidence commits atomically in the selected Agent repository. Runtime business effects remain behind host callbacks and are not committed inside an Agent database transaction. Cancellation is idempotent. Lost responses and uncertain provider starts reconcile through durable provider-run correlation rather than issuing an unguarded second start.

## 5. Security and data scope

Every persisted row is scoped by application/runtime and workspace where applicable. Runtime supplies authenticated authorization/context evidence; Agent stores only the evidence necessary to execute and audit the run. SaaS credentials and provider execution data never enter the Runtime database.

## 6. Decisions

| ID | Class | Decision | Status |
| --- | --- | --- | --- |
| D-001 | confirmed | Agent definition and execution state ownership moves to Agent source. | resolved |
| D-002 | safe_implementation_decision | Preserve existing table data and names during ownership transfer; change migration ownership without destructive recreation. | resolved |
| D-003 | confirmed | A database has exactly one host-owned `_schema_migrations` ledger. | resolved |

## 7. Traceability

| Requirement | Current evidence | Target source | Verification |
| --- | --- | --- | --- |
| AGENT-OWN-001 | Runtime consumers and former Runtime definition model | `domainry-agent-sdk` | SDK and Runtime boundary tests |
| AGENT-OWN-002 | `runtime/domain/agent` and `runtime/infrastructure/persistence/database/agent` | `domainry-agent` | Agent repository/store tests |
| AGENT-OWN-003 | Runtime module-host precedents for Scheduler, Notification, Party, and Data Exchange | Agent SDK module host + Agent Module migrations | host-ledger integration test |
| AGENT-OWN-004 | former stateless `domainry-agent/server` baseline | Agent SaaS assembly/persistence | restart and isolation test |
| AGENT-OWN-005 | existing Runtime Agent stores use ORM builders | Agent persistence | dialect tests and source search |
| AGENT-OWN-006 | Runtime workflow and authorization application services | Runtime adapters/composition | full Runtime tests |

## 8. Accepted verification flows

1. Start the same task twice in Module mode and observe one durable Agent run in the borrowed host database.
2. Claim and transition a run with fencing; a stale owner cannot overwrite the current revision.
3. Restart Agent SaaS and poll the same run to its prior durable state without Runtime database access.
4. Execute an interactive run and task handoff atomically, preserving workspace isolation.
5. Apply Agent Module migrations and observe entries only in the host `_schema_migrations` ledger with Agent source ownership.
