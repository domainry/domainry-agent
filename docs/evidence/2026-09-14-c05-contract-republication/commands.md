# C05 original contract recovery and republication

Agent cwd: `/Users/tiger/Projects/domainry-agent`, current `go.work`. Runtime cwd: `/Users/tiger/Projects/domainry-runtime`, `GOWORK=/tmp/runtime-work/go.work`. Frontend commands run in Agent `frontend`, with the bundled Node directory on PATH.

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./remote ./server ./internal/transport/http/module ./internal/capability github.com/domainry/domainry-agent-sdk github.com/domainry/domainry-agent-sdk/persistence
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^TestLegacyMixedOldRoleAgreementExplicitRepublishingThroughRealHTTP$' -count=1 -v
npm test
npm run build
AGENT_CONTRACT_PUBLICATION_BROWSER=1 AGENT_NODE_BINARY=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright AGENT_UI_TEST_OUTPUT=/tmp/c05-contract-browser-accepted go test ./internal/assembly/web -run '^TestSourceReleaseHTTPAgentDispatchMessageAndAutomaticDeliveryAcrossSubjects$' -count=1 -v
```

`regression.log` covers eight packages, including the new SaaS contract index/preview/audit forwarding case. `race.log` retains the preceding goal continuation's selected race cases in application and store, including exact current-role source access, contract preview/commit/current authorization, safe recovery metadata, immutable admission/run evidence, exact retries and original assignment authority. The original log lists each executed case; the original race shell invocation was not retained. No source change in those packages followed this race pass.

Other logs: `runtime.log`, `frontend-tests.log`, `frontend-build.log`, `browser.log`. Frontend has 75 tests. The production build passed before the two final state-test additions; those additions do not change the built client. Source SHA inventory includes the preceding phase's relevant paths plus modified/untracked Go and frontend sources across Agent/SDK/Runtime at this acceptance point; it is an inventory, not a claim of authorship for all workspace changes. Frontend dist SHA is recorded separately. Workspace files are copied.

The Runtime scenario uses actual old-role report and analysis results (both 70), actual model tool delegation and two required roots, complete original paging, automatic delivery and human acceptance. Offline ORM conversion removes only the old agreement Requirements snapshot marker and existing publication identity fields. Original immutable admission/calls/runs remain. It checks exact legacy recovery, version-only index, prospective preview without grant, current field denial, deliberate publication under the actual current role, original accepted task/delivery/decision/history preservation, exact retry, audit, reopening, publisher/original-provider withdrawal and original-page checks. SQLite and deterministic model wire only.

Browser evidence uses real Identity/Agent HTTP, actual automatic clock delivery and accepted original agreement. A route fetch actually commits the publication and then loses the response; reloading retries the identical original request ID/CAS/digest, yielding one audit. Current Share withdrawal hides the widget and directly denies the preview endpoint; restoration and closing clear previews. Desktop and 390px screenshots were inspected, and browser errors are empty. The browser uses current-format database records; legacy recovery is verified separately in store and Runtime.

Excluded failures: a browser invocation used `AGENT_UI_EVIDENCE_DIR` instead of the script's required `AGENT_UI_TEST_OUTPUT`; earlier Runtime fixture checks retained another role when selecting the original role, expected access to a never-published original role prefix, or submitted an acceptance CAS captured before task completion. The final scenario explicitly checks original-role selection and reloads the relationship after completed task observation. None of these earlier failed runs is counted as acceptance.

Production DML and schema migration 29 use ORM builders and the host migration ledger. C05 remains partial: whole missing original agreement history is not synthesized; complete historical admission flows, multiple owners/dependencies, further professional adapters, broader participant scopes, full execution-detail sharing, cross-subject transfer/lifecycle, ordinary management controls on the recovery UI and all remaining TODO items remain pending.
