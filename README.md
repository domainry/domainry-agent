# domainry-agent

Source-owned Agent Runner / AI Gateway implementation for Domainry.

Public Agent/Skill/Task/Context/Routing/Runner contracts are owned by
`domainry-agent-sdk`. This repository owns validation and execution of those
contracts; Runtime does not redeclare them.

- `module`: in-process Binding selected by project composition.
- `remote`: SaaS Binding selected by project composition.
- `server`: authenticated SaaS protocol endpoint with Descriptor handshake.
- `cmd/domainry-agent`: standalone SaaS process entrypoint.
- `internal/provider`: provider HTTP protocol, result normalization and stable error classification.
- `definition`: Agent-owned validation before provider translation.
- `persistence`: Agent-owned definition, runtime-state, task-run, interactive-run, worker and lifecycle repositories.

This module owns Agent definitions and provider-execution state, including task
claim/lease/fencing persistence. In Module mode it borrows the host database and
registers source-owned migrations in the host's sole `_schema_migrations`
ledger. In SaaS mode it persists the same model in an isolated Agent database;
host workflow transactions publish remote task mutations through a durable
outbox.

Runtime retains workflow orchestration, authorization, approvals, business
terminal commits and Runtime tool execution.
