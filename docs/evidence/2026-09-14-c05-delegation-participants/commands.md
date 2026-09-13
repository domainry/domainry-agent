# 单项委派参与人与通信：验证命令

工作目录：`/Users/tiger/Projects/domainry-agent`。使用现有 `go.work` 的本地 SDK；没有切换 Runtime 的工作文件或发布模块。最终源文件摘要见 `source-manifest.json`，范围判定见 `acceptance.json`。

## 相关回归

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/transport/http/module ./internal/capability ./remote ./server github.com/domainry/domainry-agent-sdk github.com/domainry/domainry-agent-sdk/businessrpc github.com/domainry/domainry-agent-sdk/persistence
```

结果：通过，见 `regression.log`。

## 参与链路并发检查

```sh
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/assembly/web ./remote -run 'TestDelegationParticipant|TestSaaSDelegationParticipant' -count=1
```

结果：通过，见 `race.log`。覆盖当前发送角色、消息重试、名单保存回执、待接收消息撤权及真实 Identity／HTTP／SQLite worker。

## 前端

```sh
npm --prefix frontend run check
npm --prefix frontend run build
```

结果：通过，见 `frontend-check.log`、`frontend-build.log`。构建仍提示现有大分块。

## 双用户 Chrome 页面

```sh
AGENT_PARTICIPANTS_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/tmp/domainry-g05-browser/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/c05-participants-browser-final \
go test ./internal/assembly/web -run '^TestDelegationParticipantsHTTP' -count=1 -v
```

结果：通过，见 `browser.log`、`participants-report.json`。使用隔离测试数据库和测试账号，真实 Chrome 连接实际测试 HTTP 服务。

- `participant-sender-identity.png`：实际发送用户与进入执行上下文的回执。
- `participant-mobile.png`：参与范围及窄屏通信记录，长 ID 和状态文字完整换行。

截图已人工查看。浏览器验证后的消息幂等、发送角色和准备状态调整，由最终相关回归及实际 HTTP race 场景覆盖。
