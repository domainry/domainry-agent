# F04 网页工具增量

Tools 已新增两个公开工具和固定工作区服务连接选择。F04 仍未完成：下一步验证 Integration owner 的真实 Web 读取、重启／撤权与敏感账本，再接产品、会话和网页端到端。

`module.WebAdapter`／`WebDefinitions` 通过独立 `internal/adapter/webtools` 消费 Connector SDK `web` 和 Integration SDK。不导入 Provider、Integration 实现、Agent、日历或邮件业务适配器。`ConnectionKey` 来自宿主，普通参数只有 query／url 及可选结果／字节限额。可用性仅列出匹配固定 key 的工作区账号，读取主体去掉 Personal 权限；未配置、个人连接、其他可用账号都不能冒充目标服务连接。

共享 `accounttools` 继续处理执行前后授权、读取请求身份、敏感重放和历史来源。为没有发现工具的业务适配器明确处理空 AccountsKey：空工具 key 不进入发现分支，未新增 `web_accounts`。三个业务适配器没有相互依赖，架构测试已覆盖 Web。旧结果按原 key、工作区、Provider、版本和操作契约检查；宿主改用新连接，即使新连接可用，也不能为旧结果授权。

Search 参数映射共享 SearchRequest，展示保留原 query、search_id、排名来源、片段和截断；Fetch 规范化 URL 后调用共享 FetchRequest，展示请求与来源 URL、unknown 完整性、警告和 UTF-8 限额。结果沿用 owner 的 read_at 与来源数组，不读取网页登录态或拼装凭证。工具说明明确将页面当作不可信资料、片段不是完整正文、抓取时间不替代来源发布时间，并说明重复请求可能再次计费。

六组新增公开入口测试覆盖两个实际调用契约／来源、固定连接与可用性、旧结果撤权／版本／工作区／连接替换、读取后的授权变化与敏感重放、模型注入／越界参数／非法来源以及十一工具共同注册。构造及权限测试使用公开 SDK 端口夹具；不是上游或产品端到端验证。

[首轮公开入口与架构 race](evidence/2026-09-11-f04-web-tools/tools-initial-race.log)通过，module 包 2.737 秒。随后将 Web 测试结果 DTO 独立于邮件测试，完成[Tools 全量 race](evidence/2026-09-11-f04-web-tools/tools-final-race.log)，五个有测试包通过，module 2.894 秒、架构 1.402 秒；[全量 vet](evidence/2026-09-11-f04-web-tools/tools-vet.log)通过。日历和邮件回归包含在其中。

Tools 目录没有 Git 元数据，不宣称 git diff 检查通过；已检查新增／修改文件的 gofmt 和文本尾部空白。[机器清单](evidence/2026-09-11-f04-web-tools.json)包含对应源码、日志和检查结果。当前继续 F04，进度仍为 52 / 81。
