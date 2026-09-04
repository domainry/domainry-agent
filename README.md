# domainry-agent

Source-owned Agent Runner / AI Gateway implementation for Domainry.

Public Agent/Skill/Task/Context/Routing/Runner contracts are owned by
`domainry-agent-sdk`. This repository owns validation and execution of those
contracts; Runtime does not redeclare them.

- `module`: in-process Binding selected by project composition.
- `remote`: SaaS Binding selected by project composition.
- `server`: authenticated SaaS protocol endpoint with Descriptor handshake.
- `cmd/domainry-agent`: standalone SaaS process entrypoint.
- `internal/application`: Agent-owned dialog and execution-state use cases.
- `internal/composition`: the shared Module/SaaS Agent application and HTTP Adapter assembly.
- `internal/provider`: provider HTTP protocol, result normalization and stable error classification.
- `internal/transport/http/module`: source-owned product HTTP Adapter shared by Module and SaaS bindings.
- `definition`: Agent-owned validation before provider translation.
- `internal/infrastructure/persistence/base`: engine-neutral database foundation.
- `internal/infrastructure/persistence/{mysql,postgres,sqlite}`: database engine profiles.
- `internal/infrastructure/persistence/database/agent`: Agent-owned definition, runtime-state, task-run, interactive-run, worker and lifecycle repositories.

This module owns Agent definitions and provider-execution state, including task
claim/lease/fencing persistence. In Module mode it borrows the host database and
registers source-owned migrations in the host's sole `_schema_migrations`
ledger. In SaaS mode it persists the same model in an isolated Agent database.
Runtime workflow transactions never write Agent state; the former remote task
mutation/outbox bridge has been removed.

Agent owns conversations, sessions, proposals, approval decisions, task and
interactive state machines, tool-call ledger and Agent workers. Runtime retains
Workflow state, current business authorization and the concrete Record/Action/
Workflow/Report effects exposed through narrow Host Ports.

Both bindings expose the same `dialog.state` and `execution.state`
capabilities and the same Agent-owned HTTP Adapter. All `/agent/*` handlers,
route metadata and OpenAPI operations come from Agent; Runtime mounts the Adapter and applies host middleware without
duplicating Agent handlers. Host authorization, audit and business effects are
requested internally through `InteractiveHost`, `TaskHost`, `ProposalHost`,
`AuditHost` and `AnalysisHost`.

A Runtime Workflow Agent node calls `TaskRunner.Start` and receives `accepted`.
The later Agent worker runs outside that call stack, commits Agent terminal state
first, and then sends an idempotent `CompleteWorkflowTask` callback to Runtime.
Runtime commits only Workflow state and never calls Agent while handling that
callback, so the two capability directions do not form synchronous reentry.
