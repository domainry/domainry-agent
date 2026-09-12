# N02 Report owner／Runtime 宿主增量验收

N02 尚未完成。当前已实现公开可选 SDK、Report 私有编译／计算／执行／复核，以及 Runtime 目录／权限／完整 SQL 聚合／内容版本适配。没有跳到 N03；Tools、Agent 会话、产品页面、完整表格文件路径及整段端到端测试继续留在本项。职责见 [analysis-boundaries.md](analysis-boundaries.md)。

## 实际验证

全部 Go 命令使用 `GOWORK=/tmp/domainry-n01.go.work`，在对应模块根目录运行。该文件只是已有模块组合的本地 workspace，不是 Git worktree，也没有创建分支。

| 命令 / 场景 | 结果与证据 |
| --- | --- |
| Report `go test -race -count=1 ./...` | 15 包，9 包运行测试、6 包编译；私有应用 2.678 秒、分析 domain 3.637 秒、模块 4.168 秒；[最终日志](evidence/2026-09-12-n02-analysis-owner/report-final-race.log) |
| Report SDK `go test -race -count=1 ./...` | 7 包，4 包运行测试；[SDK 日志](evidence/2026-09-12-n02-analysis-owner/sdk-race.log) |
| Runtime `go test -race -count=1 ./runtime/modulehost/report ./runtime/infrastructure/persistence/database/report ./runtime/bootstrap/composition` | 2.057／6.776／3.409 秒；[相关包日志](evidence/2026-09-12-n02-analysis-owner/runtime-related-race.log) |
| 最终 ORM 来源构造器抽取后的 Runtime Report 存储全包 race | 4.182 秒，包含现有隔离、精确数值、分页和 SQL 方言检查；[最终存储日志](evidence/2026-09-12-n02-analysis-owner/runtime-storage-final-race.log) |
| Runtime `go test -race -count=1 -run '^TestReportAnalysisRealOwner' -v ./runtime/bootstrap/runtime` | 最终 3.702 秒，场景本身 1.81 秒；[实际 owner／SQLite 日志](evidence/2026-09-12-n02-analysis-owner/runtime-analysis-final-race.log) |
| Runtime `TestReportModuleOwnsProductHTTPAndApplicationBoundary` race | 1.541 秒；[Report 边界日志](evidence/2026-09-12-n02-analysis-owner/runtime-report-boundary.log) |
| Report、Report SDK 全量 vet；Runtime 相关包 vet | 退出码 0；[Report](evidence/2026-09-12-n02-analysis-owner/report-vet.log)、[SDK](evidence/2026-09-12-n02-analysis-owner/sdk-vet.log)、[Runtime](evidence/2026-09-12-n02-analysis-owner/runtime-vet.log)、[最终存储改动后的 Runtime](evidence/2026-09-12-n02-analysis-owner/runtime-final-vet.log) |

实际组合测试使用公开 Report Factory／SDK、真实 Runtime RecordApplicationService 与权限策略、RecordStore／ReportSQLStore 和 SQLite，身份为 Identity SDK 原生 AccessBundle 夹具。本增量没有真实模型、Identity HTTP 登录、浏览器或独立服务；没有把夹具称为这些链路的验收。

数据库种入 1,205 条记录：当前用户在当前工作区拥有 1,203 条，其中 1,201 条匹配销售筛选；另一个用户和另一个工作区各 1 条。实际 SUM 为 `12.01`、均值为 `0.010000`、计数为 `1201`，证明输入越过宿主原 500 条页大小仍全部聚合。完整分组得到三组，将上限改为 2 时失败；缺失分组和全 NULL 金额保持 NULL。其他场景覆盖两个独立筛选的对比、按天趋势、`9007199254740993.01 / 3 = 3002399751580331.003333`、异常规则、相同数据库重新打开 Report owner 后复核、读权限脱敏后目录隐藏／执行拒绝／旧结果拒绝、当前用户无记录时得到真实零计数。

同一时间戳下修改金额后，旧来源指纹失效，新分析得到新版本；修改其他工作区的记录不影响当前用户结果证明。来源哈希和聚合使用同一个租户／RLS 的 ORM 构造器，没有为指纹重写一套权限规则，也没有向 Report／Agent 传递原始输入记录。

Report 私有测试还覆盖真实 ObjectSQL 编译器对四种生成规格的接受、筛选值参数绑定及大整数、未知字段和混合规格拒绝；非空计数、正负 half-even 与负零、对比并集与零／负基准、纽约 DST 日历日、缺失时段与 NULL、顺序计算与除零、规则未定义、近似来源标记，以及续页／超限／重复分组／计数矛盾等畸形执行结果。应用层检查当前身份／权限／字段／数据／定义变化及结果各部分篡改，旧结果复核没有再次执行数据查询，编译或执行失败不返回成功证明。

## 失败与修正

- 初始 compiler 测试缺少 switch 的结束括号，修正后通过；[初始日志](evidence/2026-09-12-n02-analysis-owner/initial-compile.log)、[修正日志](evidence/2026-09-12-n02-analysis-owner/compiler.log)均保留。
- 新来源版本查询第一次误用了 ORM 的 `OrderBy` 参数类型；[编译失败](evidence/2026-09-12-n02-analysis-owner/runtime-version-compile.log)保留，修正为实际 ORM `query.Ascending`。
- 首次实际组合返回 `backend.internal_error`：新增适配调用了会把 nil 转成内部错误的既有错误归一化函数；修正为成功时直接返回 nil。见[首次组合日志](evidence/2026-09-12-n02-analysis-owner/runtime-analysis-initial.log)。
- 第二次组合的最后一个空数据用例失败：夹具直接改 Principal.UserID，却仍复用原 AccessBundle 绑定主体；改为针对新主体重新创建 SDK bundle。该次其余聚合、隔离、内容版本和撤权场景已执行；见[第二次日志](evidence/2026-09-12-n02-analysis-owner/runtime-analysis-second.log)，最终整段 race 通过。
- 核对实际执行器时发现 SQL NULL 以缺少字段返回，owner 据空值标记／非空计数处理；真实 SQLite 的 NULL 用例已覆盖。来源版本最初复用快照 watermark，审阅发现同时间戳更新缺口后改为独立内容指纹端口，并补实际改值验证。
- 来源指纹曾复用拼接后的 CTE SQL；检查当前 ORM 具备所需能力后，改为抽取既有私有 ORM 来源构造器，直接排序与流式哈希。最终代码没有新增原生 SQL，原 Report 存储全包与实际 owner 组合已重新验证。

本次没有运行 Runtime 的全部发布门禁。N01 已记录 Tools／Tools SDK 没有不可变发布版本导致 H04 门禁失败，该事实保持不变，不以本次专项架构通过代替完整发布通过。新 SDK 仍为本地开发接口。

## 证据与后续

[机器清单](evidence/2026-09-12-n02-analysis-owner.json)记录本增量前的 3,041 个文件基线、变更与保留文件哈希、测试日志及 llm-proxy 工作区状态；旧 N01 证据没有重新生成。Report 应用／domain 的 [实际依赖清单](evidence/2026-09-12-n02-analysis-owner/report-dependencies.txt)中没有 Runtime、Agent 或 Tools 的实现依赖。

当前进度仍为 56 / 81。N02 继续工具契约、适配、会话／产品及 HTTP／浏览器端到端，完整表格文件数据集也尚待接入。只有该项全部验收后才勾选并进入 N03。
