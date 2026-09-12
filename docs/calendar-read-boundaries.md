# F02 日历读取的拆库边界

依据当前代码：Google 原 `sync_calendar` 只提供同步参数，Microsoft 原 `sync_calendar` 只调用 `/me/events`；Integration 的 `ConnectionAccounts` 只有列表／读取状态／固定探测／撤销，`CallProvider` 则是管理调用入口。不能直接把管理调用交给普通用户，也不能把 F01 的账号列表状态当作执行授权。

本项顺序：共享读取契约与 Google Provider → Microsoft Provider → Integration 当前账号受权读取 → Tools 适配及产品装配 → 端到端验收。以上步骤已依次完成，F02 已勾选；当前转入 F03。两种厂商协议分别在自己的 Provider 中适配，全天来源时区和分页完整性不交给 Agent 猜测。[最终产品及网页验收](testing-2026-09-11-f02-calendar-product.md)明确区分协议夹具与尚未具备的真实厂商账号环境。

| 所属层 | 职责与约束 |
| --- | --- |
| Connector SDK `calendar` | 中立的日历目录、时间窗口事件、事件详情、忙闲结果及独立版本／操作哈希。纯日期与带偏移时刻分开，日期结束排他；未知忙闲不产生空闲结论。只含类型、确定性时间规则，不含用户身份、账号、密文、数据库、网络或工具执行状态。 |
| Connectors | Google／Graph 自己完成厂商请求及响应适配，统一输出上述契约。新增读取操作保留既有同步语义；复用宿主 Transport／刷新，转换失败仍保留已完成的 SecretUpdates。Provider 之间不互相导入。 |
| Integration | 已增加独立当前账号读取端口：SDK 适配层转换 DTO，应用层通过内部领域类型及授权／调用端口编排，账号持久层检查当前 Subject 的个人／工作区归属、状态、实际 grant 和 Provider 声明。只接受已登记 call/read/hash；不能覆盖凭证、config 或 scope。执行前复核连接修订／Provider／操作契约，返回前再次核对账号。敏感正文不入 invocation，重放仅返回证据且 payload_available=false。管理调用不暴露给模型。 |
| Tools | 只通过 Integration 公开 SDK 和宿主当前身份／授权端口注册日历发现、事件查询、详情、空闲时间工具。具体账号和 calendar ID 来源于受权发现结果；工具定义、范围、输入、完整性和结果来源复核归 Tools。实现不查询 Integration 表，也不保存 token。 |
| Agent | 复用通用工具目录、调用、执行账本、预算和历史结果授权；不得新增 Google／Graph HTTP 代码或日历存储。 |
| 产品宿主／页面 | 通过公开模块门面与 SDK 组合 Tools、Integration 和 Identity；选择各产品 Agent／Skill 可见日历工具。沿用 F01 账号页面和工具开关，不增加产品私有 OAuth。 |

剩余实现必须检查：只具备 busy 权限的账号不得读取详情；账号权限和工具权限各自验证；缺失或部分分页必须保留游标／不完整状态；全天事件不能转成 UTC 零点；重复事件展开和例外保留稳定引用；身份／账号撤销后，已保存结果也要重新检查。Microsoft 返回的时区名称及全天日期需按官方 Graph 语义适配，不能套用 Google 字段或猜测 Windows 时区。

本项不增加写日程、发送邮件、后台计划或提醒，这些继续按 F03／F06／G 等后续 TODO 执行。真实厂商账号及组合部署验收按原文第三批和 H 项保留；本步协议夹具不冒充产品端到端完成。
