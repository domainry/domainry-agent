# F03 共享邮件契约与 Google 增量验收

当前仍为 F03。已完成共享契约和 Google 读取适配及其验证；Microsoft、Tools／产品、来源草稿和邮件端到端验收尚未完成，不能据本报告勾选 F03。执行边界见 [邮件拆库边界](mail-read-boundaries.md)。

## 交付与边界

- Connector SDK `mail` 子包发布独立 `mail-read-v1` 身份，SHA-256 为 `0d7e591567bb2e3211e32fd406fd376486b9857afaa17ec7517aa4bc947f1cb2`。新增 `mail_list`、`mail_search`、`mail_read` DTO／校验；查询显式声明 `gmail`／`graph-kql`，列表默认 10、最大 25，正文默认 16 KiB、最大 64 KiB。SDK 构建身份推进至 `v0.1.0-dev.18`，通用 `connector-contract-v16` 不变；三个操作分别固定 hash，外部模块可以独立编译消费。
- Google Provider 推进至 1.2.0，三个同步 read 操作通过公开 Registry／Call 注册；保留原 Gmail 后台同步、单封原始读取、发送契约。列表／搜索只获取 ID 和受限邮件头，不把正文或 snippet 放进列表。游标绑定 workspace、账号、端点、查询与页大小，换账号、改查询或重复厂商游标会拒绝。
- Google 的当前 OAuth 操作范围集中在 `oauth_operation_scopes.go`，由原 `calendar_scopes.go` 扩展并更名。日历规则不变；metadata 仅开放邮件列表，搜索／正文要求 readonly、modify 或完整邮件范围。旧 Gmail 原始操作、发送和同步没有因此取得 Integration 当前账号读取资格。
- MIME 与分页的确定性公共实现位于 Connectors 内部的 `mailcontent`／`mailpaging`，没有网络、账号存储或服务依赖。Google／后续 Microsoft 可以各自使用这些内部机制，不相互导入 Provider。HTML tokenizer／字符集解码依赖本地工作区已有的 `golang.org/x/net v0.58.0` 和 `x/text v0.41.0`，已声明正式模块依赖及校验值。
- 纯文本、HTML、常见字符集和 RFC 编码邮件头可读取；alternative 只选一种正文，related 按主正文选择；命名附件、附加邮件、图片、脚本、样式和远程地址不读取。单独存放的文本正文最多额外读取 4 个部分，每个最多 256 KiB；MIME 深度 16、访问节点 256。所有 I/O 仍由已注入的 Connector Transport 执行。
- 正文按 UTF-8 字节边界截断并标注；缺失正文、未知字符集、错误 base64、MIME 上限、外置正文过大等均不能宣称完整。发件日期只接受明确数字偏移或通用 GMT／UT，其他旧式名称保留未知；Gmail `internalDate` 单独保存为收到时刻。分页是否结束、邮件头是否完整和正文是否完整分别表述。
- 读取会话仅克隆本次凭证，多次 HTTP 复用最新刷新结果；后续格式或身份核对失败仍返还已完成的凭证刷新，同时不返回邮件正文。

## 验证证据

| 验证 | 结果与日志 |
| --- | --- |
| SDK 全量 race，含独立外部模块编译、固定身份、数据边界 | 5 个有测试包通过，[最终日志](evidence/2026-09-11-f03-mail-contract-google/sdk-race-final.log)。 |
| Google 完整 Provider 与共享文本／游标 race | 3 包通过，Google 1.641 秒；[最终日志](evidence/2026-09-11-f03-mail-contract-google/google-race-final.log)。首轮通过记录也保留。 |
| Connectors 全量兼容与架构／Catalog | 116 个有测试包通过，共 132 个包结果；[日志](evidence/2026-09-11-f03-mail-contract-google/connectors-full.log)。最终补充的 HTML 未闭合、发件日期校验另由上述最终 race 覆盖。 |
| Integration 全量兼容 | 14 个有测试包通过；Module 12.020 秒，[日志](evidence/2026-09-11-f03-mail-contract-google/integration-compatibility.log)。复用既有当前账号读取端口，无新增 owner 表。 |
| SDK／Connectors vet、diff／格式检查 | 通过，具体命令和源码／证据 hash 见[机器清单](evidence/2026-09-11-f03-mail-contract-google.json)。 |

公开调用测试实际覆盖：metadata 不泄露供应商额外返回的正文、搜索方言、跨账号／跨查询／列表与搜索间游标隔离、重复游标、空页和无效页、重复 ID、metadata 返回不同 ID、25 封大邮件头、接收人上限、Unicode 截断、MIME 替代／混合／关联结构、非 UTF-8 文本与编码头、未知编码、无效／缺失正文、外置文本上限与附件排除、取消前不出网、刷新后后续读取失败保留新凭证、冻结范围不能被调用方改写。

## 未完成与真实环境界限

本轮 Provider 测试使用 SDK 受控 Transport 夹具，Integration 全量沿用其已存在的真实 HTTP／SQLite 测试；这不代表新邮件链路已完成产品 E2E，也不是 Google 真实账号验收。用户已说明没有厂商配置，对应服务拥有 OAuth 应用部署与密钥；不会以此阻止本项剩余开发，也不会把协议夹具标为真实邮箱。后续仍须完成 Microsoft、Tools 实时授权／历史复核、Knowledge 草稿来源、产品选装和会话／页面端到端测试。发送能力按 F06 交付。

协议依据：[Gmail list](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages/list)、[Gmail message](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages)、[外置正文](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages.attachments)、[RFC 2387 主正文选择](https://www.rfc-editor.org/rfc/rfc2387.html#section-3.2)。
