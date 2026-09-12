# F01 完成核对（2026-09-11）

按 F01 原始条目逐项核对实现、当前源码及直接测试证据，本项完成。编号进度为 **50 / 81**，下一项 **F02**。本结论只覆盖连接管理这一单项；第三批“连接实际日历、邮箱和业务系统”的整批交付及 H 项仍未完成。

| 原始要求 | 当前实现 | 直接证据 |
| --- | --- | --- |
| 核对并复用已有 Provider | 复用 Google Workspace／Google、Microsoft 365／Microsoft；厂商授权、换码、刷新和固定探测仍在各自 Provider | [Provider 核对](testing-2026-09-11-f01-credential-lifecycle.md)、[最新两个完整 Provider 包及 SDK 测试](evidence/2026-09-11-f01-probe-scopes/owners-initial.log)、[生成一致性与依赖边界](evidence/2026-09-11-f01-probe-scopes/catalog-boundary.log) |
| 用户／工作区外部连接管理 | Integration 自有归属与密文；个人 owner 和共享 scope 分开，服务端当前身份过滤，不能由浏览器伪造 workspace/user，创建者不作为访问授权 | [归属 HTTP／Module／SaaS 与重启](testing-2026-09-11-f01-connection-ownership.md)、[当前真实 Identity HTTP](evidence/2026-09-11-f01-probe-scopes/identity-initial.log) |
| 授权 | Integration 管理员登记应用，用户选择 scopes，state 哈希／PKCE 密文／一次性换码；拒绝、超时、切换用户、回执丢失不能重放 code；与账号、grant 同事务提交 | [OAuth 协议与独立服务入口](testing-2026-09-11-f01-oauth-protocol.md)、[当前浏览器 9 场景](evidence/2026-09-11-f01-probe-scopes/browser-initial/report.json) |
| 凭证刷新 | 所有执行路径统一按调用前凭证版本条件保存；业务失败仍保存已成功刷新值，迟到刷新不得覆盖新值或恢复已撤销凭证 | [刷新矩阵](testing-2026-09-11-f01-credential-refresh.md)、[并发、后台、撤销、原子性与取消](testing-2026-09-11-f01-credential-lifecycle.md)、[当前 Integration 全量](evidence/2026-09-11-f01-probe-scopes/full.log)、[当前持久层 race](evidence/2026-09-11-f01-probe-scopes/race.log) |
| 状态 | 安全 DTO 区分账号状态、本地凭证配置、实际 grant 与独立测试条件；列表不解密、不探测；不足范围不得误报凭证失效或自动扩大授权 | [状态与工具设置](testing-2026-09-11-f01-tool-settings-product.md)、[测试范围修复](testing-2026-09-11-f01-probe-scopes.md)、[实际部分授予专项](evidence/2026-09-11-f01-probe-scopes/partial-grant.log) |
| 撤销 | 当前权限与版本核对，事务停用本连接凭证，拒绝外部引用共用；迟到刷新不能复活；响应丢失读取原撤销状态，重启保留 | [归属／撤销竞争与 HTTP 重开](testing-2026-09-11-f01-connection-ownership.md)、[当前网页撤销与重启](evidence/2026-09-11-f01-probe-scopes/browser-initial/report.json) |
| 工具开关 | Tools 自有持久偏好和 CAS；只操作当前受权目录，Agent 最终白名单后组合策略；实际调用、确认、恢复、旧结果重新核对 | [产品验收](testing-2026-09-11-f01-tool-settings-product.md)：Agent 6 场景、Work／PM 各 3 场景；[最新真实 Identity／Tools／Integration 范围独立与重启 race](evidence/2026-09-11-f01-probe-scopes/tools-probe-separation.log) |
| 开发和端到端测试、拆库边界、对应 TODO 下的证据 | 页面到真实 Identity、模块 HTTP、持久库及官方 Provider 的完整路径已覆盖；厂商和模型明确使用协议夹具。各 owner 仅经公开 SDK／模块门面装配，应用层不导入其他服务实现 | [最新全量](evidence/2026-09-11-f01-probe-scopes/full.log)、[产品回归](evidence/2026-09-11-f01-probe-scopes/products.log)、[最新 9 场景网页宿主审计](evidence/2026-09-11-f01-probe-scopes/browser-initial/host-audit.json)、三套前端构建和 vet 见[范围验收](testing-2026-09-11-f01-probe-scopes.md) |

## 纠正此前额外门槛

已核对 Git HEAD 中的原始文档：F01 要求连接管理的授权、刷新、状态、撤销、工具开关和 Provider 复用；文末明确“单项实现并完成对应验证后立即勾选；整批交付仍须满足全部范围和对应 H 验收”。“连接实际日历、邮箱和业务系统”是第三批交付标准。

此前增量由我额外加入“真实厂商账号通过后才能完成 F01”，将整批的外部环境条件提前设为单项门槛。本次纠正，不降低原始单项功能要求、不宣称真实厂商 OAuth 验收通过。用户已说明没有现成配置；Integration 已交付服务负责的应用登记、密文、运行配置和授权入口，不能凭空生成合法厂商客户端或用户账号。

真实厂商应用登记、可授权账号及对应日历／邮箱整段流程仍需在第三批及 H08 的实际部署验收中提供证据。之前报告里“F01 不勾选”的状态属于当时记录，以本次逐项完成核对为准；旧记录和失败日志均保留。没有新增对用户的配置确认要求。

## 证据完整性

本次复核 **232 条历史证据文件引用**，哈希全部一致；最新范围修复清单的 **36 个源码／文档快照**在本次完成文档更新前全部一致。见[哈希审计](evidence/2026-09-11-f01-complete/hash-audit.json)和[完成清单](evidence/2026-09-11-f01-complete.json)。最近完整回归包含上述当前 Integration 持久层、公开 Module、SaaS、Provider 和 Agent 用例；本次没有代码变更，不重复已通过测试或外部调用。

下一步仅进入 F02：Tools 拥有中立日历读取工具契约，Integration 拥有当前账号的受权调用，Connectors 拥有 Calendar／Graph 协议；Agent 处理工具执行和结果，产品宿主通过公开 SDK 组合。不提前实施邮件、写日程或调度。
