# Historical agreement and execution identity

Agent cwd: `/Users/tiger/Projects/domainry-agent`, current `go.work`. Runtime cwd: `/Users/tiger/Projects/domainry-runtime`, `GOWORK=/tmp/runtime-work/go.work`.

```sh
go test -overlay=/tmp/c05-peer-authority-before-overlay/overlay.json ./internal/infrastructure/persistence/database/agent -run '^TestDelegationRequirementInboxLaunchPreservesAcceptedExecutionIdentity$' -count=1 -v
go test ./internal/infrastructure/persistence/database/agent -run '^TestDelegationRequirementInboxLaunchPreservesAcceptedExecutionIdentity$' -count=1 -v
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./remote ./server ./internal/transport/http/module ./internal/capability github.com/domainry/domainry-agent-sdk github.com/domainry/domainry-agent-sdk/persistence -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent -run 'TestHistoricalContract|TestHistoricalRun|TestSameIdentityDelegationControls|TestDelegationRequirementInboxLaunch|TestContractPublication|TestLegacyContract|TestDelegationSourceReads|TestDelegationResumeChecks|TestLegacyAssignmentFallback|TestResultPreview|TestResultLocator|TestSharedSourceTraversal|TestSourceMemo|TestDeliveredWrappers' -count=1 -v
go test ./internal/assembly/web -run '^TestPeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery$' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^TestLegacyAgreementUpdateRestartResumeAndOriginalHistoryThroughRealHTTP$' -count=1 -v
git diff --check
```

`before.log` is the expected failure using the same valid fixture and tests with three pre-fix source files from HEAD and the exact pre-fix requirement-notice query. The four overlay files are retained in `before-overlay`; this comparison does not mutate the workspace. Absolute paths in `overlay.json` identify the original temporary execution locations.

`runtime-before.log` retains the failed full lifecycle before the inbox routing fix, including the actual failed peer-triggered revised run and its immutable snapshot. It is diagnostic evidence, not acceptance.

`regression-before.log` retains the broader eight-package run with seven passing packages and the web collaboration scenario failing from context size. The exact scenario also fails with the pre-routing overlay (`web-before.log`). `web-trace.log` uses the retained `context-trace-overlay` to print byte counts, roles and result sizes; it does not change authorization, page bodies or the limit. `web-locator-intermediate.log` passed after the minimal result locator fix, before the final worker scope change. Final `web.log` is the same complete HTTP scenario on the final source.

`runtime-source-denial.log` uses `source-denial-trace-overlay` and shows the report query execution check for the result reader. `runtime-scope-trace.log` uses `scope-trace-overlay` and confirms that the final flattened source check lacked contract scope, denying `analysis_run` before the first model call. The diagnostic prints are in overlay copies only; production source contains no temporary trace statements.

`runtime-after-routing-before-scope.log` retains the full lifecycle failure before the worker scope fix. `runtime-after-scope-short-wait.log` retains the next run: the original execution role successfully read both receipts, but the fixture's 60-second wait expired while the task was still running. The fixture now waits for the actual saved task timeout (180 seconds in this scenario); the accepted execution budget is unchanged. `source-before-budget-wait-sha256.json` retains that prior source snapshot. Final `runtime.log` is produced by the same command with this corrected fixture wait.

`runtime-before-source-memoization.log` then reached the actual 180-second execution timeout after reading both receipts and the revised agreement; delivery had been submitted but its tool response became uncertain. Final source scopes completed proofs within one read, separating every ledger and read boundary and retaining the original limit. `source-cache-disabled.log` isolates this change with a one-file overlay that only disables completed-proof sharing: three duplicate ledger reads are observed and the raw-private-access test still passes. The overlay is retained in `source-cache-disabled-overlay`; `source-before-source-memoization-sha256.json` identifies the prior full Runtime attempt. No execution budget was increased.

```sh
go test ./internal/application -run '^TestResult(Preview|Locator)' -count=1 -v
go test -overlay=/tmp/c05-source-cache-disabled-overlay/overlay.json ./internal/application -run '^Test(SharedSourceTraversalIsMemoizedOnlyWithinOneRead|SourceMemoCannotAuthorizeRawCollaborationAfterDeliveryProjection)$' -count=1 -v
go test ./internal/application -count=1
go test -overlay=/tmp/c05-peer-authority-before-overlay/overlay.json ./internal/assembly/web -run '^TestPeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery$' -count=1 -v
go test -overlay=/tmp/c05-peer-context-trace-overlay/overlay.json ./internal/assembly/web -run '^TestPeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery$' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test -overlay=/tmp/c05-source-denial-trace-overlay/overlay.json ./runtime/bootstrap/integrationtest -run '^TestLegacyAgreementUpdateRestartResumeAndOriginalHistoryThroughRealHTTP$' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test -overlay=/tmp/c05-source-scope-trace-overlay/overlay.json ./runtime/bootstrap/integrationtest -run '^TestLegacyAgreementUpdateRestartResumeAndOriginalHistoryThroughRealHTTP$' -count=1 -v
```

The final status is recorded in `acceptance.json`. No frontend source changes or additional browser/build acceptance are claimed for this phase.

The final malformed-boundary guard rejects JSON serialization failures instead of hashing empty bytes; it preserves the normal scope digest algorithm. The full eight core/SDK packages and selected race tests were rerun after this guard (`regression.log`, `race.log`, current `source-sha256.json`). The external Runtime and HTTP scenario binaries were already compiled before that error-path guard; their precise 128-file source snapshot is `source-runtime-compiled-sha256.json`. They validate the complete normal lifecycle and scoped memoization, while the new negative case validates malformed-boundary rejection in the current application package.
