# Agent Ownership Migration PRD

Project: `domainry-agent`

Requirement authority: explicit user decision on 2026-08-30; Runtime repository code and tests

Batch: light existing-project ownership migration

Status: confirmed

## 1. Scope and ownership

`domainry-agent` owns Agent definitions, product HTTP use cases, proposal/approval lifecycle, provider execution state and workers in both Module and SaaS topologies. Runtime owns Workflow state, current business authorization, concrete host business effects, and the database/dialect/migration facilities exposed to an embedded Module.

No frontend source is present or in scope.

## 2. Requirements

| ID | Requirement | Owner | Acceptance evidence |
| --- | --- | --- | --- |
| AGENT-OWN-001 | Public Agent, Skill, task, route, context, handoff, and execution contracts have one declaration in `domainry-agent-sdk`. | Agent SDK | declaration-boundary tests and repository search |
| AGENT-OWN-002 | Agent task runs, interactive runs, dialog state, transitions, claims, leases, tool-call ledger, and provider-run correlation are Agent-owned aggregates/repositories. | Agent | package-boundary tests and Runtime consumer compilation |
| AGENT-OWN-003 | Embedded Module persistence uses the host database, SQL dialect, transaction boundary, migration lock, and sole `_schema_migrations` ledger through the host migration registrar. | Agent Module + Runtime host | migration/store integration tests |
| AGENT-OWN-004 | Agent SaaS persists the same Agent-owned state in its own database and never accesses the Runtime database. | Agent SaaS | restart/idempotency and isolation tests |
| AGENT-OWN-005 | Persistence DDL/DML uses `github.com/domainry/domainry-orm`; raw SQL is allowed only where the ORM lacks an equivalent and carries local justification plus dialect tests. | Agent | dependency and dialect tests |
| AGENT-OWN-006 | Runtime retains Workflow state, current authorization, concrete host effects, Host adapters and composition only; it must not redeclare or persist Agent-owned state. | Runtime | ownership-boundary tests and full Runtime tests |
| AGENT-OWN-007 | Every `/agent-dialog/*` and `/operations/agent/tasks*` product endpoint, route declaration and OpenAPI operation is owned by the Agent source Surface. Runtime only mounts and governs that Surface. | Agent + Runtime host | Surface contract, route-registration, route-absence and OpenAPI composition tests |
| AGENT-OWN-008 | Workflow dispatch and completion use an asynchronous state-first protocol: Runtime receives `accepted`, Agent later commits terminal state then invokes an idempotent Runtime callback; neither side performs synchronous reentry or a cross-owner transaction. | Agent + Runtime Workflow | deterministic-correlation, callback replay and ownership tests |

## 3. State and invariants

Agent task and interactive-run identifiers remain opaque and workspace-scoped. Start is idempotent within the application/workspace namespace. State transitions use optimistic revision or lease fencing; terminal states cannot regress. Tool-call attempts are replay-safe. Module/SaaS topology switching must not allow both bindings to accept the same idempotency namespace concurrently.

## 4. Transaction and failure behavior

Each state transition and its Agent-owned evidence commits atomically in the selected Agent repository. Runtime business effects remain behind Host Ports and are not committed inside an Agent database transaction. Runtime Workflow transactions contain only Workflow state; they neither insert Agent tasks nor publish Agent task mutations. Deterministic task identity makes a retried dispatch replay the same Agent Start. Agent commits terminal state before the idempotent Workflow completion callback and retries an unacknowledged callback through reconciliation. Cancellation is idempotent. Lost responses and uncertain provider starts reconcile through durable provider-run correlation rather than issuing an unguarded second start.

## 5. Security and data scope

Every persisted row is scoped by application/runtime and workspace where applicable. Runtime supplies authenticated authorization/context evidence; Agent stores only the evidence necessary to execute and audit the run. SaaS credentials and provider execution data never enter the Runtime database.

## 6. Decisions

| ID | Class | Decision | Status |
| --- | --- | --- | --- |
| D-001 | confirmed | Agent definition and execution state ownership moves to Agent source. | resolved |
| D-002 | safe_implementation_decision | Preserve existing table data and names during ownership transfer; change migration ownership without destructive recreation. | resolved |
| D-003 | confirmed | A database has exactly one host-owned `_schema_migrations` ledger. | resolved |
| D-004 | confirmed | Runtime→Agent Start and later Agent→Runtime Host callbacks are separate call stacks; cross-owner atomicity uses idempotency and reconciliation, not shared mutation/outbox transactions. | resolved |

## 7. Traceability

| Requirement | Current evidence | Target source | Verification |
| --- | --- | --- | --- |
| AGENT-OWN-001 | Runtime consumers and former Runtime definition model | `domainry-agent-sdk` | SDK and Runtime boundary tests |
| AGENT-OWN-002 | former Runtime task/interactive/session/proposal state machines and stores | `domainry-agent/internal/application` + Agent-owned repositories; SDK `persistence.ExecutionStateBinding` | Agent state lifecycle tests, Binding contract tests and Runtime ownership search |
| AGENT-OWN-003 | Runtime module-host precedents for Scheduler, Notification, Party, and Data Exchange | Agent SDK module host + Agent Module migrations | host-ledger integration test |
| AGENT-OWN-004 | former stateless `domainry-agent/server` baseline | Agent SaaS assembly/persistence | restart and isolation test |
| AGENT-OWN-005 | existing Runtime Agent stores use ORM builders | Agent persistence | dialect tests and source search |
| AGENT-OWN-006 | Runtime Workflow, authorization, audit and business-effect services | `runtime/application/agenthost` plus composition; no Agent repository mutation in Runtime application | Runtime Host adapter tests and architecture boundary search |
| AGENT-OWN-007 | former Runtime session/proposal/interactive/task handlers | `domainry-agent/internal/transport/http/module`; Runtime host Surface mounting | Agent Surface tests, Runtime route absence test and composed OpenAPI test |
| AGENT-OWN-008 | former shared task mutation transaction and SaaS publication outbox | SDK `TaskRunner`/`TaskHost`, Agent task worker, Runtime Workflow dispatch/completion | callback replay, deterministic identity and repository-boundary tests |

## 8. Accepted verification flows

1. Start the same task twice in Module mode and observe one durable Agent run in the borrowed host database.
2. Claim and transition a run with fencing; a stale owner cannot overwrite the current revision.
3. Restart Agent SaaS and poll the same run to its prior durable state without Runtime database access.
4. Execute an interactive run and task handoff atomically, preserving workspace isolation.
5. Apply Agent Module migrations and observe entries only in the host `_schema_migrations` ledger with Agent source ownership.
6. Start a Workflow Agent node, observe Runtime commit only `waiting` Workflow state, then observe Agent terminal commit followed by one idempotent Runtime completion.
7. Fail the first completion callback and verify Agent reconciliation replays it without rerunning the provider or duplicating Workflow continuation.
