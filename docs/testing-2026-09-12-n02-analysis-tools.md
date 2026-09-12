# N02：分析工具、产品组合与网页增量

2026-09-12。接续 [Report owner／Runtime 完整聚合](testing-2026-09-12-n02-analysis-owner.md)，本增量完成业务对象数据集的 Tools、SDK HTTP、Agent／Runtime 组合及网页验证。**N02 尚未完成：完整表格文件数据集仍待交付；继续本项，不进入 N03。总进度保持 56 / 81。**

真实参与组件为 Report、Runtime、Identity、Tools、Agent、SQLite 和系统 Chrome。模型是进程内确定性 SDK 夹具，根据实际目录和 owner 结果安排调用；未调用模型厂商。独立产品连接真实 Runtime HTTP，使用独立 Agent 数据库及共享 Identity，同一测试进程内运行，不宣称独立部署验收。

## 代码与职责

- Tools SDK 单独声明 `analysis_run`；递归且有界的过滤／表达式 Schema 不携带 Report 或数据库依赖。Tools 私有 `analysistools` 通过 Report SDK DTO 和三个窄宿主端口工作，与 `reporttools` 无实现依赖。具体调用交由 owner 精确授权，发现工具才读取目录，不在每次调用前重复扫描全目录。
- Agent SDK 的独立可选 `AnalysisReader` 转发目录、分析和旧结果复核；Runtime application 新解当前身份后转发，bootstrap 才组合 Report 和 Tools。Agent application 保持通用执行器，不实现聚合、比较、时间分桶或异常规则。完整边界见[责任表](analysis-boundaries.md)。
- RPC 递归 DTO 的契约摘要使用当前祖先引用，重复兄弟类型仍完整展开；旧非递归结构的编码不变。当前 businessrpc SHA-256 为 `48ec2f8f8211ab88c34365d0663cdc75b1ed60ae4774510d8f69c28bcf69a038`，旧部署绑定需要更新。固定的分析错误保留，其他宿主错误继续脱敏。
- Tools 结果上限 1 MiB。RPC 在成功响应前检查实际响应和后续“原请求＋结果”复核报文均不超过各自 1 MiB 上限；超限整次失败，不删行、不扩大通用传输预算。限定的规格／限额／计算范围／执行中来源变化错误成为无业务数据的失败结果，模型可以明确修正规格；当前授权和原失败正文仍须核对。
- Agent Web、Work、PM 默认选装；Work／PM Agent profile 为 1.7.0、业务 Skill 为 1.2.0，声明目录优先、统计口径、NULL、精度及禁止抽样替代完整分析。权限声明不等于授权。
- 网页原先仅有 2 KB 节选，较长结果无法直接核对来源。本增量增加通用 `ConversationResultReader` 与只读 HTTP action `POST /agent/conversations/{conversationID}/runs/{runID}/result`，复用既有结果引用、来源与依赖复核。Module／SaaS 统一分发、remote 客户端转发；不重新执行工具。单页最多 8 KiB，页面核对连续 UTF-8 偏移、稳定总长及原 SHA-256 后显示完整正文；大整数原文不会因 JSON 浮点重序列化而改变。关闭详情、切换窗口／标签或卸载组件会清除正文，读取失败和取消丢弃片段。
- 真实 RPC 测试发现种子输入 `9007199254740993` 被 Runtime 旧解码在入库前变成 `9007199254740992`。修复位于 Runtime businessseed model／application，只保留种子数据数字，不全局改变其他 Manifest 字段的类型。分析结果按真实存储值计算，没有补偿或假数据。

## 验收证据

原始记录位于[本轮证据目录](evidence/2026-09-12-n02-analysis-tools/)，开发 workspace 为 `/tmp/domainry-n01.go.work`。没有新建分支、Git worktree 或修改模块缓存。

