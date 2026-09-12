# F02 Microsoft Calendar 读取增量（2026-09-11）

按 F02 内部顺序完成第二个 Provider：Microsoft 新增日历目录、窗口事件、事件详情和忙闲四个读取操作，复用 Connector SDK 的 `calendar-read-v1` 类型和操作哈希，Provider revision 为 1.1.0。原同步操作保留。**F02 未完成，整体仍为 50 / 81**；下一步是 Integration 当前账号读取授权，之后 Tools／产品与日历 E2E。

## 协议与边界

- 所选日历使用 `/me/calendars/{id}/calendarView`，覆盖重复事件的实例和例外；窗口带明确偏移并转换为 UTC，结果按请求的 IANA 时区显示。同一重复小时的两个时刻仍有不同偏移。[Graph calendarView](https://learn.microsoft.com/en-us/graph/api/calendar-list-calendarview?view=graph-rest-1.0)
- Graph `getSchedule` 的输入是邮箱日程，不能替代任意次级日历。忙闲按选中的 calendar ID 逐页读取有限字段，每个日历最多 10 页、每页 100 个事件；未知状态、重复事件、不明确时刻或分页未完均不生成空闲结论，失败响应不返回可用性。全部成功后由 SDK 裁切窗口、合并忙碌并计算共同空闲。[Graph getSchedule](https://learn.microsoft.com/en-us/graph/api/calendar-getschedule?view=graph-rest-1.0)
- 全天事件必须保留原时区中的日期和排他结束日期。首次按 UTC 读取后，对于非 UTC 全天事件，以来源 `originalStartTimeZone` 再读同一事件，交给 Graph 解析其 Windows／IANA 时区。来源区域值不能注入请求头，返回 ID／changeKey／状态必须仍匹配，开始结束必须是来源区域的午夜；缺少元数据或版本变化时拒绝猜日期。[Graph event](https://learn.microsoft.com/en-us/graph/api/resources/event?view=graph-rest-1.0)
- 详情只接受明确的 text body；列表不请求正文。读取带 `IdType="ImmutableId"`。使用精确日历／事件路径并转义 ID，错误不返回厂商正文或半成品事件。[Graph event.get](https://learn.microsoft.com/en-us/graph/api/event-get?view=graph-rest-1.0)
- nextLink 必须与当前固定 endpoint 的协议、host、路径一致，参数只能保留原范围／字段／页大小并增加一个合法分页参数。返回给调用者的是有限游标，不能指定目标 URL；换账号、workspace、日历、窗口、页大小或显示时区不能续用原游标。游标绑定不充当授权，Integration 仍须每次检查当前账号。
- 新代码全部在 Microsoft Provider，依赖公开 Connector SDK，未导入 Google、Agent、Tools 或 Integration 实现。复用宿主 Transport 和原刷新方法，每次读取独立保存后续页使用的凭证副本；后续请求／转换失败仍返回已完成的私有 SecretUpdates，不修改调用方 secrets。

## 验证

证据目录：[Microsoft Calendar](evidence/2026-09-11-f02-microsoft-calendar/)，[机器清单](evidence/2026-09-11-f02-microsoft-calendar.json)。

- [初次 Microsoft 完整包](evidence/2026-09-11-f02-microsoft-calendar/provider-initial.log)通过，2.701 秒。
- 补充全天读取中刷新／版本变化、UTC 全天无多余请求、秋季重复小时后，[最终完整 race](evidence/2026-09-11-f02-microsoft-calendar/provider-race.log)通过，1.474 秒。覆盖公开 Registry／Call 四操作、主次日历、空页续页、跨 DST、全天原日期、版本核对、来源时区注入拒绝、nextLink 目标／参数拒绝、游标范围、未知／分页上限无空闲、两次刷新后的失败保留凭证、详情身份／格式和非法请求零 I/O。
- [Connectors 全量](evidence/2026-09-11-f02-microsoft-calendar/connectors-full.log)、[架构边界与 Catalog 检查](evidence/2026-09-11-f02-microsoft-calendar/boundary-catalog.log)、[Provider vet](evidence/2026-09-11-f02-microsoft-calendar/provider-vet.log)通过。四个操作进入生成 Catalog。
- [Integration 全量兼容回归](evidence/2026-09-11-f02-microsoft-calendar/integration-compatibility.log)通过，公开 module 包 10.528 秒。

本增量全部使用确定性 Transport 协议夹具，未连接真实 Microsoft 账号，也未运行产品日历浏览器 E2E。当前缺口仍是受权执行入口和产品装配，不能以 Provider 包测试勾选 F02。没有进入 F03。
