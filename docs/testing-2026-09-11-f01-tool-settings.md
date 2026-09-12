# F01 工具开关：所有者模块历史增量（当时尚未接产品）

后续已完成产品接入，当前证据见[工具设置产品验收](testing-2026-09-11-f01-tool-settings-product.md)。以下保留原模块阶段记录及其限制。

当时只完成 Tools 所属的设置契约、策略、持久化和公开 Module 入口，HTTP 适配器已编译通过。尚未接入 Agent、Work、PM，也没有工具开关浏览器端到端证据；F01 保持未勾选，下一步继续本项，不进入 F02。

## 已实现并验证的边界

- 中立 [Tools SDK](../../domainry-tools-sdk/availability.go) 拥有 Availability／Catalog／ConnectionAvailability，Agent SDK 只保留兼容别名，不要求 Tools 导入 Agent。设置契约包括工具版本、偏好修订号、开关与安全可用状态。
- [Tools 策略服务](../../domainry-tools/internal/application/preferences/service.go) 只依赖自身 Repository 端口和公开 Tools SDK。更新只能针对当前受权产品清单中的工具，旧工具版本拒绝，启用不会授予缺少的工具权限。显式连接策略不能为空，未知映射必须拒绝；可用性检查不递归读取已过滤的工具目录，不刷新凭证或执行业务操作。
- [持久化](../../domainry-tools/internal/infrastructure/persistence/database/preferences/store.go) 使用 ORM 和宿主数据库／Driver Profile。唯一键为 runtime＋workspace＋user 的哈希和工具 key；角色不进入偏好归属，升级工具版本不会重置关闭意图。初始修订为 0，写入用唯一键／条件更新原子递增修订；失败不能退回默认启用。
- [公开 Module](../../domainry-tools/module/module.go) 只把池、Renderer、Profile 和宿主唯一迁移账本传给私有装配层。新表 `_tools_user_preferences` 的 owner 为 tools，迁移 v1；Tools 不开新数据库池或迁移账本，不访问 Integration／Agent 表。
- 两个 owner HTTP 动作声明为 `tools.preferences.list`／`tools.preferences.update`，路径为 `/tools/preferences` 和 `/tools/preferences/{toolKey}`。当前 [适配器](../../domainry-tools/internal/transport/http/preferences/adapter.go) 通过 Identity SDK evaluator 按真实主体和 owner facts 授权，拒绝未声明字段和缺失布尔／修订输入；这部分目前仅编译／架构验证，真实 Identity HTTP 验收待下一步。

## 本增量直接证据

[策略测试](../../domainry-tools/internal/application/preferences/service_test.go) 验证清单撤权／恢复、不能启用未选工具、版本变更拒绝、个人／工作区／runtime 隔离、角色改变不重置、连接故障与取消拒绝。此测试的 Repository 和连接策略为夹具。

[公开 Module 实际 SQLite 测试](../../domainry-tools/internal/assembly/preferences/module_test.go) 验证 16 个同旧修订并发提交只有 1 次成功、15 次冲突；关闭再打开同库保留状态和修订，重复旧请求不能覆盖；其他用户默认不受影响，角色改变保持原偏好，明确最新版本更新成功，宿主账本只有 1 条迁移记录，数据库关闭后查询返回错误。

命令 `go test -race ../domainry-tools/... ../domainry-tools-sdk/...` 通过，日志 [owner-module-race.log](evidence/2026-09-11-f01-tool-settings/owner-module-race.log)，实际 Module 包 1.652 秒。随后补 HTTP 动作／适配器，`go test ../domainry-tools/... ../domainry-tools-sdk/...` 通过，日志 [owner-http-compile-retry.log](evidence/2026-09-11-f01-tool-settings/owner-http-compile-retry.log)。SDK／Agent 现有工具与目录兼容检查也已通过；尚不作为工具开关产品验收。

初次补 HTTP 依赖时误填了不存在的 Identity SDK 版本，已根据本地已发布 tag 与 Agent go.mod 改正为 v0.1.4；失败日志 [owner-http-compile.log](evidence/2026-09-11-f01-tool-settings/owner-http-compile.log) 保留。没有据此变更 Identity 源码或本地替换正式依赖。

## 严格接续位置

1. 在最终 Agent／Skill 白名单选择完成后、worker 启动前，通过中立目录端口将源目录交给宿主组合的 Tools 设置模块。不能把已过滤的 Agent 目录回接给 Tools 可用性，形成循环依赖。
2. 由宿主适配 Integration 的安全连接状态端口；Integration 仍拥有凭证过期、实际 scopes 和撤销事实。现有 ConnectionAccount.Status 只是持久连接状态，不能直接等同凭证可用；需要补 owner 的只读安全状态，不得在 Tools 中读 token／Integration 表。
3. 挂载 Tools 自有 HTTP Adapter，登记其动作；共享前端通过 Tools SDK 显示和修改偏好。新角色权限仍经 Identity 显式编辑。
4. 实际验证目录、模型调用、确认后执行、恢复／核查和旧工具结果的当前开关；补双用户／共享连接、重启、并发更新、响应丢失以及 Agent／Work／PM 产品组合 E2E。

这份记录是所有者模块的中间证据，不是 F01 完成声明。厂商真实应用和 consent、Tools 的完整产品接入及最终依赖发布均尚未完成。
