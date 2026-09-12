# F04 两个 Web 接口的 Provider 与宿主网络增量

这是 2026-09-11 的阶段记录；后续产品验收见[完成记录](testing-2026-09-11-f04-web-product.md)。2026-09-12 按用户明确范围纠正：仅接入 llm-proxy 的两个现有 Web 接口，先前的服务端修改全部撤回，当前交付保留独立 Web Provider 和 Domainry 宿主网络适配。核对结果与新证据见[范围纠正](testing-2026-09-12-f04-web-scope.md)。

llm-proxy 本地基线为 `a32407a678ea7f96d70b7557765e2a01262184d4`，当前已恢复该基线且工作区干净。先前为服务添加的请求／响应限额、Jina context、HTTP 重定向策略和凭证日志修改均不属于本项交付。Provider 仍直接调用原有的 `/tool/web_search` 与 `/tool/web_fetch_jina`；没有新增服务接口或修改其认证协议。

Connectors 新增 `providers/web/llm_proxy`，不导入票据或账号 Provider。`module.PublicWebProviders` 为独立选装入口，原 WorkAccountProviders 仍只返回 Google／Microsoft。请求固定映射 objective／processor／max_results／max_chars_per_result／source_policy 与 `{url}`，按上游两种响应外壳分别解码。管理员配置原点、精确来源域名数组和 processor；凭证只进入宿主私有 Header，模型参数不能覆盖这些字段。

来源策略在 fetch 前检查目标，在返回后检查服务声明的 URL；搜索过滤无权、非公开语法、重复和超量来源。子域不隐式继承授权。标题、片段、正文、描述和警告按 UTF-8 字节限制，过滤／缩短有标记，保留搜索 ID、规范来源以及请求 URL。页面完整性为 unknown；搜索片段不代表完整正文。Provider 没有免费探测接口，不伪造连接探测；空 OAuth 替代项仍需要 owner 账号和当前权限。SDK read 的自然幂等只描述效果，本 Provider 不自动重试、不报告 retryable，也不保证重复请求免计费。

Catalog 生成器已登记 Web Provider，并更新 SDK dev.19 的快照；生成 Provider Catalog 的两个操作 SHA 与 SDK 一致。管理定义是配置与显示投影，实际调用身份以 SDK／Provider Catalog 为准。首次 Catalog 测试发现 JSON 配置只允许 object；已支持明确的 array／object 声明，并按序列化后的实际形状校验，拒绝 JSON 字符串、null 和错误形状。公开元数据测试覆盖来源数组与旧对象形状。

Integration 独立服务新增 `INTEGRATION_WEB_PROXY_ORIGIN` 选装。独立的 PublicWeb Transport 只允许该原点的两个精确 POST 路径、16 KiB JSON、4 MiB 上限响应、必要 JSON Header 和私有 Bearer；拒绝改路径、编码路径、查询、fragment、Cookie、额外秘密字段以及 Header 冲突，关闭重定向和 POST 重放。它不向模型提供任意 URL 网络客户端，WorkAccounts 原策略保持独立。README 已记录宿主配置和 service connection 的归属，尚不声称完成服务账号 E2E。

验证结果：

- [Provider 首轮 race](evidence/2026-09-11-f04-web-proxy/provider-initial-race.log)：七项通过，1.691 秒，含真实 HTTP 搜索／页面和取消、三次调用无重试、来源策略、UTF-8 限额、协议错误与凭证链检查。
- [Connectors 全量](evidence/2026-09-11-f04-web-proxy/connectors-full.log)：118 个有测试包通过；[Catalog 最终](evidence/2026-09-11-f04-web-proxy/catalog-final.log)、[boundary／Catalog 生成检查](evidence/2026-09-11-f04-web-proxy/boundary-catalog.log)、[Provider／Catalog／装配 vet](evidence/2026-09-11-f04-web-proxy/provider-vet.log)通过。保留[首次 Catalog 形状失败](evidence/2026-09-11-f04-web-proxy/catalog-initial.log)。
- [Integration 宿主 race](evidence/2026-09-11-f04-web-proxy/host-race.log)：三个新增 Web 测试与原三个 WorkAccounts 测试通过，包 1.483 秒；真实生产 Transport＋公开 Provider 的搜索／页面各一次成功，重定向／越界／大小／取消另验。独立服务配置与原 OAuth 进程重启回归通过，包 21.846 秒；该重启用例不是 Web 账号重启验收。[宿主 vet](evidence/2026-09-11-f04-web-proxy/host-vet.log)通过。
- **已撤回修改的历史测试，不属于当前交付**：llm-proxy 的[四包 race](evidence/2026-09-11-f04-web-proxy/foundation-final-race.log)、[handler／middleware 编译](evidence/2026-09-11-f04-web-proxy/handler-middleware-compile.log)和[vet](evidence/2026-09-11-f04-web-proxy/proxy-vet.log)仅保留当时记录。初次 Go 1.26 的 Sonic 编译失败见[原始日志](evidence/2026-09-11-f04-web-proxy/foundation-initial.log)。这些服务端源码已经撤回，不能依据旧通过日志声称当前服务具有新增行为。

所有 HTTP 成功证据来自隔离协议服务器，未请求实际 Parallel／Jina 或部署中的 llm-proxy，也没有将 401 当成功。[机器清单](evidence/2026-09-11-f04-web-proxy.json)保留当时源码与日志 SHA-256，其中服务端源码部分已被 2026-09-12 的范围纠正取代；当前保留的接口适配不依赖这些撤回的修改。
