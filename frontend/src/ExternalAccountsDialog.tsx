import { useCallback, useEffect, useRef, useState } from "react";
import type { IntegrationConnectionAccount as Account, IntegrationOAuthApplication as Application, IntegrationOAuthAuthorizationOption as Option, IntegrationOAuthAuthorizationSession as Authorization } from "@domainry/integration-client";
import { RefreshCw, Settings, ShieldCheck } from "lucide-react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { request } from "./api.ts";
import type { AppSession } from "./session.ts";
import { accountFailure, accountsClient, authorizationLabels, authorizationTarget, forgetAuthorization, loadAuthorization, saveAuthorization } from "./external-account-state.ts";

const readinessStates: Record<string,string> = { configured: "凭证已配置", unconfigured: "凭证配置不完整", credential_unavailable: "凭证已停用或撤销", credential_expired: "凭证已过期", provider_unavailable: "服务适配器不可用", changed: "配置已变化，请刷新" };
const testStates: Record<string,string> = { scope_required: "当前授权不包含连接测试所需的账号资料范围。其他功能按各自授权范围使用。", scope_unverified: "尚无实际授权范围记录，暂不能测试连接。", requirements_invalid: "服务的连接测试配置无效，请联系管理员。" };
const states: Record<string, string> = { active: "已授权", revoked: "已撤销", disabled: "已停用", expired: "已过期", invalid: "需要重新授权" };

