# F05 Report 宿主与产品默认工具增量

本阶段把现有 Report 查询接入 F05 的公共宿主端口，并补齐 Work／PM 默认业务工具选择。F05 仍未完成，实际共享 Identity 的跨服务产品／网页验收继续进行；没有推进 F06 或 N01。

Report 实现使用 Runtime 原来依赖的 `domainry-report v0.1.7`／`domainry-report-sdk v0.1.6`，没有改写模块缓存。Report SDK 已有 Summary 和 QueryObjectSQL；二者按已声明 report key 执行，QueryObjectSQL 不表示客户端可以提交 SQL。Agent SDK 可选 `businessrpc.ReportReader` 复用其公开请求／结果类型。Runtime application 声明窄的查询端口，bootstrap 适配 Report SDK 并投影逐次解析的当前 Identity principal；Report owner 检查报表、源记录和行字段权限。

Runtime Module 的 Agent 宿主绑定移动到实际 Report 绑定之后，服务 handler 与 Module 共用同一个装配函数。新增的两个 RPC 方法只委托 Report 自己的授权，没有制造尚不存在的报表 conversation tool 权限。客户端不能提交 SQL、ReportSubject 或浏览器 AccessToken；没有添加定义仓库、快照／导出命令或报表执行引擎。端口未装配时返回 unavailable。

新契约 SHA256：`6faee392381def2a5122579ea66ad8c94c4a0800e1d425696995d813c6420a10`。它覆盖原 16 个方法及新两个报表方法，替代前两阶段未发布的契约；旧机器清单保留历史。JSON 参数使用 `json.Number`，验证大于 JavaScript 安全整数范围的参数不损失精度；Report 数值结果保持原字符串。分页、游标、truncated、total semantics 不改写。

Work／PM 默认 profile 升至 1.4.0，选择 `business_catalog`、`query_records`、`get_record`、`query_related_records`、`invoke_action`、`workflow_start`、`workflow_get`。新增业务 Skill，要求实际目录／版本／游标、具体确认、结果未知时原操作核查，以及区分流程受理和完成。Agent 的 profile 编译加入既有业务工具定义；静态选择不创建业务 Source，不授予权限。共享产品测试同时检查七项默认选择。

| 验证 | 实际结果与日志 |
| --- | --- |
| SDK 报表与原 16 方法专项 race | 1.744 秒；数值／分页、当前主体、Report 拒绝、SQL／身份／token 注入、未装配、取消及写入不重放；[日志](evidence/2026-09-12-f05-report-host/sdk-second-race.log) |
| SDK 全量 race／vet | 三个测试包通过（根 1.503、businessrpc 2.173、persistence 1.730 秒），六个编译包通过；[race](evidence/2026-09-12-f05-report-host/sdk-full-race.log)、[vet](evidence/2026-09-12-f05-report-host/sdk-vet.log) |
| 实际 Runtime／Report／Identity／SQLite 与业务 owner 回归 | 报表 32.44 秒、原业务服务 34.83 秒，包 69.195 秒；本地与 HTTP 两种查询、稳定分页、游标跨报表拒绝、字段／read 撤权、未声明参数、跨工作区、重启；原 Action 丢响应与原键核查也通过；[race](evidence/2026-09-12-f05-report-host/owner-final-race.log) |
| Runtime application／装配／架构 | 0.528／1.201／1.188 秒；[日志](evidence/2026-09-12-f05-report-host/runtime-final.log) |
| Agent application／Product／架构／profile | 四包通过；[日志](evidence/2026-09-12-f05-report-host/agent-profile.log) |
| Work／PM 全量 | Work 装配 3.193 秒、PM 装配 1.619 秒，架构与其他编译包通过；[Work](evidence/2026-09-12-f05-business-product/work-business-profile.log)、[PM](evidence/2026-09-12-f05-business-product/pm-business-profile.log) |

首次 SDK 测试仍按 16 方法计数／旧哈希，修正为原 16 方法与两个 Report 方法分别验证。实际 Report 夹具先重复声明已有权限，随后预期了不存在的 Alpha 种子；核对真实种子 Acme 并去重后通过。初次与中间日志均保留，最终结论只取通过的命令。

本阶段实际执行了 Report 模块，但没有 report_query 工具或新浏览器验收。Report 工具、目录与来源规则依 TODO 在 N01–N03 验证；独立共享身份产品的权限发布、登录、确认和网页仍归当前 F05。[机器清单](evidence/2026-09-12-f05-report-host.json)固定本阶段源码与证据。
