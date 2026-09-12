# N01 报表查询边界

依据 2026-09-12 实际源码：Report SDK `Queries` 已提供真实 Summary／ObjectSQL 查询；Report 应用层拥有发布定义、参数规范化、报表权限、字段授权编译、稳定分页和源版本检查。Runtime 的 Record／Identity 端口负责实际受权数据投影。N01 沿用这条链路。

Report SDK 新增可选 `GovernedQueries`，从既有 `ApplicationBinding.Queries()` 获取。旧 `Queries` 接口及四个 Report 产品 HTTP 操作不变。

| 边界 | 本层负责 | 消费方式 |
| --- | --- | --- |
| Report SDK／Report 应用层 | `Catalog`、实际 `Query`、`AuthorizeQueryResult`；目录字段授权、参数、原查询分页、完整性及签名来源 | 不向调用方暴露定义仓库、SQL、物理源或 RLS 条件 |
| Runtime application | 当前 Identity 主体解析、窄宿主端口和错误映射 | 只依赖 Report SDK DTO；不导入 Report 实现、不重新编写其权限规则 |
| Runtime bootstrap | 将已解析 principal 转为公开 ReportAuthority；组合真实 Report 查询服务；通过可选通用工具组合端口选装 Tools | 缺少可选 Report 能力时拒绝，不生成占位结果；具体 Tools 装配只在 bootstrap |
| Agent SDK businessrpc | 三个有界宿主传输操作和 SDK DTO；当前执行权限、来源与部署范围检查 | 不执行报表，不接收客户端 SQL／Subject／浏览器 token |
| Tools SDK／Tools | SDK 单一定义 `report_query` 声明；Tools 适配参数和结果、调用旧结果复核 | 通过公开 DTO 和中立宿主端口消费 Report；不依赖 Agent／Runtime 实现，不持有 Report 仓库 |
| Agent／产品 | 启动前选装、现有执行账本、上下文、当前结果访问及网页呈现；Work／PM 默认配置 | Agent 应用层仅接中立工具接口；装配层组合 Tools 门面，不新增报表定义仓库、查询引擎或导出作业 |

目录返回当前有权限执行的报表键、名称、参数、编译后的结果字段和固定行数上限；只读目录不执行报表。目录分页游标绑定发布定义、当前主体和查询选择，字段拒绝的报表不会进入目录，基础设施错误不会伪装成空目录。

`Query` 仅执行已发布的 realtime ObjectSQL 报表，继续使用 owner 的参数规范化及稳定分页。来源包含报表键、定义哈希、受权数据版本、查询时间、固定行数上限和签名。`complete=true` 只表示本响应包含该发布报表在其固定行数上限内的全部结果；不表示所有原始记录。最后一张续页自身仍是 `complete=false`。数值行值保持 SDK 原有字符串格式，大整数参数／默认值保持精度。

消费者保存原请求和完整 `ReportQueryResult`，每次重新使用前调用 `AuthorizeQueryResult`。Report 重新检查报表、字段、数据权限及源版本，再验证请求、结果正文、完整性、时间和来源都未变化。该接口不重新执行报表，也不能被当成执行成功。当前采用保守失效：任何受权源版本、主体权限范围或发布定义变化都会使旧结果不可用，需重新查询；没有引入历史数据归档授权系统。

Tools 另外绑定实际宿主 `BusinessSourceIdentity` 与规范化工具输入摘要，避免把同名报表的结果换到另一个来源。目录旧结果重新读取当前目录比较，查询旧结果仅调用 owner 复核；完整回复沿用 Agent 的来源访问控制隐藏。报表工具权限声明为 `agent.conversation_tools.report_query`，仅声明不授予；实际目录与查询还需当前 Report、记录和字段权限。未绑定业务宿主、没有可见报表或缺少权限时，不向模型暴露可执行工具。

工具目录发现读取当前获准报表目录。具体调用只做当前工具权限与宿主绑定检查，再交由 owner 的目录、查询或结果复核接口授权其精确请求；不在每张结果页复核前额外重读整份目录。没有使用跨请求授权缓存，也没有放宽 Agent 的来源复核超时。

嵌入模式通过 Agent SDK 的可选 `ConversationToolComposer` 在宿主绑定时组合，保留已有工具装配回调；只有成功组合并验证定义后才启动 worker。独立产品通过 `ReportTools` 选装及公开 businessrpc 来源端口接同一 Tools 适配器。Work／PM 默认 Agent 1.6.0、业务 Skill 1.1.0；产品仍可在部署白名单与当前用户设置中限制可用工具。

新宿主方法是 `report_catalog`、`report_query`、`report_authorize_result`，对应服务契约 SHA256 为 `0e804a1dd71ff7ac10663bea843956d50927500fc41c3c39d808da09958ec2a5`。F05 的旧契约证据保留历史；新旧客户端／服务须使用相同契约。这里的 HTTP 验收是 Runtime 宿主服务，并非尚不存在的独立 Report SaaS 服务。

首个增量验收见 [Report owner／宿主记录](testing-2026-09-12-n01-report-owner.md)，工具、两种会话模式与浏览器见 [组合验收](testing-2026-09-12-n01-report-tools.md)。当前依赖仍使用开发 go.work；Runtime 不可变依赖版本门禁因 Tools／Tools SDK 未发布而失败，保留 H04，不宣称发布可消费。未实施 N02／N03；llm-proxy 范围仍仅为其现有两个 Web 接口，服务工作区未修改。
