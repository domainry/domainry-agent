# Unreadable-contract management controls

Agent cwd: `/Users/tiger/Projects/domainry-agent`, current `go.work`; frontend commands use `/Users/tiger/Projects/domainry-agent/frontend`. No checkout or branch change.

```sh
/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node --experimental-strip-types --test --test-name-pattern='unreadable contract keeps stopping controls|stopping unreadable work never initializes' /tmp/c05-recovery-controls-before/collaboration-state.test.ts
PATH=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin:$PATH npm test
PATH=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin:$PATH npm run build
AGENT_RECOVERY_CONTROLS_BROWSER=1 AGENT_NODE_BINARY=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright AGENT_UI_TEST_OUTPUT=/tmp/c05-recovery-controls-browser-null-guard go test ./internal/assembly/web -run '^TestSourceReleaseHTTPAgentDispatchMessageAndAutomaticDeliveryAcrossSubjects$' -count=1 -v
git diff --check
```

`before-state` contains the exact original state module, reconstructed by removing only this phase's action filter and checked against the preceding source snapshot SHA-256, and the current two negative cases. `before.log` fails both cases on that source; the original retained module also lacks the omitted-brief guard. `source-before-recovery-controls-sha256.json` preserves the preceding accepted backend snapshot. `source-before-null-brief-sha256.json` identifies this phase before the real browser error fix. `browser-before-null-brief.log` records those three real page errors. `browser-before-final-refresh.log` records an earlier test reading the old cancel button before the cancelled row refresh; final assertions await the authoritative visible state rather than a transient form close.

Final frontend tests/build logs, exact 134-file source and 571-file built-asset hashes, and the browser's terminal status/report are retained here. Browser permissions are changed through real Identity role-authoring APIs by isolated test fixture routes. No approval or publication action is simulated by editing the application's response. Original account/Agent dispatch, automatic source-bearing delivery, acceptance and cross-user revocation/restart also execute in the same HTTP fixture before the recovery management scenario.

The accepted backend lifecycle from the preceding phase remains in `../2026-09-14-c05-record-policy-projection`; this frontend phase does not claim a rerun of that Runtime scenario or its unchanged core/race packages.
