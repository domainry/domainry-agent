# C05 same-user role source-release verification

The preserved final logs below all exited 0. Agent/SDK/Identity commands ran from the Agent directory using its current go.work. The workspace now binds the current local Audit SDK: its cached release lacked the SubjectResource ports used by the current Audit source, so the initial web compile failed before tests. Runtime commands ran from Runtime with GOWORK=/tmp/runtime-work/go.work. Both workspace files are preserved. No branch, worktree, commit, push or deployment was performed.

- `go test ./internal/application ./internal/infrastructure/persistence/database/agent -count=1` → source-regression.log
- `go test -race ./internal/application ./internal/infrastructure/persistence/database/agent -run 'TestSourceRelease|TestSourceReleases|TestDelegationSubjects' -count=1` → source-race.log
- `go test ../domainry-agent-sdk/persistence ./internal/transport/http/module ./remote ./server -count=1` → transport-regression.log
- `go test ./internal/assembly/web -run '^(TestSourceReleaseHTTPAgentDispatchMessageAndAutomaticDeliveryAcrossSubjects|TestDelegationExecutionHTTPUsesConsentingReceiversRoleAndStopsAfterRevocation)$' -count=1` → cross-user-http.log
- `go test ../domainry-identity/internal/adapter/identitysdk -count=1` → identity-regression.log
- `go test -race ../domainry-identity/internal/adapter/identitysdk -run '^TestSDKAccessBundle' -count=1` → identity-race.log
- `GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^TestCrossUserAgentProfessionalDispatchAutomaticDeliveryAndCurrentSourceRead$' -count=1` → cross-user-professional-runtime.log
- `GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^TestSameUserDifferentRolesProfessionalDispatchAutomaticDeliveryAndCurrentSourceRead$' -count=1 -v` → same-user-professional-runtime.log

The transport and cross-user scenarios preceded the final immutable-run publication-provenance adjustment and Identity function-grant deduplication. Their original role/proof happy paths did not change. The final source full-package/race, Identity full-package/race, and same-user actual Runtime logs cover those current changes. Source hashes were captured after all functional edits; earlier phase hashes remain historical.

The same-user test uses real HTTP role assignments and refreshes the credential, checks the current Identity default role, and leaves the separately bound professional role assigned. Field/row denial also retains the original issuer role so denial is attributable to the actual reader's current field/row policy. The final case removes the professional role. All four original receipts survive restart before revocation.

An earlier same-user test attempt incorrectly called an unmounted auth/me route; the corrected fixture reads the actual Identity CurrentSession port. The raw-run assertion originally reused a poll object across JSON responses with omitted fields; the final assertion reads a new object. A subsequent three-role run exposed identity.function_grant_duplicate. Its actual owner projection is now fixed, keeping data and audit policies separate, and the final same-user Runtime run passed.

Model fixtures consume public wire results and original server-issued references. Issuer Agent dispatch requires an explicit fixture-account confirmation. Receiver executes and delivers automatically. Acceptance is an explicit issuer-account HTTP action, not autonomous issuer-model acceptance. Ordinary same-user private conversation ownership stays user-scoped; the results-only reader cannot replay the underlying professional execution data. This increment does not complete C05 or the full 25-item TODO.
