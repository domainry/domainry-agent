# F05 独立共享身份与业务服务验收

F05 已完成实际 Identity 独立进程、Runtime 业务服务和独立 Agent Web 宿主的组合验收。SDK、Runtime owner、产品环境装配、Report 及 Work／PM 默认工具的此前证据保留在 F05 下方。本次最终 HTTP race、七场景网页、截图与架构检查均通过；进度 54／81，下一项 F06。这里不包含 N01 的报表会话工具、真实模型质量或 H08 整体部署验收。

## 实际部署和代码边界

- 测试从相邻 Identity 源码构建 `cmd/identity-server`，在隔离 SQLite 和 loopback 端口启动实际进程。Runtime 与 Agent 分别通过公开 Identity SDK 连接同一 issuer／workspace／application。Agent 有自己的数据库，没有创建第二个本地 Identity 库。
- Runtime 只在 bootstrap 组合原 Record／Action／Workflow／Report owner，Agent 只消费公开 `businessrpc.Client`。每次请求保留当前主体、工具及实际业务权限检查；浏览器 Cookie 不进入业务服务，服务使用固定凭证。
- Identity 远程 SDK 没有可选的 `ProjectRoleCatalogPublisher`。受管测试部署先发布权限，再通过现有角色管理 HTTP API 创建角色、以返回的 schema hash 单独更新字段策略。组织管理员凭证仅供部署控制使用，产品不接收该管理员的登录 token。
- 独立 Identity 的字段策略需要对象／字段目录。测试部署从 Runtime 的声明中只提取 `objects` 到 Identity 配置，业务记录、Action、Workflow、Report 仍由 Runtime 持有。没有复制业务数据库或增加产品到 Identity 内部实现的依赖。此处验证读字段投影，不声称两套角色格式的 export 语义完全等价。
- 工具设置路由使用产品原有公开适配器。其 `tools:user_preferences` 权限源由受管测试部署明确发布，角色授权通过 Identity API 完成；借用 Identity 的产品仍不自行改写权限源。
- 测试模型只确定工具调用顺序；记录 ID、动作／记录版本、游标、确认和写入回执全部来自实际 owner。该测试不代表真实模型质量、线上容量或整产品部署验收。

## 本增量修复

Identity 配置加载器原来把 `map[string][]string` 当作 `map[string]string` 设置，真实服务配置权限 owner 清单时发生反射 panic。新增准确的配置类型，并复用已有环境解析函数。实际配置加载、优先级覆盖、空配置清除与启动／远程 SDK race 已通过；没有放宽权限 owner 校验。

SDK 的空 Action 能力探测经过 HTTP JSON 后，空 `RawMessage` 变成 `null`，Runtime 因此拒绝了工具目录。服务适配器只对全空的授权探测恢复 nil；具体 Action 参数和写入调用保持原样。新增 HTTP 测试覆盖空探测、具体 null、大整数和零写入，SDK 全量 race／vet 通过。契约仍为 `6faee392381def2a5122579ea66ad8c94c4a0800e1d425696995d813c6420a10`。

## HTTP 和并发检查

实际链路覆盖登录／改密、七个工具的当前授权、目录、三页客户和详情、确认前零写入、确认后单条写入、重复确认、字段／read 撤权后隐藏原回复、Report Summary、业务 owner 不可用，以及 Identity 进程和全部 Runtime／Agent binding 关闭后从原数据库重启。

| 检查 | 结果和原始日志 |
| --- | --- |
| 实际共享 Identity HTTP | 16.191 秒通过，263 次业务请求、2,457 次 Identity SDK 请求；[日志](evidence/2026-09-12-f05-business-service/explicit-quota.log) |
| 实际共享 Identity race，网页适配补齐前 | 130.787 秒通过，298 次业务请求、2,643 次 Identity SDK 请求；[日志](evidence/2026-09-12-f05-business-service/service-final-race.log) |
| 最终装配的实际共享 Identity race | 127.379 秒通过，301 次业务请求、2,656 次 Identity SDK 请求；[日志](evidence/2026-09-12-f05-business-service/service-complete-race.log) |
| SDK 全量 race／vet，空探测修复后 | 三个测试包及六个编译包通过；[race](evidence/2026-09-12-f05-business-service/sdk-probe-full-race.log)、[vet](evidence/2026-09-12-f05-business-service/sdk-vet.log) |
| Identity 配置、独立入口、远程 SDK race／vet | 1.774／1.548／1.795 秒；[race](evidence/2026-09-12-f05-business-service/identity-config-after-race.log)、[vet](evidence/2026-09-12-f05-business-service/identity-vet.log) |
| 原 Module 与实际 Report 回归 | 150.235 秒通过；[race](evidence/2026-09-12-f05-report-host/module-final-race.log) |
| 最终 Runtime application／装配／架构及 vet | 0.591／1.326／1.436 秒；[检查](evidence/2026-09-12-f05-business-service/runtime-final.log)、[vet](evidence/2026-09-12-f05-business-service/runtime-vet.log) |
| Agent／Identity 架构 | 均通过；[Agent](evidence/2026-09-12-f05-business-service/agent-boundary.log)、[Identity](evidence/2026-09-12-f05-business-service/identity-boundary.log) |

