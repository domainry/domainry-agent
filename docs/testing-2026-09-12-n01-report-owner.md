# N01 Report owner 与宿主增量验收

日期：2026-09-12。本增量完成受权目录、真实查询来源、旧结果复核及 Runtime Module／宿主 HTTP 接口；`report_query` 工具、产品选装和整段网页尚未交付，N01 不勾选，不进入 N02。

实际源码来自新检出的 Report 默认分支 `32430e917a239ff6fd007e353124aef0a957ab6d`（较既有 v0.1.7 多一项对象上下文兼容提交）及 Report SDK `f673408d40bc97075cc067ef42a9d3bdad6030b9`。没有编辑 Go 模块缓存、另开功能分支或 worktree。Report 的应用层规则与薄 facade 约束保持，新增架构测试阻止应用层导入外部 owner 实现和具体 I/O。设计见[职责边界](report-query-boundaries.md)。

使用 `/tmp/domainry-n01.go.work` 组合实际本地源码；Agent 本地 `go.work` 增加 Report／Report SDK。新增接口尚未发布不可变依赖标签，发布与 `GOWORK=off` 消费验收仍属于后续依赖交付，不以当前工作区成功替代发布。

| 验证 | 结果与证据 |
| --- | --- |
| Report 应用层专项 race | 1.452 秒；目录过滤／分页、无查询副作用、真实分页结果、完整／空结果、大整数参数及默认值、重建实例后来源复核、20 类篡改／范围变更、执行失败及执行期间来源／权限变化不返回凭证。[日志](evidence/2026-09-12-n01-report-owner/owner-race-verified.log) |
| Report 全量 | 14 个包通过／无测试包，包含 Module、HTTP adapter、数据库／迁移及已有查询／快照／导出兼容。[日志](evidence/2026-09-12-n01-report-owner/report-full.log) |
| Report SDK 全量 race | 7 个包通过／无测试包，包含公开边界与编译器。[日志](evidence/2026-09-12-n01-report-owner/report-sdk-race.log) |
| Agent SDK 全量 race | 9 个包通过／无测试包。新 HTTP 端口保持 JSON 大整数、结果字符串及来源；拒绝 9 组 SQL／Subject／token 注入，缺少能力和撤权均拒绝。[日志](evidence/2026-09-12-n01-report-owner/agent-sdk-race.log) |
| 实际 Runtime 宿主 HTTP race | 新增验收 47.63 秒，既有 Report 回归 31.97 秒，合计 81.494 秒。真实 Identity、Report、Runtime、SQLite；目录／结果分页、定义来源一致、完整性篡改、撤销字段／读取权限、完整模块重启、真实业务动作更新客户后旧结果／游标失效、新查询读取更新内容、跨工作区拒绝。[日志](evidence/2026-09-12-n01-report-owner/runtime-http-race-final.log) |
| 实际 Record 行权限 race | 30.573 秒。真实 Identity `owner` 数据权限只返回 3 条本人记录；明确的 `all` 权限返回 5 条，两条他人记录均被前者过滤；权限范围改变后旧凭证拒绝。[日志](evidence/2026-09-12-n01-report-owner/runtime-rls-race-verified.log) |
| 宿主及架构回归 | Runtime application／transport／Report modulehost、Agent 架构、Report 架构通过。[日志](evidence/2026-09-12-n01-report-owner/host-boundaries.log) |
| 静态检查 | Report、Report SDK、Agent SDK 全量及本次 Runtime 三个相关包 `go vet` 通过。[日志](evidence/2026-09-12-n01-report-owner/vet.log) |

原有 Runtime 回归测试日志中的固定 `v0.1.7 / v0.1.6` 文案是旧测试标签；本次真正执行的是上述本地源码及机器清单中的哈希，不把旧版本标签作为本次依赖证明。

保留的失败与修正：

- 新增三种 DTO 后旧契约哈希断言如期失败；审核方法、字段及拒绝行为后更新预期哈希，完整 SDK race 已通过。[原日志](evidence/2026-09-12-n01-report-owner/contract-before-update.log)
- 参数夹具最初把字符串 ID 与整数比较，编译器正确拒绝。改为合法的 `HAVING COUNT(e.id) >= :minimum`，保留默认值及超过 JavaScript 安全整数范围的核对。[原日志](evidence/2026-09-12-n01-report-owner/owner-race-final.log)
- 源数据修改夹具缺少当前动作要求的 `expected_updated_at`；改为携带公开记录查询实际返回的版本，随后真实变更及旧来源失效通过。[原日志](evidence/2026-09-12-n01-report-owner/runtime-http.log)
- 行权限对照夹具起初沿用同为 `owner` 的 headquarters 角色，之后又遗漏基础夹具已有的 `Other` 记录。最终显式区分 owner／all，按当前源码验证 3／5 条及两条他人记录隐藏。[首次](evidence/2026-09-12-n01-report-owner/runtime-rls-race.log)、[第二次](evidence/2026-09-12-n01-report-owner/runtime-rls-race-final.log)、[通过](evidence/2026-09-12-n01-report-owner/runtime-rls-race-verified.log)

本增量没有新增数据库表／迁移账本、Report SaaS 服务、模型调用或浏览器验收。历史结果在源版本变更后保守失效，不承诺继续展示任意历史数据。机器来源与文件证据见[增量清单](evidence/2026-09-12-n01-report-owner.json)。
