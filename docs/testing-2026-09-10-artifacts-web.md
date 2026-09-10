# 成果网页与会话工具验收

2026-09-10，本次使用当前编译的前端、真实 Identity Module、HTTP、临时 SQLite 和私有正文存储。模型为本地 Chat Completions SSE 协议夹具，消费真实工具结果中的 ID 和版本；不能据此宣称真实 Verdent 的理解能力或知识库命中已验收。

## 实际浏览器流程

| 操作 | 观察结果 |
| --- | --- |
| 新建“成果网页验收”会话，请求生成周报 | 创建确认展示周报标题与三节正文；确认前整页刷新后恢复同一内容 |
| 确认保存，打开“我的成果”，只看当前会话 | 只有本次创建的成果，版本 1 正文可读 |
| 下一轮要求修改刚才周报的第二节 | 工具先查询、读取，再确认；卡片显示目标版本 1、原文“待核对。”及具体替换内容 |
| 确认修改，从执行结果打开“查看此版本” | 打开同一成果的版本 2；第一节和第三节保持原文 |
| 切换版本 1 并下载 Markdown | 第一版正文仍为“待核对。”；浏览器实际保存到 Downloads，文件字节与原文完全一致 |
| 直接修改标题为“网页修订周报”，保存并整页刷新 | 仍显示同一成果的版本 3，新标题和第二节内容保留 |
| 从版本 1 再尝试保存改动 | 明确提示版本冲突，保留编辑草稿，没有覆盖版本 3 |
| 预览费用表、下载 CSV、修改金额 | `9007199254740993.01` 原样显示并导出；改为 `9007199254740993.02` 后保留全部数字。CSV 中公式形式文本添加防护前缀 |
| 打开项目趋势图 | 三周数据 3 / 7 / 5 的柱形和下方表格一致；切换成果不会残留其他成果的保存提示 |
| 撤销真实 Identity 的成果读取权限 | 刷新后正文不可读取；有受限来源的成果从列表隐藏；整页刷新后相关历史回复与工具结果隐藏，用户原始请求保留 |
| 恢复读取权限 | 原始回复和版本 3 重新可读，未重新生成或改写版本 |
| 保存包含 script、带事件属性的图片、脚本链接与外部图片的测试文档 | 正常正文可读；页面标题未被修改，成果区域的 script / iframe / object / embed / img 节点均为 0；危险链接被拦截，外部图片显示占位 |

实际检查过周报和图表窗口截图。本次只验证桌面浏览器尺寸；窄屏、长列表和后台任务关联还需补充。

下载字节核对：Markdown SHA-256 为 `2848c5eee95ba95df133bd86bbf00da332db65007b21c7b3a08f5cc09650429c`；CSV 为 `74e3b5248899f4de46f54812c494c908755fb93effc8b7f05e800b6e5c96835e`。下载副本是用户已经取得的文件，后续权限撤销不意味着能撤回本地副本。

## 自动检查与修复

- [HTTP 工具测试](../internal/assembly/web/conversation_artifact_tools_test.go)：实际宿主重启后恢复原确认、重复答复、指定原版本导出和权限撤销。该测试发现并修复创建回执缺少当前成果读取权限检查的问题；现在检查返回的真实成果 ID 和版本。
- [多步集成测试](../integration/conversation_artifact_tools_integration_test.go)与[原子存储测试](../internal/infrastructure/persistence/database/agent/conversation_artifact_tool_store_test.go)：多轮找回、改段落、源权限边界、失败回滚和重试不重复写入。
- [前端状态测试](../frontend/src/conversation-state.test.ts)：独立的个人成果范围、刷新与未确认发送重试、下一轮范围清除，以及编辑 / 导出重试保持确定的目标和版本。

通过的检查：

```sh
go test ./...
(cd ../domainry-agent-sdk && go test ./...)
go vet ./...
npm --prefix frontend test
npm --prefix frontend run build
```

前端状态测试共 18 项通过。成果相关 race 检查中，应用层、持久层、集成包通过；Web 包的新增 HTTP 工具测试首次超过原 10 秒等待窗口，没有报告数据竞争。将该测试窗口改为 60 秒、轮询间隔改为 25 毫秒后，单独的 `go test -race ./internal/assembly/web -run '^TestArtifactToolsThroughIdentityHTTP$' -count=1` 通过（约 38 秒）。其余成果 HTTP race 测试在首次运行中未报告失败。这是功能与并发检测，不是延迟或吞吐性能验收。

## 复现浏览器验收

```sh
npm --prefix frontend run build
AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run '^TestArtifactToolsThroughIdentityHTTP$' -count=1 -v
```

看到 ready 日志后打开 `http://127.0.0.1:8092`，使用测试账号 `admin@example.com` 和测试口令 `Changed-Artifact-Tool-Test!3`。页面模型标签明确标为本地协议夹具；输入“生成一份周报”“把刚才周报的第二节改为已核对”“下载周报的第一版”可复现模型决策路径。该服务只在显式测试环境变量下启动，不修改运行部署的账号或数据库。

测试专用 POST `/__acceptance/revoke-artifact-read` 和 `/__acceptance/restore-artifact-read` 用于复现权限变化；完成后 POST `/__acceptance/finish` 正常结束测试。测试路由未注册到生产宿主。

本次浏览器测试进程已正常退出，临时页面已关闭；未启动 8091 的正式体验实例。仍待真实模型联调、真实知识引用和后台任务关联的整体验收。
