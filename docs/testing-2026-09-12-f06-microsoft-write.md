# F06 Microsoft 日程与邮件写入 Provider 增量

本阶段按既定顺序完成 Microsoft Provider，实现共享契约的五个操作；F06 保持未勾选，进度仍为 54 / 81。Google 阶段之后只修改 Microsoft Provider 和它的 Catalog／验证声明，没有提前实现 Integration、Tools 或 N01。

## 代码与边界

`domainry-connectors/providers/microsoft_365/microsoft` 增加 `calendar_event_inspect`、`calendar_event_create`、`calendar_event_update`、`mail_send`、`mail_reply`，Provider revision 为 1.3.0。共享 SDK 的契约／操作 SHA 保持不变；旧同步、日历和邮件读取操作仍是原来的 GET 与只读语义。

原请求／刷新通道扩展为 Provider 内部的受控 Graph 调用：具体方法、目标、JSON、Prefer 和 ETag 均由适配器选择，生产 I/O 仍通过宿主 SDK Transport。通用结果投影移到 `response.go`；calendar 与 mail 各自保留协议转换、校验与 OAuth 声明，只共享本 Provider 的请求机制。没有 Google Provider 导入，也没有 Integration／Agent／Runtime 实现依赖。沿用中立 `internal/textcontent` 转换原生 HTML，它不依赖业务 SDK。

### 日程