| 验证 | 已观察结果 |
| --- | --- |
| Tools 实际注册及负向检查 | [最终全量 race](evidence/2026-09-12-n02-analysis-tools/tools-full-final-race.log)：7 个测试包、9 个编译包；精确数字／NULL、方法／单位／来源保留，缺失来源、typed nil、权限、伪执行、部分结果、超限、取消及篡改拒绝；旧结果不重跑分析；固定失败正文不能夹带数据 |
| Agent SDK | [全量 race](evidence/2026-09-12-n02-analysis-tools/agent-sdk-verified-race.log)：3 个测试包、6 个编译包，含真实 HTTP 的三分析端口、递归数字、每请求授权、严格未知字段拒绝和响应／复核报文大小边界；新增完整结果路由及只读 action 同步核对 |
| Tools SDK、实际 Engine Schema | [Tools SDK 编译](evidence/2026-09-12-n02-analysis-tools/tools-sdk-race.log)及 [Agent 实际编译器](evidence/2026-09-12-n02-analysis-tools/agent-contract.log)通过；递归声明不是仅通过独立 JSON Schema 夹具 |
| 实际 Report／Runtime／Identity／SQLite 经 Tools＋RPC | [race 121.025 秒](evidence/2026-09-12-n02-analysis-tools/runtime-rpc-final-race.log)：4 条授权输入、3 个非空余额，总计 `9007199254741043`，其他用户数据排除；超限原因保留；工具、字段、读取撤权和跨 workspace 拒绝，关闭全部 owner／Identity 后重开，真实客户更新使旧证明失效 |
| 完整业务对象聚合回归 | [race 3.849 秒](evidence/2026-09-12-n02-analysis-tools/runtime-owner-final-race.log)；保留前一增量 1,205 条真实 SQLite 数据的四种模式、精确计算、NULL、字段权限、来源内容变化及完整性检验 |
| 两种会话模式 | [140.334 秒](evidence/2026-09-12-n02-analysis-tools/conversation-http-final.log)：嵌入 Runtime 52.21 秒，独立产品／宿主 HTTP 86.88 秒；每轮 1 次目录、1 次明确超限失败、4 次完整分析。两模式均完成持久历史、完整重启、字段撤权、恢复权限后旧证明仍失效、新分析成功；客户读取撤销后只返回空客户目录，不执行分析 |
| 通用完整结果 HTTP | [race 17.336 秒](evidence/2026-09-12-n02-analysis-tools/result-http-final-race.log)：真实 Web／Identity／SQLite，完整分页与原摘要一致、非法偏移、错误摘要、跨会话引用、调用者注入和撤权拒绝；原模型侧压缩／尾部读取仍通过。相关 [application／remote race](evidence/2026-09-12-n02-analysis-tools/result-reuse-race.log)通过 |
| 实际 Chrome | [全部场景通过](evidence/2026-09-12-n02-analysis-tools/browser-complete/result.json)，[宿主 75.771 秒](evidence/2026-09-12-n02-analysis-tools/browser-complete-host.log)。UI 登录／建会话／发送／展开结果，查看完整比较和趋势数据，核对统计值、规则、方法与来源；关闭详情清除正文；刷新、完整重启、撤权后旧内容隐藏且直接读取拒绝、恢复后重查；390×844 页面宽与滚动宽均为 390，无脚本异常 |
| 前端输入及内容校验 | [初始 49 项通过](evidence/2026-09-12-n02-analysis-tools/frontend-test-summary.json)，随后 [4 项结果读取专项](evidence/2026-09-12-n02-analysis-tools/result-pagination-test.log)覆盖 UTF-8 连续分页、摘要、不完整／错位／变更／超大页、取消、撤权及大整数格式化。纯分页专项与浏览器相互补充 |
| 产品配置 | [Work 全量](evidence/2026-09-12-n02-analysis-tools/work-full.log)、[PM 全量](evidence/2026-09-12-n02-analysis-tools/pm-full.log)通过，包含默认工具选择、实际 profile 编译、原记录能力及架构边界；[Web 选装 race](evidence/2026-09-12-n02-analysis-tools/product-selection-race.log)验证与 Report 共存、未选装／未绑定不可用、重复声明拒绝 |
| 构建 | Agent [最终前端](evidence/2026-09-12-n02-analysis-tools/frontend-result-release-build.log)、[Work](evidence/2026-09-12-n02-analysis-tools/work-result-frontend-build.log)、[PM](evidence/2026-09-12-n02-analysis-tools/pm-result-frontend-build.log)完成，保留 Vite 大 chunk 提示 |
| Runtime 相关包与边界 | [6 个测试包及 2 个编译包 race](evidence/2026-09-12-n02-analysis-tools/runtime-modified-packages-race.log)，[5 项针对性边界 race](evidence/2026-09-12-n02-analysis-tools/runtime-source-boundary-final.log)通过；没有把针对性边界当成完整 Runtime 发布门禁 |
| Agent 全量与静态检查 | 最终全量 19 个测试包及 17 个编译包通过，见 [Agent 日志](evidence/2026-09-12-n02-analysis-tools/agent-final-full.log)。各库 vet、格式和 `git diff --check` 见机器清单；新增结果接口后的最终 vet 单列保存 |

