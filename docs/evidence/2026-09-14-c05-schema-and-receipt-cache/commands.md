# Commands and evidence

All Runtime commands ran from `/Users/tiger/Projects/domainry-runtime` with `GOWORK=/tmp/runtime-work/go.work`. Agent commands ran from `/Users/tiger/Projects/domainry-agent` with its checked-in `go.work`. The task fixture keeps `timeout_seconds: 90`; only the test observer waits up to five minutes to read the terminal state and post-completion authorization checks.

## Counterexamples

The sequence of retained race logs records the same real two-account business scenario before each optimization:

- `logs/c05-schema-projection-cache-owner-race.log`: schema provider generation cache was not yet used by the actual constructor; the delivery was absent at the 90-second task deadline.
- `logs/c05-schema-projection-cache-final-race.log`: actual constructor cache enabled; delivery persistence completed after about 30 seconds, then the task was interrupted.
- `logs/c05-schema-and-receipt-compaction-final-race.log`: direct receipt grouping alone did not group callers that supplied one condition at a time.
- `logs/c05-schema-and-assessment-compaction-final-race.log`: condition callers were grouped and `delegation_update` completed in 21,743 ms, but the following model step reached the unchanged 90-second deadline.
- `logs/c05-receipt-prefix-compaction-before.log` and `logs/c05-receipt-assessment-compaction-before.log`: focused red tests observe four repeated prefix audits where two source-run audits are sufficient.

## Focused and package tests

```sh
gofmt -w runtime/bootstrap/composition/appschema_snapshot_wiring.go runtime/bootstrap/composition/appschema_snapshot_cache_test.go
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/composition -run '^TestRecordSchemaSnapshotProvider(BuildsColdGenerationOnceUnderConcurrency|BuildsColdProjectionOnceUnderConcurrency|ReusesProjectionAcrossAccessRefresh|ReusesStableProjectionAndInvalidatesEveryMutation|DoesNotReuseExpiredAccessProjection|BoundsPrincipalsAndHandlesConcurrentPublication)$' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test -race ./runtime/bootstrap/composition -run '^TestRecordSchemaSnapshotProvider(BuildsColdGenerationOnceUnderConcurrency|BuildsColdProjectionOnceUnderConcurrency|ReusesProjectionAcrossAccessRefresh|ReusesStableProjectionAndInvalidatesEveryMutation|DoesNotReuseExpiredAccessProjection|BoundsPrincipalsAndHandlesConcurrentPublication)$' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test ./runtime/domain/appschema/service ./runtime/bootstrap/composition -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test -race ./runtime/domain/appschema/service ./runtime/bootstrap/composition -count=1 -v

go test ./internal/application -count=1 -v
go test -race ./internal/application -count=1 -v
```

The final Runtime package logs contain 85 passing top-level tests each. The final Agent package logs contain 141 passing top-level tests each. Focused receipt logs cover direct prefix compaction and condition-level caller compaction in normal and race modes.

## Actual owner and Agent chain

```sh
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^(TestBusinessIndependentReceiptReadThroughRealOwnerRPC|TestCrossUserBusinessAgentComplexFiltersOriginalCursorAutomaticDeliveryAndAcceptance)$' -count=1 -v -timeout=30m
GOWORK=/tmp/runtime-work/go.work go test -race ./runtime/bootstrap/integrationtest -run '^(TestBusinessIndependentReceiptReadThroughRealOwnerRPC|TestCrossUserBusinessAgentComplexFiltersOriginalCursorAutomaticDeliveryAndAcceptance)$' -count=1 -v -timeout=30m -cpuprofile=/tmp/c05-schema-projection-singleflight-final-race.cpu
```

Both final commands pass. The normal roots take 1.93 and 17.81 seconds. The race roots take 29.92 and 262.37 seconds; the second root includes the completed task plus repeated third-party acceptance, revocation, restart and twenty exact original-result SHA/page reads. Completion under the embedded 90-second task budget is required before those later assertions run.

## Input and patch reconstruction

`inputs/c05-schema-and-assessment-compaction-final-inputs.json` and `inputs/c05-schema-projection-singleflight-final-inputs.json` were generated from `go list -deps -test -json`, every selected source/test/embed/native file, module files, and the explicit workspace files. Both contain the same 1,167 dependencies and 9,895 paths. `inputs/input-audit.json` records zero added or removed inputs and exactly two changed paths: the provider implementation and its cache test.

`runtime.patch` and `agent.patch` are generated against the exact phase baselines under `before/`. `patch-reconstruction.json` records successful `patch -p1` reconstruction and exact equality with the `after/` manifests for both repositories.
