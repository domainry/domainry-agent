# C05 历史与执行记录读取证据

命令均在 Agent 当前目录、现有 go.work 下执行。

- `go test ./internal/application -count=1`：[完整应用包 PASS](agent-application-full.log)。
- 四类包装、通信缓存、空页、前后调用引用和任务来源检查：[逐项 PASS](agent-owner-final.log)。
- `go test ./internal/assembly/web -run 'TestPeerHistoryDeliveryReads|TestPeerTaskDeliveryReads|TestCompactedResultReadThroughIdentityHTTP' -count=1 -v`：[HTTP PASS](agent-http-final.log)。
- `go test -race ./internal/application ./internal/assembly/web -run 'TestDelivered|TestTaskDeliveryReceipts|TestPeerHistoryDeliveryReads|TestPeerTaskDeliveryReads' -count=1`：[race PASS](agent-race.log)。
- `go test -race ./internal/application -run 'TestDeliveredExecutionIndexDoesNotReleaseUnfinishedOrErrorPayloads' -count=1 -v`：[补充场景 PASS](agent-unfinished-race.log)。

补充测试首次因测试 host 未提供工具目录而 panic，见[夹具失败](agent-unfinished-fixture-failure.log)。补齐目录入口后通过，不涉及生产代码修正。

[源码摘要](source-sha256.json)保留此阶段快照；[阶段验收](acceptance.json)不表示 C05 或整张清单完成。
