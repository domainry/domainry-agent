# 验证命令与范围

Agent 命令在 `/Users/tiger/Projects/domainry-agent` 执行，使用当前本地 go.work。没有改 Runtime 发布锁或执行发布。

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./remote ./internal/capability ./internal/transport/http/module ./server github.com/domainry/domainry-agent-sdk/...

go test -race ./internal/application ./internal/infrastructure/persistence/database/agent -run '^TestDelegationExecution|^TestDelegationDiscovery|^TestDelegationExecutor|^TestDelegationBoundSender|^TestAgentExecutionBinding|^TestDelegationSubjects|^TestDelegationParticipant' -count=1

AGENT_EXECUTION_BROWSER=1 AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node AGENT_PLAYWRIGHT_MODULE=/tmp/domainry-g05-browser/node_modules/playwright AGENT_UI_TEST_OUTPUT=/tmp/c05-execution-binding-browser-verified go test ./internal/assembly/web -run '^TestDelegationExecutionHTTP|^TestSharedAgentHTTP|^TestDelegationParticipantsHTTP' -count=1 -v

AGENT_EXECUTION_BROWSER=1 AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node AGENT_PLAYWRIGHT_MODULE=/tmp/domainry-g05-browser/node_modules/playwright AGENT_UI_TEST_OUTPUT=/tmp/c05-execution-binding-browser-complete go test ./internal/assembly/web -run '^TestDelegationExecutionHTTP' -count=1 -v
```

最后一条命令在补充委派页面的私有执行提示和私有会话导航检查后运行；对应 `browser-http.log` 和本目录截图。前三条对应 `go-regression.log`、`race.log` 和 `identity-http.log`。最后的页面修改未改变 Go 实现。

前端命令在 `/Users/tiger/Projects/domainry-agent/frontend` 执行：

```sh
/Users/tiger/.nvm/versions/node/v22.22.0/bin/node --experimental-strip-types --test src/collaboration-state.test.ts
/Users/tiger/.nvm/versions/node/v22.22.0/bin/node node_modules/typescript/bin/tsc --noEmit
/Users/tiger/.nvm/versions/node/v22.22.0/bin/node node_modules/vite/bin/vite.js build
```

对应 `frontend-state.log`、`frontend-check.log`、`frontend-build.log`。类型检查成功时日志为空。构建保留现有大包提示，不作为运行失败。真实 Chrome 使用实际 Identity 账号和 HTTP 服务，未拦截为静态响应。

用例对模型时序进行控制以定位撤权边界；Todo 调用、执行记录、消息、配置和验收均走实际服务与数据库。交付测试是接收方直接提交的结论，不能用作自动跨主体成果来源读取的完成证明。完整待办状态记录在 `acceptance.json`。
