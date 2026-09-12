import { ApiError } from "./errors.ts";
import { request } from "./api.ts";
import { sessionScope } from "./session.ts";
import { attachmentPath, type Attachment } from "./attachment-state.ts";

export type PendingAttachmentIndex = { conversationID: string; attachmentID: string; sha256: string; revision: number; operation: "start" | "check" };
export const attachmentIndexReceiptKey = (a: Attachment) => `agent-attachment-index:${sessionScope()}:${a.conversation_id}:${a.id}`;
export function parsePendingAttachmentIndex(raw: string | null, a: Attachment): PendingAttachmentIndex | null {
 if (!raw || raw.length > 2048) return null;
 try {
  const p = JSON.parse(raw) as PendingAttachmentIndex;
  if (!p || p.conversationID !== a.conversation_id || p.attachmentID !== a.id || p.sha256 !== a.sha256 || !/^conv_[a-f0-9]{32}$/.test(p.conversationID) || !/^att_[a-f0-9]{32}$/.test(p.attachmentID) || !/^[a-f0-9]{64}$/.test(p.sha256) || !Number.isSafeInteger(p.revision) || p.revision < 1 || !["start", "check"].includes(p.operation)) return null;
  return { conversationID:p.conversationID,attachmentID:p.attachmentID,sha256:p.sha256,revision:p.revision,operation:p.operation };
 } catch { return null; }
}
export function attachmentIndexDetail(a: Attachment): string {
 if (a.state === "uploading") return "原文件上传尚未完成，请重新选择同一文件继续原上传请求。";
 if (a.state === "deleted") return "附件及原文件已清理。";
 if (a.state === "deleting") return "附件已停止访问。后台正在核对远端删除和原文件清理；未确认前会保留清理记录。";
 if (a.state === "ready") return "可以在当前会话中检索。原文件仍可独立预览和下载。";
 const codes: Record<string,string> = {
  attachment_put_uncertain:"上传请求的结果尚未确认，正在核对同一份远端文档，不会自动重复上传。",
  attachment_put_unconfirmed:"暂未确认远端文档可见。后台会继续核对；当前不可见不能证明上传没有发生。",
  attachment_index_failed:"知识服务报告索引失败。可核对已有任务状态，或联系管理员处理知识服务中的失败任务。",
  attachment_inspect_failed:"暂时无法读取知识服务的处理状态，可以核对已有任务。",
  attachment_index_access_denied:"当前索引或原文件读取权限不可用。恢复权限后可继续核对同一次任务。",
  attachment_index_unavailable:"当前会话的附件知识源未配置或暂不可用。原文件仍可按权限预览和下载。",
  attachment_source_changed:"附件知识源与原索引记录不一致，请联系管理员恢复原来源。",
  attachment_access_policy_changed:"私有权限配置与原索引记录不一致，请联系管理员处理。",
  attachment_remote_conflict:"远端文档标识已存在，系统已停止上传。请联系管理员核对来源。",
  attachment_content_mismatch:"原文件校验未通过，系统已停止索引。",
  attachment_delete_uncertain:"远端删除结果尚未确认，原文件清理仍在等待。核对不会盲目重发删除。",
  attachment_cleanup_failed:"原文件清理暂未完成，后台将继续处理。",
 };
 if (a.error_code && codes[a.error_code]) return codes[a.error_code];
 if (a.state === "needs_reconcile") return "上传结果尚未确认。原文件仍可按权限预览和下载；系统会核对原任务，不会自动重复上传。";
 if (a.indexing?.requested) return "索引任务已保存。可以关闭窗口，后台会继续处理。";
 return "原文件已私有保存。开始索引后才可在当前会话中检索；预览和下载不需要索引。";
}
export function attachmentIndexReason(a: Attachment): string {
 switch (a.indexing?.reason) {
  case "attachment_index_unavailable": return "尚未配置可用的附件知识源，请联系管理员。";
  case "attachment_source_changed": return "当前来源与已提交的索引不一致，请联系管理员。";
  case "attachment_conversation_archived": return "恢复会话后可以发起索引或核对。";
  case "attachment_index_access_denied": return "当前没有索引或读取原文件的权限。";
  case "attachment_index_check_denied": return "当前没有核对索引任务的权限。";
  case "attachment_size_invalid": return `当前知识源只接受不超过 ${((a.indexing.max_bytes || 0) / 1024 / 1024).toFixed(0)} MiB 的文件；原件仍可预览和下载。`;
  default: return a.indexing ? "" : "当前宿主尚未开放附件索引。";
 }
}
export function indexReceipt(a: Attachment, operation: PendingAttachmentIndex["operation"]): PendingAttachmentIndex {
 return { conversationID:a.conversation_id,attachmentID:a.id,sha256:a.sha256,revision:a.revision,operation };
}
export async function submitAttachmentIndex(p: PendingAttachmentIndex, signal: AbortSignal): Promise<Attachment> {
 const path = `${attachmentPath(p.conversationID,p.attachmentID)}/index${p.operation === "check" ? "/check" : ""}?expected_revision=${p.revision}`;
 return verifyAttachmentIndexResponse(await request<Attachment>(path,"POST",undefined,signal),p);
}
export function verifyAttachmentIndexResponse(a: Attachment, p: PendingAttachmentIndex): Attachment {
 if (!a || a.id!==p.attachmentID || a.conversation_id!==p.conversationID || a.sha256!==p.sha256 || !Number.isSafeInteger(a.revision) || a.revision<1 || a.indexing==null) throw new ApiError("agent.conversation.attachment_index_response_invalid");
 return a;
}
