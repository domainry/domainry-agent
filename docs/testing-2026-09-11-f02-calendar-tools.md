# F02 日历 Tools 增量（2026-09-11）

已完成 Tools 适配，下一步为产品装配和页面／会话 E2E。F02 保持未勾选，进度仍为 50 / 81。

`domainry-tools/module` 公开 `CalendarAdapter` 与 `CalendarDefinitions`，注册 `calendar_accounts`、`calendar_list`、`calendar_events`、`calendar_event`、`calendar_availability` 五个只读工具。内部适配层只引用 Connector SDK 的中立日历契约、Integration SDK 账号端口和 Tools 自己的注册服务；不导入 Agent、Integration 或厂商 Provider 实现。架构测试新增对所有工具适配层跨服务实现依赖的禁止。

宿主分别提供当前工具授权及具体 Integration 账号动作的 Subject。模型参数不能设置身份、权限、scope 或凭证；所有输入由公开 Registry 严格校验。发现结果按操作筛选实际可读账号，分页绑定当前用户、工作区、操作与目录修订；不会修改 owner 返回的切片。执行前后重新检查工具策略和账号来源；历史结果同样复核。账号发现名称额外受当前列表权限约束，个人列表范围被撤销后旧名称不可复用，即使仍有其他读取权限。

请求 ID 绑定真实运行、调用、actor 和输入；Integration 敏感重放没有正文时返回明确失败，不伪造数据。日历与事件页保留游标和完整性，全天事件保留日期及排他结束日期，带偏移时刻保持不变。事件摘要和详情分别限制正文长度，返回缩短标志。忙闲结果再次按中立契约核算，未知／部分来源不能生成空闲；最多返回 50 个空闲区间，超出时给出后续窗口起点并标明未完整。

连接可用性预检只调用公开账号目录与 owner 授权，不调用厂商。只有忙闲授权的账号仍可用于账号发现和空闲工具，不能使正文工具可用。未配置 Integration 的产品可保留工具设置定义，预检及执行拒绝使用。

## 验证

- [公开门面初始 race](evidence/2026-09-11-f02-calendar-tools/public-race-initial.log)通过，包含四种实际 typed request、当前身份隔离、模型参数拒绝、分页、列表权限撤销、执行中撤权、账号／hash／修订变更、历史复核、执行身份与敏感重放。
- [最终 Tools 全量 race](evidence/2026-09-11-f02-calendar-tools/full-race.log)通过，包含可用性、原设置／注册／记录工具回归和加强后的架构检查；公开门面测试 2.942 秒。覆盖 DST 的 25 小时窗口、跨回拨时刻、全天事件、缺失忙闲数据、伪造空闲、50 区间续查及 25 项大文本页的输出上限。
- [vet](evidence/2026-09-11-f02-calendar-tools/vet.log)通过（无输出）。Tools 新模块目录本身没有 Git 元数据，`git diff --check` 不适用；[命令原始输出](evidence/2026-09-11-f02-calendar-tools/diff-check.log)保留，不能算成检查通过。修改的 Go 文件已 gofmt。
- [机器清单](evidence/2026-09-11-f02-calendar-tools.json)保存实现与直接日志哈希。

这是公开 Tools 门面和 SDK 端口夹具测试，尚不是实际厂商账号或 Agent 日历网页 E2E。按顺序继续本项产品接入，不进入 F03。
