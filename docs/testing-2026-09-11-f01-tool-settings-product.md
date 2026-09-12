# F01 工具设置与账号可用性：产品增量验收

本批严格继续 F01，未实施 F02。Tools 持久开关已接 Agent、Work、PM，覆盖实际 Identity、Module HTTP、模型调用、账号授予范围、确认、旧结果、重启和浏览器操作。真实 Google／Microsoft 应用及账号仍未配置，OAuth issuer 和模型是协议夹具；不能据此宣称真实厂商授权通过，F01 保持未勾选。

## 所有者与依赖方向

| 边界 | 当前实现与职责 |
| --- | --- |
| Tools SDK／Tools | [中立契约](../../domainry-tools-sdk/settings.go)、[策略](../../domainry-tools/internal/application/preferences/service.go)、[ORM 存储](../../domainry-tools/internal/infrastructure/persistence/database/preferences/store.go)、[HTTP Adapter](../../domainry-tools/internal/transport/http/preferences/adapter.go)拥有偏好。只接受当前受权目录内的工具和匹配的版本／修订号；按 runtime、workspace、user 隔离，角色变更不重置。公开 Module 借宿主唯一迁移账本，不开新池，不读取 Agent／Integration 表。 |
| Agent 应用层 | [BindToolPolicy](../internal/application/conversation_service.go)在最终 Agent／Skill 白名单确定后、worker 启动前提供未应用偏好的目录，消费 Tools SDK Availability。目录不能回接已经过滤后的自身。Agent 保留实际执行、确认、恢复、结果与当前权限复核。 |
| 产品宿主 | [设置组合](../internal/assembly/web/tool_settings.go)仅在装配层调用 Tools 公开 Module；[账号条件](../internal/assembly/web/tool_accounts.go)只经 Identity SDK evaluator 和 Integration SDK 安全账号接口计算必要 Connector／Provider／scopes。多个账号条件是备选条件，每项 scope 都必须实际授予。此预检不能选择执行账号或授予业务动作权限。 |
| Integration | [安全状态](../../domainry-integration/internal/infrastructure/persistence/database/integration/connection_readiness.go)在账号归属过滤后读取本地凭证状态，不解密、不调用 Provider、不刷新。新增 owner 迁移 v6 保存 OAuth 实际 grant，与凭证、账号和会话回执同事务提交。列表先关闭 cursor，再读取状态，支持单连接宿主。 |
| 共享前端 | [Tools 浏览器 SDK](../../domainry-tools-sdk/browser/src/index.ts)拥有路径和载荷；[工具设置弹窗](../frontend/src/ToolSettingsDialog.tsx)提供搜索、状态、开关和显式刷新。响应不明或修订冲突后阻止连续写，刷新读取实际修订。Agent／Work／PM 各自的 shell 组合这一组件。 |

工具开关不是角色权限；设置许可与具体工具许可分别授予。宿主只登记 `tools.preferences.list/update` 定义，管理员须通过固定的显式 Identity 编辑动作启用，不会在启动时重新赋权。HTTP 使用当前身份与 Owner 范围，拒绝浏览器提供 user/workspace、缺少布尔或修订字段、旧定义版本和未知工具。

账号 `status=active` 不再直接等同凭证可用。安全状态区分已配置、配置不完整、凭证失效／过期、Provider 不可用及配置变化。只将 OAuth 换码实际返回的 scopes 作为范围证据，不把应用允许列表或请求范围冒充实际 grant。旧 owner 未提供 readiness 时按未知处理；迁移前没有 grant 证据的账号不能满足需要 scope 的工具条件。

状态表示当前本地配置事实，不能证明厂商仍授权或远端在线。现有 Provider 在实际请求遇到过期 access token 时按既有刷新协议处理；管理员为托管凭证设置的本地截止时间继续拒绝使用。外部连接执行时必须再次核对当前账号、权限、凭证和具体操作，不能依赖先前的列表快照。

## 直接验收证据

