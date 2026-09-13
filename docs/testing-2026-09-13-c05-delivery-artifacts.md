# C05：交付成果全文与既有导出

日期：2026-09-13。接续[成果回执阶段](testing-2026-09-13-c05-artifact-and-delivery-reading.md)，完成交付中准确成果版本的正文阅读与原导出下载。C05 仍保留跨用户／角色主体、Knowledge 资料检索／抽取和业务等其余 owner 策略、明确资料共享流程；整体仍为 **4／25**。

## 实现

- 新增只读 `POST /agent/delegations/{delegationID}/delivery-artifact` 和 `/delivery-export`。请求携带明确提交的原回执、准确成果 ID／版本、可选历史 delivery revision；下载还必须提供该导出回执的准确 export ID。
- 先复核交付阅读权、回执归属与 SHA256、当前工具偏好和来源，再确认目标资源确实存在于该成果工具回执。创建／编辑／正文页／目录／版本目录可指向准确成果版本；只有原导出回执可以放行相应导出。业务结果中长得像 `artifact` 的数据不能释放 Knowledge 资源。
- 正文通过 Knowledge 原有 `Artifact` 读取完整不可变内容，保留正文 hash、私有存储和递归来源校验。元数据或正文的一页本身不授予权限，实际全文仍需要当前 `artifact_read`。
- Knowledge 增加可选公开 `ArtifactExportReader.ReadArtifactExport`，以准确版本阅读权读取已经存在的导出。复用原格式转换与文件名、类型、长度、SHA256、CSV 公式防护核对及有效期／下载记账；不创建新导出。普通下载接口保留原 `artifact_export` 授权。缺少新 owner 端口时明确拒绝独立下载。
- 交付当前／历史回执页面增加“查看完整成果 · 版本 N”和“下载已导出的版本”。正文使用既有 `ArtifactPreview`，只提供本次明确交付的版本。下载沿用会话身份、取消、文件大小和 SHA256 核对；关闭回执、来源撤权、切换窗口会清除正文并取消请求。
- SDK、Module、SaaS／Remote 均已接通；公开下载响应为 Markdown／CSV 二进制，OpenAPI 与实际响应一致。累计普通 HTTP 94 条、SaaS 117 条；无新增数据表／迁移。

## 验证

- Knowledge owner 的 Markdown／CSV 场景：原下载权限不足、独立阅读成功、原内容与公式防护一致、阅读撤权、元数据／正文篡改、缺失／不可验证来源及跨 owner 拒绝。失败不增加下载记账，夹具未实现任何导出写入方法，误创建会直接失败。
- Agent 精确归属场景覆盖六类成果回执，禁止其他版本／成果／导出和业务伪装字段。
- 真实 Identity／Tools／Knowledge／Agent／SQLite HTTP：经过三次确认创建、修订和导出，原 `artifact_read` 只保存 256 字节的一页。撤回写权限及执行阅读权后，可从对应回执阅读完整原版／修订版；新版本不能借旧版引用读取。原导出下载按实际文件 hash 核对，普通下载仍拒绝；历史版本、重启和阅读撤权均覆盖。
- SaaS 真实 HTTP 转发覆盖完整资源 DTO、准确 revision／引用／主体、导出字节及 owner 拒绝；跨 workspace 继续阻止。
- 真实 Chrome 覆盖当前／历史原回执、完整原版／修订版、真实文件下载、撤权后的正文清除、刷新、关闭和 390px 窄屏。另复跑既有协作权限页面，执行过程按钮仍按执行阅读权显示。

命令、结果、截图和源码摘要见[证据目录](evidence/2026-09-13-c05-delivery-artifacts/commands.md)。本阶段最初的大文本模型夹具超过既有参数预算而被拒绝，已改用正常预算内的正文和明确分页读取验证同一目标；测试遇到终态失败立即报告，不再等整个任务期限。生产预算没有改动。
