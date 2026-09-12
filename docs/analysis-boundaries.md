# N02–N03 分析职责边界

N02 已完成 Report owner／Runtime 宿主、Tools／Agent／产品组合和完整表格文件数据集。三次增量分别见[完整结构化表格接收端](testing-2026-09-12-n02-analysis-table-contract.md)、[工具与产品验收](testing-2026-09-12-n02-analysis-tools.md)和[真实文件数据链验收](testing-2026-09-12-n02-analysis-table-files.md)。

Report 通过可选 `AnalysisTableHost` 注入独立 `AnalysisTableSource`，不要求来源实现 SQL。Report 负责完整流的精确计算和行数／单元格摘要／当前权限／前后版本核对；来源服务负责文件解析、原件／工作表版本及数据授权。Knowledge Base Connector 现已提供受信文档目录和版本化分页读取，产品可同时发现 `business_object` 与配置允许的 `table_file`；不恢复 Agent 本地解析，也不把检索片段当全量数据。

| 层 | 当前代码职责 | 跨模块约束 |
| --- | --- | --- |
| Tools SDK | `analysis_run` 的唯一工具声明、结构化输入 Schema 与执行限额 | 中立工具契约，不依赖 Report SDK 或服务实现 |
| Tools 私有 adapter | 独立 `analysistools` 适配，调用当前宿主的分析目录／执行／旧结果复核；固定的可恢复错误反馈 | 仅消费 Report SDK DTO；不依赖报表工具适配器、Agent／Runtime 实现或数据库 |
| Agent SDK businessrpc | 独立可选 `AnalysisReader`，递归 DTO 的契约摘要、固定错误码及严格 HTTP 转发 | 不实现统计或数据权限；当前执行身份校验后交由 owner 重新解析身份 |
| Report SDK | `Analyses` 可选应用能力；结构化分析 DTO；可选数据集目录、分析来源版本端口 | 仅公开契约；旧 `Queries`／`ApplicationBinding` 接口兼容；不暴露任意 SQL、代码、存储或调用者自选权限 |
| Report 私有 domain | `internal/domain/report/service/analysis` 编译限定规格、计算、比较、趋势及规则 | 不依赖 Runtime、Agent、Tools 或数据库；生成的 SQL 仍由原 Report ObjectSQL 编译器验证 |
| Report 私有 application | 当前身份、数据集发现、逐计划授权、实际宿主执行、前后版本一致性、结果签名与旧结果复核 | 不把编译／校验返回成执行结果；不读其他模块内部状态；目录不构成执行授权 |
| Runtime modulehost | 公开当前对象与可查询字段，翻译身份／行列权限、SQL 执行与来源版本端口 | 只消费 Report SDK；没有分析公式、规则或 Tools 协议 |
| Runtime persistence | 完整授权关系上的 SQL 聚合；有序流式计算来源内容指纹 | 共用现有租户／RLS 的 ORM 查询构造器；原始记录不穿过来源版本端口；没有新增原生 SQL |
| Runtime application | 新解当前 Identity，转发 Report SDK 分析 DTO 与可识别错误 | 不导入 Report／Tools 实现，不处理计算 |
| Agent 应用层 | 通用工具执行、持久账本、原始结果引用和逐页当前来源复核 | 不导入 Report 分析实现；完整结果 HTTP 读取复用既有结果读取服务，不重新调用分析 |
| 产品装配 | Agent Web／Work／PM 选装 `AnalysisTools`；SDK 定义投影权限，产品 profile 声明工具 | 装配层通过 Tools 公共门面组合；声明不自动授予读取权限 |
| Builder | CSV／XLSX／同构 JSON 解析时生成有界完整表格 artifact；按文档 generation 持久保存 | 搜索 block 仍是检索投影；超出 100,000 行／256 列／16 MiB 时不发布分析表 |
| kb-search-api | 认证团队下读取文档权限和当前 generation，调用 Builder Lambda 的目录／分页操作，前后复核 | 请求中的团队不能覆盖认证上下文；未授权文档不进入 Lambda 请求；不执行分析 |
| Knowledge Base Connector | 两个版本化只读操作；只提交宿主配置的 `analysis_document_ids`，校验每页契约 | 不接受模型选择任意文档；不解析文件、不计算、不持有用户凭据 |
| Knowledge | 把 Connector 目录／分页适配为 Report SDK `AnalysisTableSource`，逐页复核当前用户和末尾版本 | 不依赖 Runtime／Report 实现；不处理分析公式；只通过公开 Connector adapter 和 Report SDK 端口 |
| Runtime bootstrap | 提供当前对象键列表；通过 Report SDK 端口接收项目表格来源；组合既有 Report 模块及分析工具 | 不导入 Knowledge 实现，不接管 Report 的业务实现，不增加数据库或迁移账本 |
| Plane 生成组合 | 在项目组合根从环境创建 Knowledge 来源，以实际 Runtime instance ID 绑定并注入 Runtime | 具体实现依赖只出现在组合根；生成产物之外的 Runtime／Report 仍只见 SDK 端口 |

