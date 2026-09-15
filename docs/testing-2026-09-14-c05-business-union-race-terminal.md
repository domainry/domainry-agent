# C05 组合业务阅读范围：实际 race 终态

同一最终生产版本的第二次实际 race 将外层观察窗口从 90 秒改为 5 分钟，委派任务自身的 90 秒预算、12 步／12 调用／8192 字节均未改变。独立业务 RPC 根 50.46 秒通过；跨账号复杂筛选 Agent 根 134.55 秒失败，包 186.542 秒退出 1。

这次取得了委派任务真实终态。执行运行 90059 毫秒后 failed：目录、两页原查询、详情、关联查询和约定读取六次调用均完成；第 6 步 delegation_update 调用在运行截止时以 business_unavailable 失败，未形成新版交付和账户验收。因此第一次 90 秒外层观察失败不能算任务失败，本次终态则证明 race 下原 90 秒任务预算不足，不能记为完整场景通过。

CPU profile 时长 185.19 秒、总采样 332.35 秒。SchemaSnapshotHash 累计 63.88 秒，ApplicationSchemaQueryApplicationService.ForPrincipal／RecordSchemaSnapshotProvider.SchemaForPrincipal 累计 50.48 秒，来源审计累计 45.48 秒。累计 CPU 路径相互重叠，不作为墙钟时间。下一步处理同一请求内重复构建和散列应用 Schema，任务预算不扩大。

9893 份最终依赖输入核对未变。完整日志、任务与运行终态、CPU profile、观察测试、二进制摘要和验收状态见[终态归档](evidence/2026-09-14-c05-business-union-race-terminal/index.json)。第一次观察中断另见[观察归档](evidence/2026-09-14-c05-business-union-race-observation/index.json)，两个阶段不合并为通过。

C05 和整表仍未完成，整表保持 4／25。
