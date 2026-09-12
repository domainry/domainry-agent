# F06 外部账号写入边界

依据 2026-09-12 当前代码：Tools 的 calendar／mail 已分别提供独立选装的读取与写入族，产品装配只消费公开门面和 SDK。Integration 的 `AuthorizeConnectionAccountRead` 仍明确要求同步只读操作、当前账号和实际 OAuth 范围。Google Provider 已在原 `gmail_send_message` 异步投递之外新增独立的类型化日程／邮件操作，Microsoft Provider 也已实现同一写入族；Integration 通过独立的宿主写入授权、执行和原回执端口提供写入。没有把旧只读入口改成任意操作通道。

| 所属库 | 本项职责 | 依赖方向 |
| --- | --- | --- |
| Connector SDK | 独立 `calendarwrite`／`mailwrite` 类型、校验、契约与操作身份 | 只复用本族公开 read DTO；无 Provider、账号库或网络实现 |
| Connectors | Google／Microsoft 原生请求、OAuth 范围、版本前提、有限结果及不确定错误 | 只消费 Connector SDK 与宿主提供的 Transport，Provider 之间不互相调用 |
| Integration | 当前账号与范围授权、固定连接／凭证、执行领取、参数身份、持久回执与原操作核查 | 通过公开 Provider 端口；数据与迁移仍由 Integration／宿主拥有 |
| Tools | 将日程与邮件工具分别适配到公共 Integration 写入用例，保留来源与实际回执 | 中立账号写入机制只共享流程；calendar／mail 适配不相互依赖 |
| Agent | 冻结目标和正文、显示精确确认／操作范围、执行账本与未知结果恢复 | 通过 Tools／公开宿主端口，不接厂商 API 或复制账号数据库 |
| 产品／网页 | 选装工具，展示账号、日历／事件、To／CC／BCC、内容和通知范围 | 使用产品原有装配与公开接口，业务规则不下沉到组件 |

已完成 SDK、Google／Microsoft Provider 和 Integration owner 增量：日程契约的 `calendar_event_inspect` 返回确切事件版本、类型和该事件完整参与者列表；创建明确通知策略，修改只允许 PATCH、精确版本、单次或整个系列范围。系列修改可能触发单独修改过的实例通知，页面需要明确告知范围。消息契约的 `mail_send`／`mail_reply` 区分新邮件与原邮件回复；没有 From、任意 MIME／Header 或模型生成的幂等 key。正文为有界纯文本，不能把 HTML／附件等原生参数绕过声明传入。

回执只保存实际标识、版本、请求引用及结果语义。日程写入成功不等于邀请邮件已送达；邮件返回 accepted／delivery unknown，Graph 202 没有 Message ID 时保持缺失。回复先重新读取原邮件，核对完整元数据、非草稿状态、主题与 Reply-To／From；To 中额外收件人、CC、BCC 必须由模型列出并由执行层明确授权，Provider 不偷偷扩成 reply-all。

Integration 已复用 `_integration_invocations` 和宿主 ORM／数据库边界，以 workspace／actor／RequestID 固定执行身份，保存精确来源和规范化类型参数指纹及有限回执；既有敏感只读模式不保存响应正文，不能直接冒充可恢复写入回执。结果不明只核查原身份，不重置为可执行或换 key 自动发送。旧异步投递与新的用户账号写入保持各自现有职责。完整请求和确认仍在 Agent 执行账本，Integration 不复制邮件／日历正文作为另一套业务存储。

业务 Action／Workflow 继续使用 J04／J05／F05 已有 owner，F06 在最终组合中回归确认与回执，不新建通用记录写入入口。实现顺序为共享契约 → Google Provider → Microsoft Provider → Integration owner → Tools／Agent／产品 → 端到端与网页；当前产品与网页已实现，最终验证见 [产品验收](testing-2026-09-12-f06-product-write.md)，后续条目尚未提前开发。

