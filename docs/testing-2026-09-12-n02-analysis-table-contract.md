# N02：完整结构化表格的 Report 接收端

本增量完成 Report SDK 的独立表格来源端口和 Report owner 计算。**没有新增原文件解析，也没有宣称已经接通真实 Excel 数据。N02 仍未完成，不进入 N03，进度仍为 56 / 81。**

当前 Connector 的五项知识操作仍是 search / fetch / put / status / delete。重新读取既有知识服务源码快照 `dbf61406e55b8a446f026cde941ce7f9a9f33bda`：router 没有完整表格接口，fetch 仍返回 chunk.Content。该证据只对应固定源码，不推断部署版本或上游最新提交；见[源码复核](evidence/2026-09-12-n02-analysis-table-contract/upstream-source-review.json)。按[原文件解析移除要求](document-parsing.md)，不恢复本地 XLSX / CSV 解析，不把检索片段、字段提取或浏览器预览页转为全表输入。已询问现有完整表格／全量聚合服务的接口或源码位置。

## 实现与边界

- Report SDK `AnalysisTableHost` 在产品装配时选装 `AnalysisTableSource`，该来源只实现目录、当前版本与同步完整流，**不要求实现 ObjectSQLExecutor**。原 `ApplicationHost`、业务对象执行接口及工具输入形状保持兼容。来源适配器以后通过知识／数据服务公开契约接入，不允许两个 owner 互相导入实现。
- Report application 合并两类已授权目录，拒绝来源种类错误和重复键；业务对象仍要求对象 `.read`，文件权限由独立来源在版本读取、字段投影及流读取时检查。所有请求仍需当前 `report.query.execute`。目录本身不授予执行权限。
- Report domain 接收类型化单元格，不打开文件、网络、数据库，不依赖 SDK 的宿主端口包。使用已有封闭规格，支持分组、对比、趋势、表格计算与异常规则。文件计划不生成 Object SQL；原业务对象继续由宿主全量 SQL 聚合。
- 来源版本必须声明完整性、全量行数、原件／解析版本／工作表范围所绑定的数据版本和投影内容 SHA-256。Report 对按排序字段排列的整段输入计算摘要，检查结束回执、实际行数、全部单元格，再重新读取当前权限与版本；任一失败均不返回累计结果。
- NULL、空字符串和缺失列不同；缺失列、多余列、坏 UTF-8、非法数字／日期／布尔值直接拒绝。数值保留十进制字符串，以有理数累计 SUM，AVG 使用非空分母；文本比较按字节，数值及时间分组按类型相等性，对比两段的等价表示能合并。时间戳要求明确偏移，趋势保留重复 DST 小时并标注缺失时段。
- 表格计算保持来源行序；方法、单位、筛选与前置舍入语义进入结果及证明。来源可复用行缓冲，Report 保留自己的值。旧结果只复核授权、版本和签名，不重跑流；原业务对象的指纹编码保持兼容并有回归断言。
- 完整文件流最多 100,000 行、16 MiB 输入预算，单元格 16 KiB；不同值去重等累计状态另有 16 MiB 预算。输出仍最多 500 行，沿用 2 MiB 结果及 30 秒执行限制。超限拒绝整次执行，不采样，不把前几页当全量。更大的文件需由数据服务提供受控全量聚合，不能靠放大模型上下文解决。

## 验证

| 验证 | 实际结果 |
| --- | --- |
| 领域与应用层 | 1,205 行全量、精确大数 SUM / AVG、NULL 分母、独立重叠对比、等价数值分组、嵌套过滤、计算／规则、DST、空输入、非法值、各类限额和取消通过。短流／长流、内容摘要不符、忽略消费者错误、在途撤权／来源变更均拒绝 |
| 公开模块组合 | 实际 Report Factory／公开 SDK／宿主 SQLite／原迁移账本，独立结构化来源 1,501 行，结果 `9007199254741008.25`；没有执行 Object SQL。模块重开与 JSON 持久形状复核通过，撤权及来源变化拒绝旧结果；来源为明确的确定性服务夹具，并非真实 Excel／知识服务 |
| 最终四包 race | application **1.980 秒**、domain **2.422 秒**、公开 module **2.700 秒**、architecture **2.002 秒**；见[最终专项](evidence/2026-09-12-n02-analysis-table-contract/checksum-final-race.log) |
| 全量与静态检查 | Report 15 包（9 包实际测试、6 包编译），Report SDK 7 包 race（4 包实际测试、3 包编译），两个模块 vet 通过；见[Report](evidence/2026-09-12-n02-analysis-table-contract/report-complete-final.log)、[SDK](evidence/2026-09-12-n02-analysis-table-contract/sdk-complete-final-race.log) |
| 原业务对象回归 | 真实 Runtime／Report／SQLite **race 3.480 秒**；1,203 条授权输入、1,201 条筛选匹配、两个非当前归属记录排除，四模式、NULL、大数、同时间戳修改、重开及字段撤权仍通过；见[回归日志](evidence/2026-09-12-n02-analysis-table-contract/runtime-owner-final-race.log) |

本增量没有前端变更，不重复之前已通过的整段网页测试；完整文件来源的 HTTP／产品／浏览器验收仍待真实接口接通。不能用上述确定性表格流冒充已解析、上传或分析真实 Excel。

## 未通过过程与并行工作区

初次 Runtime 检查采用了错误的测试名称筛选，且在执行测试前被 Identity 并行新增函数缺少 `net/http` 导入阻断；该轮不能算回归通过。[首次日志](evidence/2026-09-12-n02-analysis-table-contract/runtime-objects-race.log)保留。准备补导入时另一任务已经完成同一修复，本任务随后误加的重复导入导致一次编译失败，已仅删除自己添加的重复行；[碰撞日志](evidence/2026-09-12-n02-analysis-table-contract/runtime-owner-exact-race.log)保留。最终改用上表中的准确测试名通过。Identity 并行功能不属于本增量，也不把其修复归为本项交付。

源码基线及新增／修改路径另行审计，不覆盖上一批机器证据；当前工作区其他修改单列，不撤销、不归到本增量。[机器清单](evidence/2026-09-12-n02-analysis-table-contract.json)记录源码及日志 SHA-256。

llm-proxy 工作区仍干净，提交 `a32407a678ea7f96d70b7557765e2a01262184d4`。实际 Web Provider 仍只调用 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`，未修改服务，也未扩展到模型或聊天接口。
