# F04 llm-proxy 接口与共享契约增量

已依据本地 llm-proxy 的路由、handler、DTO、认证和服务代码确认网页接口，新增 Connector SDK 的独立 `web` 契约。当前继续 F04，尚未交付 Provider、宿主网络策略、Tools／产品及网页端到端测试，不勾选本项。

接口证据与归属见[网页边界](web-read-boundaries.md)。本地 llm-proxy 提交为 `a32407a678ea7f96d70b7557765e2a01262184d4`：搜索 `/tool/web_search` 返回直接结果；页面 `/tool/web_fetch_jina` 返回 code/data 外壳。不要套用现有 Expense OCR Provider 的协议或较新认证说明。

SDK `public-web-read-v1` 固定哈希为 `4a23e7257e9896ea361a13e981fd4fcfd9a41b3989d91bef7cbd8826f7f26024`，web_search／web_fetch 各有独立操作哈希。查询／结果数量、UTF-8 字节限额、原始请求／来源 URL、未知完整性和本地截断均有类型及验证；搜索结果只表示排名样本。规范化 URL 拒绝内嵌凭证、常见非公开 IP、替代数字 IP、非标准端口、控制字符等，并验证规范化后的长度；不执行 DNS，也不声称替代宿主或抓取服务的网络授权。

[初次针对性测试](evidence/2026-09-11-f04-web-contract/sdk-web-initial.log)五项通过；规范化膨胀长度检查随后纳入最终测试。[最终 SDK 完整 race](evidence/2026-09-11-f04-web-contract/sdk-full-race.log)六个有测试包通过，根包 2.368 秒，包含外部临时 Go module 的公开编译覆盖；[vet](evidence/2026-09-11-f04-web-contract/sdk-vet.log)通过。SDK 构建版本为 v0.1.0-dev.19，根 Connector contract-v16、日历和邮件契约不变。

首轮完整测试发现根 Identity 断言仍期望 dev.18，修正后通过。原重定向日志在错误目录执行移动后被覆盖，保留的[控制台错误摘录](evidence/2026-09-11-f04-web-contract/sdk-full-race-initial-observed.txt)明确标注为摘录，未冒充完整原始日志。

[机器证据](evidence/2026-09-11-f04-web-contract.json)记录当时 SDK 源码、上游接口文件、边界与日志 SHA-256。后续 Provider、Integration、Tools 和产品已交付。2026-09-12 明确接入范围仅为两个既有 Web 接口，先前服务端修改已撤回；Domainry 侧保留私有凭证注入、请求／响应限制与来源策略，见[范围纠正](testing-2026-09-12-f04-web-scope.md)。
