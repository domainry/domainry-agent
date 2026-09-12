# F03 邮件当前账号读取组合验收

Google／Microsoft × Module／SaaS 四组合均通过真实 HTTP、SQLite、实际 Provider 的邮件读取 race。本次没有修改 Integration 业务实现：F02 的当前账号读取端口能直接执行新的版本化邮件契约和具体 OAuth 范围。F03 尚缺产品会话／网页／来源草稿验收。

## 组合与证据

- [四组合 race 日志](evidence/2026-09-11-f03-mail-owner/owner-race-initial.log)：测试 24.79 秒、包 26.881 秒；Google 每组合 4 次 OAuth HTTP、13 次邮件 HTTP，Microsoft 每组合 4 次 OAuth HTTP、9 次邮件 HTTP，共 60 次实际 HTTP。
- [Integration 完整测试](evidence/2026-09-11-f03-mail-owner/integration-full.log)：14 个有测试包通过，SaaS 包 9.615 秒；含原日历组合兼容。测试 Transport 从原日历夹具抽到中立账号读取夹具，保留原请求、私有字段注入和响应上限规则。
- [vet](evidence/2026-09-11-f03-mail-owner/integration-vet.log)、格式和 diff 检查通过；[源码与证据清单](evidence/2026-09-11-f03-mail-owner.json)。

每个组合实际执行：服务侧注册 OAuth 应用 → PKCE 完成账号授权 → 首次读取触发私有凭证刷新 → 邮件头列表 → 两页受权搜索 → 读取指定邮件正文／RFC ID／Reply-To → 缺失正文标明未完整 → 撤销 read 函数权限 → 关闭服务与数据库并重开 → 原请求仅返回敏感回执，新请求使用已持久化的新凭证。

随后以第二次 OAuth 会话只授予 Gmail metadata／Graph Mail.ReadBasic，分别确认正文及搜索的 read-access／read 请求在访问厂商前拒绝，邮件头列表仍可用；撤销原账号后再次请求被拒绝且 HTTP 计数不增加。不同用户、伪造查询身份和 payload 中伪造 scope 也不能越权。调用回执不含正文、查询、发件地址、原始邮件标识或凭证。

SaaS 使用真实服务 HTTP 与公开 remote SDK，并保留服务认证；产品测试入口注入固定 Identity principal 与实时数据策略，完整真实 Identity 登录将在产品 E2E 验证。本轮 OAuth 厂商服务为本地协议夹具，没有冒充真实 Google／Microsoft 账号。没有新增账号调用账本、邮箱表或跨服务实现依赖，所有测试服务随用例关闭。

下一步按 F03 完成 Agent／Work／PM 选装、工具可用性与页面、邮件摘要／事项提取，以及 Knowledge 草稿保存／编辑／导出和来源撤权。发送及其他外部写操作仍由 F06 交付。
