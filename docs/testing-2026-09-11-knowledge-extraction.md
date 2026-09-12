# K06 字段与表格提取增量验收

> 历史验收记录：下文的本地原文件解析实现已于 2026-09-11 按用户要求删除，不再属于当前能力或后续计划。现存 `knowledge_extract` 只处理 Connector 返回的文本。当前代码边界见[移除说明](document-parsing.md)，在线展示验收见[原件预览](testing-2026-09-11-original-preview.md)。

当时状态（历史）：**进行中，不勾选 K06**。真实 PDF、DOCX 提取通过；新增本地 XLSX 解析 + 真实模型 73.57 秒通过，网页来源权限及长表补验通过。持久解析缓存 / 任务已完成后续[缓存验收](testing-2026-09-11-document-parse-cache.md)；会话私有附件直接提取、原件按页读取仍未完成。当前实现与后续设计见[本地解析文档](document-parsing.md)。下方首轮记录保留历史失败，本地解析结果见末节。

## 已实现

- SDK 新增 `knowledge_extract` 和格式无关的 `KnowledgeExtractionContentSource`。模型在现有工具循环中读取正文、提出有限 RE2 规则；确定性引擎只从实际片段取得值，没有额外隐藏模型调用。
- `internal/extraction` 不做 IO；支持文本、整数、十进制、日期、布尔值、必填和枚举校验，返回 valid / missing / invalid / ambiguous；精确数字以字符串保存。表格支持列规则及有界分页。
- 输出保存规则摘要、原始来源回执、真实引用和 UTF-8 片段字节偏移；不把偏移当成 PDF 页码或 Excel 单元格。`original_complete` 保持 false，表格 complete 只针对给定解析片段内的匹配。
- 应用层通过公共端口获取正文，在发布与历史复用前复核当前权限和来源，并重算提取结果。值、引用、位置、规则、原始来源和完整性标记篡改被拒绝；SDK / 业务层不导入具体解析服务或数据库实现。
- 网页显示提取动作、Markdown 字段 / 表格和可核对来源；补了回复表格分页、具名复制 / 下载操作以及会话辅助文字可读性。源文件删除或 Identity 撤权后，相关旧回复与引用被隐藏。

## 真实 Verdent 与真实模型

模型：`glm-5.3-flash-free`。输入是仓库生成并重新读取核对过的真实 PDF / DOCX / XLSX 文件，不是修改后缀的文本。权限、上传、索引、原件重启、工具循环和删除均通过产品 Identity / HTTP / SQLite / worker 链路执行。

| 格式 | 结果 | 核对内容 |
| --- | --- | --- |
| PDF | 73.73 秒通过 | 客户 Qinghe Fixture；金额 9007199254740993.25；日期 2026-09-11；true；邮箱 missing；Pencil / Paper 两行准确值与来源 |
| DOCX 首轮 | 失败 | 模型反复使用未命名捕获组，原接口要求命名组；通用错误不足以帮助恢复，90 秒时仍运行 |
| DOCX 修复后 | 65.00 秒通过 | 与 PDF 相同字段和两行明细均由持久工具结果核对；明确支持按列顺序捕获并提供可操作错误信息 |
| XLSX 首轮 | 失败，未重传冒充成功 | 上游仅返回“表格 synthetic-extraction.xlsx[Synthetic Invoice]:9 行 × 1 列;列:K06 SYNTHETIC EXTRACTION ACCEPTANCE”，没有数据单元格。模型最终报告缺失，没有补造文件中的值 |

DOCX 重跑只执行失败格式，没有重复上传已经通过的 PDF。四份合成资料各有一次成功 POST / DELETE；失败轮通过持久清理流程删除，并额外以原权限确认 fetch 1004、search 不再包含该文档。保留其余既有命中，不把清理描述成全库为空。

XLSX 上游源码核对：`kb-search-api` `dbf61406e55b8a446f026cde941ce7f9a9f33bda` 的 `handler/fetch.go` 直接返回 chunk.Content；`router/router.go` 没有读取表格单元格的 API。已保存的 API 返回内容不足以完成正向表格验收。用户随后提出本地解析，已据当前原件存储和权限端口提出方案，不能将方案写成已实现。

## 网页及回归

- 新浏览器使用临时独立 Identity 宿主和实际前端构建，原 PDF 上传 / 下载字节真实；模型和解析正文为明确协议夹具。
- 首轮指出表格分页 / 无障碍名称和辅助文字字号问题。修正后的桌面 / 手机测试通过：值、邮箱缺失、两行明细、来源窗口、完整宿主重启、提取动作撤权 / 恢复、文档删除及历史隐藏。三个机器基线均无错误、无整页横向溢出；宿主 32.10 秒完成且自有文档清理通过。
- 新表格组件的 25 行翻页 / 复制 / 下载补验未完成：补验夹具只请求默认 20 行，导致“下一页”按实际结果正确禁用，测试失败。已将夹具请求上限修为 50，并等待流式回复完成后操作；尚未重跑这项补验，不声称已通过。此问题不影响已通过的两行明细与权限验证。
- Agent / SDK 全量 Go 测试通过；提取引擎、应用层和集成专项 race 通过；相关 vet、前端 33 项测试和构建通过。覆盖金额精度、日期 / 枚举异常、缺失 / 多值、规则边界、取消、篡改拒绝、在途撤权、冻结输入断连 / 服务重建及远端拒绝后隐藏。
- Vite 仍提示已有大 chunk；不把构建成功描述成完成包体积优化。

