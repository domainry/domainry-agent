# Current object projection during source authorization

Runtime cwd: `/Users/tiger/Projects/domainry-runtime`; all Runtime Go commands use `GOWORK=/tmp/runtime-work/go.work`. No branch or checkout change.

```sh
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/composition -run '^TestRecordPolicyUsesCurrentObjectsWithoutBuildingWholeSchema$' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/composition ./runtime/domain/appschema/service ./runtime/domain/record/service ./runtime/modulehost/report ./runtime/application/agenthost -count=1
GOWORK=/tmp/runtime-work/go.work go test -race ./runtime/bootstrap/composition ./runtime/domain/appschema/service ./runtime/domain/record/service -run 'TestRecordPolicyUsesCurrentObjects|TestMetadataProjection|TestBuildSchemaSnapshot|TestSchemaSnapshotHash|Test.*SDK.*|Test.*Scope.*' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^TestLegacyAgreementUpdateRestartResumeAndOriginalHistoryThroughRealHTTP$' -count=1 -v
git diff --check
```

`before.log` was produced by the first command before the source edit, using the same valid test fixture. The failure observes one complete schema build for an object-only policy read. `runtime-core.log` and `race.log` cover the current source. `runtime.log` is the terminal full real-HTTP Runtime scenario once complete. The precise 132-file source snapshot includes the current malformed-read-boundary guard and the Runtime object projection.

The preceding full Runtime timeout remains in `../2026-09-14-c05-historical-contract-lifecycle/runtime.log`. No frontend build/browser or online model acceptance is claimed in this phase. Status in `acceptance.json` is authoritative.

```sh
go test ./internal/infrastructure/persistence/database/agent -run '^TestSameIdentityDeliveryPersistsOwnRolePublisherForLaterReaders$' -count=1
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./remote ./server ./internal/transport/http/module ./internal/capability github.com/domainry/domainry-agent-sdk github.com/domainry/domainry-agent-sdk/persistence -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent -run 'TestSameIdentityDelivery|TestOwnRoleDelivery|TestSourceRelease|TestHistoricalContract|TestHistoricalRun|TestSameIdentityDelegationControls|TestDelegationRequirementInboxLaunch|TestContractPublication|TestLegacyContract|TestDelegationSourceReads|TestDelegationResumeChecks|TestLegacyAssignmentFallback|TestResultPreview|TestResultLocator|TestSharedSourceTraversal|TestSourceMemo|TestDeliveredWrappers' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test -overlay=/tmp/c05-delivery-view-trace-overlay/overlay.json ./runtime/bootstrap/integrationtest -run '^TestLegacyAgreementUpdateRestartResumeAndOriginalHistoryThroughRealHTTP$' -count=1 -v
```

`delivery-publisher-before.log` uses the valid same-identity launched-run fixture before the store fix and fails with no own-role verifier publication. `runtime-before-delivery-publisher.log` records the completed revised run and missing later-reader delivery. `runtime-delivery-denial-trace.log` prints projection denials from the retained diagnostic overlay only; no access rules are weakened and no temporary prints are added to production source. `source-before-delivery-publisher-sha256.json` identifies the preceding 132-file snapshot. Current `agent-core.log`, `agent-race.log` and final `runtime.log` validate the exact updated source snapshot. The Runtime fixture's failure diagnostic now exits immediately if the task has completed while the expected new delivery is absent; neither accepted execution budgets nor success assertions changed.
