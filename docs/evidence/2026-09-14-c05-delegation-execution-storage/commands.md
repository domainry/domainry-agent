# 委派与执行归属分离：验证记录

工作目录：`/Users/tiger/Projects/domainry-agent`，沿用当前 `go.work`。本轮没有发布、切分支或运行 Runtime 工程测试。

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/transport/http/module ./internal/capability ./remote ./server github.com/domainry/domainry-agent-sdk github.com/domainry/domainry-agent-sdk/persistence
```

相关回归通过，见 `regression.log`。

```sh
go test ./internal/infrastructure/persistence/database/agent -run '^TestDelegationSubjects' -count=1 -v
go test ./internal/infrastructure/persistence/database/agent -count=1
go test -race ./internal/infrastructure/persistence/database/agent -run '^TestDelegationSubjects|^TestDelegationParticipant|^TestPeerDelegation' -count=1
```

五个双主体存储场景、完整存储包和相关 race 检查通过，分别见 `subject-scenarios.log`、`store.log`、`race.log`。最终存储与 race 检查覆盖同用户不同角色的后续修正。

```sh
go test ./internal/assembly/web -run '^TestSharedAgentHTTP|^TestDelegationParticipantsHTTP' -count=1
```

原共享配置和参与通信的实际 Identity／HTTP 回归通过，见 `http-regression.log`。这个命令不验证尚未接通的跨主体应用接单。

`source-manifest.json` 保存本轮最终代码摘要。`acceptance.json` 明确区分已验证的存储链路与未开放的应用能力。SQLite 使用实际数据库；Postgres／MySQL 只检查 ORM 迁移渲染。本轮没有前端变更。
