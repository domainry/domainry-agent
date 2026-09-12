# F02 读取协议增量：共享日历契约与 Google（2026-09-11）

F01 已按[原始单项要求完成核对](testing-2026-09-11-f01-completion.md)，当前按顺序实施 F02。本批完成中立日历读取契约、Google Provider 四个新读取操作及相关回归修复；尚未接 Integration 当前用户读取端口、Tools 或产品页面，**F02 保持未勾选，整体 50 / 81**。

## 实现

- Connector SDK 新增 `calendar` 子包，独立契约 `calendar-read-v1`；四个操作各有确定性哈希，外部模块编译已覆盖。主 SDK 的既有 Provider／OAuth 接口未改动。标准请求限制每页最多 100 条、查询跨度最多 93 天、忙闲查询最多 20 个日历，并明确提供 IANA 时区和带偏移的时间窗口。
- Google 新增 `calendar_list`、`calendar_events`、`calendar_event`、`calendar_availability`，revision 升为 1.1.0；保留原 `sync_calendar`。范围事件使用 `timeMin/timeMax`、`singleEvents=true`、开始时间排序和独立 pageToken，不混用同步游标。返回游标及 complete 状态，不将部分页伪装为全量。
- 所有网络请求继续由宿主 Transport 执行，凭证继续使用既有解析／刷新路径；Provider 仅返回 SDK 私有 SecretUpdates。响应规范化或事件身份核对失败后，也保留已完成刷新值，错误结果不披露原始正文。
- 全天事件保留原日期与排他结束日期；带偏移时间保留真实时刻，重复事件保留 series ID 和 original start。来源仅提供墙上时间及 IANA 时区时，只有唯一可确定的时刻才转换；夏令时缺失／重复时刻拒绝猜测。涵盖洛杉矶及 Lord Howe 半小时回拨。
- 忙闲结果按所请求日历逐一核对。遗漏、权限错误、缺少明确 busy 列表均标为不完整；不生成 free。全部成功时裁切查询窗口、合并重叠／相邻忙碌区间，再求共同空闲时间。POST freeBusy 属于读取，不改变外部日程。

设计依据：[Google events.list](https://developers.google.com/workspace/calendar/api/v3/reference/events/list)、[events.get](https://developers.google.com/workspace/calendar/api/v3/reference/events/get)、[calendarList.list](https://developers.google.com/workspace/calendar/api/v3/reference/calendarList/list)、[freeBusy.query](https://developers.google.com/workspace/calendar/api/v3/reference/freebusy/query)。跨库实现和后续顺序见[日历边界](calendar-read-boundaries.md)。

## 验证和原始证据

目录：[2026-09-11-f02-google-calendar](evidence/2026-09-11-f02-google-calendar/)，[机器清单](evidence/2026-09-11-f02-google-calendar.json)。

- [SDK 与 Google 初次完整包](evidence/2026-09-11-f02-google-calendar/contracts-provider.log)通过。补充墙上时间解析后，[最终 SDK／Google 完整 race](evidence/2026-09-11-f02-google-calendar/race-final.log)通过，覆盖外部 Module 编译、契约身份、DST 时间窗口、全天日期、IANA 唯一时刻、部分可用性、分页、原事件 ID／重复事件例外、路径转义、无效请求禁止 I/O、转换失败保留刷新凭证。Google 包 4.161 秒。
- [Connectors 全量](evidence/2026-09-11-f02-google-calendar/connectors-full.log)、[边界／生成器／Catalog](evidence/2026-09-11-f02-google-calendar/boundary-catalog.log)通过，新增四个操作已进入生成 Catalog。
- [Integration 最终全量](evidence/2026-09-11-f02-google-calendar/integration-final.log)通过；[SDK／Provider vet](evidence/2026-09-11-f02-google-calendar/vet.log)、[Integration 持久层 vet](evidence/2026-09-11-f02-google-calendar/integration-vet.log)通过。
- 本步没有前端改动，没有执行浏览器验收；完整产品日历 E2E 仍待后续接入。上游全部使用合成响应和受控 Transport，未访问实际日历账号。

## 回归发现的时间比较缺陷

[初次 Integration 回归](evidence/2026-09-11-f02-google-calendar/integration-regression.log)中 `TestLocalWorkersPersistProviderStateBeforeDispatchingEvent` 返回 processed=0／provider_calls=0。检查到现有后台状态使用 UTC RFC3339Nano 文本做数据库时间比较：该格式省略尾零，`.123Z` 的文本顺序大于 `.1231Z`，与时刻先后相反。

修复保留在 Integration 持久层：SQL 有界扫描包括当前 UTC 秒，领取前解析 due 和 lease 的真实时刻；未来任务和未到期租约不能领取，条件领取同时核对原 deadline／lease 值，避免扫描后改期仍被领取。Provider task 和 commit 两条路径一致处理；Provider 返回的 NextDueAt 以 UTC 保存。没有新增表、连接池、迁移账本或调度服务。

固定时钟强制制造 `.123Z` → `.1231Z`，原失败现在稳定通过；同秒稍后的 due／lease 保持未领取。commit 路径另用实际 SQLite fencing 核对提前／未来／未到期租约三条路径。[专项 race](evidence/2026-09-11-f02-google-calendar/worker-deadline-final.log)通过，1.712 秒；[刷新／撤销并行回归](evidence/2026-09-11-f02-google-calendar/worker-deadline-race.log)也通过。原失败日志保留，没有用重试掩盖错误。

新 SDK 契约在首次生成及加入墙上时间语义时各触发一次预期哈希失配，计算确认后固定新的契约摘要；[初始日志](evidence/2026-09-11-f02-google-calendar/initial.log)、[语义更新日志](evidence/2026-09-11-f02-google-calendar/local-time-identity.log)保留。

下一步仍在 F02：Microsoft Calendar／Graph 适配 → Integration 账号受权读取 → Tools 与产品装配 → 日历整段 E2E。不进入 F03。
