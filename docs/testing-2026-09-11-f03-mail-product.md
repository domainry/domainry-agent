# F03 邮件产品、来源草稿与网页验收

F03 单项完成：Google／Microsoft 邮件读取与搜索、按实际范围开放工具、摘要与事项提取、本地回复草稿均已交付。草稿由 Knowledge 管理版本、预览、编辑和下载，标明未发送并保留原邮件与来源运行。进度 52 / 81，下一项 F04。

## 产品与架构

- Agent 网页通过 `Options.MailTools` 选装四个工具；Work／PM 默认 Agent 升至 1.2.0，新增各自的邮件 Skill。Skill 明确账号发现、查询方言、基础权限、分页与正文完整性、未确认的负责人／期限、外部文本不扩大权限、用户请求保存才生成本地草稿。
- 原日历宿主装配提取为中立的 [account_tools.go](../internal/assembly/web/account_tools.go)，分别实例化公开 Calendar／Mail Adapter，共用 Integration SDK 端口并分别绑定可用性；业务适配器不相互依赖。四种选装组合、重复定义拒绝和未配置 owner 不可用通过测试。原日历产品流程仍通过。
- Agent 执行／成果应用层无邮件专用代码，没有新增邮箱、token 或草稿存储。架构检查禁止执行层、应用层和通用会话装配导入 Connector 业务协议与具体 Provider。通用账号读取入口仍不挂载到浏览器。
- 工具页面增加四个中文名称及范围说明，执行记录显示中文动作。新 OAuth 应用的可编辑默认范围包含 Gmail readonly／Graph Mail.Read；已有应用配置保持原值，最终能力取决于实际授予范围。
- 草稿来源复用既有运行引用和工具结果授权。来源工具不可用时成果接口返回 `agent.conversation.tool_unavailable`（503）；其他邮箱可用而原来源失效时返回 `mail.source_invalid`（403）。不能用新账号的可用性代替旧来源授权。

## 实际验收

| 证据 | 结果 |
| --- | --- |
| [产品与装配 race](evidence/2026-09-11-f03-mail-product/product-race-initial.log) | 产品流程 240.56 秒，包 242.389 秒，无 race；真实 Identity／HTTP／SQLite，16 次邮件 HTTP、20 次模型协议 HTTP。 |
| [替代账号来源检查](evidence/2026-09-11-f03-mail-product/replacement-source-second.log) | 5.12 秒；新账号四工具全部可用，旧草稿 v1／v2 和原回复仍被具体来源授权拒绝，零厂商 I/O。此补充场景在全量和 race 后单独执行。 |
| [构建网页验收](evidence/2026-09-11-f03-mail-product/browser-second.log)、[网页报告](evidence/2026-09-11-f03-mail-product/browser-second/report.json)、[宿主审计](evidence/2026-09-11-f03-mail-product/browser-second/host-audit.json) | 8 场景、29.95 秒、16 次邮件 HTTP、19 次模型协议 HTTP、2 次 OAuth 换码，零 JavaScript 错误。 |
| [Agent Web 全量及相关包](evidence/2026-09-11-f03-mail-product/agent-full.log) | Web 107.524 秒，产品装配、架构、profile、网页命令编译通过；[原日历兼容](evidence/2026-09-11-f03-mail-product/calendar-compatibility.log) 5.657 秒。 |
| [Work 全量](evidence/2026-09-11-f03-mail-product/work-full.log)、[PM 全量](evidence/2026-09-11-f03-mail-product/pm-full.log) | 默认完整 profile 编译验证四个邮件工具／Skill，既有产品业务流程与所有包通过。 |
| [前端状态测试](evidence/2026-09-11-f03-mail-product/frontend-tests.log) | 42 / 42 通过。 |
| [Agent 构建](evidence/2026-09-11-f03-mail-product/frontend-build.log)、[Work 构建](evidence/2026-09-11-f03-mail-product/work-build.log)、[PM 构建](evidence/2026-09-11-f03-mail-product/pm-build.log) | 三套最终构建通过；保留既有大 chunk 提示。 |
| [Agent vet](evidence/2026-09-11-f03-mail-product/agent-vet.log)、[Work vet](evidence/2026-09-11-f03-mail-product/work-vet.log)、[PM vet](evidence/2026-09-11-f03-mail-product/pm-vet.log)、[架构](evidence/2026-09-11-f03-mail-product/architecture-final.log) | 全部通过。 |

