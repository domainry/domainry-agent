# F06 日程与邮件写入共享契约增量

已完成 Connector SDK 的两个可选写入协议，尚未接 Provider、Integration 账号写入用例、Tools、Agent 或网页。F06 保持未勾选，进度仍为 54／81；下一步按顺序实现 Google Provider，不进入 Microsoft、N01 或其他 TODO。完整职责分配见[写入边界](account-write-boundaries.md)。

| 协议 | 本次交付 |
| --- | --- |
| `calendar-write-v1` | `calendar_event_inspect`、`calendar_event_create`、`calendar_event_update`；确切目标／版本、日程类型与目标事件的完整参与者；显式通知策略、单次／系列修改、PATCH 的省略与清空语义；有界回执 |
| `mail-write-v1` | `mail_send`、`mail_reply`；显式 To／CC／BCC、主题和纯文本；当前原邮件与 Reply-To 核对；服务已受理、送达未知、可缺少 Message ID 的回执 |

日程契约 SHA256：`7535ffee48efcb9bdd8686ac25a5b3f8c9aa9be39cbe6a4298f603e0f66d5001`。邮件契约 SHA256：`18d472fe742b5cdc77a61827fb82b36b4bd1e604b57d8826a70824582f8cbd57`。原 `calendar-read-v1`、`mail-read-v1` 和通用 SDK 的身份保持不变，现有 Provider 尚未注册这五个新操作。

两个包只复用各自已有公开 read DTO 和校验，不导入具体 Connectors／Integration／Agent。发送人、原生 Header、关联幂等标识从可信调用上下文取得；JSON 输入拒绝 token、任意 Header／MIME、SQL、原生附件等未声明字段。请求上限 1 MiB，保留 64 KiB 文本在 JSON 最坏转义情况下的完整往返，不截断后继续写入。

日程创建要求偏移与 IANA 时区一致，拒绝无偏移、夏令时不存在时间、混用日期与时刻、全日跨时区和非正时长。两个明确偏移表示的夏令时重叠时段可用。修改要求一个实际 ETag，拒绝通配符和标签列表；空 description／location 或空参与者数组表示明确清空，省略字段不会被替换。修改前查询不能用截断参与者或未知事件状态作为确认依据。系列与实例范围分开验证，实际 Provider 仍须使用原生条件写入；当前比较快照的函数不能代替该原子前提。

邮件校验覆盖总计 50 位收件人、跨 To／CC／BCC 重复、头注入、明确 BCC-only、UTF-8 与正文上限。回复要求当前原邮件元数据完整、非草稿、主题对应，并在 To 中列出原 Reply-To（没有时用 From）；可以加入明确列出的其他收件人，不自动扩成 reply-all。原生线程信息由后续 Provider 从原邮件读取。Gmail 可返回真实 Message／Thread ID；Graph 202 可以都缺失，回执不能声称 completed／delivered。

| 验证 | 实际结果与日志 |
| --- | --- |
| 日程首次编译 | 测试对函数返回值取地址失败，已改为局部值；[初次日志](evidence/2026-09-12-f06-write-contract/calendar-initial.log) |
| 日程契约初始身份固定 | 记录初始结构计算值；加入严格 JSON 总限额后重新固定最终身份，初值不作为最终契约；[初值](evidence/2026-09-12-f06-write-contract/calendar-identity.log)、[最终身份](evidence/2026-09-12-f06-write-contract/contract-identities.log) |
| 两写入协议最终专项 race | calendarwrite 1.747 秒，mailwrite 2.329 秒；覆盖公开类型、字段拒绝、时间／目标、系列、清空、收件人、来源与回执语义；[完整日志](evidence/2026-09-12-f06-write-contract/write-final-race.log) |
| SDK 全量 race | 8 包通过，包含原 calendar／mail／web、通用契约、contracttest 和外部模块编译；[日志](evidence/2026-09-12-f06-write-contract/sdk-full-race.log) |
| SDK vet／格式／独立边界／许可证 | 通过；[vet](evidence/2026-09-12-f06-write-contract/sdk-vet.log)、[其余检查](evidence/2026-09-12-f06-write-contract/sdk-boundary-format-license.log) |

外部模块编译来自 `TestConnectorCompilesFromOutsideTheRepositoryModule`，新增两个日程写入与两个邮件写入的公共泛型操作声明，并引用快照验证方法；没有运行厂商服务来代替 SDK 编译测试。机器清单固定本阶段源码与日志，见[证据清单](evidence/2026-09-12-f06-write-contract.json)。

原生语义已核对官方文档：[Google ETag 条件修改](https://developers.google.com/workspace/calendar/api/guides/version-resources)、[Google 创建与通知](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert)、[Gmail 发送](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages/send)、[Graph 创建与邀请](https://learn.microsoft.com/en-us/graph/api/user-post-events?view=graph-rest-1.0)、[Graph 发送受理](https://learn.microsoft.com/en-us/graph/api/user-sendmail?view=graph-rest-1.0)、[Graph 回复](https://learn.microsoft.com/en-us/graph/api/message-reply?view=graph-rest-1.0)。Graph 事件条件更新的真实租户行为尚未验证，具体边界记录在上面的架构文档。

本阶段没有 OAuth 或厂商调用、没有真正发送邮件或创建日程，也不是端到端完成证据。后续继续 Provider 和实际账号／确认／持久回执／网页矩阵；已有业务 Action／Workflow 在 F06 最终组合中回归。
