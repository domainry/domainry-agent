import { accountsClient, loadAuthorization } from "./external-account-state.ts";
import { configureAuthentication, restoreSession, sessionJSON, setSession } from "./session.ts";

// Codes/state stay only in this page's memory. Clear the address before any
// session or Integration request; never persist or broadcast callback material.
const query = new URLSearchParams(location.search);
history.replaceState(null, "", location.pathname);
const message = document.getElementById("oauth-status")!;
const back = document.getElementById("oauth-back") as HTMLAnchorElement;
back.href = "/";
void (async () => {
  const pending = loadAuthorization();
  try {
    if (!pending || query.getAll("state").length !== 1 || !query.get("state") || query.getAll("code").length > 1 || query.getAll("error").length > 1 || !!query.get("code") === !!query.get("error")) throw new Error("invalid_callback");
    const config = await sessionJSON<{ authentication?: "managed" | "external" }>("/app/config");
    configureAuthentication(config.authentication);
    const session = await restoreSession();
    if (session.mode !== "identity" || session.scope !== pending.scope || session.must_change_password) throw new Error("identity_changed");
    setSession(session);
    const receipt = await accountsClient.completeOAuthAuthorization({ state: query.get("state")!, ...(query.get("code") ? { code: query.get("code")! } : { error: query.get("error")! }) });
    if (receipt.id !== pending.id) throw new Error("authorization_changed");
    query.delete("state"); query.delete("code"); query.delete("error");
    // The main account page reads the persisted receipt by ID, including an
    // exchanging/unknown result. Returning never repeats the authorization POST.
    location.replace("/" + pending.returnHash);
  } catch {
    query.delete("state"); query.delete("code"); query.delete("error");
    message.textContent = "暂时无法完成授权。请返回工作空间，使用发起授权的账号核对状态；需要时重新授权。";
    back.hidden = false;
  }
})();
