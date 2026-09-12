# N01：报表工具、会话与产品验收

2026-09-12。本增量接续 [Report owner／宿主验收](testing-2026-09-12-n01-report-owner.md)，完成 N01；尚未实施 N02／N03。真实 Report、Identity、Runtime、Tools、Agent 和 SQLite 已接入 `report_query`；Tools 不依赖 Agent／Runtime 实现，Agent 应用层没有新增报表查询引擎。

本轮模型为**进程内确定性 SDK 模型夹具**，根据实际工具目录及返回的报表键、游标和行生成下一步调用与回复。它没有调用模型厂商 HTTP，也不是一次真实模型内容可靠性验收。HTTP、网页、权限、报表执行、业务更新、持久化与重启均使用实际代码。独立产品使用真实宿主 HTTP 和公开 businessrpc 客户端；同一测试进程内启动服务，不宣称新增了独立 Report SaaS。

## 实现及边界

- [Tools SDK](../../domainry-tools-sdk/report.go) 是工具声明的单一来源：`report_query`、`catalog/query` 操作、类型化参数、分页与有界输出。Agent SDK 只投影权限声明，声明权限不等于授予权限。产品角色还需取得 `agent.conversation_tools.report_query` 及实际报表／数据读取权限。
- [Tools 适配器](../../domainry-tools/internal/adapter/reporttools/adapter.go) 通过 Report SDK DTO 和窄宿主端口查询。目录用于发现当前获准报表，查询必须取得实际 `object_sql_v1` 结果和 owner 凭证并复核成功；校验通过、空占位响应、错误或超限结果均不能成为成功的报表查询。
- 具体调用由 owner 的对应目录、查询或结果复核接口授权精确请求；Tools 不再在每次调用前额外重读全目录。工具目录发现仍读取当前可见目录，工具权限、宿主存在性及所有 owner 授权继续检查，不使用授权缓存。专项测试验证减少目录调用后撤权仍拒绝查询和旧结果。
- 输出保存宿主来源身份、规范化原请求摘要和完整 owner 结果。旧目录重新读取比较，旧查询调用 owner 结果授权接口；复核不执行查询。宿主变更、内容篡改、撤权、定义或数据版本变化均拒绝旧内容。
- [Agent 通用组合入口](../internal/assembly/conversation/host_tools.go) 在宿主绑定、配置验证和 worker 启动前组合选装工具；保留已有组合回调，错误立即返回，拒绝重复或空工具键。Runtime 只有 [bootstrap](../../domainry-runtime/runtime/bootstrap/transport/conversation_report_tools.go) 导入 Tools 公共装配门面，application 只消费公开 SDK 和已有报表端口。
- [独立 Web 组合](../internal/assembly/web/report_tools.go) 通过 `ReportTools` 选装并检查当前来源可用性；未配置宿主时不公布可执行工具。Agent Web 与 Work／PM 默认选装，Work／PM Agent 定义更新为 1.6.0、业务 Skill 为 1.1.0。已有个人工具、账号读写确认与来源复核继续组合。
- 网页复用实际执行记录和来源访问控制，新增“查询报表”名称。报表专用表格／图表呈现属于 N03，本项没有提前实现。

详细责任表、完整性和保守失效语义见 [报表边界](report-query-boundaries.md)。

## 自动化与实际浏览器

证据目录：[本轮原始日志](evidence/2026-09-12-n01-report-tools/)。开发工作区为 `/tmp/domainry-n01.go.work`，没有创建分支／worktree，也没有修改模块缓存。