Google 明确要求通过 ETag／If-Match 做条件修改；创建可携带自定义事件 ID，但这不能代替本地回执或证明失败请求没有效果。[条件修改](https://developers.google.com/workspace/calendar/api/guides/version-resources)、[创建事件](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert)。Microsoft 创建事件支持 transactionId，带参与者时会发邀请；发送邮件的 202 只表示受理，回复接口也返回 202。[创建事件](https://learn.microsoft.com/en-us/graph/api/user-post-events?view=graph-rest-1.0)、[发送邮件](https://learn.microsoft.com/en-us/graph/api/user-sendmail?view=graph-rest-1.0)、[回复](https://learn.microsoft.com/en-us/graph/api/message-reply?view=graph-rest-1.0)。

Microsoft event 文档展示 ETag／changeKey，更新文档没有逐项说明 If-Match；Graph 通用错误文档说明 412 的前提失败语义。Provider 接入需验证实际传递与冲突处理，协议夹具不能证明真实租户的并发行为。[事件资源](https://learn.microsoft.com/en-us/graph/api/resources/event?view=graph-rest-1.0)、[更新事件](https://learn.microsoft.com/en-us/graph/api/event-update?view=graph-rest-1.0)、[Graph 错误](https://learn.microsoft.com/en-us/graph/errors)。该不确定性明确保留，不以普通 GET 后比较版本替代原生条件写入。

Google Provider 1.3.0 已按上述边界注册五个操作，通用响应投影放在中立 helper，HTML 文本转换归无业务 SDK 依赖的 `internal/textcontent`；日历／邮件保留各自原生协议。发送先读当前 Gmail profile 确定 From，回复再读取原邮件头；操作 OAuth 声明覆盖实际读取与发送组合，不自动扩大既有 grant。日程先读目标后仍使用原生 If-Match，原版本冲突不重试；创建关联 ID 和 RFC Message-ID 都不宣称可重放幂等。Google 全包 race、隔离实际 HTTP、Catalog／架构及 Integration 兼容证据见 [Google 增量验收](testing-2026-09-12-f06-google-write.md)。Integration 的旧只读授权入口未放宽，持久写入领取／参数指纹／回执已由后续 Integration 增量实现。

Microsoft Provider 1.3.0 已完成同样五个操作，具体 I/O 仍通过其自身的受控 Graph／OAuth 刷新通道，没有 Google 实现依赖。账号写权限与邮箱时区／原邮件元数据读取权限按实际调用组合声明；Graph 202 只返回受理／送达未知，真实租户 CAS 仍需厂商配置验收。在线会议描述和歧义系列时间的原生限制明确拒绝，不能破坏入会信息或改变未来当地时间。实际 HTTP、15 个新增测试、全包 race、Catalog／边界及 Integration 兼容证据见 [Microsoft 增量验收](testing-2026-09-12-f06-microsoft-write.md)。Integration 的薄公共写入接口及其自身用例／存储已接入；旧只读授权函数保持原规则，Tools 只能通过公开 SDK 调用。

Integration owner 当前以四个内部端口分开当前账号授权、参数／回执契约、领取／持久回执和 Provider 执行。应用层通过端口编排，`internal/adapter/accountwrite` 固定四个可写操作及其 SHA；`calendar_event_inspect` 继续走原只读入口。原始正文、参与者／收件人和 Provider 错误均不入调用表。写入执行前后复查账号，凭证解析后再查修订和当前 grant；不跨外部 HTTP 长持数据库事务。

同一执行身份更换账号／参数会冲突。运行中或崩溃留下的领取显示 uncertain，失败亦不重新领取；只读回执没有厂商 I/O。回执保存与调用取消分离，刷新凭证保存失败不能把已确认的外部成功改为可重发失败。专用身份从旧通用 Provider 核查查询中提前排除，避免它接管状态及占满候选额度。

Module 使用公开 Go Binding，SaaS 仅提供有服务认证的三个有界 POST；客户端禁止重定向／Cookie，无自动重试。未新增面向浏览器的任意写入口，后续产品从 Agent 的具体内容确认与执行账本调用。四组实际 Provider／HTTP／OAuth 协议／SQLite 重启、并发与撤权等证据见 [Integration owner 验收](testing-2026-09-12-f06-integration-write.md)。真实厂商账号及完整产品网页仍未验收，F06 继续保持未勾选。

Tools 与 Agent 通用端口已完成增量。Tools 通过独立写入适配器提供七个工具，原十一项只读工具不变；账号机制不依赖业务协议，日历／邮件只消费各自 SDK。写入以发现所得账号修订固定目标，稳定 ID 来自宿主执行键，Integration 继续拥有领取及回执。Schema 与类型语义先于批准检查，结果不明只读原回执。Agent 在可信启动装配提供 `ConversationConfirmationVerifier`，核对原持久批准、准确参数／定义与当前执行租约，不把 Agent 数据库交给 Tools。旧结果仍按当前权限和来源复核，宿主可选策略完整保留。证据见 [Tools / Agent 增量](testing-2026-09-12-f06-tools-write.md)；当前继续产品选装、权限声明和具体内容网页验收，不进入 N01。

产品阶段通过 Agent Web、Work、PM 现有入口选择七个写入族工具。确认验证端口在其他产品包装之前捕获，再通过公开 Tools 组合；UI 只按冻结参数展示目标、清空／系列、时区、全部 To／CC／BCC 和完整正文，并查询公开账号列表核对修订，不连接厂商或复制账号授权。写权限元数据由 Integration SDK 声明；借用 Identity 的产品不接管权限发布或 owner 生命周期。默认写入参数／后续上下文预算只在选装边界配置，显式部署预算保留。实际 60 KB 正文、重启、撤权、单项／列表批准及未知回执的整段验证见 [产品验收](testing-2026-09-12-f06-product-write.md)。
