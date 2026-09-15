# E08 验收命令

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
go list -e -f '{{.ImportPath}}' ./... | rg -v '^$|/docs/evidence/|/integration$|/internal/assembly/web$'
go test <除 integration、internal/assembly/web 外的 34 个实际产品包> -count=1
go test ./integration -count=1
go test ./internal/assembly/web -count=1
git diff --check
```

完整 Web 包结果为 `ok github.com/domainry/domainry-agent/internal/assembly/web 522.725s`，Integration 为 `93.012s`；其余实际产品包全部通过。

## 生命周期竞态与关键边界

```bash
go test -race ./internal/application ./integration \
  -run 'TestConversationLifecycle|TestPrepareConversationLifecycle|TestDeferredConversationModuleAssemblesLifecycleHost' \
  -count=1
```

## 前端

工作目录：`/Users/tiger/Projects/domainry-agent/frontend`

```bash
npm test
npm run build
```

前端共 91 项测试通过；TypeScript 与 Vite 生产构建通过。Vite 只报告既有的大分块体积提示。

## 固定对照源码

- URL：`https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/packages/core/agent-loop/src/agent.ts`
- 原始文件字节数：`24885`
- SHA-256：`bbc3c4f34076e81596af855eddb1bf66f6362278730f7c4db24ea96a990e3cb3`
