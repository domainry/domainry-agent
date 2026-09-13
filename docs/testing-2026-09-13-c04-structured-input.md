# C04 增量：结构化输入契约

日期：2026-09-13。此记录只覆盖 C04 的输入契约部分，C04 整体仍待完成。

委派可提供 `structured_input: {data, schema?}`。数据和 Schema 各不超过 16 KiB，完整任务输入仍受宿主 `MaxInputBytes` 限制。Schema 复用 Tools SDK 的 JSON Schema 编译和校验，本地引用可用，运行时不请求外部 Schema。字段类型、必填项、枚举等不匹配时，接单前拒绝请求，不创建任务或执行会话。原有文本输入继续可用，输出 Schema 的校验保留。

结构化数据及格式约定进入实际任务输入。`update_input` 替换完整结构化输入，校验后推进约定版本，保留需求正文版本和旧输入历史，停止旧执行。依赖增加 `input` 字段：跟随全部要求或明确依赖输入的任务受影响；只依赖目标、约束等其他字段的任务不会因输入变化被停止。恢复时仍需核对上游需求和约定的准确版本，新的依赖快照包含实际输入值。

输入来源独立保留在委派、历史、任务、运行及使用输入的依赖快照中。接单、页面／工具结果读取和每次模型请求之前继续核对来源；私有附件不能包装成结构化数据后隐式共享。连续变更多个字段而尚未采用时，待核对记录合并保留全部未采用字段，历史仍逐版保存。

页面支持输入 JSON、输入 Schema、输出 Schema、更新输入、查看当前值和历史快照。编辑已有 JSON 的文本框具有明确可访问名称，浏览器可以定位并编辑非空内容。

依据：[输入校验](../internal/application/conversation_structured_input.go)、[字段投影](../internal/execution/brief.go)、[输入页面](../frontend/src/StructuredTaskInput.tsx)、[应用测试](../internal/application/conversation_structured_input_test.go)、[依赖／历史测试](../internal/infrastructure/persistence/database/agent/conversation_dependencies_store_test.go)、[私有来源测试](../internal/application/conversation_peer_sources_test.go)。

验证结果：受影响 Go 包全套通过、SDK 全套通过、Peer 场景 race 通过、前端 64 项测试及生产构建通过。真实 Identity／SQLite／HTTP 确认无效输入未接单、正确输入实际到达模型；Chrome 完整协作流程确认创建结构化输入、修改输入、保留 3 版历史及无关字段依赖不失效。桌面／390px 页面无横向溢出，JS 错误为零。截图和结果：`/tmp/domainry-peer-c04-input-browser/`；日志：`/tmp/peer-c04-*.log`。

接下来继续 C04：转交前核实旧执行和不明外部效果、按完成条件记录核对结果、以证据处理结论分歧。这些能力尚未完成，没有计入完整条目验收数。
