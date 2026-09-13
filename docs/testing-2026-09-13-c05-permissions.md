# C05：协作权限与当前身份复核（阶段交付）

本批是 C05 的权限基础与页面接入，**C05 尚未完成，不勾选**。C01–C04 的完成记录保留，完整条目仍为 4／25。当前资源仍按同一用户／工作区隔离；没有把切换存储 Authority 当作跨用户协作。

## 当前落地

- [SDK 授权端口](../../domainry-agent-sdk/conversation_collaboration_authorization.go)发布 `agent.collaboration.*` 的 discover、configure、initiate、view、receive、manage、communicate、execution_read、delivery_read、share 十项权限。share 目前只有权限声明，实际资料共享入口尚未交付。
- [应用服务](../internal/application/conversation_collaboration_authorization.go)批量请求当前授权，资源事实由服务加载；[Web 宿主](../internal/assembly/web/conversation_collaboration_authorization.go)与 [SaaS Identity 适配](../internal/assembly/saas/conversation_identity.go)通过 Identity SDK 重新解析主体与当前角色。未装配策略时拒绝协作请求。
- `GET /agent/collaboration-access` 提供当前可用操作与授权版本；委派详情返回当前 access。公共契约、HTTP、RPC、Remote 和 Module 绑定保持一致，当前总计 151 个 Agent Action、91 条产品 HTTP 路由、114 个 SaaS 服务动作。
- 发起、接单、管理、沟通、查看执行及读取交付分别检查权限。目录匹配和直接接单检查接收权限；worker 在执行、模型请求、工具调用及提交边界重新检查。原始操作确认继续采用原有精确操作授权，不因 Agent 通信扩大授权。
- 委派页按当前权限提供内容和操作。元数据查看不会带出任务执行、消息或交付正文。普通会话／后台任务列表、消息历史、运行详情、SSE、原结果读取、运行取消／恢复及交互回复也检查相应权限；历史工具读取与旧工具结果再次复核，不能从其他入口绕开撤权。
- 普通协作消息在进入模型及复用已消费上下文时核对沟通权限。服务器生成的状态通知保留系统身份；用户消息即使携带类似状态类型也不能作为系统通知处理。
- 读取交付与其来源核对采用限于本委派的内部读取用途。来源核对可以验证该交付的工作过程，返回给用户的仍是已提交交付；直接读取原执行、原消息和原工具结果继续需要各自权限。不同读取用途的完成缓存分开，循环检测仍共享。来源 owner 授权、私有附件范围及原回执摘要检查继续执行。
- [页面](../frontend/src/CollaborationDialog.tsx)读取当前权限，分别加载获准的目录和委派列表；撤权后关闭原执行窗口、清空交付历史和待提交管理表单。管理员可以显式点击“启用管理员协作权限”，通过已有 Identity 角色编写入口添加固定 owner 范围权限。启动和重启均不会自动添加或恢复权限。

## 验证范围

[真实 Identity／HTTP 场景](../internal/assembly/web/conversation_collaboration_permissions_test.go)覆盖：无默认授权、显式初始化、view／communicate／delivery_read／execution_read／manage／receive 分离、旧结果读取撤权、普通任务／会话旁路、原会话与运行控制，以及 SQLite 关闭重开后仍保持撤权。测试只操作临时目录中的数据库。

运行中撤权场景先允许接单，在模型请求进行期间撤回 receive，再返回工具调用；记录保留该调用的 `not_started` 状态，没有调用开始时间和结果，任务保存 `collaboration_access_denied`。工具调用计划数量不作为实际执行次数证据。

[真实 Chrome 脚本](../frontend/tests/peer-permissions.browser.mjs)通过隔离测试宿主改变真实 Identity 角色，验证打开的运行和历史内容被关闭、交付阅读不展示执行入口、管理表单撤权后消失，以及管理员初始化必须显式操作。桌面与 390px 窄屏截图见本批证据目录。原 C04 的实际交付、分歧处理、原操作回执核查及用户／Agent 转交场景一并回归。

全量回归还发现并修复两类问题：产品能力与 SaaS 清单的旧数量断言没有包含新增路由；工具会话保留最近几轮的例外规则会占满工具结果预留空间。后一项调整 [历史压缩条件](../internal/application/conversation_context.go)，不扩大 20KB 测试限额、不截断当前用户消息、不重放历史效果。[原有集成场景](../integration/conversation_execution_read_integration_test.go)再次覆盖跨轮、反复摘要、服务重启及完整回执恢复。这只是已有压缩策略的修正，E06 的完整动态上下文能力仍待办。

最终日志、源文件摘要和截图记录在 [证据目录](evidence/2026-09-13-c05-permissions/commands.md)。失败记录保留，不把早期失败运行作为验收通过。

## 下一阶段仍需实现

1. **专业工具执行权与成果查看权彻底分离。** 后续[独立结果读取阶段](testing-2026-09-13-c05-result-reading.md)已新增 Tools `ResultReadAuthorizer` 和明确注册策略，邮件／日历／网页读取适配器采用当前 Integration 数据权限复核；真实邮件交付覆盖撤销 `mail.read` 后继续阅读。报表／分析、写入回执、业务与其他 owner 尚未全部接入，仍需原执行权限及旧来源检查；来源读取策略明确拒绝时不降级。不能把当前三个读取工具族的接入记作全部专业能力已完成。
2. **跨用户／角色主体的协作。** 当前角色变更在同一用户资源范围内生效；尚无跨用户参与者、接收执行主体和委派访问范围的完整持久模型。执行身份与存储所有者必须分别建模，通过当前 Identity 主体与角色校验。
3. **明确资料共享入口。** Knowledge 当前拥有资料库成员、附件导入和成果版本。需复用这些公开 owner 能力建立有版本、范围及撤回语义的共享引用，并提供实际页面；当前私有附件继续拒绝隐式跨任务读取。

这三项继续属于 C05，不转移到后续条目，也不把仅有权限字段或页面隐藏算作完成。
