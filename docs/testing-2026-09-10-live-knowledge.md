# 真实知识库、模型引用与文档删除验收

日期：2026-09-10。使用真实 Verdent `gpt-5.6-sol` / Responses、真实知识服务，以及临时 SQLite 中装配的真实 Identity Module。浏览器使用编译后的产品前端；没有修改现有用户数据库。

## 结果

`TestLiveKnowledgeThroughIdentityHTTP` 通过，耗时 312.23 秒（包含浏览器操作）。本次覆盖 K03 的真实来源引用；K01 / K04 的远端私有文档 ACL 未完成，不能用本次本地 Identity 动作撤权替代。

1. 在配置的 bcri 知识库中创建专用合成文档；随机文档 ID 在写入前已验证不存在。资料只包含验收标识、30 日付款、周报三个章节和费用合计 200.00 元。
2. 推送成功后，首次 fetch 为 `PENDING` 且没有 chunks；等待 `INDEXED` 后取得 4 个实际片段，search 也取得 4 个命中。
3. 模型实际调用 `knowledge_search`，再调用 `knowledge_read`；最终回复准确包含付款期限、三个章节和费用合计，4 条结构化引用均绑定实际取得的片段。
4. SSE 保留工具完成事件和引用 ID；完整关闭并重建宿主后，消息正文和引用保持一致。
5. 通过真实 Identity API 撤销知识工具动作权限后，消息返回来源不可用状态，原文和引用不再提供；运行详情清除步骤与草稿，旧 SSE 返回 403。恢复权限后，原回复重新通过来源复核。
6. 浏览器打开付款条款引用，显示实际文件名、文档 ID 和 30 日付款的原始片段；整页刷新后仍可打开相同片段。远端源地址是 S3，页面没有生成虚构的公开文档链接。
7. 浏览器完成撤权隐藏、恢复可见及历史处理记录查看。一次恢复后的短等待超时，后续页面状态确认请求已完成并恢复全部引用；没有重启或重发模型调用。
8. 只删除本次创建的合成文档，远端 DELETE 返回 `err_code: 0`，随后 fetch 返回 `err_code: 1004`。旧处理记录点击刷新后隐藏正文与步骤，整页刷新后的旧回复也隐藏内容，引用按钮数为 0。验收程序随后复核历史消息，确认删除后没有重新提供原始事实或引用。

## 实现与修正

- `internal/infrastructure/provider/knowledge_citations.go` 新增 fetch 的 `metadata_object` 配置：文档元数据从 `/data` 读取，片段从 `/data/chunks` 的各项读取。继续明确配置字段，不自动猜测页码、单元格或公开链接。
- `internal/assembly/web/conversation_live_knowledge_test.go` 增加真实模型、知识接口、Identity、HTTP、SSE、重启、撤权、恢复及删除后的 opt-in 验收。
- `scripts/test-agent-knowledge-live.py` 从已索引合成文档清单启动上述验收，凭证只经进程环境或隐藏输入传入。
- `scripts/knowledge-live-document.py` 保留可复现的合成资料准备、索引检查和清理流程。默认不上传，必须显式选择 `--create`；删除只接受同一服务范围且已确认初始不存在的合成文档 ID，已删除时不再次发送 DELETE。

第一次真实运行在撤权检查处失败，原因是新验收断言错误地要求正文为空；当前公共 API 的契约是返回来源不可用提示。已修正为检查访问错误、原始事实及引用清除，并补查运行与 SSE。没有为通过测试放宽产品的权限判断；修正后的真实整段运行通过。

## 重现

先配置知识服务的 origin / team / kb 和模型服务。凭证通过环境或终端隐藏输入提供，不写进服务配置文件：

```sh
python3 scripts/knowledge-live-document.py --create
```

使用输出的 manifest 路径；内容尚未就绪时对同一 manifest 使用 `--inspect`，不要重复创建资料。随后启动：

```sh
AGENT_CONVERSATION_MODEL=gpt-5.6-sol \
AGENT_CONVERSATION_PROTOCOL=responses \
python3 scripts/test-agent-knowledge-live.py /path/to/manifest.json --browser --expect-deleted
```

程序完成后端检查后，在 8092 开启临时网页供浏览器验收。网页检查完成后，先在另一终端对同一 manifest 执行 `python3 scripts/knowledge-live-document.py --cleanup /path/to/manifest.json`，核对页面隐藏；再 POST 临时测试专用的 `/__acceptance/finish`，让程序完成删除后的断言并退出。这些控制入口只由测试装配，生产 Web Host 不会注册。

## 验证记录

- 真实完整验收：`/tmp/domainry-agent-knowledge-live-e2e-2.log`，PASS。
- Agent 全量 `go test ./...`：`/tmp/domainry-agent-knowledge-live-full.log`，退出码 0。
- `go vet ./...`：`/tmp/domainry-agent-knowledge-live-vet.log`，退出码 0。
- 合成资料脚本测试：`python3 -m unittest discover -s scripts -p 'test_knowledge_live_document.py' -v`，通过。覆盖等待索引、成功清理、重复清理不重复删除，以及拒绝业务文档 ID；使用本地 Transport 夹具。
- 本次合成文档 ID：`domainry-agent-acceptance-20260910-47bacec4a259`，清理清单已记录 `cleanup_verified: true`。
- 私有原始证据目录：`/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-knowledge-agent-live-rvypw61o`。包含实际回复、执行记录、撤权及删除后的消息投影，不包含 API Key。

临时页面已关闭，8092 验收服务已正常结束。K05–K07 的产品内上传、附件私有范围和结构化提取仍未实现；一次 API 推送测试不等同于交付这些产品功能。当前 Provider 的全响应来源比对仍可能因排序或内容变化要求重新生成，逐文档版本 / 权限复核契约仍需后续完善。