默认 Identity 应用限额为每分钟 1,200 次；本测试曾在 1,218 次 SDK 请求附近得到 429，导致旧来源不可用并停止运行，见[诊断](evidence/2026-09-12-f05-business-service/identity-http-diagnostic.log)。为集中完成确认、撤权和重启矩阵，隔离部署明确配置应用 12,000／公共 HTTP 15,000 次每分钟。生产默认值没有改动，也没有缓存权限或跳过来源校验。当前请求量意味着实际部署仍需按工作负载核定额度；这不是容量测试通过的证据。

## 构建网页验收

实际 Chrome 在动态 loopback 端口运行构建后的 Agent 网页，七个场景全部通过，总包 72.611 秒，共 821 次业务 HTTP 和 6,580 次 Identity SDK 请求，零 JavaScript 错误。该请求量包含前置 HTTP 矩阵和网页步骤，不是单次对话的请求量。[原始日志](evidence/2026-09-12-f05-business-service/browser-final.log)、[浏览器快照](evidence/2026-09-12-f05-business-service/browser-final/report.json)。

1. 实际网页登录共享 Identity，七个可选业务工具可用。
2. 网页读取已保存的目录、三页游标查询和详情；客户 Acme／Beta／Gamma 及余额来自真实 Record owner。
3. 网页请求改名，显示准确记录 ID、`customer.rename` 和 `Agent Renamed`；实际记录仍是原名，确认前没有写入。
4. 关闭并重新启动 Identity 进程及全部 Runtime／产品 binding，原待确认 ID 与参数不变；在网页确认后只修改准确的一条记录。
5. 当前字段／read 权限撤销后，页面明确显示“相关内容已隐藏”；原正文消失，恢复原权限后重新显示。
6. 业务服务不可用时旧回复隐藏，恢复后重新校验原来源，没有本地数据回退。
7. 完成后再次重启全部宿主，原回复和实际记录修改保留；390px 页面无横向溢出。

已人工查看[查询截图](evidence/2026-09-12-f05-business-service/browser-final/business-query.png)、[确认截图](evidence/2026-09-12-f05-business-service/browser-third/business-confirmation.png)、[撤权截图](evidence/2026-09-12-f05-business-service/browser-final/business-field-revoked.png)和[390px 截图](evidence/2026-09-12-f05-business-service/browser-final/business-mobile.png)。第一次完整通过时，撤权截图仍在加载中；虽然 API 已拒绝读取，但不能当作网页证据。已加强断言并完整补跑，最终截图确认隐藏提示与页面稳定状态。前一次通过日志及截图保留，不用加载画面支持最终结论。

在 Runtime 目录执行 `RUNTIME_BUSINESS_SERVICE_ACCEPTANCE=1 go test -race ./runtime/bootstrap/integrationtest -run '^TestConversationBusinessProductWithSharedIdentityService$' -count=1 -v` 可复跑实际进程矩阵。网页另设置 `RUNTIME_BUSINESS_SERVICE_BROWSER=1`、`AGENT_NODE_BINARY`、`AGENT_PLAYWRIGHT_MODULE`、`AGENT_UI_TEST_OUTPUT`；`RUNTIME_BUSINESS_SERVICE_EVIDENCE_DIR` 指定 Identity 进程日志目录。工作区需能解析 Agent／Identity 等相邻模块，网页使用现有 `frontend/dist`；本轮 Runtime 使用 `/tmp/domainry-f05.go.work`。

## 失败记录与验证范围

保留了首次编译遗漏、Runtime 角色包含 Identity 管理权限、owner 清单缺项、Identity 配置 panic、错误选择管理员角色、远程 SDK 不提供角色发布、空探测 null、字段目录缺失、实际 429 等初次日志。网页初次装配遗漏工具设置路由得到 404；补路由后缺部署权限得到 403。两者都修正在测试部署，不改生产授权判定。

原 Report 阶段机器清单是历史快照，早于空 Action 探测与本服务夹具修复。[本阶段机器清单](evidence/2026-09-12-f05-business-service.json)重新固定当前源码、构建树与日志。llm-proxy 保持无改动，F04 仍只使用其两个现有 Web 接口。测试进程和浏览器均已关闭；没有新建分支或 worktree，没有修改 Report 模块缓存。Work／PM 的默认工具与环境装配检查沿用对应阶段记录，本次七场景浏览器使用公共 Agent Web 宿主，不声称分别执行了 Work／PM 页面。