- 检查当前事件的实际 `@odata.etag`、类型、完整参与者和正文。要求明确的 organizer 身份及非隐藏参与者；缺失数据、取消事件、未知类型、重复／超限参与者拒绝进入修改。单次、occurrence、exception 和 seriesMaster 映射为 SDK 的四类，后续仍使用精确目标 ID。所有事件调用请求 ImmutableId。
- 全天事件沿用既有读取机制，按实际原时区重新读取日期，并核对 changeKey 后返回；来源复读期间变更会失败，不能把 UTC 日期猜成原日历日期。正文按可见文本校验限额，HTML 转义扩大原生长度时不会错误拒绝合法纯文本，超限也不裁剪成“完整”。
- 创建只提交已声明的标题、纯文本描述、地点、起止、全天标记及参与者。transactionId 从宿主工作区／连接／调用引用生成，仅作关联，不声明可重放幂等，也不把 409 当成成功。API 创建返回实际事件和 201；回执没有合法 ID／ETag 时为 uncertain。[创建事件](https://learn.microsoft.com/en-us/graph/api/user-post-events?view=graph-rest-1.0)。
- 修改先复读并核对已确认的版本／单次或系列范围，再携带原始 `If-Match` 执行 PATCH。未提供字段不提交，空字符串／参与者数组保留清空语义；保留已有参与者 RSVP。401 刷新保留同一正文与版本；412 不覆盖、不刷新版本重试。Graph 对参与者变更和整个系列有不同的通知行为，回执只表示请求了通知，不保证邀请送达。[修改和通知行为](https://learn.microsoft.com/en-us/graph/api/event-update?view=graph-rest-1.0)。
- 创建及修改时间前读取当前邮箱支持的 IANA 时区，响应有界且不跟随续页；不猜 Windows 映射。日历写权限之外声明该读取所需的 User.Read 或文档列出的更高权限，短名／完整 URI 混合形式均正确作 AND 校验。全天保留本地午夜及独占结束日期；常规时间保留地区时区。Graph 的本地 dateTime 没有重复时刻选择字段，单次事件遇到 DST fold 时转换为同一个 UTC 瞬间；整个系列不能改成 UTC 而改变后续当地时间，因此明确拒绝该歧义。[邮箱支持时区](https://learn.microsoft.com/en-us/graph/api/outlookuser-supportedtimezones?view=graph-rest-1.0)、[时间表示](https://learn.microsoft.com/en-us/graph/api/resources/datetimetimezone?view=graph-rest-1.0)。

在线会议的正文含原生入会信息。当前纯文本契约没有稳定的会议 blob 编辑协议，直接替换可能禁用在线会议；因此 isOnlineMeeting 为真或状态未知时拒绝描述字段修改，其他受支持字段仍可修改。没有删除会议 blob 或构造猜测的替代内容。[Microsoft 的正文修改要求](https://learn.microsoft.com/en-us/graph/api/event-update?view=graph-rest-1.0)。这是明确的 Provider 限制，不冒充已实现在线会议正文编辑。

### 邮件

新邮件通过 `/me/sendMail`，由当前认证邮箱决定 From，显式提交 To／CC／BCC、主题及纯文本，保留 Sent Items。仅 BCC、空主题、有界 64 KiB 正文均支持。调用方不能提交 From、任意 MIME／Header 或附件。回复先按 ImmutableId 重新读取原邮件元数据，核对目标、非草稿、完整性、主题和 Reply-To／From，再调用原生 `/me/messages/{id}/reply`；不自行拼线程或扩成 reply-all。[发送](https://learn.microsoft.com/en-us/graph/api/user-sendmail?view=graph-rest-1.0)、[原生回复](https://learn.microsoft.com/en-us/graph/api/message-reply?view=graph-rest-1.0)。

发送要求 Mail.Send／Mail.Send.Shared，Mail.ReadWrite 不隐含发送；回复还要求当前原邮件元数据的读取权限，基础权限仍不开放正文搜索。Graph 的 202 没有响应体和消息 ID，正常回执仅为 accepted／delivery unknown，不编造 MessageID、ThreadID 或送达证明。错误状态、意外响应、5xx、断线均按当前读／写阶段区分；已经可能产生效果的失败为 uncertain，Provider 不自动重放。刷新后的凭证更新在失败时仍返回宿主，不修改传入的 secret map。

## 验证与证据

对应仓库执行以下检查，均使用 `GOWORK=/Users/tiger/Projects/domainry-agent/go.work`：

| 检查 | 结果 | 日志 |
| --- | --- | --- |
| Connectors：`go test -race ./providers/microsoft_365/microsoft -count=1 -v` | Microsoft 全包 2.463 秒通过；新增 15 个测试函数及子场景 | [最终 race](evidence/2026-09-12-f06-microsoft-write/final-race.log) |
| 包内实际 HTTP 流程 | 1 次创建、3 次 PATCH 尝试（401、成功、412）、2 次发送（202、受理后断线）、1 次刷新和 1 次邮箱时区读取；无不确定写入重放；取消原邮件读取后零发送 | 同一日志 `TestAccountWriteActualHTTP*` |
| Connectors：`go test ./...` | 134 包通过／完成编译 | [全量](evidence/2026-09-12-f06-microsoft-write/connectors-full.log) |
| Connectors：`go vet ./...` | 退出 0 | [vet](evidence/2026-09-12-f06-microsoft-write/connectors-vet.log) |
| Connectors：`make fmt-check boundary catalog-check license-check` | 退出 0 | [边界与 Catalog](evidence/2026-09-12-f06-microsoft-write/connectors-boundary.log) |
| Integration：`go test ./...` | 23 包通过／完成编译，原账号组合兼容 | [兼容全量](evidence/2026-09-12-f06-microsoft-write/integration-full.log) |

专项还覆盖：四类事件的实际目标保持、清空 PATCH、版本冲突、隐藏／缺失参与者、非 organizer、在线会议正文拒绝、全天原时区与复读变更、DST 两个同名时刻、未知／不完整时区列表、正文 HTML 与大小、创建错误回执／409、显式邮件收件人、原邮件变更／草稿／不完整元数据、BCC／最大正文、非法参数与缺失引用零 I/O、发送和修改阶段的刷新、403／5xx／断线／无效 JSON／意外 200 或虚假消息 ID。

初次专项 1.715 秒通过，增加实际 HTTP／复读及更多边界用例后全包 2.388 秒通过，最终新增正文和创建回执用例后为上述 2.463 秒；本阶段没有失败测试。[初次专项](evidence/2026-09-12-f06-microsoft-write/initial-race.log)、[中间全包](evidence/2026-09-12-f06-microsoft-write/provider-race.log)。

Catalog 仅 Microsoft 从 1.2.0 变为 1.3.0，并增加五个操作；SDK 身份不变。前一阶段记录的 Google／SDK／共享工具共 60 个文件哈希未变。Microsoft 生产依赖共 219 项，没有跨库服务实现或其他 Provider。llm-proxy 服务仍在原提交且工作区干净。[边界审计](evidence/2026-09-12-f06-microsoft-write/boundary-audit.json)及[当前机器清单](evidence/2026-09-12-f06-microsoft-write.json)。

## 未完成范围

本阶段使用隔离 Graph HTTP 服务器和合成 OAuth 数据，不声称真实 Microsoft 租户／邮件送达／外部邀请验收。Graph 更新文档没有逐项列明 event 的 If-Match，通用错误文档定义 412；本阶段证明原版本确实传递、412 不重试，以及实现内部的并发保护路径，不能据协议夹具宣称真实租户原子更新已验收。[Graph 错误语义](https://learn.microsoft.com/en-us/graph/errors)。真实账号配置缺失这一事实保留，不用 GET 比较替代原生条件写入。

下一阶段继续 F06 的 Integration owner：当前账号与实际 grant 授权、精确参数身份、持久执行领取／回执及原调用查询；旧只读授权入口保持只读。之后接 Tools／Agent／产品确认、整段端到端和网页。此处没有数据库或确认 UI 新实现，也没有业务 owner 复制、实际模型或 H08 整体部署结论。