## 证据与复现

仓库内[机器记录](evidence/2026-09-11-k06-extraction.json)及同名证据目录保存逐轮结论、模型输出、清理复核、浏览器报告和日志，内容均为合成资料。数据库及原始持久恢复目录保留在本机临时路径，未复制凭证。

```sh
go test ./internal/extraction ./internal/application ./integration -run 'Extract|Normalized' -count=1
python3 scripts/test-agent-managed-document-live.py --live --extract-formats
# 对明确失败格式的定向重试：
python3 scripts/test-agent-managed-document-live.py --live --extract-formats --formats docx
```

真实脚本会创建新合成文档并完成持久清理；不得以重复创建代替核查未完成的旧任务。本批仅在本地解析接通并完成专项测试后定向重跑 XLSX，没有重复已通过的 PDF / DOCX。

## 本地原件解析与 XLSX 定向重验（2026-09-11）

已新增 SDK `DocumentParser`，由 Module 注入 `internal/infrastructure/documentparser`，应用层 `knowledge_local_document.go` 通过原件存储和授权端口协调。XLSX 使用 Excelize 2.11.0，CSV / TSV 使用标准解析器；不运行公式或宏，保留已有缓存值、原始字符串、合并区域、工作表与单元格位置。应用层未导入具体解析实现。

解析前后与历史重用检查当前 Identity 下载动作、资料库成员、文件哈希、归档 / 删除及原件引用；已保存解析结果会按当前原件重算，位置或值被篡改不能通过。解析状态还未独立持久化，目前没有缓存 / 后台解析队列；工具回执沿用现有持久保存。详细资源边界见本地解析文档。

实际输入仍为 `integration/testdata/knowledge-extraction/synthetic-extraction.xlsx`，SHA-256 为 `1c418895a86623474b9e3b00c3d9a6f1d9cd8f04ed28c8bf3a665dcce6f1c5b2`，与首轮失败文档的原件 SHA 一致。定向真实验收命令：

```sh
python3 scripts/test-agent-managed-document-live.py --live --extract-formats --formats xlsx
```

真实 Identity / HTTP / SQLite / 资料库索引任务 / Verdent 检索与 `glm-5.3-flash-free`，测试 **73.57 秒通过**。持久工具回执的来源为 `agent_parsed_document`，并校验解析器标识与原件哈希；不是用摘要或测试文本替代单元格。

| 数据 | 核对结果 | 真实位置（Synthetic Invoice） |
| --- | --- | --- |
| 客户 | Qinghe Fixture | B3 |
| 金额 | 9007199254740993.25，以字符串保存 | B4 |
| 日期 / 状态 | 2026-09-11 / true | B5 / B6 |
| 邮箱 | missing，value=null | 无证据，不编造位置 |
| 明细 1 | Pencil / 2 / 1.2，原价字符串 1.20 | A10 / B10 / C10 |
| 明细 2 | Paper / 3 / 12.5，原价字符串 12.50 | A11 / B11 / C11 |

本次合成文档一次 POST / DELETE 均成功，清理完成、搜索不再包含该文档。其余既有命中保持不变。恢复目录 `domainry-managed-private-live-nyi_m1t1/xlsx` 保存原始数据库，仓库仅复制无凭证的合成结果与清理报告。

- `TestLocalSpreadsheetConversationAuthorizationAndRestart`：真实 XLSX / 文件存储 / ORM / 工具循环，远端夹具刻意只有摘要；原件字段和 B4 通过，断连后重建服务恢复同一冻结请求，撤销下载动作 / 成员后隐藏、恢复后复核；解析中移除成员或持久标记删除，内容不会进入模型。
- 解析器四项测试覆盖同一原件、稀疏单元格、合并区域、公式不执行、CSV 引号 / 换行 / 空格、坏 UTF-8 / ZIP、哈希错误、路径穿越、重复条目、宏和解压炸弹 / 行列上限。提取授权补验字段位置与引用位置篡改。
- `browser-xlsx-local`：二进制原件网页上传 / 下载校验，实际本地解析，确定性模型；字段、两行明细、来源工作表与第 4 行，完整宿主重启、真实 Identity 撤权 / 恢复、手机删除与历史隐藏全部通过（宿主 45.83 秒）。该网页用例不冒充真实模型；真实模型由上述独立联调验证。
- `browser-table-local`：补验 25 行，前页 20 / 后页 5；复制与下载只含当前页，桌面 / 手机和删除隐藏通过（宿主 38.99 秒）。原先夹具请求上限导致的失败已解决，保留原失败报告。
- 两个网页用例各 3 个质量基线均 0 错误、无整页横向溢出，截图已人工查看；自有夹具文档全部清理、临时服务正常退出。
- Agent / SDK 全量通过；解析器 / 应用 / 集成专项 race、`go vet ./...`、前端 33 项测试及构建通过。Vite 仍报告已有大 chunk，未声称已优化。

**仍未完成：** 持久解析缓存 / 租约任务、私有会话附件直接提取；PDF / DOCX 本地适配器仍是后续实现（当前格式场景使用已验证远端解析）。本批不新增数据表，不将检索替换为另一套向量服务，不勾选 K06。

后续缓存增量已通过：第 12 个迁移、私有 JSON、短租约续租、在途停止接管、原子撤销 / 清理；带缓存真实模型 60.85 秒及网页通过。上文“缓存未完成”属于前批时点，当前实现与完整证据见[缓存验收](testing-2026-09-11-document-parse-cache.md)。