产品数据集可包含 `business_object` 和配置允许的 `table_file`。业务对象字段来自当前宿主元数据及字段授权；`report.query.execute` 与对象 `.read` 必须同时成立，分析目录不推导或追加任意对象读取能力。脱敏、拒绝及需要逐记录策略计算的字段不进入分析目录，具体执行仍检查计划涉及的字段。表格文件只通过独立来源目录进入，文件归属、阅读和所选字段权限由 Knowledge／检索 API 在目录、每页和结束复核，不能用推导的对象权限替代。两类目录拒绝重复键与错误来源种类。

结构化文件流的输入最多 100,000 行、16 MiB，单元格 16 KiB，去重等累计状态另限 16 MiB；超限整次失败。来源提供覆盖原件、解析版本、工作表范围和数据内容的版本，以及排序投影字段对应的完整流 SHA-256。Report 一次流式累计后核对全部行和单元格，失败不返回中间值；历史复核不重跑。该路径不产生 SQL，不接触原文件或检索缓存。

规格只允许 `aggregate`、`compare`、`trend`、`table`。字段必须来自目录，筛选为有界树和类型化参数；分组最多 4 列、指标／选择列／计算／规则各最多 16 项，默认最多 100 个结果行，上限 500。查询使用 `MaxRows+1` 检测输出溢出，不对聚合输入取前页。任意分段出现游标、续页或超限时，整次分析失败；对比组的并集也检查上限。表格模式只接受完整、有限的筛选结果，不将前几页当作完整表格。单个源单元格最多 16 KiB，输入值及最终结果分别受 2 MiB 限制，执行与旧结果检查有 30 秒上下文上限。

数值以字符串或 NULL 返回。整数／定点小数使用有理数运算；平均数先在宿主执行 SUM 与非空 COUNT，再除法，避免均值提前舍入。返回匹配行数与各指标的非空计数。原始 `number` 明确保留近似来源标记，后处理使用有理数不能使其变为精确来源。差值和变动率保留 6 位小数，采用 half-even；变动率为 `100 × (当前值－基准值) / abs(基准值)`。零基准或缺失值返回 NULL 和原因。表格计算按顺序引用已经舍入的前置单元格，规则对输出值判断；方法与精度一并记录。

对比的基准筛选与当前筛选相互独立，可以重叠，来源计数不能相加冒充去重并集。趋势按 IANA 时区和日历粒度分桶，与同组上一个有数据的时段比较；不补造零值，缺失时段或时间会标注。货币单位来自 Runtime 的实际 `currency_code`；`XXX` 或没有单位时保持未知，不猜币种。百分数字段在现有存储中为比例，单位记录为 `ratio`。

分析来源使用独立的 `AnalysisSourceVersionReader`。原快照接口的行数与最新时间戳不能识别同一时间戳的内容修改，不能直接用作本项版本证明。Runtime 在只读事务内流式哈希当前授权输入的相关字段、记录 ID 与时间，Report 只接收哈希。查询前后分别核对当前授权、元数据、编译计划和内容版本，变化则拒绝；旧结果复核不重新执行分析。签名绑定完整规格、值、单位、方法、范围、计数、时间与来源。与分析输入无关的其他工作区修改不会影响证明。

