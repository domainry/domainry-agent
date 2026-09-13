# C05 来源发布验证命令

工作目录：`/Users/tiger/Projects/domainry-agent`，使用当前 go.work。

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent -count=1
go test ./internal/assembly/web -run '^TestSourceReleaseHTTPAgent|^TestDelegationExecutionHTTP' -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent -run '^TestSourceRelease|^TestAgentSubjectLifecycle' -count=1
```

原始输出分别见 [应用与存储回归](go-regression.log)、[真实 HTTP](http-source-and-execution.log)及 [新增边界 race](race-source-boundaries.log)。三个命令全部通过。

[范围记录](acceptance.json)保留 C05 未完成和整表 4／25。真实 HTTP 使用实际 Identity、SQLite 与模型工具夹具；调用确认由测试账号完成，验收由发起账号提交。自动交付场景仅覆盖内置时钟。

[文件摘要](source-sha256.json)记录本阶段的来源契约、存储、迁移、应用检查、相关入口和验证代码；历史阶段证据保留各自当时的版本。
