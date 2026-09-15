# K08 验收命令

工作区：`/Users/tiger/Projects/domainry-agent`，日期：2026-09-16。

```sh
cd /Users/tiger/Projects/domainry-agent-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-agent
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./cmd/... ./definition/... ./integration/... ./internal/... ./module/... ./remote/... ./server/... ./testsupport/... ./web/... ./webhost/...
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test -race ./internal/application -run '^TestCodingRuntimeCatalogAuthorizationAndScope$' -count=1

cd /Users/tiger/Projects/domainry-runtime
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./pkg/codingruntime ./pkg/runtimehost ./runtime/bootstrap/runtime ./runtime/bootstrap/transport -count=1
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test -race ./pkg/codingruntime -count=1

cd /Users/tiger/Projects/domainry-agent/frontend
npm test
npm run build

git -C /Users/tiger/Projects/domainry-agent diff --check
git -C /Users/tiger/Projects/domainry-agent-sdk diff --check
git -C /Users/tiger/Projects/domainry-runtime diff --check
```

结果：Agent SDK 全包通过；Agent 的生产源码包集合通过。Runtime 编码执行、公开 host、启动装配和 transport 包通过。核心 Agent 授权测试与 Runtime 的文件、PTY、进程、沙箱和 LSP 真实链路通过 race detector。前端 94 项测试与生产构建通过，Vite 只报告既有大 chunk 提示。

仓库根的无过滤 `go test ./...` 会把 `docs/evidence` 中历次验收保存的 273 个 `.go` 源码快照识别为当前模块包，因此验收命令明确枚举生产目录；这些快照不是可构建源码。