产品流程实际执行九次工具调用：账号发现 → 邮件头列表 → 两页搜索 → 指定邮件正文 → 创建草稿 → 读草稿 → 修改 v2 → 导出。模型协议夹具消费真实工具反馈后生成预算摘要、待处理事项和原邮件引用；成果的来源会话／运行、Reply-To、账号、邮件 ID、thread_id、RFC Message-ID 与时间均核对。下载字节与指定草稿版本完全一致；SSE 有执行证据且无凭证。

随后关闭正文工具、完整重启、重新启用、撤销 list/read 权限和账号，分别验证旧回复、草稿读取、修改、导出、旧下载和列表隐藏；双用户不能发现个人账号或读取对方会话／成果。缺失正文不生成新草稿；第二个实际 OAuth 仅授予 metadata 时仅执行账号发现和邮件头列表，搜索／正文不可用。替代账号用例另外确认原来源授权未被新的可用账号替代。

网页通过真实登录、操作权限展开并授权、消息提交、处理记录、成果预览、人工编辑 v3、浏览器下载、工具开关、重启、撤权刷新、另一个用户及 390px 页面交互验收。已核对[草稿截图](evidence/2026-09-11-f03-mail-product/browser-second/mail-draft.png)、[手机权限](evidence/2026-09-11-f03-mail-product/browser-second/basic-mobile-settings.png)、[缺失正文](evidence/2026-09-11-f03-mail-product/browser-second/partial-mobile.png)与[下载文件](evidence/2026-09-11-f03-mail-product/browser-second/reply-draft-v3.md)。

## 修正与可复核范围

保留初次失败，未改写成成功日志：

- [产品初次](evidence/2026-09-11-f03-mail-product/product-initial.log)：模型测试按直接导出对象解码，实际契约是嵌套 `export`，修正夹具。
- [产品第二次](evidence/2026-09-11-f03-mail-product/product-second.log)：预期撤权统一 403；实际来源工具不可用为 503，按既有契约区分。
- [产品第三次](evidence/2026-09-11-f03-mail-product/product-third.log)：第二轮初始化重复更新 OAuth 应用但未带期望修订，owner 正确拒绝；测试改为独立的基础范围应用。
- [网页初次](evidence/2026-09-11-f03-mail-product/browser-initial.log)：没有先展开本次操作权限，无法勾选成果授权；补实际展开交互。最终无需绕过权限或页面 API。
- [来源补充初次](evidence/2026-09-11-f03-mail-product/replacement-source-initial.log)：夹具缺回调静态文件，网关拒绝启动；补齐后通过。
- Agent [diff 检查](evidence/2026-09-11-f03-mail-product/agent-diff-check.log)通过；Work／PM 没有 Git 元数据，对应 [Work](evidence/2026-09-11-f03-mail-product/work-diff-check.log)／[PM](evidence/2026-09-11-f03-mail-product/pm-diff-check.log)命令不可适用，不能声称通过。变更的六个 UTF-8 文档／JSON 已单独检查格式与行尾。

Provider／SDK、Tools 与 Integration 的前四个增量证据继续有效，见 F03 对应条目和[邮件边界](mail-read-boundaries.md)。最终[机器清单](evidence/2026-09-11-f03-mail-product.json)记录当前源码与所有本阶段证据的 SHA-256，并验证前四个增量的证据文件；旧源码清单是各阶段快照，中立装配重命名等后续变化由最终清单覆盖。

本次 OAuth 发行方、厂商邮件与语言模型均为隔离协议夹具；Identity、Agent、Tools、Integration、实际 Provider、Knowledge、SQLite、HTTP 和构建网页真实执行。没有真实 Google／Microsoft 应用或账号，不把此结果表述为厂商账号、真实语言模型质量、依赖发布或所有部署数据库验收。服务负责具体 OAuth 应用配置。F06 外部写入、第三批完整业务场景及 H08 部署要求仍按原顺序保留。
