# F06 Google 日程与邮件写入 Provider 增量

本阶段完成 Connectors 的 Google Provider，F06 保持未勾选，总进度 54 / 81。顺序为共享契约 → **Google Provider（本阶段）** → Microsoft Provider → Integration → Tools／Agent／产品 → 整段端到端与网页。没有提前进入 Microsoft 或 N01。

## 实现与职责

`domainry-connectors/providers/google_workspace/google` 注册五个独立的同步操作，复用上一阶段 Connector SDK 的固定契约：`calendar_event_inspect`、`calendar_event_create`、`calendar_event_update`、`mail_send`、`mail_reply`。Provider revision 从 1.2.0 升为 1.3.0。旧 `gmail_send_message` 仍为异步投递，已有读取工具的操作身份、SDK 根身份及权限语义保持不变。

- 日程检查读取当前目标、ETag、系列／实例身份及完整参与者；取消事件、特殊事件类型、隐藏／省略参与者、额外匿名访客及超限集合拒绝进入修改。Google 的普通和已修改实例使用同类原生字段，本实现统一标为 occurrence，始终修改确切实例 ID，不据此跳转到系列。
- 创建支持单次事件、全天独占结束日期、带 IANA 时区及明确偏移的时间。纯文本描述转义后提交 Google；不能将 HTML、附件或 recurrence 参数混入输入。事件 ID 来自宿主工作区、连接及调用引用的确定性摘要。该 ID 仅用于关联，不声明厂商幂等；409 不冒充成功。
- 修改先重新读取目标，再按已确认版本提交原生 `PATCH` 和 `If-Match`；412 直接失败。单次／整个系列范围必须一致。省略字段保持，空字符串／空参与者集合清空；日期和时间类型切换显式清除另一原生字段。替换参与者数组保留已有 RSVP／备注，拒绝 Google 会忽略的既有 resource 类型变更。通知固定 `sendUpdates=all`，回执只表示已请求通知。Google 说明了条件修改与自定义 ID 的限制。[条件修改](https://developers.google.com/workspace/calendar/api/guides/version-resources)、[创建与通知参数](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert)。
- 邮件由当前认证邮箱的 profile 确定 From，显式使用 To／CC／BCC，支持仅 BCC、空主题及 64 KiB 正文；不选择别名。MIME 的主题／姓名编码、换行、长行和正文均由 Provider 生成。回复新读原邮件的元数据，核对目标、非草稿、完整性、主题及 Reply-To／From，原生线程和 RFC 引用仅来自该读取。坏引用拒绝，不清洗后继续发送。[当前邮箱](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users/getProfile)、[回复线程要求](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages)。
- OAuth 声明与实际请求一致：发送需要 profile 读取和发送范围；仅 `gmail.send` 不满足本实现。`gmail.compose` 可发送，但回复还需要 metadata／readonly；modify 或完整邮箱范围可满足两者。只读／metadata 授权不会因此获得发送权限。日程写入使用厂商列出的写范围，原只读声明保留；不自动扩大已授权账号的 grant。[发送授权](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages/send)、[原邮件元数据授权](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages/get)。

所有生产 I/O 仍通过宿主注入的 SDK Transport。通用结果投影抽到 `response.go`，HTML 到文本的纯转换移到 `internal/textcontent`，不依赖任何业务 SDK；原邮件 helper 保留入口，日程与邮件各自处理业务协议；没有 Provider 互相导入，也没有导入 Integration／Agent／Runtime 实现。401 可刷新后使用相同内容和原版本继续；刷新后的成功或失败都带回凭证更新，不修改宿主传入的 map。网络断线、5xx 或写入成功但回执不合法均为 uncertain，不自动重放；发送结果仅 accepted／delivery unknown。

本阶段没有账号授权入口、数据库、确认卡或执行账本的新实现。Provider 不保存正文、执行领取或回执，不承担用户确认；后续 Integration 仍需实现精确参数指纹、持久回执与原调用查询，Agent 展示具体账号／目标／内容／通知范围。当前产品尚未开放这些五个操作，不能把 Provider 测试算作完整 F06。

## 验证

以下命令在对应独立仓库执行，均使用 `GOWORK=/Users/tiger/Projects/domainry-agent/go.work`：

| 检查 | 结果 | 证据 |
| --- | --- | --- |
| Connectors：`go test -race ./providers/google_workspace/google ./internal/mailcontent ./internal/textcontent -count=1 -v` | Google 全包 2.421 秒／原邮件文本 2.394 秒通过，中立文本包完成编译；新增 14 个测试函数及子场景 | [最终 race](evidence/2026-09-12-f06-google-write/final-race.log) |
| 实际本地 HTTP，包含在上项 | 1 次创建、2 次 PATCH（其中一次 412）、2 次发送（其中一次受理后关闭连接）、1 次 OAuth 刷新；没有自动重放写入。另验证 profile 读取期间取消后零发送 | 同一日志的 `TestAccountWriteActualHTTP*` |
| Connectors：`go test ./...` | 134 个包通过／完成编译 | [全量](evidence/2026-09-12-f06-google-write/connectors-final.log) |
| Connectors：`go vet ./...` | 退出 0 | [vet](evidence/2026-09-12-f06-google-write/connectors-final-vet.log) |
| Connectors：`make fmt-check boundary catalog-check license-check` | 退出 0 | [边界与 Catalog](evidence/2026-09-12-f06-google-write/connectors-final-boundary.log) |
| Integration：`go test ./...` | 23 个包通过／完成编译，原账号组合兼容 | [兼容全量](evidence/2026-09-12-f06-google-write/integration-final.log) |

针对性验证还覆盖：修改前和修改瞬间的版本冲突、系列／实例范围、完整参与者、资源类型、RSVP 保留、全天／DST 偏移、空值 PATCH、长主题和姓名、BCC、正文完整性、来源变化／草稿／不完整元数据、非法 RFC 引用、403、5xx、空或错误回执、写阶段 401 刷新保持同一内容／ETag、刷新后失败保留新凭证、未声明参数和无调用引用时零 I/O。

Catalog 生成前后只 Google Provider 改变，增加上述五个操作，SDK 身份不变；见 [Catalog 差异](evidence/2026-09-12-f06-google-write/catalog-delta.json)。Google 生产依赖共 220 项，没有跨库服务实现或其他 Provider；前阶段清单中的 24 个 SDK 文件哈希未变。llm-proxy 仓库仍位于 `a32407a678ea7f96d70b7557765e2a01262184d4` 且工作区干净，F04 仍只消费两个已有 Web 接口。[边界审计](evidence/2026-09-12-f06-google-write/boundary-audit.json)。

## 失败记录与限制

首次专项测试的长主题夹具超出 2,048 字节，正确被 SDK 拒绝；改为限额内长主题后通过。[首次日志](evidence/2026-09-12-f06-google-write/initial-race.log)、[第二次日志](evidence/2026-09-12-f06-google-write/second-race.log)。初次格式命令包含一个不存在的文件名，随后用实际文件完成 gofmt，最终格式门禁通过。

第一版 HTTP 服务夹具把全天日期解码到已含 dateTime 的结构，形成非法混合时间；生产校验拒绝。已将夹具目标结构清零后解码，最终 HTTP 场景与全包 race 通过。[原 HTTP 失败](evidence/2026-09-12-f06-google-write/http-race.log)。没有放宽契约来让夹具通过。

补充复现了实际代码问题：64 KiB 内的纯文本描述经 HTML 转义后，检查逻辑对原生 HTML 长度校验，导致刚创建的事件无法再次进入修改。日程检查现通过中立 HTML 文本转换恢复可见文本后按共享契约校验，不裁剪；限额内包含大量尖括号、换行及字面 HTML 的往返用例已通过，原邮件文本测试同步通过。[修复前失败](evidence/2026-09-12-f06-google-write/description-before.log)、[修复后最终 race](evidence/2026-09-12-f06-google-write/final-race.log)。修复后重新执行上述全量和静态检查，先前阶段日志保留。

测试使用隔离的 Google 协议 HTTP 服务器与合成账号，没有真实 Google OAuth 应用／账号配置，不声称真实厂商投递、邀请送达、租户并发或模型质量验收。尚无本阶段产品网页测试；应在 F06 后续宿主／工具／产品接通后执行。旧阶段清单作为历史快照保留，当前文件及日志哈希见[机器清单](evidence/2026-09-12-f06-google-write.json)。
