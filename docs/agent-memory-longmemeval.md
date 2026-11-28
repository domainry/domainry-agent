# Agent Memory LongMemEval-S Retrieval

## 结论

Domainry 的全在线混合记忆召回在官方 LongMemEval-S 470 个可回答样本上达到：

| 指标 | Domainry | Recallium 公开自报 | 结论 |
| --- | ---: | ---: | --- |
| Hit@1 | **97.87%** | 95.4% | Domainry +2.47pp |
| Hit@5 | 99.36% | **99.8%** | Domainry -0.44pp |
| Hit@10 | 99.57% | **99.8%** | Domainry -0.23pp |
| Evidence Recall@10 | **98.94%** | 98.6% | 精确值 Domainry +0.34pp |
| All Evidence@10 | **98.09%** | 96.8% | Domainry +1.29pp |
| MRR | **0.9855** | 0.972 | Domainry +0.0135 |

Recallium 数字来自其公开 benchmark 页面，属于第三方自报，不是本仓库独立复跑。两边对 abstention 与 served-page precision 的披露方式不完全相同，所以这里只比较同名的 session-level retrieval 指标，不能扩展为最终回答质量或 ChatGPT 产品能力对齐结论。

Domainry 当前在首位排序、多证据完整性和 MRR 上领先该公开参考；Hit@5 仍差 0.44 个百分点，Hit@10 仍差 0.23 个百分点。它不是“所有指标第一”。

## 固定数据集

