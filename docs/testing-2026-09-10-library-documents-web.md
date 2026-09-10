# 个人资料与共享资料库：文档网页验收

日期：2026-09-10。采用已确认的“个人资料＋共享资料库”，文档继承库成员权限，不做组织树继承。本批将已有文档服务接入资料库窗口；后端机制见[文档应用链路验收](testing-2026-09-10-managed-documents.md)。

## 已实现的用户流程

- 从“资料库”打开个人或共享资料库，查看文件、索引状态和详情，分页读取文档。
- 编辑者和管理者可选文件上传；共享库明确提示全部阅读成员可访问。未连接支持入库的知识源时展示原因，不提供无效上传入口。成员角色之外，每次请求仍由后端检查 Identity 操作权限。
- 原文件上传和知识索引分别展示。只有后端返回 ready 才标为“可检索”；结果未知、索引失败和删除待清理各有独立提示。
- 上传前保存按登录身份及资料库隔离的重试回执，仅包含编号、文件名、大小和摘要。响应丢失或刷新后，重新选择同一文件可使用原编号续传；不同内容不能复用回执。结束重试不会删除已上传文档。
- 下载先检查返回内容大小和 SHA-256，再发起浏览器文件保存。身份切换或请求取消后不继续保存迟到内容。
- 删除前展示具体文件和共享影响，提交时绑定文档修订号。后端停止访问后继续显示实际清理状态，完成后移出列表。
- 窗口自动刷新索引状态；刷新失败清空文档及详情，自动刷新不会吞掉上传错误。文件选择不会因窗口重新获得焦点丢失，重复点击已选文件也不会清空详情。

主要代码：[文档窗口](../frontend/src/KnowledgeDocumentsPanel.tsx)、[二进制传输与状态](../frontend/src/knowledge-document-state.ts)、[资料库入口](../frontend/src/KnowledgeLibraryDialog.tsx)。SDK 的 `documents_configured` 表示宿主已配置文档管理，不表示 Identity 授权或索引完成；应用在返回资料库时计算该字段。

## 浏览器验收范围

使用安装的 Chrome，以 Playwright 启动全新、独立的 headless 测试配置，访问临时 `127.0.0.1:8092`。不使用用户浏览器的 Cookie；本轮未操作被锁定的桌面浏览器。测试登录、Identity、Agent、HTTP、SQLite、原文件存储、后台任务及宿主关闭重开均使用实际实现；远端知识服务和模型使用明确的协议夹具，没有发起新的真实 Verdent 写入。

脚本：[knowledge-documents.browser.mjs](../frontend/tests/knowledge-documents.browser.mjs)。服务器为 `TestKnowledgeDocumentsIdentityHTTPAndPersistentOriginal` 的显式可选浏览器模式；只有 `AGENT_TOOL_UI_ACCEPTANCE=1` 才开放临时测试控制接口。脚本校验测试工作区，结束时关闭浏览器和测试服务器。服务器最后核对每个测试文档只推送一次、只删除一次且已清理。

| 场景 | 已验证的结果 |
| --- | --- |
| 未绑定个人资料库 | 页面说明尚未开放上传，没有选文件入口 |
| 共享上传 | 清楚展示共享范围；焦点刷新保留已选文件 |
| 上传响应丢失 | 服务端实际受理后中断浏览器响应，刷新仍保留同一回执 |
| 错文件与续传 | 不同内容被拒绝且不会发起第二次请求；换回原文件后复用原编号，服务端不重复推送 |
| 自动刷新 | 索引从等待变为可检索；上传错误在自动刷新后仍可见 |
| 下载 | 浏览器保存的原文件与上传内容逐字节一致 |
| 撤销权限 | 撤销下载权限不产生下载；撤销列表权限清空文件和详情，恢复后重新可读 |
| 完整宿主重启 | 关闭并重开实际宿主，原浏览器登录继续有效，重新下载内容一致 |
| 对话引用 | 上传文件经目录、搜索、读取进入模型循环，来源保留本地 kdoc 文档编号及实际片段 |
| 归档与恢复 | 归档暂停上传、下载及检索；恢复后文档操作重新开放 |
| 手机删除 | 390×844 页面无横向溢出；取消不删，确认后停止访问，最终清理完成 |
| 删除后历史 | 刷新对话后显示资料不可读取提示，相关回复与来源按钮不再展示 |

前两次浏览器尝试因脚本没有匹配完整导航名称、没有完成“新建会话”表单而失败，未计为通过。第三次完整流程通过；截图检查后又收紧脚本，等待回复完成再打开引用，并要求历史明确显示隐藏提示，避免把加载中的空列表误当验收成功。第四次加强版完整通过，浏览器无 JavaScript 运行错误，全部测试文档已由实际后台清理。

## 自动验证与证据

- 前端状态测试 23 项、TypeScript 检查、生产构建通过。构建仍有既有的包体超过 500 kB 提示。
- Agent、Agent SDK 全量 Go 测试与 `go vet` 通过。
- 浏览器宿主以 `go test -race` 运行，包含实际关闭重开宿主和后台索引 / 删除竞争；第三次及第四次完整流程、最终清理审计均通过。第四次宿主测试耗时 46.35 秒，race 包进程总计 48.039 秒。
- 最终原件下载证据、8 组流程报告和桌面 / 手机截图保存在 `/tmp/domainry-library-documents-browser-4/`。已实际查看引用窗口、手机删除确认和删除后的历史隐藏截图。两个下载文件 SHA-256 均为 `4b5fa5281b048daf2433c79850949e01303f97cba1fe4bc0de1f6cdcaf17b49d`，与上传原文一致。

测试日志：`/tmp/domainry-library-documents-ui-unit-final.log`、`/tmp/domainry-library-documents-ui-check-final.log`、`/tmp/domainry-library-documents-ui-build-4.log`、`/tmp/domainry-library-documents-agent-full-final.log`、`/tmp/domainry-library-documents-sdk-full-final.log`、`/tmp/domainry-library-documents-agent-vet-final.log`、`/tmp/domainry-library-documents-sdk-vet-final.log`、`/tmp/domainry-library-documents-browser-4.log`、`/tmp/domainry-library-documents-browser-host-4.log`。

## 尚未完成

本批不代替真实 Verdent 独立多库的应用生命周期验收。会话附件另存个人或共享库随后已在[另存批次](testing-2026-09-10-attachment-library-copy.md)完成。自动创建 / 配置远端数据源、跨库共享 / 移动、未知远端结果的人工核查入口、PDF / Office 提取预览仍未完成。文件类型可上传不表示这些格式的真实解析已在本轮验收。资料库列表分页已有接口和页面，本轮浏览器未覆盖超过 20 个文档的翻页场景。

K05 / K07 整项继续保留未勾选；资料库文档网页这部分已完成，不能再描述为“尚未接入”。
