# C05 个人工具回执阅读证据

所有命令在 `/Users/tiger/Projects/domainry-agent` 使用当前 go.work 执行。

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/application -count=1` | PASS，0.789s；[完整应用包](agent-application-full.log) |
| `go test ./internal/assembly/web -run 'TestPeerPersonal.*DeliveryReads' -count=1 -v` | PASS，14.849s；[真实 HTTP](agent-http-final.log) |
| `go test -race ./internal/application ./internal/assembly/web -run 'TestPersonalDeliveryReceiptsUseOriginalOwnerAndCurrentDataWithoutWriteTools\|TestPeerPersonal.*DeliveryReads' -count=1` | PASS，应用 2.136s／Web 332.041s；[race](agent-race.log) |

[设置夹具失败](agent-http-settings-fixture-failure.log)是撤回工具执行权后从已隐藏的设置项读取版本；停用检查移至撤权前。[状态码夹具失败](agent-http-status-fixture-failure.log)是将记忆删除的明确来源拒绝误期望为 503，实际为 403；修正断言后通过。另一次提问等待夹具使用了不存在的 waiting_input，按 SDK waiting_user 修正；没有变更执行状态契约。

[八份来源文件摘要](source-sha256.json)记录个人阶段测试时的源码；后续任务来源策略继续修改共用文件，保留此历史快照。[验收](acceptance.json)仅标记本阶段完成，C05 和整体目标未完成。没有公开契约变更、浏览器新增验收或发布声明。