工具结果上限为 1 MiB，RPC 的请求／响应也各为 1 MiB。RPC 在成功返回分析前，检查实际响应和后续原请求＋结果复核报文均可容纳；不截断结果或扩大通用传输上限。Tools 在结果超限前停止送入复核，把限定的分析规格／限额／计算范围／执行中来源变化错误转成无业务数据的失败结果，供模型明确调整规格。未知错误仍不披露。旧失败结果只允许精确固定正文，不能插入数据或伪造成功。

完整结果网页读取通过 Agent SDK 可选 `ConversationResultReader` 和 `POST /agent/conversations/{conversationID}/runs/{runID}/result`；HTTP action 明确只读。单页最多 8 KiB，每次复用原工具、依赖来源、主体及内容摘要检查。浏览器核对连续 UTF-8 字节偏移、稳定总长和原 SHA-256，完成后才呈现；数值格式化不经浮点重序列化。关闭详情、切换窗口／标签或离开组件后清除正文，取消与撤权丢弃片段。该通用入口不提供 SQL、公式执行或导出。

真实服务验收发现 Runtime 的旧种子 JSON 解码在入库前舍入大整数；修复归 Runtime 的 businessseed model／application，保留 JSON 数字而不改变所有 Manifest 字段的解码类型。实际 SQLite 验证原整数、NULL 分母及来源变更。

受控完整表格文件数据集已经完成真实 XLSX → Builder artifact → 检索 Handler → Connector → Knowledge → Report → Tools 的整段权限验收；知识检索片段和附件预览页始终不作为全表。

N03 在既有 owner 结果上增加三个由 SDK 定义的声明式投影：`visualization`、`coverage` 与 `references`。Report 私有 domain 只按已编译列决定 `bar`／`line` 规格，图表只含类型与列键；不合适的结果返回明确 `omitted_reason`。`coverage` 对成功结果明确 `complete=true`、`truncated=false`、返回行数和请求上限，并汇总结果 NULL、来源 NULL 与单元格问题。计数是无符号十进制字符串，Report 用任意精度整数计算，避免 JavaScript 安全整数上限造成来源缺失数失真。Runtime 和 Knowledge 各自在 SDK 数据集目录上声明业务对象或知识文档来源；引用只用于展示与追溯，不扩大数据范围，也不能直接寻址存储。

Report 在生成来源证明前写入这些投影，因此历史结果的图表、覆盖或引用被修改都会使当前复核失败。Tools 只接受完整且未截断的成功结果，验证图表轴引用返回列、Y 轴为数值列、缺失计数格式和非空来源；超限仍整次失败。Tools SDK 的封闭输入 Schema 只有有界筛选、指标、时间桶、算术树和规则，新增契约测试递归核对所有带属性的对象均为 `additionalProperties=false`，并禁止 `sql`、`query`、`code`、`script`、`javascript`、`command` 等输入属性。

长分析复用 Agent 已有的持久会话 run／worker：发送 HTTP 请求立即返回 `queued`，worker 在后台执行并保存步骤；N03 不提前注册后续 L01 的 `task_start` 等通用任务工具。大结果由现有稳定引用分页读取，每页重新检查原工具、来源和当前主体，浏览器校验连续字节与 SHA-256 后才解析。前端只接受 `status=completed` 的正式工具收据和封闭分析投影，数值保持字符串，并通过既有 `ArtifactPreview` 安全渲染图表／表格；页面不执行返回内容中的脚本。

分析成果继续通过 R01 的 `artifact_create` 保存声明式表格／图表，再由 R04 的 `artifact_export` 导出指定版本。成果读取和下载复用当前来源授权；来源撤权后，已生成下载、新导出和完整结果读取都拒绝。发布与不可变版本门禁保留在 H04；本地 SDK 增量尚未发布。

llm-proxy 边界保持用户纠正后的范围：只消费既有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`，服务工作区不修改。对应证据在 F04 下方。
