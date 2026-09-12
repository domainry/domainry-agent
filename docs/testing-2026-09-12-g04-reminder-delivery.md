# G04 提醒投递与去重验收

G04 已完成。计划到点后由 Runtime 接受 Scheduler 签名事实，重新解析当前用户，把受限提醒事实交给 Notification；Notification 负责站内 inbox、用户偏好、外部渠道计划、重试和去重，Integration／Connector 负责具体通道调用与调用结果。Agent 没有新增通知表、worker 或具体渠道依赖。

## 架构边界

| 层 | 责任与依赖方向 |
| --- | --- |
| Scheduler / SDK | 保存和投递计划、owner、时间窗、输入及 exact allowed Action；不知道 Notification 的表、模板或 Connector。 |
| Runtime application | `NotificationTargetRuntime` 是来源中立端口；dispatch application 不导入 Scheduler SDK、Notification SDK 或任一实现模块。 |
| Runtime composition | 唯一的 Scheduler → Notification 映射处；复用 G03 的当前 Identity 解析，只把 `title`、`message`、到点时间和稳定身份映射成 Notification SDK intent。 |
| Notification | 已有模块继续独占 event、inbox、preference、channel plan、delivery reservation、retry 和去重状态；本项没有修改 Notification 实现。 |
| Integration / Connector | Runtime 只提交明确的出站交接；Integration 记录 provider invocation，项目 Connector 执行具体邮件或消息通道。 |
| 存储 | 每个数据库仍只使用宿主 `_schema_migrations`；Runtime 没有读取或写入 Notification／Integration 私有表，也没有宣称跨库原子事务。 |

[架构机器审计](evidence/2026-09-12-g04-reminder-delivery/architecture-audit.json)记录依赖扫描、实现仓状态、存储归属和发布门禁。

## 窄化提醒契约和当前身份

Runtime 只接受 target `owner=notification`、`operation=publish_reminder`，计划的 allowed Actions 必须精确等于 `notification.reminder.publish`。输入使用拒绝未知字段的 JSON 解码器，只允许非空且有长度上限的 `title` 和 `message`；计划不能指定 event type、收件人、workspace、连接、Provider、URL 或渲染后的载荷。

签名 payload 中的 owner 仍只是待核对事实。Runtime 使用和 G03 相同的组合授权缝隙，通过 Identity SDK 读取当前用户，拒绝产品不匹配、停用／未知用户和 workspace 变化。Notification intent 的收件人和 locale 来自当前 Identity，未沿用浏览器 token、旧 RoleKey 或旧 AccessBundle。

`workspace + plan + Scheduler idempotency key` 生成稳定提醒身份；同一时间窗使用相同 event ID、source event ID 和 dedupe key。到点时间进入 `occurred_at` 和模板变量，Scheduler execution ID 只作为关联 ID。

## 站内通知和已连接外部渠道

Runtime 提供内建、强制站内的 `scheduler.reminder.due` 事件类型，并从现有 12 个 locale catalog 编译展示内容。站内标题和正文只渲染已验证的提醒事实，历史 inbox 保存 Notification 生成的不可变快照。

外部渠道继续由产品通知规则显式绑定。项目为同一事件发布外部模板和 rule，并把 channel 绑定到已声明的 Integration connection；没有规则时只产生站内通知。Notification 读取用户偏好、生成稳定 channel plan、渲染模板并将出站请求交给 Runtime outbox。Runtime 不根据字符串直接调用邮件、Slack 或其他 Provider。

整链 E2E 使用一个公开 Connector SDK 的 `collaboration` fixture：签名回调经过 Runtime callback receipt、当前 Identity、Notification event／inbox worker、Notification channel worker、Runtime publication outbox、Integration delivery 和项目 Connector。测试观察到：

- 站内 inbox 只有 1 条 `scheduler.reminder.due`，收件人为当前用户，标题和正文与输入一致；
- Connector 收到 1 次编译后的消息，收件人和请求引用正确；
- `/notification/deliveries` 返回 1 条 `accepted` 交接记录；
- `/integration/invocations` 返回 1 条 `succeeded` 调用记录和 Connector response ref；
- 完全相同的签名请求返回 `replay=true`，inbox 和 Provider 调用仍各为 1。

Notification 自身的全量测试还覆盖偏好关闭时不进入外部 gateway、重新启用后的失败重试、稳定 plan identity、delivery reservation 和完成后不重复投递。这样，Runtime 回调重放与 Notification／Integration 各自的幂等账本共同覆盖进程边界，没有共享私有数据库实现。

## 最终验证

| 范围 | 命令 | 结果与证据 |
| --- | --- | --- |
| Runtime 相关整包 | dispatch、composition、runtime；排除已知 H04 lock 用例 | 全部通过。[日志](evidence/2026-09-12-g04-reminder-delivery/runtime-affected-full.log) |
| Runtime 通知 E2E | 计划提醒整链，以及既有 Action → Notification 重放／重启回归 | 全部通过。[日志](evidence/2026-09-12-g04-reminder-delivery/runtime-notification-e2e.log) |
| G04 race | target 路由、组合映射和真实整链 | 全部通过。[日志](evidence/2026-09-12-g04-reminder-delivery/runtime-g04-race.log) |
| Notification 整库 | `GOWORK=/tmp/domainry-g04-20260912/go.work go test ./... -count=1` | 全部通过，包含偏好、重试、去重、Module／SaaS 和持久化边界。[日志](evidence/2026-09-12-g04-reminder-delivery/notification-full.log) |
| Notification SDK race | `go test -race ./... -count=1` | 全部通过。[日志](evidence/2026-09-12-g04-reminder-delivery/notification-sdk-race.log) |
| 静态与边界 | Runtime／Notification vet、Runtime DDD／owner／SDK／schema 边界 | 全部通过。[Runtime vet](evidence/2026-09-12-g04-reminder-delivery/runtime-vet.log) · [Notification vet](evidence/2026-09-12-g04-reminder-delivery/notification-vet.log) · [边界测试](evidence/2026-09-12-g04-reminder-delivery/runtime-boundary.log) |

11-module 锁定启动仍只报告 G03 已记录的本地未发布 Agent capability digest 与 lock 不一致；Notification digest 没有新增差异。[已知失败日志](evidence/2026-09-12-g04-reminder-delivery/runtime-local-lock-known-gap.log)保留该门禁结果，依赖发布和锁更新继续由 H04 按顺序处理。

本项没有修改 `/Users/tiger/Projects/anti/llm-proxy` 或 `/Users/tiger/Projects/devops/llm-proxy`。F04 仍只消费已有 `POST /tool/web_search` 和 `POST /tool/web_fetch_jina`，不会把 llm-proxy 接成模型服务。
