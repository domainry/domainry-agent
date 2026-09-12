# F03 邮件工具与共享账号机制增量验收

四个邮件读取工具已通过 Tools 公开门面交付；当前继续 Integration 邮件组合与产品／会话／草稿端到端验收，F03 保持未勾选。

## 边界与实现

- `module.MailAdapter`／`MailDefinitions` 公开四个只读工具：`mail_accounts`、`mail_list`、`mail_search`、`mail_read`。独立 `mail.*` 动作、版本 1、自然幂等、90 秒时限、1 MiB 输出上限；严格 schema 不接受账号身份、工作区、范围或凭证。
- `mailtools` 只依赖 Mail SDK、Integration SDK 和 Tools 内部端口。参数分别转换为中立列表／搜索／读取 DTO；搜索核对返回方言，详情核对原始邮件 ID。邮件头缩短明确改变 `metadata_complete`，列表每类地址最多 10、详情 20；地址与原邮件／RFC ID 不缩短成其他目标。原正文完整性、遗漏原因、分页、Graph 搜索上限均保留。
- 从当前日历适配器抽出 `internal/adapter/accounttools`，集中管理宿主实时身份、账号发现、精确操作授权、执行前后复核、敏感重放与历史来源。各业务适配器只提供操作身份、参数及展示规则，两个业务包互不导入，共享机制也不导入日历／邮件协议。新增架构检查锁定这些方向，应用／领域不得反向依赖适配层的规则不变。
- 日历公开字段、定义、四个 Provider 操作、`calendar-tool:` 请求身份和结果 envelope 保持兼容；邮件使用自己的 `mail-tool:` 身份。没有新增账号表、邮箱表或直接依赖 Integration 实现。测试的账号 owner 夹具也已独立，避免邮件测试依赖日历业务夹具。
- 账号发现按指定操作筛选；基础邮件范围只显示其允许的操作。发现结果中的账号名称同时受当前 list 和 read 归属约束，历史正文仍需当前工具策略及对应账号来源授权。执行中撤权或来源修订变化不会返回正文；敏感调用重放缺正文时明确失败。
- 同一个 Registry 可以选装 5 个日历工具和 4 个邮件工具；适配器无法借共享机制接受另一业务的操作。若来源 ID 的 JSON 表示本身已超过输出上限，会拒绝披露，不截断为另一个邮件 ID。

## 验证与证据

| 验证 | 结果 |
| --- | --- |
| Tools 完整 race | 5 个有测试包通过，公开 Module 3.400 秒；[日志](evidence/2026-09-11-f03-mail-tools/tools-full-race.log)。 |
| Tools vet | 通过；[日志](evidence/2026-09-11-f03-mail-tools/tools-vet.log)。 |
| 原日历产品流程复验 | `TestCalendarProductIdentityConversationAndRestart` 通过，Agent Web 包 6.396 秒；[日志](evidence/2026-09-11-f03-mail-tools/calendar-product-compatibility.log)。真实 Identity／HTTP／SQLite，模型与厂商协议夹具，含重启和撤权。 |
| 格式、公开边界、源码／证据 hash | [机器清单](evidence/2026-09-11-f03-mail-tools.json)。Tools 目录没有 Git 元数据，不把 Git diff 检查算作通过。 |

新增公开调用测试覆盖三个读取契约映射与原邮件引用、基础账号可用性、发现历史撤权、禁止身份参数和越界／外来字段；执行前后工具／账号／范围／修订变化、不同用户与工作区、错误来源 hash／账号、旧正文复核、稳定调用身份、敏感重放、未配置产品、Graph 搜索上限、正文部分返回、错误方言／邮件 ID、25 封大邮件头、超大来源拒绝及九个工具并存。原日历的公开边界与完整性测试保留并通过。

本轮尚未新增邮件产品页面、模型摘要／事项提取或 Knowledge 草稿验收。下一步按 F03 继续验证 Google／Microsoft × Module／SaaS 的真实 owner 链路，再完成产品选装、来源草稿和邮件 E2E；不会据此进入 F04 或 F06。