浏览器可见证据：[桌面分析回复](evidence/2026-09-12-n02-analysis-tools/browser-complete/01-desktop-answer.png)、[工具记录中的来源身份](evidence/2026-09-12-n02-analysis-tools/browser-complete/02-analysis-source.png)、[撤权后隐藏](evidence/2026-09-12-n02-analysis-tools/browser-complete/04-fields-revoked.png)、[手机重新分析](evidence/2026-09-12-n02-analysis-tools/browser-complete/05-mobile-fresh-query.png)。完整 owner 结果保存在浏览器 `result.json` 对应 `complete_result` 下，图片不能代替数值与权限断言。

## 失败、范围及剩余工作

1. 最初 RPC 测试覆盖循环未排除新增独立分析端口、契约摘要尚未更新，见 `sdk-rpc-initial.log`／`sdk-rpc-second.log`；新增报文双向限额后再次更新摘要，见 `sdk-rpc-bounds-initial.log`。没有取消契约门禁。
2. 实际 RPC 首次及诊断运行发现种子大整数舍入，见 `runtime-rpc-initial.log`／`runtime-rpc-diagnostic.log`；修复后普通运行 6.653 秒及最终 race 均通过。
3. 广泛的种子回归额外命中两个 workspace 基线测试失败，见 `seed-regression-race.log`。使用 Go overlay 恢复本增量前的种子解码，仍复现相同两项失败，见 `seed-existing-failures-baseline.log`；恢复文件 SHA 与开始快照完全一致。未改变 workspace 授权规则，也不声称整个 Runtime bootstrap 包通过。基线源码以 `.go.txt` 保存，避免证据被当作可编译包。
4. 初次会话在客户目录为空时，测试模型反复请求目录，最终达到执行上限，见 `conversation-http-initial.log`。客户撤权后仍可能有其他可分析对象，不能假设整个分析工具消失；夹具改为明确报告无客户数据集。最终两种模式通过。
5. 浏览器首轮把 2 KB 节选当成完整 JSON，见 `browser/result.json`。随后发现网页缺少通用完整读取入口并补齐；第二轮完整数据断言通过，但脚本对本来未截断的表格也等待完整读取按钮对应元素，见 `browser-final/result.json`。最终脚本区分节选与完整返回，`browser-complete/result.json` 全部通过。Go 宿主正常关闭的 PASS 不等于前两轮浏览器通过。
6. 增加完整结果 HTTP 路由后，SDK、Capability、Module、SaaS 精确目录计数需增加 1；相关初次失败保留在 `agent-sdk-result-*.log`／`agent-result-full.log`。额外只读断言首次误用了 Action 字段名，见 `agent-sdk-final-complete-race.log`；已按实际 `EffectClass` 修正。完整结果测试首次误以为一次记忆检索包含第 16 条记录，改为核对实际存储结果的全文摘要，没有把分页检索结果冒充全部记忆。
7. 保存诊断恢复文件时，`.go` 扩展名一度使证据目录参与 Go 包扫描，见 `agent-result-vet.log`／`agent-result-full.log`；已改为文本归档，未排除生产包。最后检查以最终日志为准。
8. Runtime 共享工作区还有本次以外的并行修改；机器清单将本次明确编辑路径和其他变化分开记录，不覆盖或把其他改动归到本项。前一 owner 的历史清单不重新生成。
9. 尚未完成完整表格文件的数据集路径，不能把知识片段或附件预览当作全表。图表、长任务与导出仍按 N03 顺序处理。真实模型、真实部署数据库、多实例及脱离 go.work 的依赖消费仍在 H04／H08。Runtime Tools／Tools SDK 仍为 `v0.0.0`，此前不可变版本门禁缺口未消除。
10. llm-proxy 仅消费既有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`；服务工作区保持干净，不接其模型／聊天接口。

当前源码与证据的逐文件摘要见[机器清单](evidence/2026-09-12-n02-analysis-tools.json)。
