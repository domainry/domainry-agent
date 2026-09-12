# F02 日历读取完成验收（2026-09-11）

**F02 已完成并勾选，进度 51 / 81，下一项 F03。** 按共享契约／Google → Microsoft → Integration 当前账号读取 → Tools → 产品／会话／网页的顺序交付，没有进入后续写日程或邮件实现。

## 产品装配与边界

- 产品宿主通过 `CalendarTools` 显式选装公开 Tools 门面的五个工具。Agent 网页默认启用；Work／PM 默认 Agent 及各自日历 Skill 选择这五个工具。原记录工具与日历在宿主分别注册、组合，业务应用和 Agent 执行引擎没有导入日历协议或 Integration 实现。
- 宿主按当前 Identity 的具体 `integration.connection_accounts.list/read` 动作解析个人／工作区权限；Tools 再经 Integration SDK 检查真实账号、实际 grant 和操作契约。未配置 Integration 的产品保留已授权工具的设置定义，读取不可用；部分实现的旧 Binding 不能假装具备当前账号读取端口。
- 日历执行权限使用 Integration 声明的权限定义，浏览器仍只挂载账号管理与 OAuth 页面，不开放通用 Provider 调用接口。管理员通过既有账号设置明确授予日历工具和账号读取权限；其他角色仍由 Identity 分配，启动不恢复已撤销权限。
- 当前账号权限明确拒绝时，Tools 可用性返回不可用；基础设施错误仍保持未知／拒绝执行。设置开关只过滤当前权限，不替代账号授权。
- 页面补充日历工具的中文用途与执行名称，Microsoft 应用默认日历范围为 `Calendars.Read` 与刷新所需的 `offline_access`。关闭工具设置／外部账号窗口时，清理已渲染的来源内容及旧执行详情，重新读取当前授权结果；重新打开的消息和历史仍由服务端逐项复核。
- 没有新日历表、凭证副本、迁移账本、后台作业或写操作。使用当前源码工作区的 `go.work`，发布和实际部署仍在 H08。

## 直接验证

| 范围 | 结果与证据 |
| --- | --- |
| 共享 SDK／Google | [读取协议、时区／DST、全天、分页与不完整忙闲](testing-2026-09-11-f02-google-calendar.md)，SDK／Provider race 与边界检查通过。 |
| Microsoft | [目录／calendarView／详情／共同空闲](testing-2026-09-11-f02-microsoft-calendar.md)，次级日历、严格续页、全天来源时区复读及 race 通过。 |
| Integration | [当前账号读取与 Google／Microsoft × Module／SaaS 四组合](testing-2026-09-11-f02-account-read.md)，实际 Provider／HTTP／SQLite、刷新、重启、仅忙闲／基础授权、敏感重放、执行前后撤权和严格架构检查通过。 |
| Tools | [公开门面、原始全量 race 和实现边界](testing-2026-09-11-f02-calendar-tools.md)通过；本次[明确拒绝的可用性增量 race](evidence/2026-09-11-f02-calendar-product/tools-availability-race.log)通过。 |
| 产品会话 | [最终真实 Identity／HTTP／SQLite race](evidence/2026-09-11-f02-calendar-product/conversation-race-final.log)通过，测试 115.65 秒。受权发现 → 日历目录 → 两页安排 → 详情 → 两个日历共同空闲，六次持久工具调用、五次厂商协议请求；另验不完整忙闲、SSE、设置关闭、完整重启、列表／读取权限撤销与恢复、另一真实用户、账号撤销后无厂商 I/O。 |
| 构建后的网页 | [最终浏览器运行](evidence/2026-09-11-f02-calendar-product/browser-complete.log)通过，17.37 秒、七个场景、零 JavaScript 错误。见[逐步报告](evidence/2026-09-11-f02-calendar-product/browser-complete/report.json)和[宿主审计](evidence/2026-09-11-f02-calendar-product/browser-complete/host-audit.json)：一次真实协议换码、十次厂商 HTTP 请求、十四次模型协议 HTTP 请求、完整宿主重开。 |
| 回归与配置 | [Agent Web 全量](evidence/2026-09-11-f02-calendar-product/agent-web-full.log)通过，95.803 秒；[Work 全量及默认 profile](evidence/2026-09-11-f02-calendar-product/work-profile-final.log)与[PM 全量及默认 profile](evidence/2026-09-11-f02-calendar-product/pm-profile-final.log)通过。默认 Agent／Skill 先完整编译，原故意去掉 `time_now` 的记录场景相应取消依赖它的 Skill，保持生产严格校验。 |
| 前端与静态检查 | [42 项状态测试](evidence/2026-09-11-f02-calendar-product/frontend-tests.log)、[Agent 最终构建](evidence/2026-09-11-f02-calendar-product/frontend-product-final.log)、[Work 最终构建](evidence/2026-09-11-f02-calendar-product/work-frontend-final.log)、[PM 最终构建](evidence/2026-09-11-f02-calendar-product/pm-frontend-final.log)通过；Agent 架构、产品装配、命令编译、相关 vet、gofmt 和 Git whitespace 检查见[执行核对](evidence/2026-09-11-f02-calendar-product/checks.json)。构建保留既有大资源分块提示。 |

