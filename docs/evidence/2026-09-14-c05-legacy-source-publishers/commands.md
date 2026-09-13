# C05 legacy publication-role verification

Agent commands run in `/Users/tiger/Projects/domainry-agent` with the current `go.work`. Runtime commands run in `/Users/tiger/Projects/domainry-runtime` with `GOWORK=/tmp/runtime-work/go.work`.

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent ../domainry-agent-sdk ../domainry-agent-sdk/persistence ./internal/transport/http/module ./remote ./server ./internal/capability -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent -run 'TestSourcePublisher|TestLegacySourcePublisher|TestLegacyManualSource|TestLegacyMessageSource|TestPublishedSourceContext|TestSourceRelease|TestDelegationSameIdentityAuthority|TestPeerTransferLegacyAssignmentUsesOriginalProvenance' -count=1 -v
go test ./internal/assembly/web -run '^(TestSourceReleaseHTTPAgentDispatchMessageAndAutomaticDeliveryAcrossSubjects|TestDelegationExecutionHTTPUsesConsentingReceiversRoleAndStopsAfterRevocation)$' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^(TestMixedOldRoleSourceToolDispatchReadAndAutomaticDelivery|TestUnpublishedMixedOldRoleReportAndAnalysisFirstDeliveryThroughRealHTTP|TestCrossUserAgentProfessionalDispatchAutomaticDeliveryAndCurrentSourceRead|TestSameUserDifferentRolesProfessionalDispatchAutomaticDeliveryAndCurrentSourceRead)$' -count=1 -v
```

The corresponding complete logs are `regression.log`, `race.log`, `http.log`, and `runtime.log`. `source-sha256.json` identifies the current source bytes; the two workspace files preserve module resolution. Source changes were finished before the final commands compiled.

The Runtime mixed-role scenario converts only its actual **delivery** source publications to the old format after account acceptance and stopping the Runtime. Immutable professional results, actual Agent tool executions, contract publications, and delivery/verification history remain intact. Subsequent original-value reads therefore exercise publication-role recovery from the historical receiving run. Exact delegation and publication purpose prevent a contract grant from substituting for a delivery grant. Current equivalent-reader access, publisher/original-provider/field revocation, page replay, and reopening remain checked.

Additional application/store cases verify unknown-publisher rejection, actual manual publication roles, assessment/acceptance without re-publication, explicit manual resubmission, contract change actors, message sender actors, and binding changes without historical role substitution. Recovery does not mutate legacy IDs or payloads.

Excluded attempts: a test fixture omitted required message table columns; its ORM insert was corrected. A stricter persisted-source scope initially left an Evidence Run lookup using the reader; resolution before that lookup was corrected. Earlier Runtime runs compiled before the final reference-resolution changes are not used as current acceptance logs.

C05 is not complete. Legacy required-root contract association, full republishing UI/history handling, multi-owner source chains/dependencies, additional professional adapters, participant management/execution/delivery ranges, complete execution-detail sharing, cross-subject transfer/lifecycle and C06 onward remain pending. This phase covers SQLite and deterministic model wire, without a full browser or online-model acceptance claim.
