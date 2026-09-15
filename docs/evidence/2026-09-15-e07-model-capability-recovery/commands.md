# E07 验收命令

日期：2026-09-15。以下正式验收命令退出码均为 0；历史证据目录包含归档的外部 Go 源码和混合包，因此产品包清单使用 `go list` 明确排除 `docs/evidence`。

## Agent SDK

工作目录：`/Users/tiger/Projects/domainry-agent-sdk`

```bash
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...
git diff --check
```

## Agent 产品回归

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
go list -e -f '{{.ImportPath}}' ./... | rg -v '^$|/docs/evidence/'
go test <除 integration、internal/assembly/web 外的 34 个实际产品包> -count=1
go test ./integration -count=1
go test ./internal/assembly/web -count=1
git diff --check
```

完整 Web 包结果为 `ok github.com/domainry/domainry-agent/internal/assembly/web 533.856s`，Integration 为 `99.131s`；其余实际产品包全部通过。

## 模型恢复竞态

```bash
go test -race ./internal/application \
  ./internal/infrastructure/provider \
  ./internal/infrastructure/persistence/database/agent \
  ./integration \
  -run 'Test(ConversationModelSelection|ConversationAgentRecheck|DelegationExecutorContext|ConversationHTTPFailures|ConversationRetryDetails|ConversationNetworkFailure|ConversationUnexpectedEOF|ConversationModelDeclaresCapabilities|ConversationModelIdentityChanges|ConversationModelCapabilityEnvironment|ConversationModelAttempts|TerminalModelFailure|ConversationExecutionRetriesModel|ConversationExecutionResumesAfterModelFailure|ConversationModelRetryStops)' \
  -count=1
```

## 前端

工作目录：`/Users/tiger/Projects/domainry-agent/frontend`

```bash
npm test
npm run build
```

前端共 91 项测试通过；TypeScript 与 Vite 生产构建通过。Vite 只报告既有的大分块体积提示。
