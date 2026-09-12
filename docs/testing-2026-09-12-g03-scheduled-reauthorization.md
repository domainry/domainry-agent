# G03 计划任务重新授权与确认验收

G03 已完成。Scheduler 仍只拥有计划、时间窗、重试和签名投递；Runtime 在收到已签名的计划事实后重新解析当前 Identity；Agent 再检查当前会话、允许动作、工具权限和外部连接，最后才把工作接入 L01 的同一后台任务队列。整个链路不保存或复用浏览器登录令牌。

## 架构边界

| 层 | 责任与依赖方向 |
| --- | --- |
| Scheduler / SDK | `ScheduledPlanDispatch` 只携带 plan、owner、输入、允许动作与会话引用。Scheduler 不导入 Agent，也不写 Agent／Integration 表。 |
| Runtime application | 新增通用 `AgentTargetRuntime` 端口；dispatch application 不认识 Scheduler 或 Agent SDK。 |
| Runtime composition | 唯一的 Scheduler → Agent 映射处。先通过 Identity SDK 解析当前用户，再把受限事实映射到 Agent SDK；不沿用 RoleKey、AccessBundle 或浏览器令牌。 |
| Agent / SDK | 新增可选 scheduled task 服务和精确 Runtime service Action。Agent 负责会话、Action、工具、连接状态、确认和执行账本。 |
| Integration / Tools | Agent Web 宿主继续通过已有公开 SDK 查询所选工具的连接可用性；账号状态和凭证仍由 Integration／Connector 管理。 |
| 存储 | Scheduler 保留自己的 run；Agent migration 16 只在 `_agent_conversation_tasks` 增加私有计划关联列，继续复用原任务 worker、Run、步骤、确认和幂等账本。 |

[架构机器审计](evidence/2026-09-12-g03-scheduled-reauthorization/architecture-audit.json)记录依赖扫描、凭证字段扫描、存储归属和发布门禁状态。

## 可信身份与重放

Runtime HTTP 验收构造完整 Scheduler v2 签名，签名绑定方法、路径、Runtime ID、幂等键、时间戳和请求体。请求依次经过签名网关、Runtime callback receipt、通用 target dispatcher、Identity resolver、G03 composition adapter 和 Agent scheduled service。首次返回 `agent-task / agent / accepted`；第二次发送完全相同的签名请求返回 `replay=true`，Identity 与 Agent 调用次数都保持一次。

计划 owner 的 `workspace_id + user_id + product_key` 只是待核对事实。Runtime 拒绝产品不匹配、Identity 当前 unknown／停用和用户已迁移到其他 workspace 的请求；Agent 还会通过产品自己的 Identity 应用范围再次解析身份。SDK 请求中没有 AccessToken、RefreshToken、Cookie 或其他浏览器凭证字段。

## 当前权限、连接与确认

Agent scheduled 入口要求精确的 `agent.scheduled_conversation_task.start` 服务 Action。没有该证据时，Module 和 SaaS 都在业务服务前拒绝；外层与内层 authority 不一致也会被 SaaS HTTP 拒绝。

接单前，Agent 读取当前注册目录，只从 Scheduler 的 exact allowed Action 中选工具，拒绝未注册 Action、`task_*` 递归工具、权限不符或当前不可用的工具。连接可用性只检查本计划选中的工具，所以无关账号断开不会阻塞计划。冻结的工具 key、version、Action、definition hash 和 authorization revision 随任务进入现有 worker，worker 在每次执行尝试前继续做当前授权检查。

实际 Web 组合测试先建立日历 ConnectionAccount，定时任务使用 `calendar_events` 并经现有 worker完成；相同窗口精确重放后，测试通过 Integration API 撤销该账号。下一窗口在接单阶段被拒绝，任务数量、模型调用数和 vendor 请求数都不增加。这个场景验证的是产品内真实 Integration／Tools 状态链路；按用户说明，没有另配 Google Workspace 或 Microsoft 365 厂商 OAuth 账号，本项不把本地服务夹具写成真实厂商 OAuth 验收。

