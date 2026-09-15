# K03 验收命令

工作区：`/Users/tiger/Projects/domainry-agent`，日期：2026-09-15。

```sh
cd /Users/tiger/Projects/domainry-connector-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-connectors
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-integration-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-integration
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-tools-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-tools
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-agent-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-agent
GOWORK=/Users/tiger/Projects/domainry-agent/go.work \
go test ./cmd/... ./definition/... ./integration/... ./internal/... ./module/... ./remote/... ./server/... ./testsupport/...

# 首轮 assembly/web 整包累计超过默认 10 分钟；20 分钟复验时一个旧协作场景超过自己的等待上限。
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./internal/assembly/web -timeout 20m
GOWORK=/Users/tiger/Projects/domainry-agent/go.work \
go test ./internal/assembly/web -run '^TestPeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery$' -count=1 -timeout 5m -v
GOWORK=/Users/tiger/Projects/domainry-agent/go.work \
go test ./internal/assembly/web -run '^TestMCPToolsThroughAgentLedgerConfirmationAndRestart$' -count=1 -v

cd /Users/tiger/Projects/domainry-runtime
GOWORK=/tmp/domainry-k03-20260915.work \
go test ./runtime/application/agenthost ./runtime/bootstrap/transport

# 启动包可编译，但当前本地多仓工作区不满足既有发布锁测试；保留失败作为已知发布状态。
GOWORK=/tmp/domainry-k03-20260915.work go test ./runtime/bootstrap/runtime

# 对 agent、agent-sdk、tools、tools-sdk、integration、integration-sdk、connectors、connector-sdk、runtime 分别执行：
git diff --check
```

`logs/` 保存 K03 真实 Agent 链、Connector、Integration、Tools、SDK 和 Runtime 聚焦验证输出。Agent 根目录的 `docs/evidence` 含故意不完整的历史 Go 源码快照，因此实际源码全量命令显式列出源码根。
