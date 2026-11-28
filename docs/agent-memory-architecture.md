# Agent Memory 架构

## 结论与边界

Domainry Agent Memory 统一服务 Conversation 与 AgentTask，提供已保存个人记忆、相关历史对话和产品自有业务记忆的召回、冻结、回执、候选更新和生命周期管理。

目标是实现 ChatGPT 官方公开可观察的产品行为：保存记忆与历史对话引用分离、按请求选择相关内容、用户可关闭引用或创建、项目隔离、临时不使用/不产生记忆、纠正与删除、来源和本次使用回执。ChatGPT 服务端 Memory 的源码、排序模型、冲突合并算法和内部数据结构没有公开，因此本实现不声称复制其私有算法；实现依据只能是公开产品行为和 Domainry 自己的企业授权边界。

## 原根因

原实现只有 Conversation 在构建模型上下文时读取 `_agent_user_memories`。AgentTask 由 Runtime Workflow 直接启动，不经过 Conversation，也没有 Memory 契约，所以 Vesper Case Agent 永远拿不到该上下文。原召回还是单一进程内的字面匹配，没有历史对话引用、业务 Provider、不可变快照、候选更新、生命周期或使用回执。

修复不是让 Vesper 把 Case 数据复制进个人记忆表，而是在 SDK 建立统一的 `MemoryContextProvider` 和 `AgentMemoryCandidateSink` 边界，再由 Agent 框架负责运行级编排。

## 三层所有权

| 层 | 所有者 | 典型内容 | 默认策略 |
| --- | --- | --- | --- |
| Personal Memory | Agent | 语言、表达方式、稳定个人偏好 | 高置信度且无冲突时可按策略自动批准 |
| Task/Project Memory | Agent 或产品 Provider | 当前目标、任务约束、长期项目事实 | 候选态或人工审核；project-only 时只允许项目 Provider |
| Business Memory | 产品 Provider | Vesper Case Pattern、Known Defect、版本适用性、客户边界 | 产品负责授权、语义检索、审核和原始数据；Agent 只冻结投影和回执 |

`_agent_user_memories` 只保存 Agent 所有的个人/任务记录。Vesper Case 原文、缺陷版本和客户资料继续由 Vesper 与 Knowledge 所有，通过产品注册的 Provider 返回有界投影。

## 读取链路

```text
当前授权 + 定义级 MemoryPolicy + 单次 MemoryPreference
    -> personal / history / business providers
    -> provider 自行核对 owner、角色、授权修订和业务范围
    -> Agent 校验来源、生命周期、scope、有效期和 do-not-mention
    -> retrieval_mode=hybrid: Provider/向量相关度 + 默认模型语义重排 + 确定性信号
       retrieval_mode=lexical: 只用精确/字面/时间/重要性/置信度等确定性信号
    -> 条目/字节预算
    -> 冻结 MemoryContextSnapshot 与逐条 selected/excluded 回执
    -> Conversation ModelInput 或 AgentTask Run 持久化
    -> 模型只读取冻结版本；重试、回放不重新召回
```

业务 Provider 在 `retrieval_mode=hybrid` 时可用 embedding 或自己的混合检索返回 `Relevance`；框架不读取业务索引。`retrieval_mode=lexical` 是明确的成本/隐私边界：框架会把 preference 归一化到 Provider 看到的 Policy，Provider 不得调用 embedding、向量搜索或语义重排服务，只能返回正常的精确、字面、时间或最近记录结果。一个 Host 可通过 `MemoryContextProviderRegistry` 同时注册历史、项目文件、连接应用和业务知识等多个独立 Provider，来源身份不会被压成一个虚假的综合来源。Agent 自有个人记忆在 hybrid 模式使用当前模型做有界语义重排，宿主实现 `MemoryContextRanker` 时可替换为 Knowledge 的 dense/hybrid/rerank。任何内容都必须先通过项目、任务、会话、用户、生命周期和 do-not-mention 过滤才会进入 ranker，相关度不能扩大权限。

定义级 `AgentMemoryPolicy.retrieval_mode` 必须显式设置为 `hybrid` 或 `lexical`。`AgentMemoryPreference.retrieval_mode=lexical` 可在单次 Task 或单个 Conversation 上关闭 embedding；它只能把 hybrid 收窄为 lexical，不能把定义为 lexical 的 Agent 重新开启 semantic/vector 检索。关闭后记忆读取、生命周期过滤、预算、快照、回执和候选写入仍正常运行；只跳过 semantic candidate 扩展、统一 ranker 和语义冲突检测。

