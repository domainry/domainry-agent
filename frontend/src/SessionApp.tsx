import { useEffect, useState } from "react";
import { LogOut, ArrowUpRight, FileText, ListChecks, MessageSquare } from "lucide-react";
import App from "./App";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { ApiError } from "./errors";
import { configureAuthentication, restoreSession, sessionJSON, sessionLock, setSession, type AppSession } from "./session";

type ExternalLogin = { display_name: string; login_url: string; logout_url?: string; credential: { location: string } };
function accountURL(value: string) {
  const url = new URL(value);
  if (url.protocol !== "https:" || url.username || url.password) throw new Error("Invalid account URL");
  return url.href;
}

function authError(error: unknown) {
  if (error instanceof ApiError) {
    if (error.code === "auth.invalid_credentials") return "账号或密码不正确。";
    if (error.code.includes("password")) return "密码未通过校验，请检查当前密码和新密码要求。";
    if (error.code.includes("lock") || error.status === 429) return "尝试次数较多，请稍后再试。";
    if (error.status === 401) return "登录已过期，请重新登录。";
  }
  return "暂时无法完成登录操作，请重试。";
}

export default function SessionApp() {
  const [session, updateSession] = useState<AppSession | null>(null);
  const [mode, setMode] = useState<"loading" | "identity" | "local">("loading");
  const [workspace, setWorkspace] = useState("");
  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const [external, setExternal] = useState<ExternalLogin | null>(null);

  function accept(value: AppSession | null) { setSession(value); updateSession(value); }
  function clear() {
    accept(null);
    setPassword(""); setNewPassword(""); setConfirmation("");
    history.replaceState(null, "", location.pathname + location.search);
  }
  function notifyPeers() {
    if (typeof BroadcastChannel === "undefined") return;
    const channel = new BroadcastChannel("domainry-agent:identity");
    channel.postMessage("changed"); channel.close();
  }

  useEffect(() => {
    let disposed = false;
    setError("");
    void (async () => {
      try {
        const config = await sessionJSON<{ mode: "identity" | "local"; authentication?: "managed" | "external"; workspace_id: string }>("/app/config");
        if (disposed) return;
        configureAuthentication(config.authentication);
        if (config.authentication === "external") {
          const provider = await sessionJSON<ExternalLogin>("/auth/external/config");
          if (provider.credential?.location !== "cookie") throw new Error("Agent browser requires cookie authentication");
          provider.login_url = accountURL(provider.login_url);
          if (provider.logout_url) provider.logout_url = accountURL(provider.logout_url);
          if (disposed) return;
          setExternal(provider);
        } else setExternal(null);
        setMode(config.mode); setWorkspace(config.workspace_id);
        if (config.mode === "local") {
          const local = await sessionJSON<AppSession>("/app/session");
          if (!disposed) accept(local);
        } else {
          try {
            const value = await restoreSession();
            if (!disposed) accept(value);
          } catch (e) {
            if (!disposed && (!(e instanceof ApiError) || ![401, 403].includes(e.status || 0))) setError(authError(e));
          }
        }
      } catch { if (!disposed) setError("暂时无法连接服务，请重试。"); }
    })();
    return () => { disposed = true; };
  }, [retry]);

  useEffect(() => {
    const expired = () => { clear(); setError("登录状态已变化，请重新登录。"); };
    window.addEventListener("agent-session-expired", expired);
    const channel = typeof BroadcastChannel !== "undefined" ? new BroadcastChannel("domainry-agent:identity") : null;
    if (channel) channel.onmessage = () => { clear(); setRetry((n) => n + 1); };
    return () => { window.removeEventListener("agent-session-expired", expired); channel?.close(); };
  }, []);

  async function logout() {
    setBusy(true); setError("");
    try {
      if (external) {
        if (!external.logout_url) throw new Error("Account logout URL is unavailable");
        clear(); notifyPeers();
        window.location.assign(external.logout_url);
        return;
      }
      await sessionLock(() => sessionJSON("/auth/logout", "POST", {}));
      clear(); notifyPeers();
    } catch (e) { setError(authError(e)); }
    finally { setBusy(false); }
  }

  if (session && !session.must_change_password)
    return <App key={session.scope} session={session} onLogout={session.mode === "identity" && (!external || external.logout_url) ? logout : undefined} accountBusy={busy} accountError={error} />;

  const changing = !!session?.must_change_password;
  return <main className="login-page">
    <aside className="login-story">
      <div className="brand"><span className="brand-symbol">d.</span><div>Domainry<small>你的智能工作空间</small></div></div>
      <div className="login-story-content"><span className="login-kicker">思路在这里，工作向前走</span><h2>从一个想法，<br />到下一步行动。</h2><p>让对话连接资料与工作，<br />把每一次讨论，变成有用的积累。</p>
        <div className="login-story-items"><span><MessageSquare size={18} />持续的讨论<ArrowUpRight size={16} /></span><span><FileText size={18} />有依据的答案<ArrowUpRight size={16} /></span><span><ListChecks size={18} />清晰的下一步<ArrowUpRight size={16} /></span></div>
      </div><span className="login-story-footer">DOMAINRY · AGENT WORKSPACE</span>
    </aside>
    <div className="login-form-panel"><section className="login-card">
    <div className="login-brand"><span className="brand-symbol">d.</span><span>DOMAINRY AGENT</span></div>
    <h1>{mode === "loading" ? "正在连接…" : changing ? "设置你的新密码" : "登录，继续你的工作"}</h1>
    <p>{changing ? "首次登录需要修改初始密码，完成后即可开始对话。" : "对话历史和个人记忆将保存在你的账号下。"}</p>
    {workspace && <div className="login-workspace">工作空间 · {workspace}</div>}
    {error && <div role="alert" className="login-error">{error}</div>}
    {mode === "loading" || mode === "local" ? <Button onClick={() => setRetry((n) => n + 1)}>重新连接</Button> : external ? <div className="external-login">
      <Button onClick={() => window.location.assign(external.login_url)}>使用 {external.display_name || "账号服务"} 登录<ArrowUpRight size={16} /></Button>
      <Button variant="ghost" onClick={() => setRetry((n) => n + 1)}>已完成登录，继续进入</Button>
    </div> : <form onSubmit={async (event) => {
      event.preventDefault(); if (busy) return;
      if (changing && (newPassword.length < 8 || newPassword !== confirmation)) { setError("新密码至少 8 位，两次输入需要一致。"); return; }
      setBusy(true); setError("");
      try {
        await sessionLock(async () => {
          const result = await sessionJSON<{ status?: string }>(changing ? "/auth/password/change" : "/auth/login", "POST", changing ? { current_password: password, new_password: newPassword } : { workspace_id: workspace, login, password });
          if (result.status === "challenge_required") throw new ApiError("auth.challenge_required");
          const value = await sessionJSON<AppSession>("/app/session");
          if (!changing) history.replaceState(null, "", location.pathname + location.search);
          accept(value);
        });
        setPassword(""); setNewPassword(""); setConfirmation(""); notifyPeers();
      } catch (e) { setError(authError(e)); }
      finally { setBusy(false); }
    }}>
      {!changing && <label>账号<Input autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} required autoFocus /></label>}
      <label>{changing ? "初始密码" : "密码"}<Input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required /></label>
      {changing && <><label>新密码<Input type="password" autoComplete="new-password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} required minLength={8} /></label><label>再次输入新密码<Input type="password" autoComplete="new-password" value={confirmation} onChange={(e) => setConfirmation(e.target.value)} required /></label></>}
      <Button type="submit" disabled={busy}>{busy ? "正在处理…" : changing ? "保存密码并进入" : "登录"}</Button>
      {changing && <Button type="button" variant="ghost" onClick={logout} disabled={busy}><LogOut size={15} />切换账号</Button>}
    </form>}
  </section><p className="login-footer">专注眼前的工作，其余交给有序的协作。</p></div></main>;
}
