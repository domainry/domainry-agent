import { useRef, useState } from 'react';
import { request } from './api.ts';
import { sessionScope } from './session.ts';

export function pendingPeerMutation(path:string):Record<string,unknown>|null {
 try {
  const saved=JSON.parse(localStorage.getItem(`agent-collaboration:${sessionScope()}:${path}`)||'null');
  const signature=saved&&typeof saved.clientID==='string'&&JSON.parse(saved.signature);
  return signature?.method==='POST'&&signature.body&&typeof signature.body==='object'&&!Array.isArray(signature.body)?signature.body:null;
 } catch {return null;}
}

// Preserve the exact retry identity across transport failures and page reloads.
export function usePeerMutation() {
 const lock = useRef(false); const [busy, setBusy] = useState(false);
 async function mutate<T>(path: string, method: string, body: Record<string,unknown>): Promise<T> {
  if (lock.current) throw new Error('操作正在提交');
  lock.current = true; setBusy(true);
  const storageKey = `agent-collaboration:${sessionScope()}:${path}`;
  const signature = JSON.stringify({ method, body });
  try {
   let clientID = crypto.randomUUID();
   try { const saved = JSON.parse(localStorage.getItem(storageKey) || 'null'); if (saved?.signature === signature) clientID = saved.clientID; } catch { /* malformed draft */ }
   localStorage.setItem(storageKey, JSON.stringify({ signature, clientID }));
   const value = await request<T>(path, method, { ...body, client_id: clientID });
   localStorage.removeItem(storageKey); return value;
  } finally { lock.current = false; setBusy(false); }
 }
 return { mutate, busy };
}
