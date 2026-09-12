# R03 成果网页、来源关联与安全渲染验收

日期：2026-09-12。结论：通过。R03 已完成，下一项为 L01。

## 交付范围

- 成果详情使用 Agent SDK 已公开的 `source_conversation_id` 和 `source_run_id`，增加“来源会话”和“来源消息与处理记录”入口。前者切回原会话，后者打开实际持久 Run 的消息、工具步骤、状态和保存回复。
- 会话 Run 本身由持久 worker 执行，关闭页面后仍可继续，因此当前成果能关联生成它的后台处理记录。本项没有提前增加 L01 的通用 `task_start` 契约，也没有让前端依赖任务服务实现。
- 保留现有按类型预览：Markdown 只解析受限标记；表格把所有单元格作为文本显示并保持十进制原文；图表只读取封闭的 `bar`／`line`、列键和表格数据生成静态 SVG，并同时展示精确数据表。
- Markdown 明确跳过原始 HTML，不加载外部图片，只允许 HTTP／HTTPS 链接。生成内容没有脚本、事件处理器、任意绘图代码或 `dangerouslySetInnerHTML` 入口。
- 编辑继续通过期望版本写入新修订；下载明确绑定当前选择版本。读取、版本列表、编辑、导出和下载都由后端按当前动作及来源权限复核。

生产改动只在 Agent 前端的展示与导航层。来源事实、授权、版本、存储和导出仍由现有 Agent SDK／应用服务处理；没有导入 Knowledge、Runtime、Report、HTTP 装配或存储实现。见[架构审计](evidence/2026-09-12-r03-artifact-web/architecture-audit.json)。

## 实际浏览器端到端

`TestArtifactToolsThroughIdentityHTTP` 启动真实 Agent Web Host、Identity、SQLite、会话 worker、成果应用服务和实际编译前端；只有模型协议是本地确定性夹具。浏览器完成以下流程：

1. 从“我的成果”打开工具生成的周报，点击“来源会话”返回 `conv_3f14b68cb4e4c705be0261b5445c8f74`；点击“来源消息与处理记录”打开原 `artifact_create` Run，看到完成步骤和保存回复。
2. 把周报标题和正文修改后保存为版本 3，再选择版本 1；页面显示原始“待核对。”内容并下载 `art_f94a45faf5d1ae4b47b67edaf68c05da-v1.md`。
3. 图表成果显示可访问的柱状图及三行精确表格。表格成果把 `=SUM(A1:A2)` 显示为普通文本，并完整保留 `9007199254740993.01`。
4. 恶意 Markdown 同时包含 `<script>`、外部图片、`javascript:` 链接和 HTTPS 链接。实际 DOM 检查为 `script=0`、`img=0`、`window.__artifactExecuted=false`；只有 HTTPS 链接保留，危险链接没有 `href`。
5. 撤销 `artifact_read` 并刷新后，来源绑定周报从列表隐藏，页面声明部分成果因当前来源不可读取而省略；恢复权限后版本 3 重新出现。

浏览器宿主测试通过 **240.86 秒**，见[宿主日志](evidence/2026-09-12-r03-artifact-web/logs/browser-host.log)和[浏览器观察记录](evidence/2026-09-12-r03-artifact-web/logs/browser-observation.log)。验收结束后完成控制端点返回 204，测试进程正常退出，浏览器页已关闭。

## 服务端与回归验证

- HTTP race：`TestArtifactToolsThroughIdentityHTTP` 通过 **45.564 秒**。除完整创建／确认／重启／编辑／旧版导出／撤权链路外，新增断言 artifact 的来源会话和来源 Run 精确等于创建它的会话与完成 Run。见[日志](evidence/2026-09-12-r03-artifact-web/logs/artifact-http-race.log)。
- 应用集成 race：`TestArtifactsSaaSRoundTripVersionsReceiptsAndDownloadAuthority` 与 `TestArtifactProvenanceProtectsMetadataEditsAndExportsAfterRestart` 通过，覆盖版本、幂等回执、当前下载授权、来源撤权、恢复和宿主重开。见[日志](evidence/2026-09-12-r03-artifact-web/logs/artifact-integration-race.log)。
- 前端：53 项状态测试全通过；TypeScript 检查和 Vite 生产构建通过。构建只有既有大 chunk 提示，不是编译错误。见[测试](evidence/2026-09-12-r03-artifact-web/logs/frontend-test.log)与[构建](evidence/2026-09-12-r03-artifact-web/logs/frontend-build.log)。
- 静态检查：相关 Go 包 vet 和 Agent 工作区 `git diff --check` 通过。见[vet](evidence/2026-09-12-r03-artifact-web/logs/web-vet.log)与[diff 检查](evidence/2026-09-12-r03-artifact-web/logs/diff-check.log)。

全部文件尺寸和 SHA-256 见[机器清单](evidence/2026-09-12-r03-artifact-web.json)，工作区状态见[状态记录](evidence/2026-09-12-r03-artifact-web/repository-state.json)。

## 边界结论

- 前端只读取公共 artifact DTO 并发出导航回调；App 负责切换会话和打开 RunDialog，ArtifactDialog 不直接调用会话内部、任务、知识或存储服务。
- 来源权限仍由应用层的 `ConversationSources` 审计完成，列表的 `omitted` 只显示服务端裁剪结果；前端没有自行推断或缓存授权。
- Markdown 的 HTML、图片和链接过滤与表格／图表渲染都留在纯展示组件；生成内容不进入 JavaScript 执行路径。
- `llm-proxy` 工作区为空，HEAD 为 `a32407a678ea7f96d70b7557765e2a01262184d4`。本批没有修改或对接模型服务；F04 始终只消费它已有的 `/tool/web_search` 与 `/tool/web_fetch_jina` 接口。

## 保留的命令修正

- 首次并行启动前端测试和构建时，两个命令依赖同一个尚未创建的日志目录，`tee` 先执行的一侧报目录不存在；创建目录后按顺序重跑，53 项测试与生产构建均通过。没有修改产品代码来掩盖该命令错误。
- 浏览器下载后尝试读取控制台时使用了 CUA 不支持的属性；下载点击本身成功，页面出现绑定版本 1 的文件名。安全结论来自实际 DOM 的脚本、图片、链接和全局变量检查，不声明本次浏览器记录具有控制台错误计数。

本项不声明真实云模型或外部厂商部署；R03 的产品行为由实际 Web／Identity／SQLite／worker／浏览器链路验收。通用后台任务 ID、目录、取消与恢复按 TODO 顺序从 L01 开始。
