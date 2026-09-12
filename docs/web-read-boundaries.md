# F04 llm-proxy 的两个 Web 接口接入边界

F04 只消费 llm-proxy 已有的 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`，分别提供 `web_search` 与 `web_fetch` 工具。Connectors 负责这两个接口的协议适配；本项不修改 llm-proxy 服务端，不引入其模型、聊天或其他接口。2026-09-12 已撤回先前越界的服务端修改，核对与证据见[范围纠正](testing-2026-09-12-f04-web-scope.md)。F04 产品验收已完成，后续仍按 TODO 顺序推进 F05。

## 已核对的接口

本地 `/Users/tiger/Projects/anti/llm-proxy`，提交 `a32407a678ea7f96d70b7557765e2a01262184d4`，调研时工作区无修改。接口以当前路由、handler、请求／响应模型和实际服务实现为依据，不从票据 OCR Provider 推测网页协议。

| 操作 | 当前 llm-proxy 实现 |
| --- | --- |
| 搜索 | `POST /tool/web_search`，请求 `objective`，可选 `search_queries`（最多 5 个，每个最多 200 字节）、`processor`（base／pro）、`max_results`、`max_chars_per_result`。响应直接为 `{search_id, results:[{url,title,excerpts}]}`，没有 `code/data` 外壳，也没有完整匹配集或分页契约。 |
| 页面读取 | `POST /tool/web_fetch_jina`，请求 `{url}`，响应 `{code:0,data:{title,description,url,content,warning?,usage}}`；handler 调用 Jina Reader 服务，不能把搜索响应的解码器直接复用到此接口。 |
| 凭证 | 本地路由使用 AuthMiddleware 和环境相关内部用户限制。此版本认证读取 Cookie 或 Authorization，再调用 Passport token 验证；不能据较新的 OCR 文档宣称本地版本已支持 `sk-` API Key。Domainry 通过宿主私有 Header 注入服务 token，用户／模型不能传 Header、Cookie 或凭证。 |
| 完整性与取消 | 当前 Jina 服务没有对外提供整页完整性证明；其调用使用默认 HTTP client，没有继承请求 context 或读取上限。Domainry 的请求取消／字节限制只能证明自身边界，不能冒充远端工作已停止或页面完整。 |

现有 llm-proxy 测试脚本将 401 视为路由可达；F04 验收不将其计为实际搜索成功。搜索 handler 的 Header 日志和认证失败日志属于现有服务实现；本项只核对接口，不改造该服务，也不把凭证写入新增证据。

## 归属与实现约束

| 归属 | 边界 |
| --- | --- |
| Connector SDK | 独立 `web` 子包和每操作身份，定义搜索结果、来源 URL、截断及未知完整性；URL 函数仅做确定性语法校验，不能进行 DNS、网络或账号授权。 |
| Connectors | 独立网页 Connector 的 llm_proxy Provider，适配上述两种协议。所有 I/O 使用宿主 Transport，固定服务路径；服务原点与允许的来源策略来自受管连接配置，模型不能覆盖。与 expense_ocr／llm_proxy Provider 不互相导入。 |
| Integration | 复用当前账号受权读取端口和敏感调用账本。服务连接由管理员登记为获准工作区账号，凭证仍在 owner 中；Provider 显式声明空 OAuth 范围替代项，只表示无需 OAuth scope，不代表无需用户权限、有效凭证或账号归属。 |
| 宿主网络 | 精确选择允许的代理 origin、固定两条路径、请求方法、超时、字节上限及私有凭证注入，禁止跨 origin 重定向。当前独立 Integration 的 WorkAccounts Transport 只允许 Google／Microsoft，不能让新的连接配置直接绕过它。 |
| 抓取服务 | llm-proxy／远端 Reader 拥有实际网页下载、DNS、子资源及重定向行为。仅允许代理 origin 或在返回后检查 URL，不能证明这些网络跳转全部受 Domainry 本地 Transport 控制。来源策略、限制与验收须据实际服务实现单独说明。 |
| Tools | 公开 web_search／web_fetch；从宿主明确指定的服务连接读取，校验参数并保留 URL、标题、查询／请求来源与 owner 读取时间，历史结果继续查原连接及当前权限。不要让新服务连接替代旧来源授权。 |
| Agent／产品 | 通用工具执行、来源审计与引用展示。时效信息注明抓取时间，搜索片段不冒充完整正文；页面内容是不可信资料。登录后资源走其账号 Connector，不透传浏览器登录态。 |

计划让产品独立选装两个网页工具，并由宿主配置所使用的 Integration 连接 key；未配置时注册为不可用，不借用用户的日历／邮件连接，也不在 Agent 中另建网页 HTTP 客户端。实现时仍须验证以上策略，不能以本文件代替测试。

## 当前共享契约

`public-web-read-v1` 的搜索默认 5 条、最多 10 条，单条片段总量默认 1,500／最多 8,000 字节；页面内容默认 16 KiB／最多 64 KiB。排名结果以 `ranked_results` 标明，页面来源完整性只有 unknown／partial，局部截断单独标记。输出保留请求 URL 与服务声明的来源 URL，后者不称为已验证的最终网络目标。

URL 规范化限制 HTTP(S)、标准端口、ASCII DNS／punycode，拒绝内嵌账号密码、常见非公开地址、替代数字 IP、控制字符、反斜线等，去掉仅定位用的 fragment。它不解析 DNS，也不把语法有效的域名当作可访问授权。

Jina Reader 的公开定位与 URL 转换机制可参阅[官方仓库](https://github.com/jina-ai/reader)。本项不依赖第三方声称替代本地代码验证，也不推断托管服务与开源版本的网络策略完全相同。

## Provider／宿主增量

上文服务表对应现有接口基线。先前对 llm-proxy handler、HTTP client、日志和认证中间件的修改已全部撤回，服务仓库恢复干净；旧阶段服务测试仅作历史记录，不计入当前交付。Domainry 侧的超时、限额、凭证注入和固定路径校验继续保留，它们不依赖服务端修改，也不证明远端 Reader 的内部网络行为。

Connectors 已新增独立 `web/llm_proxy` 与 `module.PublicWebProviders`；SDK 固定读操作身份来自生成 Provider Catalog。管理定义提供字段／说明投影，不替代 SDK wire identity。连接的 `allowed_source_hosts` 是 1–16 个精确公开域名；搜索过滤返回来源，页面请求与返回来源都检查，拒绝子域隐式放行。服务 processor 来自管理员配置。SDK 要求 read 使用 natural idempotency，这仅说明读取效果；Provider 不重试、不返回 retryable，也不保证上游重复调用免计费。

Integration 独立宿主通过 `INTEGRATION_WEB_PROXY_ORIGIN` 选装 Web Provider，并提供独立 Transport，只允许该原点的两个 POST 路径、受限 JSON 与私有 Bearer。Google／Microsoft 的网络允许范围未扩大。Module 宿主仍需提供自己的 Transport。当前已通过 Provider、Catalog、真实宿主 HTTP、取消与重定向检查；账号 owner、Tools／产品和网页端到端仍待后续完成，见[本增量验收](testing-2026-09-11-f04-web-proxy.md)。

## owner／Tools／产品完成增量

Tools 已通过中立账号机制选取固定工作区服务连接，两项 Web 工具不依赖邮件／日历适配；没有配置 key 时不可用。Integration Module／SaaS owner 已验证工作区双用户、零 OAuth、凭证加密／轮换、权限与状态变更和敏感正文不落入调用账本。敏感同步调用采用唯一插入领取：同一身份成功不重放正文，失败／运行中／崩溃也不能再次领取；新请求身份仍受当前授权。

产品选装与 `INTEGRATION_WEB_CONNECTION_KEY` 已接 Agent／Work／PM，默认 Skill 与来源卡片保留查询、URL、读取时间、裁剪及未知完整性。报告使用 Knowledge 既有成果版本，来源撤权和宿主替换服务连接均重新授权。真实 Identity 会话、网页和报告来源验证见[产品记录](testing-2026-09-11-f04-web-product.md)。本节是后续增量，前文基线与阶段限制保留为历史说明。
