# F04 网页工具产品与端到端验收

本批于 2026-09-11 开始，收尾跨至 Asia/Shanghai 2026-09-12。Agent／Work／PM 已接入独立 Web 工具，真实 Identity、Integration、Tools、Agent、Knowledge、HTTP、SSE 和 SQLite 的产品链路已实现。F04 按单项实现与对应验证完成，下一项为 F05；历史增量清单保留其完成时的源码快照。

2026-09-12 范围纠正：本项只消费 llm-proxy 的 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`。先前超出此范围的 llm-proxy 服务端修改已全部撤回；本页产品链路及构建代码保持不变，历史机器清单中的服务端源码不再属于当前交付。见[纠正记录及针对性复验](testing-2026-09-12-f04-web-scope.md)。

## 代码归属

- Connector SDK 的 `web` 子包拥有共享查询／页面协议；Connectors 独立 `web/llm_proxy` Provider 适配两个现有 Web 接口。Integration 拥有工作区连接、私有凭证、当前授权与外部调用账本；Tools 仅经公开 SDK 读取。相关实现和底层验证见[接口](testing-2026-09-11-f04-web-contract.md)、[Provider／网络](testing-2026-09-11-f04-web-proxy.md)、[Tools](testing-2026-09-11-f04-web-tools.md)、[owner](testing-2026-09-11-f04-web-owner.md)。
- 产品通过 `Options.WebTools`／`Product.WebTools` 选装两个工具，`Options.WebConnectionKey` 固定服务连接。Agent、Work、PM 入口读取 `INTEGRATION_WEB_CONNECTION_KEY`；Integration 端的代理 origin、来源域名、凭证和工作区登记由服务管理员配置。没有个人 OAuth，没有从模型读取账号或认证参数。三种工具族在中立 `account_tools.go` 装配，八种选择组合覆盖 0／2／4／5／6／7／9／11 个工具及冲突拒绝。
- Work／PM 默认 Agent profile 升至 1.3.0，并分别新增 `work.web`／`pm.web` Skill。完整默认配置须经公开 profile 编译；新 Skill 要求保留查询、来源、读取时间和不完整提示，区分发布日期，不将网页指令当权限。请求保存的报告沿用 Knowledge 成果版本与源会话／运行授权。
- `WebResult.tsx` 展示排名片段、来源链接、请求和服务声明 URL、读取时间、警告、裁剪及未知完整性。React 转义文本，不执行页面 HTML／Markdown；链接仅渲染无账号密码的 HTTP(S)，新窗口采用 `noopener noreferrer`。部分 JSON／外层裁剪仍使用受限通用预览。工具设置与执行记录增加中文名称。
- Agent 执行和 Knowledge 应用层本批无需引入 Web 协议、Provider、Integration 数据库或彼此实现。原有通用来源复核直接保护回复、工具结果、报告、编辑和导出；架构测试继续限制执行层不得导入 Connector 业务子包或 Provider。

## 实际产品验证

[Identity 会话与反向场景 race](evidence/2026-09-11-f04-web-product/product-second-race.log)通过，包 160.376 秒。主场景 98.42 秒，2 次实际代理 HTTP、8 次模型 HTTP、零 OAuth；四种不足／失败场景共 60.30 秒。

主场景通过真实身份登录、强制改密及角色设置注册工作区服务，执行 `web_search → web_fetch → artifact_create → artifact_read → artifact_edit → artifact_export` 六步。模型协议夹具从每次实际工具反馈构造下一步，最终报告 v2 保留查询、来源 URL、读取时间、unknown 和厂商警告；实际下载逐字等于对应版本。SSE 存在 Web／成果回执且无服务 token。

关闭 `web_fetch` 后，旧回复隐藏，报告列表省略，读取／修改／新导出／已保存下载均拒绝。关闭并重开 Identity／Agent／Integration／Knowledge 绑定与数据库，偏好和报告版本保留；明确恢复后可读原报告。分别移除当前 Identity list／read 权限，同样拒绝；恢复角色权限后可读。第二个真实用户可以发现同工作区 Web 服务，但读取第一人的会话、报告、下载均为 404。撤销工作区连接后，既有内容隐藏，新会话不再发出代理 HTTP。

反向场景实际调用数分别为：空搜索 1、空正文 2、裁剪正文 2、503 失败 1。模型收到这些回执后给出明确不足／失败答复，不创建任何报告；失败没有自动重试。模型夹具不代表真实模型质量、时效判断或提示注入抵抗能力。

替代连接 65.25 秒通过（包 66.862 秒），见[独立 race](evidence/2026-09-11-f04-web-product/replacement-final-race.log)：宿主改选另一个独立凭证／工作区连接，重开宿主；新连接两个工具可用，原连接仍存在。旧报告 v1／v2 和原回复仍因 `web.source_invalid` 拒绝，历史授权不调用代理 HTTP。

## 实际构建网页

[浏览器宿主日志](evidence/2026-09-11-f04-web-product/browser-initial.log)通过，23.43 秒；[网页报告](evidence/2026-09-11-f04-web-product/browser-initial/report.json)八个场景、JavaScript 错误 0；[宿主审计](evidence/2026-09-11-f04-web-product/browser-initial/host-audit.json)记录 4 次实际代理 HTTP、10 次模型 HTTP、零 OAuth。运行使用本次构建前端和 Chrome／Playwright，未 mock 产品 API。

浏览器实际提交授权请求、展开来源卡片、核对安全链接、预览报告、编辑 v3、下载 [Markdown](evidence/2026-09-11-f04-web-product/browser-initial/web-report-v3.md)。继续验证工具关闭、完整重启、角色撤权时移除已显示内容、双用户隔离、裁剪不足答复及连接撤销。已人工查看[桌面来源卡片](evidence/2026-09-11-f04-web-product/browser-initial/web-run-sources.png)、[报告与下载](evidence/2026-09-11-f04-web-product/browser-initial/web-report.png)、[390px 答复](evidence/2026-09-11-f04-web-product/browser-initial/partial-mobile.png)、[390px 工具设置](evidence/2026-09-11-f04-web-product/browser-initial/revoked-mobile-settings.png)：文本可读、来源与未知状态可定位，弹窗不横向溢出。

## 初次失败与验证边界

[夹具编译初次日志](evidence/2026-09-11-f04-web-product/fixture-compile.log)和[产品初次 race](evidence/2026-09-11-f04-web-product/product-initial-race.log)记录测试组合误把 SDK `ProviderSet` 当切片，修正为公开 `Providers` 字段。[前端初次构建](evidence/2026-09-11-f04-web-product/frontend-build-initial.log)记录 warnings 的 TypeScript 回调类型缺失，已补 `unknown[]` 收窄并通过构建。替代连接[初次失败](evidence/2026-09-11-f04-web-product/replacement-initial-race.log)来自夹具复用已属于原连接的凭证，被 owner 正确拒绝；新连接现在登记独立 Secret，未放松凭证归属规则。并行启动的[首次 Web 全量](evidence/2026-09-11-f04-web-product/agent-full.log)编译了相同旧夹具，也仅在该场景失败，其他目标包通过；最终全量使用修正后的夹具。

实际 Provider 通过夹具 Transport 发出真实 llm-proxy 协议 HTTP，模型走真实模型适配 HTTP／SSE，但服务器均为隔离协议夹具。没有调用托管 llm-proxy／Parallel／Jina 或使用真实外部账号。生产 Integration Transport、实际 Module／SaaS、凭证加密／轮换、两个重启和失败重复领取的底层验证见 owner 增量；本产品夹具不冒充生产 Transport 的网络验收。远端 Reader 的 DNS、渲染与跳转策略仍由服务负责，当前来源域名和代理 origin 校验不证明那些内部网络行为；详见[治理边界](web-read-boundaries.md)。依赖发布、真实厂商配置及 H08 整体部署仍按原批次要求执行。

## 回归与机器证据

- Agent Web 全量、Product 装配、架构、profile 和 Web 入口：[最终日志](evidence/2026-09-11-f04-web-product/agent-full-final.log)。通过，Web 包 141.502 秒。
- [Work 全量](evidence/2026-09-11-f04-web-product/work-full.log)／[PM 全量](evidence/2026-09-11-f04-web-product/pm-full.log)通过，含完整默认 profile 编译及产品实际 HTTP；两个产品的全量 vet 通过。
- [前端 42 项状态测试](evidence/2026-09-11-f04-web-product/frontend-tests.log)通过。[Agent 构建](evidence/2026-09-11-f04-web-product/frontend-build-second.log)、[Work 构建](evidence/2026-09-11-f04-web-product/work-build.log)、[PM 构建](evidence/2026-09-11-f04-web-product/pm-build.log)通过；原有大 chunk 提示保留，没有构建错误。
- [Agent vet](evidence/2026-09-11-f04-web-product/agent-vet.log)及修正后[夹具 vet](evidence/2026-09-11-f04-web-product/fixture-final-vet.log)通过；[gofmt／差异检查](evidence/2026-09-11-f04-web-product/format-diff-check.json)通过。Work／PM 没有 Git 元数据，git diff 检查明确不适用，没有声称通过。

当时的[机器清单](evidence/2026-09-11-f04-web-product.json)合并四个 F04 阶段及本产品增量的源码，记录日志／截图／下载、三份构建和历史阶段清单哈希。该清单保持原始快照，其中 llm-proxy 服务端修改现已撤回；当前范围以[纠正清单](evidence/2026-09-12-f04-web-scope.json)为准，不用新的哈希覆盖历史证据。
