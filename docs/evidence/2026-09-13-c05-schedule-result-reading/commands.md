# C05 计划原回执读取证据

Agent、Tools 和 SDK 命令均在 `/Users/tiger/Projects/domainry-agent` 使用现有 go.work。该本地工作区已加入当前 Scheduler 和 Scheduler SDK checkout，不创建分支或 worktree。Scheduler 命令在 `/Users/tiger/Projects/domainry-scheduler` 使用 `GOWORK=/Users/tiger/Projects/domainry-agent/go.work`。

| 命令 | 结果 |
| --- | --- |
| `go test ../domainry-tools/internal/adapter/scheduletools ../domainry-scheduler-sdk/...` | [SDK 和适配器 PASS](scheduler-sdk-and-tools.log) |
| `go test ../domainry-tools/internal/adapter/scheduletools ./internal/application -count=1` | [七类策略及完整 Agent 应用包 PASS](agent-application-and-tools.log) |
| `go test ./internal/application -run TestDeliveredScheduleCannotReuseProvenanceForPrivateOrigin -count=1 -v` | [原始通信来源、账本与权限 PASS](agent-origin.log) |
| `go test ./internal/assembly/web -run TestPeerScheduleDeliveryReadsSevenReceiptsAfterWriteRevocation -count=1 -v` | [七次真实执行／原交付／撤权／重启 PASS](agent-http-final.log) |
| `go test ./internal/assembly/web -run 'TestScheduleNaturalLanguageAndProductManagementEndToEnd|TestPeerHistoryDeliveryReads|TestPeerTaskDeliveryReads' -count=1` | [既有计划管理及历史／任务交付 PASS](agent-regression.log) |
| `go test -race ../domainry-tools/internal/adapter/scheduletools ./internal/application ./internal/assembly/web -run 'TestScheduleReceiptRead|TestDeliveredSchedule|TestPeerScheduleDeliveryReads' -count=1` | [PASS](agent-race.log)，Web 204.156s |
| `go test ./internal/application/scheduler ./remote ./internal/assembly/saas ./internal/transport/http/saas ./module -count=1` | [Scheduler 应用／远端／SaaS 全包 PASS，Module 编译通过](scheduler-owner-final.log) |
| `go test -race ./internal/application/scheduler ./remote ./internal/assembly/saas -run 'TestReadScheduledPlanDeletion|TestRemoteDeletionRead|TestDeletionReceipt|TestScheduledPlanManagement|TestScheduledPlanSaaSHTTP' -count=1` | [最终 owner 及真实 SQLite／SaaS／重启／只读初始化边界 PASS](scheduler-owner-race-final.log) |

四个代码库 `git diff --check` 通过。[28 份源码摘要](source-sha256.json)对应此阶段快照；旧阶段的摘要保留历史值。

## 保留的测试夹具修正

- [SaaS 夹具首次构建](scheduler-http-fixture-build-failure.log)使用了不存在的 transport 构造名，按当前 SDK 的 Open／Config／CapabilitySummary 修正。
- [Agent 首次验收断言](agent-http-confirmation-fixture-failure.log)预期五次确认，但当前宿主的 Schedule 授权只检查实际动作权限，七次调用成功且未请求确认。修正测试预期并明确记录，没有修改生产确认策略。
- [工具停用夹具](agent-http-preference-fixture-failure.log)在撤回执行权限后从当前可执行目录取设置，拿到了缺失记录的空版本。改为在有权时设置偏好，再撤回写权限检查交付隐藏，并通过同一路径恢复；未放宽产品设置权限。

初始 [owner race](scheduler-owner-race.log)也通过；最终复核补充了只读 SaaS 接口不能触发延迟应用初始化的实现与测试，最终日志另存。Agent race 对应最终 Agent／Tools 生产代码，后续只修改 Scheduler 只读装配与其测试。

[阶段验收](acceptance.json)仅标记计划回执范围完成，C05 和整张清单仍未完成。发布锁差异继续保留，未发布或修改已发布模块锁。
