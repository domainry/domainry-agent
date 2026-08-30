# Development TODO

Agent session ID: agent-ownership-migration-20260830

Batch: Agent definition and runtime-state ownership migration

Requirement: Move Agent definitions, execution aggregates, repositories, persistence, and source-owned migrations from Runtime into domainry-agent while preserving host workflow/authorization ownership.

Project mode: existing

Execution scope: implementation

Frontend mutation scope: backend_only

Current stage: done

## requirements

- [x] Scope, actors, roles, permissions, data scope, frontend ownership, and out-of-scope behavior are explicit
  - Evidence: `docs/backend-requirements-prd.md` sections 1, 3, 5
- [x] `frontend_not_present` is recorded only when `frontend/src` contains no regular source file
  - Evidence: repository has no `frontend/src`; task is backend module extraction
- [x] Objects, identifiers, relations, lifecycle states, invariants, retention, and ownership are settled
  - Evidence: PRD AGENT-OWN-001 through AGENT-OWN-006 and Agent SDK `state/`, `repository/`
- [x] Actions, transactions, rollback, concurrency, idempotency, external effects, and terminal outcomes are settled
  - Evidence: PRD sections 3 and 4; fencing/idempotency repository contracts
- [x] Calculations, settlement, units, precision, rounding, history, corrections, reports, export, and pagination semantics are settled where applicable
  - Evidence: not applicable except Agent-owned report state lifecycle, covered by `AgentLifecycleRepository`
- [x] Schedules, automation, notifications, files, and integrations are settled where applicable
  - Evidence: provider integration remains behind Agent runners; no new schedule/file ownership
- [x] Every accepted requirement has a stable ID, source, owner, acceptance result, and traceability row; no business blocker remains hidden
  - Evidence: PRD requirements and traceability tables
- [x] `docs/backend-requirements-prd.md` is complete and internally consistent
  - Evidence: confirmed PRD

### Requirement decisions and findings

| ID | Owner | Decision or finding | State (`open` / `resolved`) | Evidence / resolution |
| --- | --- | --- | --- | --- |

## model

- [x] Accepted requirements map to explicit owned model and source contracts without changing business truth
  - Evidence: SDK definitions, state aggregates and repositories; Agent persistence schema
- [x] Only required capability categories and references were loaded
  - Evidence: infrastructure-library migration; no unrelated capability metadata used
- [x] Project-owned model metadata is complete; generated files were not edited
  - Evidence: source-owned SDK/Agent Go contracts and PRD; no generated source edited
- [x] Model validation and plan pass
  - Evidence: Agent SDK `go test ./...`
- [x] Plan review accounts for destructive or evolution-sensitive changes
  - Evidence: table names/data preserved; ownership and migration registrar changed without destructive recreation

### Model findings

| ID | Owner | Finding | State (`open` / `resolved`) | Evidence / resolution |
| --- | --- | --- | --- | --- |

## apply

- [x] Reviewed model is applied or explicitly classified as source-owned library evolution
  - Evidence: Agent SDK, Module persistence, Runtime adapters and SaaS repository transport implemented
- [x] Only project-owned backend source and tests are implemented; generated source and CLI-owned state remain untouched
  - Evidence: current diffs
- [x] Transaction, authorization, idempotency, external-effect, and error semantics match the accepted requirements
  - Evidence: `remote/publication_store.go` enqueues in the host transaction and relays idempotently; integration test proves rollback suppression, retry, and eventual delivery
- [x] Finalization checks pass after focused tests
  - Evidence: `git diff --check`; Agent and SDK full tests; Runtime Agent-focused persistence and bootstrap tests
- [x] Focused tests and the affected backend test set pass
  - Evidence: `go test ./...` in Agent and SDK; focused Runtime workflow, lifecycle, schema, appschema, bootstrap, and boundary packages
- [x] No apply/source finding remains open
  - Evidence: APPLY-001 resolved by durable SaaS task publication outbox

### Apply findings

| ID | Owner | Finding | State (`open` / `resolved`) | Evidence / resolution |
| --- | --- | --- | --- | --- |
| APPLY-001 | Agent SaaS adapter | Host workflow transaction cannot atomically mutate a remote Agent DB; add source-owned durable publication outbox and relay instead of HTTP inside the transaction. | resolved | `remote/publication_store.go`; `TestSaaSBindingPersistsDefinitionsStateAndWorkerRunsRemotely` proves rollback produces no publication, a transient 503 is retried, and the committed mutation reaches Agent SaaS exactly through the durable outbox. |

## verify

- [x] Structural verification passes for current source
  - Evidence: Agent and SDK `go test ./...`; Runtime Agent-focused boundary, persistence, bootstrap, composition, HTTP, and integration tests
- [x] Module topology uses the host database and sole host `_schema_migrations` ledger
  - Evidence: `module.Factory.OpenModule` submits owner `agent`; Runtime composition test uses `ApplyOwnedMigrations`; legacy-index adoption test passes
- [x] SaaS topology owns an isolated Agent database
  - Evidence: authenticated repository integration test uses a separate Agent DB and a host-only durable publication outbox
- [x] Ownership boundary tests prove Runtime no longer declares Agent-owned definitions, aggregates, repositories, stores, or DDL
  - Evidence: `agent_sdk_ownership_boundary_test.go`, module definition ownership tests, and zero production matches for obsolete Runtime Agent repository/store/table paths
- [x] Module and SaaS lifecycle, idempotency, concurrency, restart, and workspace isolation tests pass
  - Evidence: Agent full suite; Runtime workflow/lifecycle suites; SaaS rollback/retry/delivery integration; `TestTwoRuntimeInstancesShareAgentSessionAndReportHTTPState`
- [x] Temporary resources are cleaned and no verification finding remains open
  - Evidence: test-owned temporary databases; `git diff --check` in all three repositories

### Verify findings

| ID | Owner | Finding | State (`open` / `resolved`) | Evidence / resolution |
| --- | --- | --- | --- | --- |
| VERIFY-001 | Runtime owners outside Agent | Full Runtime suite is not green because concurrent Report, Scheduler, View, and Identity Profile Binding extraction is incomplete. | resolved | Classified outside this Agent ownership migration after Agent-focused suites and the isolated multi-instance Agent restart test passed; representative failures are missing `report_snapshots`, unsupported `scheduler`/`identity_profile_binding`, and unavailable report modules. |

## done

- [x] `Current stage: done` is set and all preceding in-scope items have current evidence
  - Evidence: this TODO and linked source/tests
- [x] TODO consistency check passes with `--expected-stage done`
  - Evidence: `domainry-development-todo-check.py --expected-stage done`
- [x] Final report states delivery identity, changed files, tests, Runtime result, highest evidence level, and explicit limitations without overstating completion
  - Evidence: final handoff for this session
