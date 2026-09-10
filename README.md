# domainry-agent

Source-owned Agent Runner / AI Gateway implementation for Domainry.

Public Agent/Skill/Task/Context/Routing/Runner contracts are owned by
`domainry-agent-sdk`. This repository owns validation and execution of those
contracts; Runtime does not redeclare them.

Persistent personal conversations are a separate `conversation.v1` capability,
independent of one-shot Interactive routing. See [会话接口、存储和配置](docs/conversations.md)
for durable messages, explicit user memory, automatic compaction, cancellation,
restart recovery, persisted streaming drafts and Gateway / compatible model protocols.
The optional `conversation.execution.v1` port adds durable model/tool iterations.
The Identity web host wires permission-scoped time, calculation, history, memory,
personal todo and user-question tools; see [阶段实现和验收范围](docs/testing-2026-09-10-tools.md).
Optional [文档检索](docs/knowledge.md) exposes permission-scoped search/read
tools when a tool-capable host is configured. Text-only deployments retain
pre-reply retrieval, including the bcri configuration.
Personal and shared libraries have separate membership management. Optional
host bindings connect each library to a dedicated remote KB, exposing a library
catalog and scoped search/read with live membership and Identity checks. See
[资料库权限与当前边界](docs/knowledge-permissions.md). Explicit managed bindings
now expose document upload, status, download and deletion via Module/SaaS APIs,
with durable indexing jobs and document-level filtering. The library dialog now
supports file selection, indexing status, verified original downloads and deletion.
Browser recovery, Identity revocation, host restart, citations and mobile deletion
have passed with the actual host and protocol fixtures; see
[文档网页验收](docs/testing-2026-09-10-library-documents-web.md).
Explicit [附件另存](docs/testing-2026-09-10-attachment-library-copy.md) now creates an independent library copy after checking source and target permissions.
Real multi-library acceptance, remote source setup and cross-library moves remain unfinished.
Conversation-only SaaS deployments can start without an Interactive/Task provider.

后续能力与交付顺序见 [Agent 日常工作能力 TODO](docs/agent-capabilities-todo.md)。

- `module`: in-process Binding selected by project composition.
- `remote`: SaaS Binding selected by project composition.
- `server`: authenticated SaaS protocol endpoint with Descriptor handshake.
- `cmd/domainry-agent`: standalone SaaS process entrypoint.
- `cmd/domainry-agent-web`: browser host embedding Identity and Agent modules; see [Identity Module 接入与启动](docs/identity-module.md).
- 外部账号登录：见 [Agent 复用已有账号与个人 Workspace](docs/external-account-login.md)。
- `internal/application`: Agent-owned dialog and execution-state use cases.
- `internal/composition`: the shared Module/SaaS Agent application and HTTP Adapter assembly.
- `internal/infrastructure/provider`: provider HTTP protocol, result normalization and stable error classification.
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

When business execution is configured, Module/SaaS bindings expose `dialog.state`
and `execution.state` through the same Agent-owned HTTP Adapter. Conversation-only
SaaS bindings expose the separate conversations Adapter and advertise their available capabilities. All `/agent/*` handlers,
route metadata and OpenAPI operations come from Agent; Runtime mounts the Adapter and applies host middleware without
duplicating Agent handlers. Host authorization, audit and business effects are
requested internally through `InteractiveHost`, `TaskHost`, `ProposalHost`,
`AuditHost` and `AnalysisHost`.

A Runtime Workflow Agent node calls `TaskRunner.Start` and receives `accepted`.
The later Agent worker runs outside that call stack, commits Agent terminal state
first, and then sends an idempotent `CompleteWorkflowTask` callback to Runtime.
Runtime commits only Workflow state and never calls Agent while handling that
callback, so the two capability directions do not form synchronous reentry.

本地浏览器对话验收：见 [AI Elements 对话页面](frontend/README.md)，构建前端后运行 `go run ./cmd/domainry-agent-playground`，默认打开 http://127.0.0.1:8090 。