export function ExternalAccountsDialog({ session, onClose }: { session: AppSession; onClose: () => void }) {
  const [accounts, setAccounts] = useState<Account[]>([]), [options, setOptions] = useState<Option[]>([]);
  const [application, setApplication] = useState(""), [scope, setScope] = useState<"personal" | "workspace">("personal"), [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>([]), [pending, setPending] = useState(() => loadAuthorization(session.scope));
  const [authorization, setAuthorization] = useState<Authorization | null>(null);
  const [loading, setLoading] = useState(true), [busy, setBusy] = useState(false), [error, setError] = useState(""), [notice, setNotice] = useState("");
  const [setup, setSetup] = useState<{ ready: boolean; administrator: boolean } | null>(null);
  const [revoking, setRevoking] = useState(""), [admin, setAdmin] = useState(false);
  const lock = useRef(false), mounted = useRef(true);
  const connected = session.modules?.includes("integration") === true;
  const selected = options.find(item => item.key === application);
  const reload = useCallback(async (signal?: AbortSignal) => {
    if (!connected) { setLoading(false); return; }
    setLoading(true); setError("");
    const results = await Promise.allSettled([accountsClient.listConnectionAccounts(signal), accountsClient.listOAuthAuthorizationOptions(signal), request<{ready: boolean; administrator: boolean}>("/app/product/account-setup", "GET", undefined, signal)]);
    if (signal?.aborted || !mounted.current) return;
    const [list, choices, permissionSetup] = results;
    setSetup(permissionSetup.status === "fulfilled" ? permissionSetup.value : null);
    setAccounts(list.status === "fulfilled" ? list.value.accounts : []);
    setOptions(choices.status === "fulfilled" ? choices.value.options : []);
    const failed = results.find(item => item.status === "rejected");
    if (failed?.status === "rejected") setError(accountFailure(failed.reason));
    setLoading(false);
  }, [connected]);

  useEffect(() => { mounted.current = true; const abort = new AbortController(); void reload(abort.signal); return () => { mounted.current = false; abort.abort(); }; }, [reload]);
  useEffect(() => {
    if (!pending || !connected) return;
    const abort = new AbortController(); let timer: ReturnType<typeof setTimeout> | undefined;
    const read = async () => {
      try {
        const value = await accountsClient.getOAuthAuthorization(pending.id, abort.signal);
        if (abort.signal.aborted) return;
        setAuthorization(value);
        if (value.status === "exchanging") timer = setTimeout(() => void read(), 2000);
        if (value.status === "connected") void reload(abort.signal);
      } catch (failure) { if (!abort.signal.aborted) { setAuthorization(null); setError(accountFailure(failure)); } }
    };
    void read();
    return () => { abort.abort(); clearTimeout(timer); };
  }, [pending, connected, reload]);

  async function perform(action: () => Promise<void>) {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError(""); setNotice("");
    try { await action(); } catch (failure) { if (mounted.current) setError(accountFailure(failure)); }
    finally { lock.current = false; if (mounted.current) setBusy(false); }
  }
  async function start() {
    if (!selected || scopes.length === 0) return;
    const result = await accountsClient.startOAuthAuthorization({ application_key: selected.key, name: name.trim() || selected.name, scope, scopes });
    if (!mounted.current) return;
    const target = authorizationTarget(result.authorization_url || "");
    const saved = { id: result.id, scope: session.scope, returnHash: location.hash };
    saveAuthorization(saved); setPending(saved);
    location.assign(target);
  }
  return <Dialog open onOpenChange={open => { if (!open) onClose(); }}><DialogContent className="attachment-dialog external-accounts-dialog"><DialogHeader><DialogTitle>外部账号</DialogTitle><DialogDescription>个人账号仅自己使用；工作空间账号按管理员授予的权限共享。凭证由账号服务保存。</DialogDescription></DialogHeader>
    {!connected ? <p role="status">此工作空间尚未连接外部账号服务。管理员完成服务配置后，可在这里授权和管理账号。</p> : <>
      <div className="external-account-heading"><strong>我的连接</strong><Button variant="ghost" disabled={loading || busy} onClick={() => { void reload(); setPending(loadAuthorization(session.scope)); }}><RefreshCw size={15} />刷新账号</Button><Button variant="outline" disabled={busy} onClick={() => setAdmin(value => !value)}><Settings size={15} />应用配置</Button></div>
      {setup?.administrator && !setup.ready && <section className="external-authorization"><p>启用后，当前管理员角色可配置授权应用、管理自己的账号及工作空间共享账号。其他角色由工作空间管理员在权限管理中分配。</p><Button disabled={busy} onClick={() => void perform(async () => { await request("/app/product/account-setup", "POST", {}); setAdmin(false); await reload(); })}>启用管理员账号管理</Button></section>}
      {error && <p role="alert" className="error-text">{error}</p>}{notice && <p role="status" className="subtle">{notice}</p>}
      {pending && <section className="external-authorization" aria-label="授权结果"><strong>{authorization ? authorizationLabels[authorization.status] || "请核对授权状态" : "正在核对上次授权"}</strong>
        {authorization?.granted_scopes?.length ? <p>实际授权范围：{authorization.granted_scopes.join("、")}</p> : null}
        {authorization?.status === "pending" && <p>授权页面尚未完成。若已关闭授权页面，可清除本页提示后重新发起；旧会话会自动过期。</p>}
        {authorization?.status === "needs_reauthorization" && <p>上次换码结果无法确认或缺少持续访问凭证。请重新授权，服务不会重复使用旧授权码。</p>}
        {authorization?.account?.status === "revoked" && <p>此授权对应的账号已撤销。</p>}
        <Button variant="ghost" disabled={busy || authorization?.status === "exchanging"} onClick={() => { forgetAuthorization(); setPending(null); setAuthorization(null); }}>关闭授权提示</Button>
      </section>}
      {loading ? <p>正在读取账号…</p> : accounts.length === 0 ? <p className="subtle">还没有可见的外部账号。</p> : <ul className="external-account-list">{accounts.map(account => <li key={account.key}><div><strong>{account.name || account.connector_key}</strong><span>{account.scope === "personal" ? "个人" : "工作空间"} · {states[account.status] || "状态待核对"}</span>{account.status === "active" && <span>{readinessStates[account.readiness?.state || ""] || "凭证状态待核对"}</span>}{account.readiness?.test && !account.readiness.test.allowed && <p role="status">{testStates[account.readiness.test.state] || "暂不能测试连接。"}{account.readiness.test.scope_alternatives?.length ? <span>测试需要以下任一组合：{account.readiness.test.scope_alternatives.map(group => group.join(" + ")).join("；")}</span> : null}</p>}</div>
        {account.status !== "revoked" && <div className="external-account-actions"><Button variant="outline" disabled={busy || account.readiness?.available === false || account.readiness?.test?.allowed === false} onClick={() => void perform(async () => { const result = await accountsClient.testConnectionAccount(account.key); if (mounted.current) setNotice(result.connected ? `「${account.name || account.connector_key}」连接测试通过。` : "连接测试未通过，请核对账号授权。"); await reload(); })}>测试连接</Button><Button variant="ghost" disabled={busy} onClick={() => setRevoking(account.key)}>撤销连接</Button></div>}
        {revoking === account.key && <div className="external-revoke"><p>撤销「{account.name || account.connector_key}」后，{account.scope === "workspace" ? "工作空间中使用此账号的用户" : "你"}将不能继续通过此连接访问。已完成的外部操作不会撤回。</p><Button disabled={busy} onClick={() => void perform(async () => { await accountsClient.revokeConnectionAccount(account.key, account.updated_at); if (mounted.current) { setRevoking(""); setNotice("连接已撤销。"); } await reload(); })}>确认撤销此连接</Button><Button variant="ghost" disabled={busy} onClick={() => setRevoking("")}>保留连接</Button></div>}
      </li>)}</ul>}
      <section className="external-authorize-form" aria-label="连接新账号"><h3>连接新账号</h3>{options.length === 0 ? <p className="subtle">没有可用的授权应用。管理员可以在「应用配置」中登记客户端和授权范围。</p> : <>
        <label htmlFor="external-application">授权应用</label><select id="external-application" value={application} disabled={busy} onChange={event => { setApplication(event.target.value); setScopes(options.find(item => item.key === event.target.value)?.scopes || []); }}><option value="">选择应用</option>{options.map(option => <option key={option.key} value={option.key}>{option.name || option.key}</option>)}</select>
        <label htmlFor="external-name">账号备注</label><Input id="external-name" value={name} maxLength={128} disabled={busy} onChange={event => setName(event.target.value)} placeholder="例如：我的工作邮箱" />
        <label htmlFor="external-scope">使用范围</label><select id="external-scope" value={scope} disabled={busy} onChange={event => setScope(event.target.value as typeof scope)}><option value="personal">个人账号</option><option value="workspace">工作空间账号（需要共享管理权限）</option></select>
        {selected && <fieldset><legend>请求授权范围</legend>{selected.scopes.map(value => <label key={value} className="external-scope-option"><input type="checkbox" checked={scopes.includes(value)} disabled={busy} onChange={event => setScopes(current => event.target.checked ? [...current, value] : current.filter(item => item !== value))} /><span>{value}</span></label>)}</fieldset>}
        <Button disabled={busy || !selected || scopes.length === 0 || !!pending} onClick={() => void perform(start)}><ShieldCheck size={16} />前往授权</Button>
      </>}</section>
      {admin && <OAuthApplications onSaved={() => void reload()} />}
    </>}
  </DialogContent></Dialog>;
}

function OAuthApplications({ onSaved }: { onSaved: () => void }) {
  const [items, setItems] = useState<Application[]>([]), [selected, setSelected] = useState<Application | null>(null);
  const [key, setKey] = useState(""), [provider, setProvider] = useState("google"), [name, setName] = useState("");
  const [clientID, setClientID] = useState(""), [secret, setSecret] = useState(""), [tenant, setTenant] = useState("common"), [scopes, setScopes] = useState("https://www.googleapis.com/auth/calendar.readonly\nhttps://www.googleapis.com/auth/gmail.readonly");
  const [redirect, setRedirect] = useState(location.origin + "/oauth/callback"), [enabled, setEnabled] = useState(true);
  const [error, setError] = useState(""), [notice, setNotice] = useState(""), [busy, setBusy] = useState(false), [allowed, setAllowed] = useState(false);
  const mounted = useRef(true), lock = useRef(false);
  useEffect(() => { const abort = new AbortController(); mounted.current = true; accountsClient.listOAuthApplications(abort.signal).then(result => { if (!abort.signal.aborted) { setItems(result.applications); setAllowed(true); } }).catch(failure => { if (!abort.signal.aborted) setError(accountFailure(failure)); }); return () => { mounted.current = false; abort.abort(); }; }, []);
  function choose(value: Application | null) {
    setSelected(value); setKey(value?.key || ""); setProvider(value?.provider_key || "google"); setName(value?.name || ""); setClientID(value?.client_id || ""); setSecret(""); setRedirect(value?.redirect_uri || location.origin + "/oauth/callback"); setScopes(value?.scopes.join("\n") || "https://www.googleapis.com/auth/calendar.readonly\nhttps://www.googleapis.com/auth/gmail.readonly"); setTenant(String(value?.connection_config?.tenant_id || "common")); setEnabled(value?.enabled ?? true); setNotice("");
  }
  return <section className="external-applications" aria-label="OAuth 应用配置"><h3>OAuth 应用配置</h3><p className="subtle">在 Google Cloud 或 Microsoft Entra 注册应用后，将客户端信息登记在这里。密钥只写入账号服务；修改已有应用时留空表示保留原密钥。</p>
    {error && <p role="alert" className="error-text">{error}</p>}{notice && <p role="status">{notice}</p>}
    {allowed && <form onSubmit={event => { event.preventDefault(); if (lock.current) return; lock.current = true; setBusy(true); setError(""); setNotice(""); void (async () => {
      try {
        const saved = await accountsClient.upsertOAuthApplication(key.trim(), { name: name.trim(), connector_key: provider === "google" ? "google_workspace" : "microsoft_365", provider_key: provider, client_id: clientID.trim(), ...(secret ? { client_secret: secret } : {}), redirect_uri: redirect.trim(), scopes: scopes.split(/\s+/).filter(Boolean), connection_config: provider === "microsoft" ? { ...selected?.connection_config, tenant_id: tenant.trim() } : selected?.connection_config, enabled, expected_updated_at: selected?.updated_at });
        if (!mounted.current) return;
        choose(saved); setItems(current => [...current.filter(item => item.key !== saved.key), saved]); setNotice("应用配置已保存。"); onSaved();
      } catch (failure) { if (mounted.current) setError(accountFailure(failure)); }
      finally { lock.current = false; if (mounted.current) { setSecret(""); setBusy(false); } }
    })(); }}>
      <label htmlFor="oauth-existing">已登记应用</label><select id="oauth-existing" disabled={busy} value={selected?.key || ""} onChange={event => choose(items.find(item => item.key === event.target.value) || null)}><option value="">登记新应用</option>{items.map(item => <option key={item.key} value={item.key}>{item.name || item.key}</option>)}</select>
      <label htmlFor="oauth-provider">服务</label><select id="oauth-provider" disabled={busy || !!selected} value={provider} onChange={event => { setProvider(event.target.value); setScopes(event.target.value === "google" ? "https://www.googleapis.com/auth/calendar.readonly\nhttps://www.googleapis.com/auth/gmail.readonly" : "offline_access\nCalendars.Read\nMail.Read"); }}><option value="google">Google Workspace</option><option value="microsoft">Microsoft 365</option></select>
      <label htmlFor="oauth-key">配置标识</label><Input id="oauth-key" required pattern="[a-zA-Z0-9_-]+" maxLength={128} disabled={busy || !!selected} value={key} onChange={event => setKey(event.target.value)} />
      <label htmlFor="oauth-name">显示名称</label><Input id="oauth-name" required maxLength={128} disabled={busy} value={name} onChange={event => setName(event.target.value)} />
      <label htmlFor="oauth-client">Client ID</label><Input id="oauth-client" required autoComplete="off" disabled={busy} value={clientID} onChange={event => setClientID(event.target.value)} />
      <label htmlFor="oauth-secret">Client Secret</label><Input id="oauth-secret" type="password" required={!selected} autoComplete="new-password" disabled={busy} value={secret} onChange={event => setSecret(event.target.value)} />
      {provider === "microsoft" && <><label htmlFor="oauth-tenant">Tenant ID</label><Input id="oauth-tenant" required disabled={busy} value={tenant} onChange={event => setTenant(event.target.value)} /></>}
      <label htmlFor="oauth-redirect">回调地址（与厂商登记完全一致）</label><Input id="oauth-redirect" required type="url" disabled={busy} value={redirect} onChange={event => setRedirect(event.target.value)} />
      <label htmlFor="oauth-scopes">允许请求的 Scopes（每行一个）</label><textarea id="oauth-scopes" required rows={4} disabled={busy} value={scopes} onChange={event => setScopes(event.target.value)} />
      <label className="external-enabled"><Switch checked={enabled} disabled={busy} onCheckedChange={setEnabled} />允许用户发起授权</label><Button type="submit" disabled={busy}>保存应用配置</Button>
    </form>}
  </section>;
}
