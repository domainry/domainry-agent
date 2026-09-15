# K04 验收命令

工作区：`/Users/tiger/Projects/domainry-agent`，日期：2026-09-15。

```sh
cd /Users/tiger/Projects/domainry-agent
go test ./integration -run '^TestConversationCodeMode' -count=1
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./module ./integration -count=1
go test ./...

# 根命令会扫描 docs/evidence 中故意不完整的历史 Go 快照，并且 Web 整包触及 10 分钟上限。
# 产品包结果中仅 Web 未取得整包通过，故补以下两个边界验证。
go test ./internal/assembly/web -run '^$' -count=1
go test ./internal/assembly/web -run '^TestKnowledgeDocumentTransfersIdentityHTTPProtocol$' -count=1

cd /Users/tiger/Projects/domainry-agent-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./... -count=1

cd /Users/tiger/Projects/domainry-runtime
GOWORK=/tmp/domainry-k04-20260915.work go test ./pkg/coderuntime ./pkg/runtimehost -count=1

cd /Users/tiger/Projects/domainry-agent/frontend
npm test
npm run build

cd /Users/tiger/Projects/domainry-agent
git diff --check
git -C ../domainry-agent-sdk diff --check
git -C ../domainry-runtime diff --check
```

日志文件：

- `agent-code-mode-tests.log`：四条 K04 真实 Agent 链。
- `agent-tests.log`：根级运行，包含历史证据目录编译错误、实际产品包结果和 Web 10 分钟整包超时。
- `agent-web-compile.log`、`agent-web-timeout-test-isolated.log`：Web 编译和超时点对应旧场景单独复验。
- `agent-sdk-tests.log`：SDK 全包。
- `runtime-tests.log`：受限进程与 Runtime 宿主。
- `frontend-tests.log`、`frontend-build.log`：状态投影测试与生产构建。
- `diff-check.log`：三仓 `git diff --check`，空文件表示无错误。
