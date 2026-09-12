# G02 持久调度、领取、重试与重启恢复验收

G02 已完成。G01 的计划记录现在投影到 Scheduler 已有的 definition state、run、事件、租约、重试和 dead-letter 内核；Module 与 SaaS 没有新增第二套计划 worker 或 run 表。一次性计划和重复计划都由同一个 clock `Tick` 领取和投递。

## 架构边界

| 层 | 归属与依赖方向 |
| --- | --- |
| Scheduler SDK | `Policy` 增加 misfire grace，`schedule.ResolveMisfire` 负责纯时间计算，`ScheduledPlanDispatch` 定义下游交接事实；SDK 不依赖实现仓。 |
| Scheduler application | 把 plan 变成保留命名空间的 execution definition，规范化 durable policy，并把 plan ID、owner、输入、允许动作和会话引用封装进下游 payload。 |
| Scheduler run store | 继续拥有 cursor、唯一时间窗、run、lease、fencing、retry 和 DLQ；新增 source_kind 隔离 Runtime definition 与 scheduled plan 的 execution state。 |
| Module / SaaS | 共用同一 run store。Module 由宿主 worker admission 启动；SaaS 在首次保存计划或重启恢复到计划时启动服务自有 worker。 |
| 下游产品 | Scheduler payload 只提供可信签名的计划事实，不授予用户权限。G03 将在 Agent／Identity／Integration 边界重新授权。 |

[架构机器审计](evidence/2026-09-12-g02-scheduler-execution/architecture-audit.json)记录 source 隔离、领取 CAS、恢复、misfire、下游交接和依赖扫描。

## 持久领取与重复去重

- 每个 `runtime_id + definition_key + scheduled_for` 只有一条 run，run ID 和幂等键由这三个事实稳定生成。
- `Due` 把读取到的精确 cursor 带给 `Claim`；`Claim` 在同一事务中插入 run、比较并推进该 cursor、写入 lease event。另一个 worker 即使读到相同旧 cursor，也不能提交第二条 run。
- 领取后 worker 按 lease TTL 续租；旧 worker 丢失 lease 时取消 dispatch，不能提交 receipt。过期 lease 重新进入 Due 队列，由新 worker 增加 fencing token 和 attempt 后接管。
- 手动触发和 retry 不再移动 recurrence cursor；只有从 `Due` 读出的时间窗能够 CAS 推进它。
- 一次性计划成功领取后立即禁用 execution cursor。同一 plan revision 在重启 hydration 时保留这个终态，不会因 plan 记录仍为 enabled 而再次触发。

实际 SQLite 并发测试让两个不同 worker 同时读取和领取同一计划窗口，最终一个领取成功且 `_scheduler_runs` 只有一行。另一个重启恢复测试让 worker A 留下过期 lease，worker B 取得同一 run，attempt 与 fencing token 均从 1 增至 2。

## 错过时间策略

计划把策略和触发规则一起持久保存，默认值在创建前写入记录：

| 策略 | 到期超过 grace 后的行为 |
| --- | --- |
| `skip` | 不创建旧窗口 run，把 cursor 原子推进到当前时间之后；一次性计划直接终止 execution cursor。 |
| `catch_up_one` | 执行最早的一个到期窗口，然后把 cursor 推到当前时间之后。 |
| `catch_up_bounded` | 按时间顺序执行最多 1–100 个窗口；若仍有积压，最后一次领取把 cursor 推到当前时间之后。 |

默认 grace 为 1 分钟，正常轮询延迟仍执行原窗口。一次性计划默认 `catch_up_one`，确保停机跨过唯一时间后能补跑；重复计划默认 `skip`，避免服务恢复时产生无界突发。interval 的快进保持原始时间相位，daily／weekly／monthly／cron 使用规则计算当前时间之后的第一个窗口。

## 失败重试与实际 E2E

计划默认 3 次尝试、30 秒初始退避、15 分钟最大退避和 5 分钟单次超时，也可在校验范围内显式设置。失败 run 进入 `retrying` 并保存 `next_retry_at`；到达最大次数后沿用 Scheduler 原有的原子 dead-letter 与事件记录。

`TestScheduledPlanWorkerRetriesOnceAfterSaaSRestartWithoutDuplicateWindow` 走完整实际链路：

1. Scheduler SDK HTTP transport 携带 Runtime 机器凭证调用私有 SaaS Server 创建一次性计划。
2. Server → `DatabaseService` → Scheduler application → SQLite 保存计划并投影 execution state，SaaS 自动启动 clock worker。
3. 第一次实际 downstream dispatch 返回失败，run 持久进入 `retrying`；随后关闭整个 Scheduler SaaS service。
4. 新 service 只从 SQLite 分页恢复计划和 execution state，自动启动新 worker；到达 `next_retry_at` 后用同一 run ID／幂等键发出 attempt 2。
5. 第二次投递成功，payload 中的 plan ID、owner、输入、允许动作和 conversation/Run 引用逐项一致；数据库只有一条 run、一个 retry event，最终 attempt=2、fencing token=2。

Module E2E 另行创建并实际执行一次性计划，关闭 binding 后重开数据库和 worker，75 ms 观察窗内无第二次 dispatch，run 总数保持 1。source_kind 测试还证明 Runtime snapshot 的 `DisableMissing` 只处理 `runtime_definition`，不会误停用户计划。

## 最终验证

| 范围 | 命令 | 结果与证据 |
| --- | --- | --- |
| Scheduler 整库 race | `GOWORK=/tmp/domainry-g01/go.work go test -race ./... -count=1 -timeout 8m` | 全部通过。[日志](evidence/2026-09-12-g02-scheduler-execution/logs/scheduler-race.log) |
| Scheduler SDK 整库 race | `go test -race ./... -count=1 -timeout 6m` | 全部通过。[日志](evidence/2026-09-12-g02-scheduler-execution/logs/sdk-race.log) |
| G02 跨层 race | application、SQLite store、Module、SaaS 的 `ScheduledPlan`／`Expired`／`Misfire` 用例 | 全部通过，含 SDK 私有 HTTP、失败、重启、并发 CAS 和一次性去重。[日志](evidence/2026-09-12-g02-scheduler-execution/logs/g02-e2e-race.log) |
| misfire race | SDK schedule 的计划校验、三种策略、grace 和 interval 相位 | 全部通过。[日志](evidence/2026-09-12-g02-scheduler-execution/logs/misfire-race.log) |
| 静态检查 | Scheduler／SDK `go vet ./...`、`git diff --check`、architecture 和禁止依赖扫描 | 全部通过。[Scheduler vet](evidence/2026-09-12-g02-scheduler-execution/logs/scheduler-vet.log) · [SDK vet](evidence/2026-09-12-g02-scheduler-execution/logs/sdk-vet.log) · [架构审计](evidence/2026-09-12-g02-scheduler-execution/architecture-audit.json) |
| Runtime 组合 | 公开宿主组合与锁定 11-module 启动测试 | 全部通过，未修改 capability 锁。[组合日志](evidence/2026-09-12-g02-scheduler-execution/logs/runtime-composition.log) · [启动日志](evidence/2026-09-12-g02-scheduler-execution/logs/runtime-startup.log) |

G02 没有增加用户管理页面；查看、修改、暂停、恢复、删除和自然语言计划管理属于 G05。下游授权撤销、用户停用、外部连接失效和等待确认属于紧接的 G03，本项不把签名 payload 当作权限。

本项没有修改 `/Users/tiger/Projects/anti/llm-proxy`。该服务只为 F04 提供现有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`；模型请求不会接入它。