| 检查 | 结果与证据 |
| --- | --- |
| 两种会话部署整段 HTTP／最终浏览器 | 去除重复目录读取后[98.615 秒通过](evidence/2026-09-12-n01-report-tools/conversation-browser-optimized.log)：嵌入 Runtime 的 Agent 及完整 Chrome 流程 53.86 秒，独立 Agent 产品连接真实 Runtime businessrpc 44.10 秒；每次成功查询 2 次目录分页及 3 次结果分页。此前[112.569 秒 HTTP](evidence/2026-09-12-n01-report-tools/conversation-http-final.log)保留 |
| 权限、来源和持久化 | 两种模式均验证全部 owner／Identity／Agent 关闭重开，旧回复恢复；字段撤权隐藏旧回复；恢复权限后旧凭证仍失效，重新查询成功；实际客户动作将 Acme 改为 Updated Acme 后旧回复隐藏，新查询取得 Beta／Gamma／Updated Acme；业务读取撤销后目录不提供工具 |
| 嵌入式完整 race | 最终 [316.698 秒通过](evidence/2026-09-12-n01-report-tools/conversation-module-race-optimized.log)，覆盖同一完整 HTTP 场景的 3 次实际分页查询、重启、字段／读取撤权、恢复与实际数据更新；无竞争报告 |
| Tools 负向及边界 | [最终全量 race](evidence/2026-09-12-n01-report-tools/tools-full-race-final.log)，6 个测试包及 9 个编译包：有界严格 JSON、大整数原样、9 类非法输入、11 类结果篡改、目录变化、来源缺失／typed nil、授权拒绝、校验型占位结果、查询失败、结果过大及复核不重查；模块门面和依赖方向同时检查 |
| Agent 全量 | [18 个测试包及 18 个编译包通过](evidence/2026-09-12-n01-report-tools/agent-full.log)，包括会话执行、持久化、Web、架构、公共 module／remote／server |
| 通用组合及工具 Schema | [新增专项 race](evidence/2026-09-12-n01-report-tools/assembly-contract-race.log)：已有组合顺序、前后错误传播、重复键拒绝和 Agent 实际编译器检查；最终 Tools 改动后[组合／Web 再验](evidence/2026-09-12-n01-report-tools/composition-race-final.log)通过 |
| Web／共享 Identity | [专项 race 10.788 秒](evidence/2026-09-12-n01-report-tools/web-composition-race.log)：选装、无来源、重复声明、权限命名及借用 Identity 不重复发布／管理其生命周期 |
| SDK | [Agent SDK／Tools SDK 全量 race](evidence/2026-09-12-n01-report-tools/sdks-race.log)：3 个测试包及 9 个编译包；最终工具输入结构另由 Agent 实际编译器与整段 HTTP 验证 |
| Work／PM | 最终 [Work 全量](evidence/2026-09-12-n01-report-tools/work-full-final.log)及 [PM 全量](evidence/2026-09-12-n01-report-tools/pm-full-final.log)，共 4 个测试包及 10 个编译包，覆盖默认产品选装与已有组合 |
| Runtime | [相关宿主 race](evidence/2026-09-12-n01-report-tools/runtime-boundary-race.log)：agenthost 2.578 秒、transport 3.414 秒；同次完整 boundary 中发布版本门禁失败，见下文；[其余架构检查单列 108.355 秒通过](evidence/2026-09-12-n01-report-tools/runtime-architecture-race.log) |
| 前端构建 | Agent [14.37 秒](evidence/2026-09-12-n01-report-tools/frontend-build-final.log)、Work [24.82 秒](evidence/2026-09-12-n01-report-tools/work-frontend-build.log)、PM [24.20 秒](evidence/2026-09-12-n01-report-tools/pm-frontend-build.log)；均完成，保留 Vite 大 chunk 提醒 |
| 静态检查 | Agent、Agent SDK、Tools、Tools SDK、Work、PM 全量 vet，以及 Runtime 改动相关包 vet 均返回 0；对应 `*-vet.log`；改动 Go 文件格式与五个 Git 仓库 `git diff --check` 通过 |

实际 Chrome 首次 [全部通过](evidence/2026-09-12-n01-report-tools/browser/result.json)，对应 [Go 宿主 131.894 秒](evidence/2026-09-12-n01-report-tools/browser-host.log)；最终代码再次[全部通过](evidence/2026-09-12-n01-report-tools/browser-final/result.json)，对应上表 98.615 秒整段。通过 UI 登录、建会话、发送消息、展开工具记录，核对 5 次完成调用、实际行、来源、固定上限和完整性；完整重启、撤权／恢复及手机 390×844 验证通过，页面无脚本异常，文档无横向溢出。浏览器及测试专用宿主在脚本结束时关闭。

