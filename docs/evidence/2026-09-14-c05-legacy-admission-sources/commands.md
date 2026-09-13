# C05 legacy admission roots and historical page identity

Agent cwd: `/Users/tiger/Projects/domainry-agent`, current `go.work`. Runtime cwd: `/Users/tiger/Projects/domainry-runtime`, `GOWORK=/tmp/runtime-work/go.work`.

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent ../domainry-agent-sdk ../domainry-agent-sdk/persistence ./internal/transport/http/module ./remote ./server ./internal/capability -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent -run 'TestDeliveryResultEndpointRequires|TestLegacyRequiredSource|TestLegacySourcePublisher|TestLegacyMessageSource|TestLegacyManualSource|TestPublishedSource|TestDelegationSource|TestSourceRelease|TestPeerTransferLegacyAssignmentUsesOriginalProvenance' -count=1 -v
go test ./internal/assembly/web -run '^(TestSourceReleaseHTTPAgentDispatchMessageAndAutomaticDeliveryAcrossSubjects|TestDelegationExecutionHTTPUsesConsentingReceiversRoleAndStopsAfterRevocation)$' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^(TestMixedOldRoleSourceToolDispatchReadAndAutomaticDelivery|TestUnpublishedMixedOldRoleReportAndAnalysisFirstDeliveryThroughRealHTTP|TestCrossUserAgentProfessionalDispatchAutomaticDeliveryAndCurrentSourceRead|TestSameUserDifferentRolesProfessionalDispatchAutomaticDeliveryAndCurrentSourceRead)$' -count=1 -v
```

Logs: `regression.log`, `race.log`, `http.log`, `runtime.log`. `source-sha256.json` contains current source hashes and the two workspace files preserve module resolution.

Legacy contract requirements are matched against the original completed admission call at the trusted initial agreement change step, exact ResourceID, and declared root. Later metadata is neither necessary nor sufficient. Application cases exercise historical assignment membership and immutable execution role while the current reader changes, without granting invocation in the historical conversation. Those cases do not claim a real cross-subject transfer.

The actual Runtime mixed-source scenario converts both its contract and delivery source publications to old JSON while the fixture is stopped. Original professional results, actual admission/read/delivery tool ledgers, and immutable agreement/delivery history stay intact. Every delivery-result page requires the exact delivery purpose and delegation; contract grants cannot substitute. Current-reader page replay, publisher/original-provider/field revocation, restoration and reopening remain checked.

Earlier Runtime runs compiled before the final historical-role checks are excluded from current acceptance. A focused application fixture lacked the subject-validator port for source sharing; it was corrected before the final commands. No migration or frontend code changed in this phase. Database coverage is SQLite and the model is a deterministic wire fixture.

C05 remains incomplete: full republishing UI and accepted-history handling, complete historical assignment flows, multi-owner roots/dependencies, other professional adapters, broader participant scopes, complete execution sharing, cross-subject transfer/lifecycle and C06 onward remain pending.