- Benchmark: [LongMemEval](https://github.com/xiaowu0162/LongMemEval)，官方仓库 commit `9e0b455f4ef0e2ab8f2e582289761153549043fc`。
- Paper: [LongMemEval: Benchmarking Chat Assistants on Long-Term Interactive Memory](https://arxiv.org/abs/2410.10813)。
- Dataset: [longmemeval-cleaned](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned)，commit `98d7416c24c778c2fee6e6f3006e7a073259d48f`。
- File: `longmemeval_s_cleaned.json`，277,383,467 bytes。
- SHA-256: `d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442`。
- 500 cases：470 个有 gold evidence 的样本计入 retrieval，30 个 abstention 样本不计 retrieval。

原始 265 MiB 数据集不提交到仓库。

## 最终实现逻辑

这不是单一向量 RAG，而是四段式、来源受控的混合召回：

```text
原始问题
  -> 在线 qwen-plus 安全查询扩展
  -> 在线 text-embedding-v4 session 候选召回
  -> 在线 qwen3.5-397b-a17b 对 Top-20 候选做时间/计数/关系 rerank
  -> Qwen precision anchor + Qwen/embedding 交错 coverage
  -> 每 3 条模型排序结果与 3 条确定性 lexical 结果做块交错
  -> Agent 生产策略、scope/lifecycle 过滤、10 item/32 KiB 预算
  -> 冻结 MemoryContextSnapshot、逐条回执与可回放证据
```

### 1. 查询扩展

模型输入只有问题文本和批次内整数索引。加载数据时会删除：`answer`、`answer_session_ids`、`has_answer`、`question_id`、`question_type`。

扩展结果必须保留原问题作为前缀。服务端拒绝新增数字、专名、时间约束、否定或答案式措辞；不合格结果直接回退原问题。最终 500 条中 449 条扩展通过约束。

### 2. 在线 embedding 候选

候选模型为 DashScope `text-embedding-v4`，1024 维。每个 session 先用查询词项选出 3 个最相关 turn，形成最多 3,000 字符的有界投影，再调用在线 embedding。这里不下载、不运行本地模型。

### 3. 在线 Qwen rerank

`qwen3.5-397b-a17b` 只接收扩展问题、问题日期、候选日期和每个候选最高相关的 3 个 turn；不接收 gold 字段。候选文本被明确标记为不可信证据，不能作为指令执行。排序请求显式关闭 thinking，缓存不保存思维链。

rerank 输出只允许候选整数排列。若模型漏排或重复索引，保留其合法唯一相对顺序，缺失候选按 embedding 预排序补到末尾，并记录 `repaired_rankings`。本次开发/holdout 分别修复 10/1 个格式不完整响应；开发集有 5 个服务调用 fallback，holdout 无 fallback。

### 4. Precision + coverage

冻结缓存先保留 Qwen precision 首位，再在 Qwen 与 embedding 排名之间交错。生产侧随后以固定的 3 条模型排序、3 条确定性 lexical 排序做块交错；lexical 命中使用自己的 turn evidence 投影，而不是复用模型投影。两路信号相互独立，可降低语义模型共模漏召回。该组合不读取题型或 gold，最终仍通过 `BuildMemoryContextSnapshotForEvaluation` 调用生产 policy、预算、snapshot 和 receipt 逻辑。

## 防作弊与 split 纪律

- 开发/holdout 由 `SHA-256(question)[0] % 10 < 7` 固定切分。
- prompt、候选数、projection 和 coverage 策略只在 development 比较。
- 配置冻结后只跑一次对应 holdout。
- 生成缓存时 gold 字段在 JSON object hook 阶段剥离；检索代码接收的 Go 类型也不能表示答案或 gold session。
- 查询、embedding、rerank 和 split cache 都有 SHA-256 来源链。
- 最终评分在 `env -i PATH=/usr/bin:/bin` 下执行；进程环境只有 `PATH`，没有模型 Key。
- 评分阶段不调用模型，只读取冻结 cache；模型服务不可用不会改变同一 cache 的分数。
- 数据集 hash、cache hash、可执行文件 hash、Go 版本、环境键和算法版本写入报告。

## 最终全量结果

| Split | Cases | Hit@1 | Hit@3 | Hit@5 | Hit@10 | Evidence@10 | All@10 | MRR@10 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Development | 334 | 98.20% | 99.40% | 99.70% | 100.00% | 99.40% | 98.80% | 0.9886 |
| Holdout | 136 | 97.06% | 98.53% | 98.53% | 98.53% | 97.79% | 96.32% | 0.9779 |
| **All** | **470** | **97.87%** | **99.15%** | **99.36%** | **99.57%** | **98.94%** | **98.09%** | **0.9855** |

最终内容地址：

- query rewrite cache: `70d57324b427c0a1ed2b877d08a37f271a203851b38aff0166ff5490bcd4fa24`
- online embedding cache: `3fa932dbdf3735ac5ecfe90980e0a252942cca441c31e75b360949fe7bb3503e`
- final split-merged cache: `61053d5db3020bba0e136ca348f7a613e4417ee68de7e122ab7674c03e695010`
- final report: `92618aadc3750bc378be62e5ef244010f181e539da4df669743737a0162b576c`

机器可读报告见 `docs/benchmarks/longmemeval-s-qwen-online-2026-11-28.json`。

## 复现

下载并校验数据集：

```bash
curl -L \
  https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/98d7416c24c778c2fee6e6f3006e7a073259d48f/longmemeval_s_cleaned.json \
  -o /tmp/longmemeval_s_cleaned.json
shasum -a 256 /tmp/longmemeval_s_cleaned.json
```

缓存生成脚本位于 `cmd/domainry-longmemeval/scripts/`。Key 只通过进程环境传入，不写配置或产物：

```bash
export LONGMEMEVAL_LLM_API_KEY='<redacted>'
export LONGMEMEVAL_EMBEDDING_API_KEY='<redacted>'

python3 cmd/domainry-longmemeval/scripts/longmemeval_online_query_rewrite.py --help
python3 cmd/domainry-longmemeval/scripts/longmemeval_online_embedding_cache.py --help
python3 cmd/domainry-longmemeval/scripts/longmemeval_online_rerank.py --help
python3 cmd/domainry-longmemeval/scripts/longmemeval_precision_coverage_cache.py --help
python3 cmd/domainry-longmemeval/scripts/longmemeval_merge_split_cache.py --help
```

用冻结 cache 在干净环境评分：

```bash
go build -trimpath -o /tmp/domainry-longmemeval ./cmd/domainry-longmemeval
env -i PATH=/usr/bin:/bin /tmp/domainry-longmemeval \
  -input /tmp/longmemeval_s_cleaned.json \
  -semantic-cache /tmp/domainry-longmemeval-final-cache.json \
  -split all \
  -modes domainry_precision_coverage_provider \
  -semantic-weight 1 \
  -top-k 10 \
  -output /tmp/domainry-longmemeval-report.json
```

## 尚未对齐的能力

- 本结果是 retrieval，不是 LongMemEval 最终回答正确率。
- 30 个 abstention 样本没有做“应回答不知道”的 QA 判定。
- 没有对 ChatGPT 私有 Memory 做同数据集黑盒复跑；ChatGPT 服务端源码与内部召回算法不公开。
- 当前每题固定选择 10 条，公开参考平均只服务 3.8 条；Domainry 的 served-page precision/成本效率仍需 relevance gate 优化。
- 默认 SQLite 历史 Provider 只提供字面搜索与最近 turn；达到本结果需要产品注册全历史 Knowledge embedding Provider，并配置在线 ranker。
