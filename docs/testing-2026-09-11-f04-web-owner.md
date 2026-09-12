# F04 Web 服务连接 owner 验收

Integration 的实际 Module／SaaS、公开 Web Provider、生产宿主 Transport、HTTP 与 SQLite 流程已通过。F04 接下来继续产品／会话／网页端到端，不提前勾选或推进 F05。

Web 服务由管理员通过公开 Management 保存 Passport 凭证和连接，再用独立 ConnectionAccountAdministration 登记工作区归属。当前用户必须拥有对应账号动作与工作区数据范围；Provider 显式空 OAuth 替代项不制造 OAuth grant，不授予匿名权限。测试通过真实 HTTP 当前账号读取入口，经 Module 或服务认证的 SDK remote／SaaS 到达相同 owner。Identity policy principal 在本 owner 测试中由夹具构造并交给实际 SDK／HTTP 授权解析；真实 Identity 登录仍在产品阶段验证。

发现并修复两个实现缺口：

- 敏感同步账本原来只阻止成功重放，失败或正在执行的相同请求可以再次置为 running。先用[失败复现](evidence/2026-09-11-f04-web-owner/sensitive-claim-initial.log)证明两种情况都实际调用两次，再改为外部 I/O 前唯一主键插入领取。已有失败／运行中／中断身份不能重新领取；成功身份只返回无正文证据。新的请求 ID 仍需当前授权。调整位于 Integration 账本 owner，没有给 Provider 或 Tools 加数据库依赖；普通 delivery 的原有重试流程继续通过回归。
- Module 的原生 `[]string` 来源配置被类型检查拒绝，HTTP 解码的 `[]any` 却通过。改为按可序列化的 JSON object／array 形状一致校验，拒绝 JSON 字符串、null、标量和不可序列化值；内容规则仍由 Provider 决定。新增原生数组／映射与 HTTP 形状的相等行为测试。

两个拓扑各完成：管理员保存及登记前拒绝；工作区服务可用且数据库 OAuth grants 为零；生产 AES-GCM 凭证密文保存；匿名／动作无权／跨工作区和伪造权限参数拒绝；普通用户不能访问管理接口；没有探测契约时不发出计费“连接测试”；两名获准工作区用户分别获得独立调用证据；真实搜索过滤不允许来源并保留 search_id，页面正文与 unknown 完整性／警告保留；函数权限撤销、Secret 禁用和连接停用均阻止请求；轮换凭证在下一次新读取生效。

每个拓扑关闭并重开整个绑定／SQLite 两次，成功请求重放没有正文且不发出代理 HTTP，失败请求在重启前后都不再调用代理；明确的新请求仍能执行。共 14 次实际隔离 llm-proxy 协议 HTTP（每拓扑 7 次），未访问真实托管 llm-proxy、Jina 或 Parallel。敏感 owner 账本没有 query、页面正文、来源 URL 或凭证明文；SaaS 后端不接受未带服务凭证的主体投影。

验证：

- [账本修正 race](evidence/2026-09-11-f04-web-owner/sensitive-claim-final.log)通过，3.750 秒，含原账号归属／范围／执行前后变化回归。[JSON 形状与敏感读取存储 race](evidence/2026-09-11-f04-web-owner/owner-storage-race.log)通过，2.422 秒。
- [最终 Web Module／SaaS race](evidence/2026-09-11-f04-web-owner/owner-third-race.log)通过，用例 17.92 秒，包 19.630 秒；Module 6.40 秒、SaaS 11.51 秒。
- [Integration 全量](evidence/2026-09-11-f04-web-owner/integration-full.log)14 个有测试包通过，saas 包 15.202 秒，persistence 包 2.933 秒；[全量 vet](evidence/2026-09-11-f04-web-owner/integration-vet.log)通过。

首次 Web owner 测试的[原始失败](evidence/2026-09-11-f04-web-owner/owner-initial-race.log)同时包含原生 JSON 类型缺陷和测试错误地从登记回执要求 readiness；现改为通过当前用户 get 接口获取 readiness。[第二次失败](evidence/2026-09-11-f04-web-owner/owner-second-race.log)来自对一般账号 GET 误套读取接口 no-store 断言，已按实际接口范围修正；没有为满足该断言修改一般账号 GET 行为。所有失败与最终通过记录均保留。

[机器证据](evidence/2026-09-11-f04-web-owner.json)记录当前相关源码、日志、gofmt 与差异检查。较早 F04 清单属于各增量完成时的历史快照，后续 owner 改动不改写那些历史源码哈希。尚未以本阶段替代产品浏览器、真实厂商配置或 H08 部署验收。
