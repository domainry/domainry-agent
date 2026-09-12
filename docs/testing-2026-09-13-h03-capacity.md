# H03 执行容量与外部超时验收（2026-09-13）

H03 已完成。Agent 对用户和工作区分别限制排队数与运行数，Scheduler 对持久触发积压设置 Runtime 级上限，外部模型、工具、授权、资料和调度派发都有 owner 明确的截止时间。容量判断依赖数据库中的持久计数与 guard，不依赖单进程内计数，多 worker 不会分别超额受理。

## Agent 执行容量

- Agent SDK 新增可选 `ConversationCapacityProvider` 和持久化 `ConversationCapacityRepository`。原 `ConversationService`、`ConversationRepository` 未扩方法，旧宿主保持源码兼容；启用显式配额而持久化端口缺失时，启动直接失败。
- 默认值为：每用户排队 8、每工作区排队 `max(128, workers × 16)`、每用户运行 `min(2, workers)`、每工作区运行 `workers`。standalone 和 Module 都可通过 `AGENT_CONVERSATION_MAX_QUEUED_PER_USER`、`AGENT_CONVERSATION_MAX_QUEUED_PER_WORKSPACE`、`AGENT_CONVERSATION_MAX_RUNNING_PER_USER`、`AGENT_CONVERSATION_MAX_RUNNING_PER_WORKSPACE` 配置。
- migration 19 为 run 和后台／计划 task 增加 `workspace_key` 与索引，并增加 `_agent_conversation_capacity_guards`。入队、恢复、交互继续、后台和计划任务受理都在同一数据库事务内锁定 workspace guard、统计当前用户／工作区容量并写入；稳定拒绝码为 `agent.conversation.user_queue_full`、`agent.conversation.workspace_queue_full`、`agent.conversation.user_execution_quota`、`agent.conversation.workspace_execution_quota`。
- 排队统计包含 queued run 和尚未转换成 run 的 queued task；task 转 run 在事务内完成，因此不会重复计数。旧版本遗留的少量 `workspace_key IS NULL` 活动记录只在迁移排空期间从权限 JSON 投影；新记录全部走索引列。
- worker 分页读取候选并跳过达到运行上限的用户／工作区，仍可领取其他工作区任务。过期租约接管不会额外占用一个运行名额，终态释放容量。

## Scheduler 触发积压

- Scheduler SDK 新增可选 `TriggerBacklogProvider`、`TriggerBacklog`、`ErrTriggerBacklogFull` 和能力标记；基础 `RunStore` 保持不变。`WorkerConfig.MaxPendingTriggers` 默认 10,000、最大 1,000,000。
- migration 8 增加 Runtime 级 `_scheduler_capacity_guards`。新窗口 Claim 和终态 Retry 都在自己的事务内锁定 guard，先保留既有窗口幂等语义，再统计该 Runtime 下 `leased` 与 `retrying` run；达到上限时不插入新 run。另一 Runtime 的容量独立，完成后可再次领取。
- Module 与 SaaS 将积压满映射为 HTTP 429。standalone 可通过 `SCHEDULER_MAX_PENDING_TRIGGERS` 配置，并通过可选投影读取当前 pending 与 limit。

## 外部服务超时

- Agent 的 `AGENT_CONVERSATION_EXTERNAL_CALL_TIMEOUT` 默认取 `min(2 分钟, run timeout)`，上限 5 分钟。每次模型生成／流式步骤、工具调用／核查、来源复核和 follow-up 投递都受该上限约束；工具定义可以给出更短截止时间，不能延长 owner 上限。工具目录和知识搜索最多 30 秒，当前授权／可用性检查最多 5 秒。
- 模型超时形成稳定 `provider_timeout`；只读工具超时形成 `tool_timeout`。外部写在截止时间内没有确定回执时仍走原有 `uncertain`／`external_result_unknown` 语义，不会因超时自动重放写操作。
- Tools Registry 继续负责执行工具定义声明的 `TimeoutMillis`。Scheduler 的 `DispatchTimeout` 默认 5 分钟、最大 30 分钟，并与定义中的更短 timeout 取最小值；定义未写 timeout 时仍有 Scheduler owner 上限。
- Integration 的 Public Web 宿主继续只允许 llm-proxy 的精确 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`，生产 HTTP client 上限保持 45 秒。本批用 25 ms 注入上限分别验证两个接口都能及时中止并返回脱敏错误；Integration 生产实现及 llm-proxy 服务均未修改。

## 端到端结果

真实 Web 组合使用实际 Identity 登录、Agent HTTP、SQLite 和 worker。单 worker 开始一个会一直等待的模型调用后，同用户第二个请求排队，第三个请求得到 HTTP 429 和精确响应 `{"code":"agent.conversation.user_queue_full"}`。外部调用在 500 ms 上限后以 `provider_timeout` 结束，已排队任务随后完成。关闭完整宿主、重新打开同一 SQLite、重新登录后，先前被拒绝的会话可以再次提交并完成。该场景随 Agent 全量测试通过，并单独复跑通过。

专项竞态测试还验证了并发入队只接受限额内请求、用户与工作区隔离、计划任务共享同一 Agent 排队容量、幂等重放在满队列下仍可读取原结果、饱和工作区不会阻塞其他工作区、模型和工具超时会释放 worker，以及 Scheduler 两个 worker 共享同一持久积压上限。

## 验证结果与边界

- Agent、Scheduler、Tools、Integration 及 Agent SDK、Scheduler SDK 全量测试通过；六仓 `go vet ./...` 与 `git diff --check` 通过。
- Agent 容量、Agent 外部超时、Web 端到端、Scheduler 积压与派发超时、Tools 定义超时、Integration 两个 Public Web 路径均通过专项 `-race`；Agent 架构边界专项也在 `-race` 下通过。
- Agent 架构测试确认应用层只经 Agent SDK 的可选持久化端口使用容量能力，容量 store 不引用 Runtime／Identity 实现或其表。Scheduler 只管理自己的 run／guard；Integration 和 Tools 各自保留网络调用与工具定义的超时职责。
- 本批使用 SQLite 验证数据库事务、重启和并发行为。PostgreSQL／MySQL 与实际多实例部署留在 H08，未用 SQLite 结果替代它们。
- Agent SDK 在隔离工作区和 `GOWORK=off` 下全量通过。包含当前未发布 Identity SDK 工作树的旧大工作区会使 `browsergateway` 因 `PrincipalResolutionRequest.Application` 契约漂移编译失败；原始失败已保留，依赖发布与公共契约兼容按顺序留在 H04，本批未越过 H03 修改 Identity 或发布锁。
- 两个 llm-proxy 工作树均为空。本项目只消费它已有的 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`，不把 llm-proxy 接成模型服务。

全部命令、日志、源码摘要和限制见[机器清单](evidence/2026-09-13-h03-capacity.json)，owner 边界及工作区检查见[架构审计](evidence/2026-09-13-h03-capacity/architecture-audit.json)。
