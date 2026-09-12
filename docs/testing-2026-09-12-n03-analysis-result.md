# N03 结构化分析结果、后台执行与导出验收

日期：2026-09-12。结论：通过。N03 已完成，下一项为 R03。

## 交付范围

- Report SDK 增加结构化 `visualization`、`coverage` 和 `references`。图表规格只有 `bar`／`line`、X 列及最多 8 个 Y 列；不能携带表达式或代码。无法安全生成图表时返回明确原因。
- Report owner 从已编译规格和实际结果确定图表，声明成功结果完整、未截断、返回行数、请求上限、结果 NULL、来源 NULL 和单元格问题。缺失计数使用十进制字符串和任意精度整数，超过 JavaScript 安全整数仍保持精确。
- Runtime 业务对象与 Knowledge 表格文件各自通过 Report SDK 数据集目录声明来源引用。引用只用于追溯，不改变权限范围；图表、覆盖和引用都进入结果证明，修改旧结果后当前授权复核失败。
- Tools 只接受完整且未截断的 owner 结果，检查缺失计数、引用、图表轴和数值列。默认输入 Schema 是封闭的结构化规格；递归契约测试和执行测试均拒绝 `sql`、`code`、`script`、`command` 等入口。有限算术仍是明确的引用／十进制常量树。
- 长分析通过已有持久会话 run／worker 后台执行。发送接口立即返回 `queued`，浏览器可以关闭；本项没有提前实现 L01 的通用 `task_start` 工具。
- 大结果使用持久引用分页读取，每页重新检查来源与当前身份，完整字节和 SHA-256 通过后才显示。分析图表通过 `artifact_create` 保存，再由 R04 的 `artifact_export` 导出指定版本；完整结果、已有导出下载和新导出都重新检查当前来源权限。
- 前端只解析 `status=completed` 的正式工具收据，按封闭字段投影到现有 `ArtifactPreview`；小数保持字符串，原始内容不会成为脚本。

职责与依赖方向见[分析边界](analysis-boundaries.md)和[架构扫描](evidence/2026-09-12-n03-analysis-result/architecture-audit.json)。Report／Runtime／Knowledge／Tools 只通过公开 SDK 或模块组合连接；Agent 前端只消费保存后的 JSON 协议。

## 真实文件与服务端端到端

沿用 N02 由真实 XLSX 生成的 1,205 行 Builder artifact，执行实际 kb-search-api Handler → Knowledge Base Connector → Knowledge → Report → Tools 链路。本次新增核对结果为：

| 项目 | 实际值 |
| --- | --- |
| 完整汇总 | `1000012.29` |
| 分组图表 | `bar`，X=`region`，Y=`total` |
| 覆盖声明 | `complete=true`，`truncated=false` |
| 缺失声明 | `note/null_value = 241` |
| 来源引用 | `knowledge_document:doc-1/tbl_e9c83fb03919603b_0@gen_current` |
| 数据行 | 1,205 |
| 当前权限读取 | 177 次 |
| 撤权 | 保存结果复核拒绝 |

命令和输出见[真实文件 race 日志](evidence/2026-09-12-n03-analysis-result/logs/file-e2e-race.log)。

## 后台运行、成果与当前权限端到端

`TestAnalysisLongRunStructuredChartArtifactExportAndCurrentPermission` 使用真实 Agent Web Host、Identity、SQLite、持久会话 worker、Tools adapter、结果分页、成果存储和导出 HTTP。只有模型协议与分析来源数值是确定性夹具。

测试让分析来源阻塞，`POST /messages` 在 1 秒内返回 `queued`，持久运行状态同时为 `running`；释放后，模型经过 3 次 `tool_result_read` 读取并校验完整结果，创建 80 行柱状图成果并导出 CSV。随后临时撤销来源权限，已生成 CSV 下载返回 503，新导出返回 503，保存结果读取返回 403。HTTP race 结果见[日志](evidence/2026-09-12-n03-analysis-result/logs/agent-http-race.log)，会话持久化与过期 lease 接管回归见[持久化 race](evidence/2026-09-12-n03-analysis-result/logs/agent-conversation-persistence-race.log)。

