# C06 verification commands

All commands ran from `/Users/tiger/Projects/domainry-agent` unless noted.

```sh
npm test
npm run build
git diff --check
AGENT_PEER_BROWSER=1 AGENT_NODE_BINARY=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright AGENT_UI_TEST_OUTPUT=/tmp/c06-collaboration-inline-browser go test ./internal/assembly/web -run '^TestPeerCollaboration' -count=1 -timeout 10m -v
```

Results:

- Frontend state tests: 83/83 passed. See `logs/c06-final-frontend-test.log`.
- TypeScript and production build: passed. See `logs/c06-final-frontend-build.log`.
- `git diff --check`: exit 0 with empty output.
- Peer browser package: 2/2 Go roots passed in 59.276s; the real browser report has no page errors. See `browser-report.json`.
- Visual evidence: `screenshots/peer-inline-progress.png`, `screenshots/peer-inline-progress-mobile.png`, and `screenshots/peer-accepted.png`.