最终截图已目视核对：[日历设置](evidence/2026-09-11-f02-calendar-product/browser-complete/calendar-settings.png)、[完整会话](evidence/2026-09-11-f02-calendar-product/browser-complete/calendar-conversation.png)、[持久执行记录](evidence/2026-09-11-f02-calendar-product/browser-complete/calendar-run.png)、[手机不完整忙闲](evidence/2026-09-11-f02-calendar-product/browser-complete/partial-mobile.png)、[手机撤销后设置](evidence/2026-09-11-f02-calendar-product/browser-complete/revoked-mobile-settings.png)。

## 验收中发现并修正

- [第一次编译](evidence/2026-09-11-f02-calendar-product/conversation-race-initial.log)发现测试 Transport 的长度比较类型错误，改为 `int64` 后编译通过。
- [首次会话 race](evidence/2026-09-11-f02-calendar-product/conversation-race.log)遇到设置列表五秒期限；[定位记录](evidence/2026-09-11-f02-calendar-product/conversation-diagnostic.log)显示单次日历预检约 67–75 毫秒，并暴露原两步测试的十五秒会话等待不足。目录断言改为一次快照，日历六步用例独立使用两分钟等待、降低查询频率；没有扩大生产超时。后续[有界验证](evidence/2026-09-11-f02-calendar-product/conversation-race-bounded.log)已通过主体链路，仅第二个用户缺少测试角色的工具设置权限；补齐该用户的独立读权限后，最终 race 全部通过。
- [首次网页](evidence/2026-09-11-f02-calendar-product/browser-initial.log)发现关闭工具后服务端已隐藏正文，但当前页面仍显示旧内容。补上窗口关闭后的清理及重新授权读取；后续直接在当前页面验证隐藏，再验重启和恢复。
- [第二次网页](evidence/2026-09-11-f02-calendar-product/browser-final.log)的脚本在桌面页面尚未载入时误判成手机导航，按实际 viewport 选择导航并等待控件后通过。最终再等待页面的“已保存”状态并滚动到手机回复，避免只截到后台完成前的中间画面；最终截图清楚显示不完整来源提示。

全部实现快照、当前直接证据及前序证据哈希核对见[机器清单](evidence/2026-09-11-f02-calendar-product.json)。OAuth issuer、厂商日历响应和模型都是明确的协议夹具；实际 Identity、数据库、Provider、HTTP、网页和来源授权链路已运行。**没有声称真实 Google／Microsoft 账号或真实日历模型验收完成**，这些外部环境与第三批整段业务及 H08 继续保留。