- [真实 Identity HTTP](../internal/assembly/web/tool_settings_test.go)：无权限拒绝、设置权限不能授予工具、最终 Agent 白名单、生效的计算结果、关闭后模型目录移除、条件写与旧请求冲突、伪造 owner 拒绝、重启、撤权／恢复保留偏好、OAuth 真实 grant 与缺少 scope、个人／共享范围、当前账号权限撤销。最终日志 [identity-http-final.log](evidence/2026-09-11-f01-tool-settings/identity-http-final.log)，两项测试包 5.565 秒。
- [确认跨重启](../internal/assembly/web/tool_settings_confirmation_test.go)：产生等待确认后关闭 memory_save，原确认立即返回 403；完整宿主重开仍拒绝，重新开启后原确认完成，同一 Run 仅写入一条实际个人记忆。
- [统一来源复核](../internal/application/conversation_sources.go)：关闭本地工具后，其历史回复替换为来源不可验证提示，不暴露原计算结果。此前仅业务／知识分支检查连接，本次统一到所有完成的工具结果入口。[冻结恢复](../integration/conversation_tool_catalog_integration_test.go)验证断开期间不把已接受结果交给模型，恢复后原效果只执行一次，日志 [frozen-resume.log](evidence/2026-09-11-f01-tool-settings/frozen-resume.log)。
- Integration [状态测试](../../domainry-integration/internal/infrastructure/persistence/database/integration/connection_readiness_test.go)覆盖实际授予与请求范围分离、凭证停用／过期、恢复配置、其他用户拒绝、撤销与敏感字段不外露。Module／SaaS OAuth 流程验证首次和重复回调返回相同已提交回执；日志 [readiness-receipt-fix.log](evidence/2026-09-11-f01-tool-settings/readiness-receipt-fix.log)。
- Agent 工具设置浏览器 6 场景：显式启用、当前目录、真实模型输入、重启和另一用户、丢失响应后读原修订、并发仅一次成功、撤权／恢复、账号 grant／撤销、390px 搜索与开关。脚本 [tool-settings.browser.mjs](../frontend/tests/tool-settings.browser.mjs)，最终 [报告](evidence/2026-09-11-f01-tool-settings/browser-verified/report.json)、[宿主审计](evidence/2026-09-11-f01-tool-settings/browser-verified/host-audit.json)、[日志](evidence/2026-09-11-f01-tool-settings/browser-verified.log)。
- Work、PM 分别 3 场景：使用各库实际产品定义、Agent／Skill、Identity、Tools、数据库和交付的前端；启用产品及设置权限、仅本产品工具、关闭后模型输入移除计算、整个产品重开保留偏好及手机重新开启。共享 [宿主](../testsupport/producttest/tool_settings_browser.go)由各产品自己的测试入口调用。最终 [Work 报告](evidence/2026-09-11-f01-tool-settings/products-browser-observed/domainry-work/report.json)、[PM 报告](evidence/2026-09-11-f01-tool-settings/products-browser-observed/domainry-pm/report.json)、[日志](evidence/2026-09-11-f01-tool-settings/products-browser-observed.log)。
- 账号网页原 8 场景随本批变更重新通过，JavaScript 错误 0、敏感 Referer 0，实际换码 2 次、连接探测 1 次。日志 [account-browser-regression.log](evidence/2026-09-11-f01-tool-settings/account-browser-regression.log)，[报告](evidence/2026-09-11-f01-tool-settings/account-browser-regression/report.json)。新构建的独立 Integration 可执行程序与产品 Identity 完成三次真实进程启动，日志 [production-integration.log](evidence/2026-09-11-f01-tool-settings/production-integration.log)，2.58 秒。

Agent／SDK、Integration／SDK、Tools／SDK、Work、PM 全量最终通过，见 [full-final-retry.log](evidence/2026-09-11-f01-tool-settings/full-final-retry.log)。专项 race 通过，Agent Web 包 74.935 秒、Integration SaaS 包 34.998 秒，见 [race-final.log](evidence/2026-09-11-f01-tool-settings/race-final.log)。[vet](evidence/2026-09-11-f01-tool-settings/vet-final.log)、[diff](evidence/2026-09-11-f01-tool-settings/diff-check.log)、[前端测试](evidence/2026-09-11-f01-tool-settings/frontend-tests.log)、[Tools SDK 浏览器测试](evidence/2026-09-11-f01-tool-settings/tools-client-test.log)、Agent／Work／PM 三套构建均通过；构建日志中的既有大 chunk 提示保留。实际数据库验收是 SQLite，三种数据库的迁移生成测试不冒充真实 MySQL／PostgreSQL 运行验收。

## 失败与修正

保留所有失败日志：首次在相邻库目录执行时未选择共享 go.work，编译引用了旧 SDK；设置测试最初误用不存在的 SecretInput.Status，改用所有者已有生命周期入口。首次 OAuth 回执缺少新 readiness 字段，导致重放字节不同，已在原事务内生成完整首次回执。确认夹具原期望关闭后仍接受，实际更早在 HTTP 返回 403；历史隐藏夹具原期望空文本，实际为安全提示，均根据真实行为修正。全量恢复测试原期望 tool_access_denied，本次统一来源检查更早给出 tool_unavailable；效果不重放断言保留。Work／PM 浏览器启动夹具漏了 Identity 改密的幂等头，补齐后通过。增强截图检查后的复验还暴露一次产品初始化导航竞争：脚本在页面重载前点击设置入口；仅等待一次导航仍会命中初始化的同地址导航；最终同时等待页面加载后的请求稳定，并记录实际导航及安全路径／状态码，再操作产品页面。原失败与报告保留。

截图初次截在开关过渡动画期间，可能与状态文案暂时不一致；最终脚本先核对 thumb 实际位置及弹窗边界再截图。已查看 Agent 手机图及产品截图，不把仅 aria 状态变化视为视觉验收。

完整源码与证据哈希在[机器证据](evidence/2026-09-11-f01-tool-settings-product.json)。原[Tools 模块增量](testing-2026-09-11-f01-tool-settings.md)与旧快照保留，不能将旧哈希当作本批最新源码。

## 严格接续位置

继续 F01 的最终账号接入审计：当前开发及上述产品协议 E2E 已交付，尚无真实 Google／Microsoft 应用与用户 consent，真实厂商授权／失效／刷新未验收。已有配置入口与服务端 OAuth 流程由 Integration 承担，不要求产品自行处理密钥。依赖发布、脱离 go.work 以及目标数据库／部署验收仍按 H 项处理。未新增分支、worktree，也未使用 Domainry builder skill。
