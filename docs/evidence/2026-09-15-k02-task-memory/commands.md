# K02 验收命令

工作区：`/Users/tiger/Projects/domainry-agent`，日期：2026-09-15。

```sh
cd /Users/tiger/Projects/domainry-agent-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-agent
go test ./cmd/... ./definition/... ./integration/... ./internal/... ./module/... ./remote/... ./server/... ./testsupport/...

cd /Users/tiger/Projects/domainry-agent/frontend
npm test
npm run build

cd /Users/tiger/Projects/domainry-agent
AGENT_K02_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/Users/tiger/Projects/domainry-agent/docs/evidence/2026-09-15-k02-task-memory/browser-pass \
go test ./internal/assembly/web -run '^TestScopedMemoryBuiltBrowser$' -count=1 -timeout 8m -v

git diff --check
cd /Users/tiger/Projects/domainry-agent-sdk
git diff --check
```

`product-tests-pass.log` 覆盖全部实际产品源码根；根目录 `go test ./...` 不作为验收命令，因为 `docs/evidence` 内保存了多个故意不完整的历史 Go 源码快照。`context-regression-tests.log` 记录三个 64 KiB 边界回归场景的单独复验。SDK、前端、浏览器日志和机器可读报告均在本目录；通过截图位于 `browser-pass/scoped-memory-mobile.png`。