纯 lexical 路径已在官方 LongMemEval-S 470 个可回答样本上全量执行：评分进程 `model_apis_used=false`，环境只有 `PATH`，Hit@1 为 89.15%，Hit@10 为 97.02%。这证明关闭 embedding 后链路可以正常完成，但也量化了语义召回的效果差距。机器可读报告见 `docs/benchmarks/longmemeval-s-lexical-only-2026-11-28.json`，SHA-256 为 `ac05fc99dca6800235db96d961323779435670e63621a9f7cd208bf706526383`。

默认历史 Provider 会完整跟随字面搜索游标，不再只检查第一个物理页；用户和 assistant 的原始 turn 都可成为历史证据，因为旧回答可能包含此前已确认或转换后的事实。无字面重合时，它把最近 64 条原始 turn 交给语义重排。要覆盖任意久远、无字面重合的历史，产品必须注册拥有全历史向量索引的 Provider。框架不能假装最近消息池等于全历史语义检索。

Memory 内容被标记为不可信数据，不是系统指令，也不能扩大工具权限。模型请求前后的正常授权仍独立执行。

## 更新链路

```text
成功的 Conversation / AgentTask
    -> 独立 memory purpose 模型调用提取最多 8 个稳定候选
    -> 服务端再次校验 kind、scope、project-only、来源和审核要求
    -> 业务结果先原子提交，候选同时成为运行证据
    -> CandidateSink 去重、检测可能冲突并应用策略
    -> auto_approved / review_required / duplicate / not_submitted / failed 回执
    -> 回执失败由持久 pending 索引恢复，不回滚已成功的业务结果
```

候选模型从不直接写数据库。普通个人偏好只有在 `auto_personal`、高置信度、字面与语义冲突检查均通过且无需审核时才激活；存在可比较旧偏好但语义检查被 lexical 模式关闭或 ranker 不可用时，候选降级为人工审核，而不是盲目自动覆盖。项目事实和任务上下文默认进入审核。项目级业务事实由产品 Provider 决定审核流程。

效果验收使用 [Agent MemoryBench](agent-memory-benchmark.md)，以 no-memory、Domainry、oracle 和可选 ChatGPT 黑盒四组对照计算归一化回答提升，并把跨范围选择作为零容忍失败。

## 生命周期与用户控制

生命周期为 `candidate -> active -> superseded | contradicted | expired | retracted`。记录同时支持 `valid_from`、`valid_until`、`last_verified_at`、`confidence`、`importance`、`source_revision` 和 `supersedes_id`。激活替代记录时，旧记录在同一事务内变为 `superseded`。

单次会话或任务可选择：

- `disabled`：既不引用也不创建；
- `reference_only`：只引用，不产生候选；
- `reference_and_create`：在定义策略上限内引用并产生候选；
- `retrieval_mode=lexical`：本次运行不调用 embedding、向量搜索或语义 ranker，使用确定性召回；
- `project_only + project_id`：禁止个人和全工作区历史进入上下文，只允许精确项目范围；
- `do_not_mention`：保留管理记录，但召回时强制排除。

运行证据保存快照哈希、条目数、选择/排除原因、候选和落库回执。用户仍可通过 memory tools 搜索、纠正、停用或删除当前记录；不可变变更表保留审计轨迹。

`memory_summary` 提供当前 Workspace/Project/Conversation/Task 可用记忆的高层管理投影。每条摘要都绑定真实 memory ID 和 revision，默认隐藏候选、过期、撤回和 do-not-mention 内容；管理模式可显式查看这些记录。摘要是实时派生视图，不复制或改写事实来源，因此纠正和删除仍作用于原始版本化记忆。

Project Memory 现在是正式 `project` scope，而不是只存在于候选 JSON 中。project-only 召回、候选落库、手工保存、搜索和 Memory Summary 都要求精确匹配 `project_id`。

## Vesper 接入

Vesper 的 TaskHost 实现 `MemoryContextProvider`，在 `ResolveMemoryContext` 内按 `WorkspaceID`、`UserID`、`RoleKey`、`AuthorizationRevision`、`ProjectID` 和当前 Case 客户边界重新授权。请求为 hybrid 时从本地 Knowledge 做向量/字段混合检索；请求为 lexical 时只走字段、全文字面或最近记录查询，不调用 embedding。返回内容应是有界摘要与稳定来源 ID/Revision/URI，不能返回原始大文档。

如需把运行结论转为 Case Pattern 或 Known Defect，同一 Host 再实现 `AgentMemoryCandidateSink`。它负责版本适用性、来源 Case、客户隔离、人工审核和业务状态；Agent 只保存回执，不复制业务实体。

## 公开参考

- OpenAI Memory FAQ: <https://help.openai.com/en/articles/8590148-memory-in-chatgpt>
- Temporary Chat FAQ: <https://help.openai.com/en/articles/8914046-temporary-chat-faq>
- Projects in ChatGPT: <https://help.openai.com/en/articles/10169521-using-projects-in-chatgpt>
