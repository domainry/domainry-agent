# F03 邮件读取与草稿边界（2026-09-11）

F03 的交付顺序为：共享契约 → Google → Microsoft → Tools／Integration 组合 → 产品、会话和网页验收推进；对应增量证据写在 TODO 的 F03 下方。

| 归属 | 实现职责 |
| --- | --- |
| Connector SDK 的 `mail` 子包 | 独立版本化的列表、搜索、读取 DTO；来源标识、查询方言、分页和截断语义；没有账号、网络或持久化。 |
| Connectors | Google MIME／Graph 邮件协议、当前 OAuth 操作范围、厂商游标与受控 Transport；正文转换为有界文本，附件不自动导入。既有 Gmail／Outlook 后台同步接口保持兼容。 |
| Integration | 复用 F02 当前账号读取端口。实时检查用户／工作区、账号状态、实际授予范围、操作与契约，管理刷新和敏感调用回执。 |
| Tools | 邮件账号发现、列表、搜索、读取的参数和结果适配；仅调用 Integration SDK。与日历共享机制时只抽中立的账号访问机制，不让两个业务适配器相互引用。 |
| Agent | 通用工具循环和来源审计。摘要／待处理事项基于实际读取；正文是外部资料，不能扩大工具权限。 |
| Knowledge | 邮件草稿保存为有版本的成果，保留原邮件标识及来源会话／运行依赖；通过原工具的结果授权复核来源。真实 Identity／HTTP／SQLite 和网页已验证：来源工具、列表／读取权限或账号撤销后，原回复与草稿的读取、预览、修改、导出及旧下载均受限。 |
| 产品装配／Skill／页面 | 选择工具和明确权限，展示账号可用能力、摘要、事项与“未发送”的草稿。应用配置由对应服务部署，不要求用户提供密钥。 |

列表只返回邮件头，基础／metadata 权限可以开放列表；搜索和正文读取要求相应完整邮件读取范围。搜索输入显式声明 Gmail 或 Graph KQL 方言，不把不同厂商语义包装成相同结果。分页未结束、厂商搜索上限、正文截断／缺失都要标明，不能据有限页宣称已处理整个邮箱。

草稿不是厂商邮箱写入；发送及其他外部写操作按 F06 的目标、内容、授权、确认与幂等规则另行交付。正文、token 和邮箱索引不能另存到 Agent 记忆或新建的邮件数据库中。

协议依据（已核对官方文档）：[Gmail 列表](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages/list)、[邮件结构](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages)、[MIME 正文](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages.attachments)、[Graph 列表](https://learn.microsoft.com/en-us/graph/api/user-list-messages?view=graph-rest-1.0)、[Graph 搜索](https://learn.microsoft.com/en-us/graph/search-query-parameter)、[Graph 单封读取](https://learn.microsoft.com/en-us/graph/api/message-get?view=graph-rest-1.0)。Gmail metadata 不允许 `q`；Graph 搜索最多返回 1,000 条，不能把达到上限的结果当作完整匹配集。

产品宿主通过 `CalendarTools`／`MailTools` 独立选装，中立装配一次注册选中工具并绑定每个工具族的可用性；未配置 Integration 时不可用，通用 Provider 读取端点不挂载到浏览器。Work／PM 的默认 Agent 与邮件 Skill 明确选择所需工具，不添加邮件专用数据库、Knowledge 邮件类型或跨业务适配器引用。
