import { isAccountWrite } from "./account-write-state.ts";

export function accountWriteResult(tool: string, raw?: string) {
  if (!isAccountWrite(tool) || !raw) return null;
  try {
    const envelope = JSON.parse(raw) as { data?: Record<string, unknown> }, data = envelope.data;
    if (!data || typeof data.request_ref !== "string") return null;
    if (tool === "mail_send" || tool === "mail_reply") {
      if (data.status !== "accepted" || data.delivery !== "unknown" || typeof data.accepted_at !== "string") return null;
      return <div className="account-write-content"><strong>邮件已由服务受理</strong><p>送达状态尚未确认。</p>{typeof data.message_id === "string" && <p>邮件标识：{data.message_id}</p>}{typeof data.in_reply_to_message_id === "string" && <p>回复原邮件：{data.in_reply_to_message_id}</p>}<p className="subtle">受理时间：{data.accepted_at}</p></div>;
    }
    if (data.outcome !== (tool === "calendar_event_create" ? "created" : "updated") || typeof data.calendar_id !== "string" || typeof data.event_id !== "string" || data.notifications !== "requested") return null;
    return <div className="account-write-content"><strong>{data.outcome === "created" ? "日程已创建" : "日程已修改"}</strong><p>目标日历：{data.calendar_id}</p><p>事件标识：{data.event_id}</p><p className="subtle">已请求通知参与者，通知送达情况尚未确认。</p></div>;
  } catch { return null; }
}
