# F03 Microsoft 邮件读取增量验收

Microsoft Provider 已复用共享 `mail-read-v1` 完成列表、搜索和单封读取。当前继续 F03 的 Tools、产品和邮件／草稿端到端验收；本增量不能单独勾选 F03。

## 实现与边界

- Provider 1.2.0 增加 `mail_list`、`mail_search`、`mail_read`；三个操作与 Google 使用同一中立类型及固定 hash。仍使用既有 `executeWithRefresh` 和宿主 Transport；原 `sync_outlook_mail` 保留原行为。邮件实现没有导入 Google Provider 或日历业务逻辑，共享确定性文本／游标机制位于 Connectors 的内部包。
- 列表只请求邮件头，默认按收到时间倒序；搜索显式使用 Graph KQL，交由 Graph 执行搜索排序。两个操作均排除 body、bodyPreview、附件和扩展字段。返回范围明确包括已删除文件夹，不把它描述为仅收件箱。
- 每个请求均带 `Prefer: IdType="ImmutableId"`；单封额外请求纯文本，若 Graph 返回 HTML 则转换为有界文本。正文缺失、未知格式、不完整 HTML、UTF-8 字节截断均明确标记。原邮件／会话／RFC ID 和 Reply-To 地址保留；缺失元数据不猜值。
- `Mail.ReadBasic`／`.Shared` 只开放列表；搜索、正文要求 `Mail.Read`／`Mail.ReadWrite` 或对应 `.Shared`，同时接受短 scope 和完整 Graph scope。`Mail.ReadBasic.All`、`Mail.Send`、`User.Read` 不会取得正文读取资格。具体当前账号归属和授予范围继续由 Integration 检查。
- 游标绑定账号、workspace、固定端点及完整查询。nextLink 必须保持协议、主机、路径、查询字段和页大小，只允许一个 `$skiptoken` 或正 `$skip`。保留 Graph 的实际 skip 值，不按页大小重新计算；换账号、改查询、重复续页和添加正文／其他用户路径均拒绝。累计达到 1,000 个搜索结果时返回 `complete=false`／`provider_search_limit`，不继续翻页或宣称匹配集完整。
- 失败不披露正文；发生 OAuth 刷新后即使返回了错误邮件 ID，完成的凭证轮换仍交还 Integration 持久化。

## 验证

| 验证 | 结果与证据 |
| --- | --- |
| Microsoft 整个 Provider 最终 race | 通过，3.413 秒；[最终日志](evidence/2026-09-11-f03-microsoft-mail/provider-race-final.log)。 |
| Connectors 全量 | 116 个有测试包通过；[日志](evidence/2026-09-11-f03-microsoft-mail/connectors-full.log)。 |
| Integration 全量兼容 | 14 个有测试包通过；[日志](evidence/2026-09-11-f03-microsoft-mail/integration-compatibility.log)。 |
| vet／架构边界／Catalog 生成一致性 | 最终通过；[vet](evidence/2026-09-11-f03-microsoft-mail/connectors-vet.log)、[边界与生成器](evidence/2026-09-11-f03-microsoft-mail/catalog-boundary-final.log)。 |
| Catalog 更新后读取验证 | 见 [Catalog 读取日志](evidence/2026-09-11-f03-microsoft-mail/catalog-read.log)。 |
| 源码及日志绑定 | [机器清单](evidence/2026-09-11-f03-microsoft-mail.json)。 |

公开 Registry／Call 测试覆盖：只读描述、metadata 列表、KQL 传递、两页续读、稳定 ID、回复目标、明确时区、HTML 回退、空正文与缺失正文区别、未知格式、Unicode 截断；40 × 25 条达到搜索上限；11 类恶意 nextLink、原始 skip=73 保留、账号变更、重复游标、无效／重复 ID、缺失元数据、取消前不出网、25 封大邮件头及接收人／名称缩短；基础／共享／发送／应用权限范围分离；刷新后转换失败保留轮换结果。

首次 Catalog 生成一致性检查发现生成文件未更新，[原失败](evidence/2026-09-11-f03-microsoft-mail/catalog-boundary.log)保留。已补两个 Provider 的邮件验证套件并重新生成 Catalog，核对仅 Google／Microsoft 条目及 SDK 构建版本变化，其他 Provider 内容保持原状；[变更审计](evidence/2026-09-11-f03-microsoft-mail/catalog-change-audit.json)。此前 Google 增量中的 Catalog 是包测试；本次补齐生成器检查。

## 下一步与限制

Provider 验证使用受控 Transport 协议夹具，不是实际 Microsoft 邮箱或产品邮件 E2E。没有启动新的常驻服务。当前进入 Tools／Integration 组合；需要继续验证工具的当前账号权限、执行前后／历史复核、产品选装，以及摘要、事项提取、Knowledge 草稿的原邮件来源与撤权后的读取／导出。

官方依据：[邮件列表](https://learn.microsoft.com/en-us/graph/api/user-list-messages?view=graph-rest-1.0)、[读取消息](https://learn.microsoft.com/en-us/graph/api/message-get?view=graph-rest-1.0)、[Graph 搜索上限](https://learn.microsoft.com/en-us/graph/search-query-parameter)、[稳定 ID](https://learn.microsoft.com/en-us/graph/outlook-immutable-id)、[邮件权限](https://learn.microsoft.com/en-us/graph/permissions-reference#mailreadbasic)。