写工具场景使用实际 Identity、Agent Web、SQLite 和成果存储。计划任务调用 `artifact_create` 后进入持久 `waiting_confirmation`，worker 已释放；等待期间撤销对应 Identity Action，再批准时被拒绝，`_agent_artifact_versions` 仍为 0。计划工作因此复用 C02/C03 的确认与恢复语义，没有单独的调度确认表或常驻 worker。

## 任务存储和执行

Agent 使用 owner + Scheduler idempotency key 生成稳定 task ID，并对整个规范化请求计算 hash。完全相同请求跨 store 实例返回同一任务和 replay receipt；同 key 改 goal、Action、时间或 owner 会冲突或不可见。计划关联列是 Agent 私有关联事实，不进入浏览器任务 DTO，也不授予读取权限。

计划任务允许只引用现有 conversation，不强制伪造 source Run；入队后由现有 `LaunchConversationTask` 生成普通 background Run，再由同一 lease worker 领取。SQLite 测试核对 migration、单行保存、owner 隔离、私有关联值、启动和 claim 都走原队列。

## 最终验证

| 范围 | 命令 | 结果与证据 |
| --- | --- | --- |
| Agent 整库 | `GOWORK=/tmp/domainry-g03-20260912/go.work go test ./...` | 全部通过。[日志](evidence/2026-09-12-g03-scheduled-reauthorization/agent-full.log) |
| Agent G03 race | scheduled SQLite、实际 Web 日历连接／确认、SaaS 精确 Action | 全部通过；Web race 204.479 秒。[日志](evidence/2026-09-12-g03-scheduled-reauthorization/agent-g03-race.log) |
| Agent SDK race | `GOWORK=/tmp/domainry-g03-20260912/go.work go test -race ./... -count=1` | 全部通过。[日志](evidence/2026-09-12-g03-scheduled-reauthorization/agent-sdk-race.log) |
| Runtime 签名整链 race | callback gateway、Agent target、Identity 映射、拒绝与 replay | 全部通过。[日志](evidence/2026-09-12-g03-scheduled-reauthorization/runtime-signed-callback-race.log) |
| Runtime 相关包 | application dispatch、composition、HTTP dispatch 全包 | 全部通过。[日志](evidence/2026-09-12-g03-scheduled-reauthorization/runtime-target-full.log) |
| 静态与边界 | Agent／SDK／Runtime vet、五仓 `git diff --check`、依赖和凭证字段扫描 | 全部通过。[Agent vet](evidence/2026-09-12-g03-scheduled-reauthorization/agent-vet.log) · [SDK vet](evidence/2026-09-12-g03-scheduled-reauthorization/agent-sdk-vet.log) · [Runtime vet](evidence/2026-09-12-g03-scheduled-reauthorization/runtime-vet.log) · [架构审计](evidence/2026-09-12-g03-scheduled-reauthorization/architecture-audit.json) |

Runtime 使用本地未发布 Agent SDK 运行 11-module 锁定启动时，门禁按设计报告 Agent capability digest `ee89b6...` 与当前 lock `8b896e...` 不一致。[失败日志](evidence/2026-09-12-g03-scheduled-reauthorization/runtime-local-lock-known-gap.log)保留该结果；没有覆盖已有 lock，也没有把本地 `go.work` 成功冒充发布验证。依赖发布、锁更新和脱离 `go.work` 构建由 H04 按 TODO 顺序完成，不影响本项本地公开 SDK 组合与签名执行链的结论。

本项没有修改 `/Users/tiger/Projects/anti/llm-proxy` 或 `/Users/tiger/Projects/devops/llm-proxy`。两个工作区均保持干净；F04 仍只消费现有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`，没有把 llm-proxy 接成模型服务。
