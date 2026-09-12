import { useEffect, useMemo, useState } from "react";
import { accountsClient, accountFailure } from "./external-account-state.ts";
import { sessionScope } from "./session.ts";
import { accountWritePreview, type AccountWritePreview, type WriteAddress, type WriteMoment } from "./account-write-state.ts";

function Addresses({ values, empty }: { values: WriteAddress[]; empty: string }) {
  return values.length ? <ul className="account-write-addresses">{values.map((a, i) => <li key={`${a.address}:${i}`}>{a.name && <span>{a.name} · </span>}<span>{a.address}</span></li>)}</ul> : <p>{empty}</p>;
}
function Moment({ value, end }: { value: WriteMoment; end?: boolean }) {
  return <span>{value.date || value.date_time} · {value.time_zone}{value.date && (end ? "（全天活动的排他结束日期）" : "（全天活动）")}</span>;
}
export function AccountWriteContent({ value }: { value: AccountWritePreview }) {
  if (value.kind === "mail_send" || value.kind === "mail_reply") return <div className="account-write-content">
    <strong>{value.kind === "mail_send" ? "发送新邮件" : "回复邮件"}：{value.message.subject || "（无主题）"}</strong>
    {value.kind === "mail_reply" && <p>原邮件：{value.messageID}</p>}
    <dl>{([["收件人 To", value.message.to], ["抄送 CC", value.message.cc], ["密送 BCC", value.message.bcc]] as const).map(([label, addresses]) => <div key={label}><dt>{label}</dt><dd><Addresses values={addresses} empty="无" /></dd></div>)}</dl>
    <p>完整邮件正文</p><pre className="account-write-text">{value.message.text || "（空正文）"}</pre>
    <p className="subtle">将从所选账号发送给以上全部收件人。服务受理后仍需以实际送达情况为准。</p>
  </div>;
  const c = value.changes, updated = value.kind === "calendar_update";
  const kind = { required: "必需参与者", optional: "可选参与者", resource: "资源" };
  return <div className="account-write-content">
    <strong>{updated ? "修改日程" : "创建日程"}{c.title ? `：${c.title}` : ""}</strong>
    <p>目标日历：{value.calendarID}</p>
    {updated && <><p>目标事件：{value.eventID}</p><p>修改范围：{value.scope === "series" ? "整个重复系列" : "这一项事件"}</p></>}
    {updated && value.scope === "series" && <p>将修改整个重复系列，可能向单独修改过的实例参与者发送通知。</p>}
    {c.start && c.end && <dl><div><dt>开始</dt><dd><Moment value={c.start} /></dd></div><div><dt>结束</dt><dd><Moment value={c.end} end /></dd></div></dl>}
    {c.description !== undefined && <><p>{updated && !c.description ? "清空日程说明" : "日程说明"}</p><pre className="account-write-text">{c.description || "（空）"}</pre></>}
    {c.location !== undefined && <p>{updated && !c.location ? "清空地点" : `地点：${c.location || "（无）"}`}</p>}
    {c.attendees !== undefined && <><p>{updated ? "修改后的完整参与者" : "邀请以下参与者"}</p>{c.attendees.length ? <ul className="account-write-addresses">{c.attendees.map((a, i) => <li key={`${a.address}:${i}`}>{a.name && `${a.name} · `}{a.address} · {kind[a.kind]}</li>)}</ul> : <p>{updated ? "移除原有全部参与者" : "无参与者"}</p>}</>}
    {updated && <p className="subtle">只修改以上列出的内容。按已读取的事件版本提交，版本冲突后需要重新读取并确认。</p>}
    <p className="subtle">此操作会请求向参与者发送通知；通知是否送达仍需核实。</p>
  </div>;
}

export function AccountWriteOperationPreview({ tool, argumentsText, onReady }: { tool: string; argumentsText: string; onReady: (ready: boolean) => void }) {
  const value = useMemo(() => accountWritePreview(tool, argumentsText), [tool, argumentsText]);
  const [account, setAccount] = useState<{ name: string; shared: boolean } | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    setAccount(null); setError(""); onReady(false);
    if (!value) return;
    const controller = new AbortController(), scope = sessionScope();
    accountsClient.listConnectionAccounts(controller.signal).then(({ accounts }) => {
      if (controller.signal.aborted || sessionScope() !== scope) return;
      const item = accounts.find(a => a.key === value.accountKey);
      if (!item || item.updated_at !== value.accountRevision || item.status !== "active" || !item.readiness?.available) {
        setError("账号已变化或当前不可用，请停止本次操作并重新选择账号。"); return;
      }
      setAccount({ name: item.name || item.key, shared: item.scope === "workspace" }); onReady(true);
    }).catch(error => { if (!controller.signal.aborted && sessionScope() === scope) setError(accountFailure(error)); });
    return () => controller.abort();
  }, [value, onReady]);
  if (!value) return <p role="alert">操作目标或内容不完整，暂时无法确认。请停止后重新指定。</p>;
  return <div className="account-write-operation" aria-label="外部账号操作内容">
    {error ? <p role="alert">{error}</p> : !account ? <p>正在核对目标账号…</p> : <p><strong>账号：{account.name}</strong> · {account.shared ? "工作空间账号" : "个人账号"}</p>}
    <AccountWriteContent value={value} />
    <details><summary>查看目标标识</summary><p>账号：{value.accountKey}</p>{value.kind === "calendar_update" && <p>已读取事件版本：{value.expectedVersion}</p>}</details>
  </div>;
}
