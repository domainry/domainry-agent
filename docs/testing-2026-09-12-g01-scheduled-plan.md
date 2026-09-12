# G01 计划任务记录验收

G01 已完成。Scheduler 现在持久保存计划所有者、IANA 时区、一次性或重复触发规则、结构化输入、允许动作、执行目标、来源会话／Run、启停状态、版本和创建／更新时间。Module 与 SaaS 使用同一份公开 SDK 契约和同一个存储实现；产品无需导入 Scheduler 实现包。

## 架构边界

| 层 | 归属与依赖方向 |
| --- | --- |
| Scheduler SDK | 拥有部署中立的计划 DTO、`ScheduledPlanService`、校验和窄 repository 端口；不知道 Scheduler、Agent、Runtime 或产品实现。 |
| Scheduler application | 归一化记录、校验一次性／重复规则、生成 owner 范围的稳定 ID 和请求摘要；仅依赖 SDK 契约及端口。 |
| Scheduler persistence | 通过 domainry-orm 建表和读写；每次读写绑定 Runtime、workspace、user、product 和 plan ID，同一 owner 的 client ID 提供精确幂等。 |
| Module / SaaS | 两种部署均装配同一个 `ScheduledPlanStore`。SaaS 私有 HTTP 的 Runtime 身份由机器凭证绑定，调用方请求体不能改选另一个 Runtime。 |
| 产品 / Agent | 产品负责解析当前用户和执行目标；会话引用只是关联，不授予访问权。Agent、Runtime、Work、Todo 没有新增计划表或 Scheduler 实现依赖。 |

[架构机器审计](evidence/2026-09-12-g01-scheduled-plan/architecture-audit.json)保存边界和依赖扫描结果。

## 记录语义

- 一次性记录必须且只能保存一个 `at` 时间；重复记录必须且只能保存一个已校验的 `Schedule`。
- 每条计划显式保存 IANA 时区；拒绝进程本地 `Local`，重复规则的时区必须与计划时区一致。
- 输入必须是一个 JSON 对象且不超过 64 KiB；允许动作最多 64 个，去空白后不得为空或重复；目标不能另带 payload 绕开计划输入。
- 状态支持 `enabled`、`disabled` 和 `paused`。记录从 revision 1 开始，后续修改 CAS 留给 G05。
- `client_id` 在 Runtime + workspace + user + product 内幂等。相同规范化内容返回 `replay=true`；同一键换内容返回冲突；其他 owner 读取同一 plan ID 返回不存在。

## 端到端验收

实际 SQLite 和实际私有 HTTP 链路均通过：

1. SDK HTTP transport 使用 Runtime 机器凭证调用 Scheduler SaaS Server。
2. Server 从凭证取得权威 Runtime，再把产品已解析的 owner 交给 `DatabaseService`。
3. application 规范化并校验命令，数据库 adapter 写入 `_scheduler_plans`。
4. 相同请求重放只保留一行，内容变化返回 409；服务关闭并重新打开 SQLite 后，SDK Get 仍读到原重复规则、动作、输入和会话引用。
5. 改用另一个 workspace／user／product 或 Runtime 后无法读取原计划。

Module 端另行写入一条一次性和一条每周重复计划，关闭绑定、重开数据库后逐项读取一致。并发 race 测试用 16 个 goroutine 重放同一创建命令，最终只写入一条记录。

G01 的用户可见管理入口属于 G05，因此本项没有伪造公开浏览器入口。当前 E2E 覆盖正式 SDK → 私有 HTTP → SaaS service → application → SQLite，以及 Module Binding → 同一 application/store 的两条真实部署路径。

## 最终验证

| 范围 | 命令 | 结果与证据 |
| --- | --- | --- |
| Scheduler 整库 race | `GOWORK=/tmp/domainry-g01/go.work go test -race ./... -count=1 -timeout 8m` | 全部通过，含 architecture、Module、SaaS、HTTP、持久化和 remote 包。[日志](evidence/2026-09-12-g01-scheduled-plan/logs/scheduler-race.log) |
| Scheduler SDK 整库 race | `go test -race ./... -count=1 -timeout 6m` | 全部通过，含计划校验和 HTTP codec／错误映射。[日志](evidence/2026-09-12-g01-scheduled-plan/logs/sdk-race.log) |
| G01 聚焦跨层 race | `go test -race` 覆盖 application、database、Module、SaaS、HTTP 和 remote 的 `ScheduledPlan` 用例 | 全部通过；包含真实重启、并发写和双部署 E2E。[日志](evidence/2026-09-12-g01-scheduled-plan/logs/g01-e2e-race.log) |
| 静态检查 | 两仓 `go vet ./...`、三仓 `git diff --check`、禁止依赖扫描 | 全部通过。[Scheduler vet](evidence/2026-09-12-g01-scheduled-plan/logs/scheduler-vet.log) · [SDK vet](evidence/2026-09-12-g01-scheduled-plan/logs/sdk-vet.log) · [架构审计](evidence/2026-09-12-g01-scheduled-plan/architecture-audit.json) |
| Runtime 公开组合 | `GOWORK=/tmp/domainry-g01/go.work go test ./pkg/runtimehost ./runtime/bootstrap/composition` | 通过，证明公开宿主契约可组合。[日志](evidence/2026-09-12-g01-scheduled-plan/logs/runtime-composition.log) |
| Runtime 锁定启动 | `GOWORK=/tmp/domainry-g01/go.work go test ./runtime/bootstrap/runtime -run TestPinnedModuleSetComposesAllElevenBindingsOnOneHostDatabase -count=1 -timeout 5m` | 最终通过。[日志](evidence/2026-09-12-g01-scheduled-plan/logs/runtime-startup-final.log) |

Runtime 锁定启动第一次发现 Scheduler 公开 Module capability 摘要被 G01 的可选 plan 服务改动，和既有锁文件不兼容。该失败没有作为通过证据，并保留在[原始失败日志](evidence/2026-09-12-g01-scheduled-plan/logs/runtime-startup.log)。实现随后把可选 plan 能力保留在 Scheduler Binding／SaaS descriptor，恢复原 Module HTTP capability 摘要；同一个 Runtime 测试最终通过，未改 Runtime 锁文件。

G01 只建立记录，不宣称已经到点触发。时钟、并发领取、触发去重、失败重试、重启恢复和 misfire 补跑／跳过策略由紧接的 G02 实现。

本项没有修改 `/Users/tiger/Projects/anti/llm-proxy`。该服务只为 F04 提供现有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`；模型请求不会接入它。
