# N02 完整表格文件数据集验收

日期：2026-09-12。结论：通过。本增量关闭 N02 最后一个缺口；N03 未开始。

## 交付范围

本轮把结构化文件的完整数据链接入既有 `analysis_run`，没有把知识检索片段、预览页或截断搜索结果作为分析输入。

- `kb-builder` 在既有 CSV／XLSX／同构 JSON 解析器内生成有界的完整表格 artifact。artifact 位于文档 generation 对应的持久 `_analysis` 前缀，不属于会在 finalizer 删除的 `_pipeline` 中间产物。
- `kb-search-api` 增加内部目录和分页读取 Handler。团队来自认证上下文；调用者传入的 `team_id` 不能改变租户。每次 Lambda 调用前后都重新读取文档权限和当前 generation。
- 官方 Knowledge Base Connector 增加 `analysis_table_catalog`、`analysis_table_read` 两个版本化操作。可分析文档只能来自宿主配置 `analysis_document_ids`，不能由模型或浏览器选择任意文档。
- `domainry-knowledge` 通过 Connector 读取目录及每一页，并实现 Report SDK 的 `AnalysisTableSource`。每页重新经过当前用户权限，流结束后再读一次版本；NULL 保持 NULL。
- Runtime 只接收 Report SDK 端口。Plane 生成的项目组合负责创建 Knowledge 来源，Runtime 以实际 `RuntimeInstanceID` 调用来源工厂；Runtime、Report、Tools 和 Agent 生产代码都不导入 Builder 或检索服务实现。
- Report 继续负责结构化规格、完整流计算、版本证明和旧结果复核；Tools／Agent／网页继续复用前两次 N02 增量完成的协议与展示。

文件 artifact 上限为 100,000 行、256 列和 16 MiB；单元格上限 16 KiB。Builder 超限时不发布分析表，检索 API 单页最多 500 行，Knowledge 以 100 行一页流入 Report。目录和分页都绑定原文 generation、定义版本、数据版本及完整字段投影 SHA-256。

## 真实文件端到端

证据程序先用 `openpyxl` 创建真实的 1,205 行 XLSX，再调用生产 `structured_parser.summarize_xlsx` 生成 Builder artifact。随后依次运行实际的：

1. `kb-search-api` Gin Handler；
2. Knowledge Base Connector；
3. `domainry-knowledge` 的 Report SDK 来源端口；
4. Report 模块的完整表格流计算；
5. Tools 的 `analysis_run` 注册、schema 校验、执行和旧结果授权复核。

AWS S3／Lambda 的物理边界用内存 invoker 替代；Builder 自身另有 moto S3 多状态流水线测试，验证真实 XLSX artifact 的持久化、目录读取、500／500／205 分页和过期 generation 拒绝。跨层证据没有复制任何生产计算或权限规则；测试 invoker 只把 Builder 产出的 artifact 模拟成 Lambda 返回值。

最终结果：

| 项目 | 实际值 |
| --- | --- |
| XLSX 数据行 | 1,205 |
| 列类型 | `boolean`、`number`、`date`、`text` |
| 分析规格 | `aggregate`，`sum(amount)` + `count(*)` |
| 完整汇总 | `1000012.29` |
| 来源 | `table_file`，`complete=true`，证明非空 |
| Tools | `completed`，完整结果 1,521 bytes |
| 当前权限读取 | 93 次，包括目录、版本、分页及旧结果复核 |
| 撤权 | 保存后的工具结果重新授权失败，未继续返回原数据 |

可复现命令：

```bash
cd /Users/tiger/Projects/domainry-agent
GOWORK=/tmp/domainry-n02-go.work go run -race \
  ./docs/evidence/2026-09-12-n02-analysis-table-files/e2e \
  ./docs/evidence/2026-09-12-n02-analysis-table-files/fixture/analysis-table-artifact.json
```

输出见[最终 E2E 日志](evidence/2026-09-12-n02-analysis-table-files/logs/e2e-final-race.log)，输入文件见[真实 XLSX](evidence/2026-09-12-n02-analysis-table-files/fixture/revenue.xlsx)和[Builder artifact](evidence/2026-09-12-n02-analysis-table-files/fixture/analysis-table-artifact.json)。文件字节数与 SHA-256 见[机器清单](evidence/2026-09-12-n02-analysis-table-files.json)。

## 验证结果

- Builder：补齐 `pymilvus` 测试依赖后全量 `621 passed, 10 skipped`；真实 XLSX 多状态专项 `1 passed`；`compileall` 通过。
- kb-search-api：全量 `go test -race ./...` 和 `go vet ./...` 通过；覆盖认证团队优先、未授权文档不调用 Lambda、调用期间权限变化拒绝、畸形来源行拒绝。
- Connectors：全量 race／vet 通过，重新生成 catalog 后目录和 Knowledge Base Provider 专项通过；覆盖受信文档列表、严格路径和分页返回校验。
- Knowledge：全量 race／vet 通过；205 行三页测试覆盖目录、初始版本、末尾版本、NULL、工作区拒绝和来源变化。
- Report SDK：全量 race／vet 通过；`AnalysisColumn.Name` 保留来源列标签，计算仍只消费公开表格端口。
- Runtime：来源工厂、真实 instance ID 传递、Report 宿主端口和 composition 包专项 race 通过；相关 vet 通过。
- Plane：module／SaaS 三种生成拓扑的外部编译、source finalizer 模板和相关 vet 通过。
- Agent：分析网页装配 race、Module race、SaaS／Playground 命令编译及相关 vet 通过。
- 九个改动／核对仓库均通过 `git diff --check`；`llm-proxy` 工作区为空，HEAD 为 `a32407a678ea7f96d70b7557765e2a01262184d4`。

详细命令、日志摘要和文件哈希见[机器清单](evidence/2026-09-12-n02-analysis-table-files.json)，依赖扫描见[架构审计](evidence/2026-09-12-n02-analysis-table-files/architecture-audit.json)，仓库状态见[状态记录](evidence/2026-09-12-n02-analysis-table-files/repository-state.json)。

## 保留的失败记录

- Builder 第一次全量测试缺少测试专用 `pymilvus`，结果为 620 通过、10 跳过、1 个环境缺包失败；添加运行时测试依赖后全量通过。没有修改生产依赖来迎合测试。
- Runtime 大范围 bootstrap 测试发现并行工作树中的 Agent／Integration capability lock 与当前契约摘要不一致。本批 Runtime 来源工厂、Report 宿主和 composition 专项均通过，未改锁文件。
- Agent 大范围网页 race 在既有账户写入／Integration capability 验证中运行满 10 分钟后超时。本批分析网页装配、Module、命令编译和真实文件 E2E 均通过，未修改账户写入路径。
- Plane 大范围 codegen 测试发现并行 Runtime `runtimeext` 哈希与生成夹具不一致；本批三种拓扑外部编译、模板专项和 vet 均通过，未更新无关 runtimeext 身份。

上述失败的原始输出均保留在机器清单引用的日志中。本项不声明真实云 S3／Lambda 部署、不声明 H04 不可变依赖发布门禁，也不包含 N03 的图表、后台任务或导出。

`llm-proxy` 范围遵循用户纠正：仅消费其现有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`；本轮没有修改或对接该服务的模型能力。
