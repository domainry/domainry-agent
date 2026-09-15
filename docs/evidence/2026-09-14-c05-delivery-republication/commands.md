# C05 delivery republication

Agent cwd: `/Users/tiger/Projects/domainry-agent`, current `go.work`. Runtime cwd: `/Users/tiger/Projects/domainry-runtime`, `GOWORK=/tmp/runtime-work/go.work`. Frontend commands run in the Agent `frontend` directory.

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent ../domainry-agent-sdk ../domainry-agent-sdk/persistence ./internal/transport/http/module ./remote ./server ./internal/capability -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent -run 'TestDeliveryPublication|TestRepublish|TestPublishedSource|TestSourceRelease|TestLegacyRequiredSource' -count=1 -v
GOWORK=/tmp/runtime-work/go.work go test ./runtime/bootstrap/integrationtest -run '^TestLegacyManualMixedOldRoleDeliveryExplicitRepublishingThroughRealHTTP$' -count=1 -v
npm test
npm run build
AGENT_DELIVERY_PUBLICATION_BROWSER=1 AGENT_NODE_BINARY='/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node' AGENT_PLAYWRIGHT_MODULE='/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright' AGENT_UI_TEST_OUTPUT='/tmp/c05-delivery-publication-browser-published' go test ./internal/assembly/web -run '^TestSourceReleaseHTTPAgentDispatchMessageAndAutomaticDeliveryAcrossSubjects$' -count=1 -v
```

Logs: `regression.log`, `race.log`, `runtime.log`, `frontend-tests.log`, `frontend-build.log`, `browser.log`. The source inventory includes 98 current Agent/SDK/Runtime/frontend sources and tests plus copied module workspace files.

The Runtime scenario creates complete actual old-role report and analysis originals (both total 70), human submission and acceptance, then stops the fixture and converts only its actual delivery-publication JSON to the old format without Publisher. Original tool calls, proofs, results and immutable submission/assessment records remain intact. Old reads must deny, preparation must produce no grant, current-source denial must stop commit, explicit publication must restore exact original reads without changing original acceptance, retries must create one audit, and publisher-role withdrawal and reopening must retain the boundaries. SQLite and deterministic model wire only.

The browser scenario uses the real Identity and Agent HTTP host, actual automatic clock delivery and human acceptance, and the built client. It checks choosing the accepted original, preview without publication, deliberate sharing, a real committed response lost in transport, reload plus exact request-ID/CAS/record retry with one audit, original immutable history, actual publisher audit displayed alongside the original assessment, actual Identity share withdrawal, hidden preview, restoration and desktop/narrow rendering. A client-only replay changes the new publication assessment arrays to null after fetching real authorized history, matching unverified legacy record serialization; it verifies rendering compatibility. It does not use an old-format browser database or an online model.

Earlier failing compilation, route-count assertions and browser diagnostics are excluded. The browser direct fixture reads were corrected to use the page's real X-Agent-Scope; the catalog is awaited before reading options. Earlier Runtime runs compiled before the final missing-provenance guard are excluded. The final frontend build follows the form-spacing adjustment and publication-history audit display and unverified-history empty-array compatibility.

No production migration was added. All production DML and offline test-format conversion use ORM builders. C05 remains incomplete: old-contract/historical admission flows, multiple owners and dependencies, further professional adapters, broader participant scopes, full execution-detail sharing, cross-subject transfer/lifecycle and C06 onward remain pending.