编译后的实际产品页面在本地 Chrome 完成整段验收：

1. 打开分析工具的完整结果，页面先校验来源和内容摘要，再显示柱状图、80 行表格、`完整：是`、`截断：否`、业务对象版本引用和两类缺失声明。
2. “我的成果”显示“销售区域分析 / 版本 1 / 图表”，点击“下载此版本 CSV”后出现实际文件名 `art_71582336ca6d2060379b72f780ee0493-v1.csv`。
3. 通过仅测试环境存在的控制端点撤销分析来源，重复下载立即显示“工具已停用或所需连接不可用”，操作按钮停用；浏览器控制台无错误或警告。

浏览器宿主测试通过 260.520 秒，见[宿主日志](evidence/2026-09-12-n03-analysis-result/logs/browser-acceptance-host.log)和[目视记录](evidence/2026-09-12-n03-analysis-result/logs/browser-observation.log)。浏览器第一次暴露出前端只解析内部 `content`、未解析持久工具收据外层的问题；补正为只接受 `status=completed` 的工具收据后，53 项前端测试及生产构建通过，实际页面出现结构化卡片。该失败来自端到端覆盖，修复后没有以单测代替浏览器复验。

## 验证结果

- Report SDK：全量 race 和 vet 通过。
- Report：15 个包全量 race 和 vet 通过；结构化元数据专项 race 通过，覆盖大整数缺失计数、图表省略和引用校验。
- Tools SDK：新增封闭 Schema／任意执行入口测试；全量 race 和 vet 通过。
- Tools：全量 race 和 vet 通过；覆盖成功投影、图表／覆盖／引用篡改拒绝、超限固定失败和旧结果当前复核。
- Knowledge：全量 race 和 vet 通过；目录引用绑定文档、generation 和表格子资源。
- Runtime：真实 SQLite 1,203 条授权输入的专项 race 通过，相关模块 vet 通过；业务对象引用由 Runtime owner 产生。
- Agent：后台 HTTP 整段 race、应用层 race、会话持久化仓库 race 和相关 vet 通过；前端 53 项测试及生产构建通过。
- 八个核对仓库均通过 `git diff --check`。`llm-proxy` 工作区为空，HEAD 为 `a32407a678ea7f96d70b7557765e2a01262184d4`；只消费既有 `/tool/web_search` 和 `/tool/web_fetch_jina`，没有把它接成模型服务，也没有修改它。

全部日志及 SHA-256 见[机器清单](evidence/2026-09-12-n03-analysis-result.json)，仓库状态见[状态记录](evidence/2026-09-12-n03-analysis-result/repository-state.json)。

## 保留的命令与环境修正

- 首次 Report 命令误指向没有 Go 文件的父目录 `internal/domain/report/service`；改为真实 `service/analysis` 后通过。
- 首次真实文件 E2E 未传 Builder artifact 路径，程序按预期拒绝；传入固定 fixture 后通过。
- 首次 Runtime 测试选择器没有命中，vet 又写成不存在的目录；改为 `TestReportAnalysisRealOwnerFullSQLiteAggregationAndCurrentPermissions` 和真实包路径后通过。
- 新增 Tools SDK 测试第一次没有带本地工作区，缺少尚未发布依赖的 `go.sum`；按本批统一 `GOWORK=/tmp/domainry-n03-20260912-go.work` 重跑后通过。未修改生产依赖来掩盖发布门禁。

本项不声明真实云厂商部署或不可变依赖发布；这些要求仍留在 H04／H08。N03 只使用已有持久会话 worker 承载长分析，通用后台任务工具继续按顺序留给 L01–L03。
