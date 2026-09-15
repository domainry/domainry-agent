# K05 验收命令

工作区：`/Users/tiger/Projects/domainry-agent`，日期：2026-09-16。

```sh
cd /Users/tiger/Projects/domainry-agent
go test ./internal/infrastructure/persistence/database/agent ./internal/infrastructure/provider ./integration -run 'TestValidStoredConversationContent|TestConversationImageInput|TestConversationModelContent' -count=1
go test ./internal/assembly/web -run '^TestSourceReleaseHTTPAgentDispatchMessageAndAutomaticDeliveryAcrossSubjects$' -count=1 -v
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/infrastructure/provider ./integration -count=1
go test github.com/domainry/domainry-knowledge/internal/application/knowledge -count=1

cd /Users/tiger/Projects/domainry-agent-sdk
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...

cd /Users/tiger/Projects/domainry-agent/frontend
npm test
npm run build

cd /Users/tiger/Projects/domainry-agent
git diff --check
git -C ../domainry-agent-sdk diff --check
git -C ../domainry-knowledge diff --check
```

测试结果：

- K05 持久化／Provider／Integration 聚焦包通过。
- 三账号来源发布、自动委派执行、第三账号执行查看、撤权、拒绝未发布运行和重启链路 26.84 秒通过。
- Agent application、persistence、provider 与完整 Integration 包通过；Integration 用时 113.237 秒。
- Agent SDK 在当前联合工作区全包通过；Knowledge 附件应用包通过。
- 前端 93 项测试通过，生产构建通过；Vite 仅报告既有大 chunk 提示。
- 三个改动仓库的 `git diff --check` 均无错误。

Agent SDK 单独脱离联合工作区运行时仍解析已发布的 `domainry-tools-sdk v0.1.4`，缺少 K03／K04 同批尚未发布的 MCP 与并行读取契约；当前源码联调必须使用 Agent 的 `go.work`。这属于跨仓发布顺序，不是 K05 编译失败。
