const messages: Record<string, string> = {
  document_transfer_invalid: "文档来源、版本或目标不符合要求，请刷新后选择另一个资料库。",
  document_transfer_unavailable: "当前服务尚未启用跨资料库复制或移动。",
  document_transfer_pending: "另一窗口已有待确认的文档操作，请先核对该操作。",
  document_import_invalid: "附件来源或版本不符合要求，请刷新附件后重新选择。",
  document_import_unavailable: "当前服务尚未启用附件另存到资料库。",
  documents_unavailable: "当前服务尚未启用资料库文档管理。",
  document_management_unavailable: "此资料库暂未开放文档上传，请联系管理员连接支持入库的知识源。",
  document_write_denied: "只有资料库编辑者或管理者可以上传和删除文档。",
  document_upload_access_denied: "上传权限已变化，后台暂未推送此文件，请联系资料库管理者处理。",
  document_not_found: "文档不存在、已删除，或你已失去所在资料库的访问权限。",
  document_content_not_found: "原文件尚未保存、已停止访问或已经删除。",
  document_size_invalid: "请选择不超过 16 MiB 的非空文件。",
  document_type_unsupported: "暂不支持这个文件格式，请选择 PDF、Word、Excel 或支持的文本文件。",
  document_invalid: "文件名称或上传参数无效，请检查后重试。",
  document_storage_limit: "此资料库的文件数量或容量已达到上限，请先清理不再需要的文档。",
  document_content_mismatch: "原文件完整性校验失败，暂时不能下载，请刷新后重试或联系管理员。",
  document_response_invalid: "上传回执与所选资料库或文件不一致，请刷新核对；上次上传编号已保留。",
  document_storage_unavailable: "文件存储暂时不可用，请稍后重试。",
  document_put_uncertain: "远端上传结果待核查，系统不会盲目重传，请稍后刷新状态。",
  document_put_unconfirmed: "暂时未能确认远端收到文件，系统会继续核查。",
  document_delete_uncertain: "已停止本地访问，远端删除结果仍待核查，请联系管理员查看。",
  document_cleanup_failed: "原文件清理暂未完成，后台会继续处理；文档已停止访问。",
  document_index_failed: "知识服务未能完成索引，文档暂时不能检索。",
  document_inspect_failed: "暂时无法查询知识服务的处理状态，后台会继续核对。",
  document_remote_conflict: "发现远端文档冲突，系统已停止推送，请联系管理员核查。",
  document_work_unavailable: "文档后台处理暂时不可用，已保存的任务会继续保留。",
  document_source_changed: "资料库的知识源配置不一致，请联系管理员核对。",
  library_archived: "资料库已归档，请恢复资料库后再上传、检索或下载。",
  knowledge_library_required: "请先查询可访问的资料库，再选择资料库搜索或读取文档。",
  libraries_unavailable: "当前服务尚未启用资料库管理。",
  library_access_denied: "当前账号没有这项资料库操作权限，或权限已被撤销。",
  library_not_found: "资料库不存在，或你已不是该库成员。",
  library_invalid: "资料库名称或参数不符合要求，请缩短内容后重试。",
  library_manage_required: "只有资料库管理者可以修改设置或成员。",
  library_member_unavailable: "该用户不是当前工作区的有效成员，请核对用户 ID。",
  library_last_manager: "至少需要保留一位管理者，请先将另一位成员设为管理者。",
  library_member_not_found: "该成员已经移出资料库，请刷新成员列表。",
  library_member_invalid: "成员用户 ID 无效，请核对后重试。",
  library_role_invalid: "请选择阅读、编辑或管理角色。",
  personal_library_private: "个人资料仅自己可见，不能添加其他成员。",
  library_limit: "当前工作区的资料库数量已达上限。",
  library_member_limit: "资料库成员数量已达上限。",
  library_query_invalid: "资料库分页参数无效，请重新打开资料库列表。",
  attachments_unavailable: "当前服务尚未启用会话附件。",
  attachment_access_denied: "当前账号没有这项附件操作权限，或权限已被撤销。",
  attachment_not_found: "附件不存在、已删除或不属于当前会话。",
  attachment_content_not_found: "附件原文件尚未保存或已经删除。",
  attachment_size_invalid: "请选择不超过 16 MiB 的非空文件。",
  attachment_type_unsupported: "暂不支持这个文件格式，请选择 PDF、Word、Excel 或支持的文本文件。",
  attachment_invalid: "附件名称或上传参数无效，请检查文件名称后重试。",
  attachment_limit: "附件数量或容量已达到上限，请先删除不再需要的附件。",
  attachment_conversation_archived: "会话已归档，请恢复后再上传附件。",
  attachment_content_mismatch: "原文件校验失败，暂时无法读取，请重新上传资料。",
  attachment_storage_unavailable: "文件存储暂时不可用，请稍后重试。",
  business_action_changed: "业务动作定义已更新，原确认已失效。请重新查看动作并确认新的操作内容。",
  business_action_invalid: "业务动作参数或前置条件不符合要求，请根据当前动作说明核对后再执行。",
  business_record_version_conflict: "目标记录已被其他操作修改，本次操作未应用。请读取最新记录，核对后重新确认。",
  business_action_assurance_required: "这项业务操作还需要宿主要求的身份验证或审批，请完成后再继续。",
  business_action_receipt_conflict: "操作标识与已有执行记录不一致，请先核查原操作结果。",
  business_access_denied: "当前账号无法读取这些业务对象、记录或字段，请核对权限后重新查询。",
  business_record_not_found: "业务记录不存在或当前不可读取，请重新查询确认目标。",
  business_request_invalid: "查询条件不符合业务目录的字段或类型要求，请核对后重试。",
  business_source_changed: "业务数据或访问范围已变化，请重新查询后生成结果。",
  business_unavailable: "业务服务暂时不可用，请稍后重试。",
  business_response_invalid: "业务服务返回的记录或分页信息不符合请求，暂时无法使用。",
  knowledge_citation_mapping_invalid: "知识服务返回的来源字段与配置不符，暂时无法验证引用，请核对知识源配置。",
  knowledge_citation_limit_exceeded: "本次资料引用超过展示上限，请缩小查询范围后重试。",
  artifact_version_conflict: "成果已在其他地方修改。请取消当前编辑并刷新，核对最新版本后再修改。",
  artifact_not_found: "成果或指定版本不存在，或当前不可读取。",
  artifact_access_denied: "当前账号没有这项成果操作的权限。",
  artifact_source_not_finished: "来源对话尚未完成，请等待处理结束后再保存。",
  artifact_export_expired: "下载已过期，请重新导出指定版本。",
  artifact_export_mismatch: "下载内容与所选版本的校验不一致，请刷新后重试。",
  artifact_storage_unavailable: "成果存储暂时不可用，请稍后重试。",
  artifact_invalid: "成果内容不符合格式或大小要求，请核对标题、正文和表格数据。",
  model_changed: "模型配置已变化，本次运行不能直接续接。已完成的操作仍然保留。",
  execution_limit: "本次处理已达到执行预算，请查看已完成步骤后缩小任务范围。",
  execution_context_exceeded: "本次工具上下文超过预算，已完成的操作和结果已保存。",
  execution_reference_invalid: "运行引用无效，请使用历史消息提供的运行记录。",
  execution_reference_changed: "原执行结果已变化，请重新读取处理记录。",
  execution_cursor_invalid: "运行读取位置无效，请重新读取第一页。",
  execution_cursor_changed: "运行记录已更新，请从第一页重新读取。",
  execution_read_unavailable: "当前宿主未提供历史执行读取能力。",
  execution_read_exceeded: "运行目录超过本次读取上限，请减少每页条数后重试。",
  execution_read_failed: "历史执行读取失败，请稍后重试。",
  result_not_found: "原始工具结果已不存在，或你当前无法访问该来源。",
  result_reference_changed: "结果引用校验失败，请重新获取原始结果引用。",
  result_reference_invalid: "结果引用无效，请使用实际返回的来源引用。",
  result_offset_invalid: "结果读取位置无效，请使用上一次返回的继续位置。",
  result_page_budget_exceeded: "结果读取信息超过当前预算，请缩小本次处理范围。",
  tool_access_denied: "当前工具不可用：权限可能已被撤销，或所需连接、工具开关已停用。",
  tool_unavailable: "工具已停用或所需连接不可用；恢复连接或工具开关后可继续。",
  tool_availability_failed: "暂时无法检查工具连接状态，请稍后重试。",
  tool_changed: "工具版本已变化，无法按原参数继续执行。",
  tool_catalog_invalid: "工具配置不可用，请检查宿主的工具注册。",
  tool_confirmation_required: "请先在操作卡片中确认这项操作。",
  interaction_unavailable: "当前宿主尚未接入补充信息与确认能力。",
  interaction_access_denied: "当前账号无权提交补充信息或确认，或权限已被撤销。",
  interaction_response_required: "请通过当前问题或确认卡片继续处理。",
  interaction_response_invalid: "提交内容不符合当前问题或确认要求。",
  interaction_response_conflict: "此事项已经收到另一份答复，请刷新查看最新状态。",
  interaction_not_found: "此等待事项已不存在，请刷新会话。",
  interaction_closed: "此事项已关闭，无法继续原操作。可以发送新的请求。",
  interaction_expired: "等待事项已过期，本次处理已停止。可以发送新的请求。",
  interaction_rejected: "你已拒绝此操作，本次处理已停止。",
  question_must_be_separate: "模型把补充提问和其他操作混在同一步中，本步未执行。可以重试，让模型先处理提问。",
  tool_result_uncertain: "操作结果尚未确认。继续处理时会先核查结果，请勿重复发起同一操作。",
  "agent.web.identity_changed": "账号已切换，请重新进入当前账号的会话。",
  "agent.web.login_required": "登录已过期，请重新登录。",
  "agent.web.password_change_required": "请先修改初始密码，再开始对话。",
  network_error:
    "无法连接服务，请检查网络后重试。未发送的内容会保留在输入框中。",
  response_unreadable:
    "服务返回了无法读取的结果，请刷新查看发送状态；重试同一条消息不会重复创建。",
  provider_network: "Agent 暂时无法连接模型服务，请稍后重新生成。",
  provider_timeout: "模型响应超时，已生成的草稿会保留，可以重新生成。",
  provider_rate_limited:
    "模型请求受到限流，请稍后重新生成，并检查 Provider 的调用额度。",
  provider_quota_exhausted:
    "模型额度或账单受到限制，请检查 Provider 账户额度后重新生成。",
  provider_access_denied:
    "模型服务拒绝访问，请检查 API Key 和该模型的使用权限。",
  provider_unavailable: "模型服务暂时不可用，请稍后重新生成。",
  provider_request_invalid:
    "模型配置或请求不被支持，请检查所选模型与协议是否匹配。",
  provider_failed: "模型未能完成回复，已输出的草稿会保留，可以重新生成。",
  context_failed: "整理历史上下文失败，原始消息仍然保留，可以重新生成。",
  knowledge_access_denied: "无法访问知识库，请检查工作区绑定、API Key 和文档权限。",
  knowledge_not_found: "未找到当前可读取的文档，请重新检索并选择实际返回的文档。",
  knowledge_quota_exhausted: "知识库服务额度不足，请检查 Gateway 账户额度后重新生成。",
  knowledge_rate_limited: "知识库检索受到限流，请稍后重新生成。",
  knowledge_timeout: "知识库检索超时，请稍后重新生成。",
  knowledge_network: "暂时无法连接知识库服务，请稍后重新生成。",
  knowledge_request_invalid: "知识库配置或查询无效，请检查团队 ID、知识库 ID 和检索配置。",
  knowledge_unavailable: "知识库服务暂时不可用，请稍后重新生成。",
  knowledge_failed: "文档检索失败，原始消息仍然保留，可以重新生成。",
  knowledge_response_invalid: "知识库返回了无法读取的结果，请检查检索服务后重新生成。",
  knowledge_context_exceeded: "检索结果超过上下文预算，请缩小问题范围或调低检索条数后重新生成。",
  knowledge_source_changed: "知识资料或可见范围已变化，无法继续使用旧结果。请重新生成以获取当前资料。",
  source_access_unavailable: "这次处理使用的资料当前无权读取或暂时无法验证，相关内容已隐藏。可在权限或连接恢复后刷新查看。",
  source_reference_invalid: "资料来源记录无法验证，请重新查询资料后再处理。",
  source_read_unavailable: "当前服务无法验证历史资料来源，请检查服务配置。",
  source_limit_exceeded: "资料来源过多，无法在本次读取范围内完成验证。请缩小资料范围。",
  source_snapshot_changed: "资料记录正在更新，请稍后刷新。",
  response_invalid: "模型返回的回复为空、过长或不完整，请缩短要求后重试。",
  execution_interrupted: "本次生成已中断或超时，可以重新生成。",
  model_not_configured:
    "还没有配置可用模型，请先配置 Provider、模型名称和 API Key。",
  busy: "这个会话正在生成回复，请等待完成或先停止生成。",
  archived: "会话已归档，恢复会话后才能继续发送消息。",
  not_found: "会话、消息或记忆已不存在，请刷新列表。",
  revision_conflict: "内容已在其他页面更新，请重新打开后再修改。",
  revision_mismatch: "内容已在其他页面更新，请重新打开后再修改。",
  memory_limit: "个人记忆已达到 32 条，请删除不再需要的记忆后再保存。",
  memory_invalid: "记忆名称或内容不符合长度要求，请缩短后保存。",
  todos_unavailable: "当前宿主尚未接入待办存储或权限检查。",
  todo_invalid: "事项内容不符合要求，请检查标题、说明和截止日期。",
  todo_not_found: "该事项已不存在，请刷新待办列表。",
  todo_date_invalid: "截止日期或时间无效，请重新选择。",
  todo_timezone_invalid: "时区无效，请填写 Asia/Shanghai 等地区时区。",
  todo_timezone_mismatch: "截止时间的偏移与所选时区不一致，请重新指定时间。",
  todo_batch_invalid: "每批可创建 1 至 20 项待办。",
  todo_limit: "个人待办已达到 1000 项，请清理不再需要的事项。",
  todo_cursor_invalid: "待办分页已失效，请刷新列表。",
  message_invalid: "消息不能为空，且最多为 16 KB，请缩短后发送。",
  run_superseded:
    "这次回复之后已有新消息，无法重新生成旧回复，请发送新的问题。",
  idempotency_conflict:
    "这次发送的标识与已有消息冲突，请刷新查看已保存的消息。",
  stream_interrupted: "实时连接已断开，正在从已保存的草稿恢复。",
  principal_required: "当前访问身份已失效，请刷新页面或重新登录。",
  permission_denied: "当前身份无权访问这个会话。",
};
export function errorMessage(code?: string, status?: number) {
  const key = code?.replace(/^agent\.conversation\./, "") || "";
  if (messages[key]) return messages[key];
  if (status === 401 || status === 403)
    return "当前访问身份已失效或无权访问，请刷新页面或重新登录。";
  if (status === 404) return messages.not_found;
  if (status === 409) return messages.revision_conflict;
  if (status === 429) return "请求过于频繁，请稍后重试。";
  if (status && status >= 500) return "服务暂时不可用，请稍后重试。";
  if (status === 400) return "提交内容不符合要求，请检查后重试。";
  return "操作未完成，请稍后重试。";
}
export class ApiError extends Error {
  code: string;
  status?: number;
  constructor(code: string, status?: number) {
    super(errorMessage(code, status));
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}
export function describeError(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  if (error instanceof TypeError) return messages.network_error;
  if (error instanceof DOMException && error.name === "NotAllowedError")
    return "浏览器未允许这项操作，请检查页面权限。";
  return error instanceof Error ? error.message : "操作未完成，请稍后重试。";
}
