import { useEffect, useRef, useState } from "react";
import { Check, Link2, RefreshCw } from "lucide-react";
import { Button } from "./components/ui/button";
import { describeError } from "./errors.ts";
import { sessionScope } from "./session.ts";
import { bindLibrarySource, parsePendingDatasource, readLibrarySources, type LibrarySources, type PendingDatasource } from "./knowledge-datasource-state.ts";
import type { KnowledgeLibrary } from "./knowledge-document-state.ts";

export function KnowledgeSourcesPanel({ library, disabled, onBusyChange, onBound }: { library: KnowledgeLibrary; disabled: boolean; onBusyChange: (busy: boolean) => void; onBound: (library: KnowledgeLibrary) => void }) {
  const storageKey = `agent-library-source:${sessionScope()}:${library.id}`;
  const [pending, setPending] = useState<PendingDatasource | null>(() => { try { return parsePendingDatasource(localStorage.getItem(storageKey)); } catch { return null; } });
  const [page, setPage] = useState<LibrarySources | null>(null), [cursors, setCursors] = useState([""]), [refresh, setRefresh] = useState(0);
  const [choice, setChoice] = useState(""), [busy, setBusy] = useState(false), [loading, setLoading] = useState(true), [error, setError] = useState("");
  const lock = useRef(false), active = useRef<AbortController | null>(null);
  const cursor = cursors.at(-1)!;
  useEffect(() => {
    const abort = new AbortController(); setLoading(true); setPage(null);
    void readLibrarySources(library.id, cursor, abort.signal).then(value => {
      if (abort.signal.aborted) return;
      setPage(value); setError("");
      if (value.current_key) {
        const saved = parsePendingDatasource(localStorage.getItem(storageKey));
        if (saved?.datasource_key === value.current_key && value.status === "connected") { localStorage.removeItem(storageKey); setPending(null); }
      }
    }).catch(failure => { if (!abort.signal.aborted) { setPage(null); setError(describeError(failure)); } }).finally(() => { if (!abort.signal.aborted) setLoading(false); });
    return () => abort.abort();
  }, [library.id, cursor, refresh, storageKey]);
  useEffect(() => () => { active.current?.abort(); onBusyChange(false); }, [onBusyChange]);
  const blocked = disabled || busy;
  const selected = page?.items.find(item => item.key === choice && item.available);
  async function bind() {
    if (lock.current || disabled || !page?.can_bind || !pending && !selected) return;
    lock.current = true; setBusy(true); onBusyChange(true); setError("");
    const abort = new AbortController(); active.current = abort;
    try {
      const input = pending || { datasource_key: choice, expected_revision: page.revision };
      localStorage.setItem(storageKey, JSON.stringify(input)); setPending(input);
      const result = await bindLibrarySource(library.id, input, AbortSignal.any([abort.signal, AbortSignal.timeout(30000)]));
      if (abort.signal.aborted) return;
      localStorage.removeItem(storageKey); setPending(null); setChoice("");
      setPage({ ...page, current_key: result.datasource_key, status: "connected", revision: result.revision, items: page.items.map(item => ({ ...item, available: false })) });
      onBound(result);
    } catch (failure) { if (!abort.signal.aborted) setError(describeError(failure)); }
    finally { lock.current = false; if (!abort.signal.aborted) { setBusy(false); onBusyChange(false); } }
  }
  return <section className="library-sources" aria-label="资料库知识源" data-static-ui-list>
    <div className="interaction-actions"><h4>知识源</h4><Button variant="ghost" size="sm" disabled={blocked || loading} onClick={() => setRefresh(value => value + 1)}><RefreshCw size={14} />刷新知识源</Button></div>
    {error && <p role="alert" className="error-text">{error}</p>}
    {loading ? <p className="subtle">正在读取可用知识源…</p> : page && <>
      {page.status === "connected" ? <p className="subtle"><Check size={15} className="inline" /> 已连接知识源，可上传资料并建立索引。</p> : page.status === "unavailable" ? <p className="subtle">当前连接暂不可用，请联系管理员核对知识服务配置。已保存的原文件仍按资料库权限保留。</p> : page.status === "host_managed" ? <p className="subtle">此资料库的知识源由管理员配置维护。</p> : <p className="subtle">选择一个知识源连接到“{library.name}”。连接后，该知识源供此资料库使用。</p>}
      {page.status === "unbound" && <>
        {!page.can_bind && <p className="subtle">当前没有连接知识源的权限，或资料库已归档。</p>}
        {page.items.length === 0 ? <p className="subtle">暂无可选择的知识源，请联系管理员添加配置。</p> : <div className="attachment-list">{page.items.map(item => <Button key={item.key} variant={choice === item.key ? "secondary" : "outline"} aria-pressed={choice === item.key} disabled={blocked || !!pending || !item.available} onClick={() => setChoice(item.key)}><Link2 size={16} /><span><strong>{item.name}</strong><small>{item.available ? item.description || "可连接" : "暂不可连接"}</small></span></Button>)}</div>}
      </>}
      <nav className="interaction-actions" aria-label="知识源分页" data-static-ui-pagination><Button size="sm" variant="outline" disabled={blocked || loading || cursors.length === 1} onClick={() => { setChoice(""); setCursors(values => values.slice(0, -1)); }}>上一页知识源</Button><span className="subtle">第 {cursors.length} 页</span><Button size="sm" variant="outline" disabled={blocked || loading || !page.next_after} onClick={() => { setChoice(""); setCursors(values => [...values, page.next_after!]); }}>下一页知识源</Button></nav>
      {pending && <p className="subtle">上次连接结果待确认。继续确认会使用同一个知识源，不会重复创建绑定。</p>}
      {(page.status === "unbound" || pending) && <div className="interaction-actions"><Button disabled={blocked || !page.can_bind || !pending && !selected} onClick={() => void bind()}><Link2 size={15} />{busy ? "正在连接…" : pending ? "继续确认连接" : "连接知识源"}</Button>{pending && <Button variant="ghost" disabled={blocked} onClick={() => { localStorage.removeItem(storageKey); setPending(null); }}>结束本次重试</Button>}</div>}
    </>}
  </section>;
}
