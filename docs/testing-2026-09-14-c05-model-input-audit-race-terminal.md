# C05 模型输入审计：同一实际 race 的终态

上一阶段 session 26525 对应的实际进程已结束，PID 56394／56478 已不存在，完整日志记录失败终态：`TestLegacyAgreementUpdateRestartResumeAndOriginalHistoryThroughRealHTTP` 根耗时 475.27 秒、包耗时 476.834 秒。

修订运行状态 failed，运行时长 180006 毫秒，原任务 180 秒预算保持。第 0／1 步原报表和分析分页完成，第 2 步 delegation_get 完成，第 3 步 delegation_update 完成，调用时长 30007 毫秒；第 4 步中断，没有工具调用。新版交付已提交，但后续新账户验收没有执行，不能把此前保留的旧验收记录当成新版验收，也不能记作完整生命周期通过。

CPU profile 时长 475.41 秒、总采样 874.27 秒。pthread_cond_signal 平坦采样 299.91 秒；当前主体解析累计 165.49 秒、来源审计 run 累计 155.50 秒。累计采样重叠且包含整个根场景，不作为修订运行的墙钟或受控性能收益。继续处理实际生命周期中的重复授权读取和耗时。

3969／9890 份原启动保守依赖输入全部核对未变，含未执行的依赖包测试源码。完整日志、失败运行 JSON、终态 CPU profile、二进制摘要、输入核对及验收记录在[独立终态归档](evidence/2026-09-14-c05-model-input-source-audit-race-terminal/index.json)。此前 RunningAtSeal 归档保持原样。

本终态补齐此前等待，C05 与整表仍未完成，整表保持 4／25。
