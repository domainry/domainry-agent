# C05 任务回执来源阅读证据

全部命令在 `/Users/tiger/Projects/domainry-agent` 使用现有 go.work 执行。

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/application -count=1` | PASS，0.511s；[完整应用包](agent-application-full.log) |
| `go test ./internal/assembly/web -run 'TestPeerTaskDeliveryReads' -count=1 -v` | PASS，4.832s；[实际取消／恢复与交付](agent-http-final.log) |
| `go test ./internal/assembly/web -run 'TestTaskStartThroughIdentityHTTPWorkerRecoveryAndSQLite\|TestScheduledTaskUsesCurrentIdentityAndWaitsForUserConfirmation\|TestPeerPersonal.*DeliveryReads' -count=1 -v` | PASS，20.227s；[真实任务／计划／个人回执回归](agent-http-regression.log) |
| `go test ./internal/application -run 'TestTaskDeliveryReceipts' -count=1 -v` | PASS，0.671s；[五种工具及成果来源](agent-owner-artifact.log) |
| `go test -race ./internal/application ./internal/assembly/web -run 'TestTaskDeliveryReceipts\|TestPeerTaskDeliveryReads' -count=1` | 最终代码 PASS，应用 1.480s／Web 77.311s；[race](agent-race.log) |

`git diff --check` 通过。[13 份来源摘要](source-sha256.json)对应当前阶段；前阶段个人回执的源码快照继续保留历史值。

[首次 HTTP 夹具失败](agent-http-route-fixture-failure.log)使用了错误的 `/agent/tasks/` 路径，按已有 HTTP 场景修正为 `/agent/conversation-tasks/`。[第二次夹具失败](agent-http-busy-fixture-failure.log)发生在委派完成真实唤醒发起会话时，夹具又向同一会话发起查询；改为从独立普通会话读取委派任务，并等待发起会话完成通知处理后撤权。没有取消通知、放宽 busy 或变更生产调度逻辑。

[阶段验收](acceptance.json)仅标记五种任务回执与嵌套来源检查完成，C05 和整张清单未完成。没有新 SDK／RPC 契约、数据库迁移、生产前端或发布变更。