- [桌面实际回复](evidence/2026-09-12-n01-report-tools/browser-final/01-desktop-answer.png)
- [展开后的宿主来源及实际行](evidence/2026-09-12-n01-report-tools/browser-final/02-report-source.png)
- [字段撤权后的隐藏状态](evidence/2026-09-12-n01-report-tools/browser-final/04-fields-revoked.png)
- [手机重新查询结果](evidence/2026-09-12-n01-report-tools/browser-final/05-mobile-fresh-query.png)
- [读取撤权后未执行](evidence/2026-09-12-n01-report-tools/browser-final/06-read-revoked.png)

## 失败记录与验收限度

1. [首次 HTTP](evidence/2026-09-12-n01-report-tools/conversation-http.log) 的工具 Schema 只定义 `oneOf`，Agent 编译器要求根节点声明 `type: object`，因此两种模式拒绝目录。已修正 Tools SDK 单一声明，并增加实际编译器回归；最终两种模式通过。
2. [Schema 修正后一次运行](evidence/2026-09-12-n01-report-tools/conversation-http-schema-fixed.log) 在嵌入模式最后撤权后发送时使用旧访问令牌，被正确拒绝为 `execution_access_denied`；独立产品模式通过。测试角色切换后增加显式 `/auth/refresh`，没有放宽产品授权；[专项 27.127 秒](evidence/2026-09-12-n01-report-tools/conversation-module-refresh.log)及最终整段均通过。
3. **Runtime 完整 boundary 不是全绿。** `TestRuntimeConsumesDomainryModulesByImmutableVersion` 拒绝新接入的 Tools／Tools SDK `v0.0.0`。这些拆分库当前没有可消费的不可变发布版本；没有填造 tag，也没有修改测试或添加正式 `replace`。其余架构检查使用 `-skip '^TestRuntimeConsumesDomainryModulesByImmutableVersion$'` 单列运行，不能当作完整发布门禁通过。此缺口归 H04，脱离 go.work 的发布消费仍未验收。
4. [首次整段 race](evidence/2026-09-12-n01-report-tools/browser-host-race.log) 在恢复权限后的查询触发原 3 分钟测试观察上限，总计 420.136 秒失败；快照包含已完成的 5 次工具调用和模型回复步骤，但运行仍为 `running`，没有 `DATA RACE`。首次[浏览器等待](evidence/2026-09-12-n01-report-tools/browser-race/result.json)也在宿主开放端口前达到 3 分钟上限；延长后的浏览器等待在宿主退出时主动终止，见[未进入网页的尝试](evidence/2026-09-12-n01-report-tools/browser-race-final/attempt.json)。将 Go 测试观察上限改为 10 分钟、轮询从 400ms 改为 2s 后，[第二次 race](evidence/2026-09-12-n01-report-tools/conversation-module-race-final.log)仍在最终回复步骤之后以 `provider_failed` 失败，199.720 秒，不能仅归结为测试观察上限。检查代码发现每次具体调用及旧结果授权前多余地读取全目录，随后 owner 又执行精确授权，扩大了来源复核开销。已去除这项重复读取，并重跑 Tools 全量 race、两产品、两种会话模式和 Chrome；均通过。没有放宽生产来源复核时限、权限或租约，也没有加入跨请求授权缓存。修正后的嵌入式整段 race 另见[日志](evidence/2026-09-12-n01-report-tools/conversation-module-race-optimized.log)，Chrome 使用最终普通运行验收。
5. 当前验证 SQLite、本地 workspace 和确定性模型决策。真实模型协议、发布版本、真实部署数据库、多实例和整批联调仍按 H04／H08 验收。N01 的完整性只指发布报表固定行数上限内的结果；最后续页不代表整份报表，亦不能将有上限的报表结果冒充原始数据全集。
6. llm-proxy 仓库仍在 `a32407a678ea7f96d70b7557765e2a01262184d4` 且干净。它的接入范围仅是既有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`；本轮没有修改服务或接入其模型接口。

机器源码／证据清单见 [本轮清单](evidence/2026-09-12-n01-report-tools.json)。旧 owner 清单保留为前一增量的历史快照，不用本轮源码重新覆盖。
