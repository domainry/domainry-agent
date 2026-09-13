import { useEffect, useRef, useState } from 'react';
import { Button } from './components/ui/button';
import { request } from './api.ts';
import { ApiError, describeError } from './errors.ts';
import { documentLabel, documentPath, readKnowledgeDocument, type KnowledgeDocument, type KnowledgeDocumentPage, type KnowledgeLibrary } from './knowledge-document-state.ts';
import type { SharedDocumentReference } from './collaboration-state.ts';

type LibraryPage = { items: KnowledgeLibrary[]; complete: boolean; next_after?: string };
export const sharedDocumentReference = (doc: KnowledgeDocument): SharedDocumentReference => ({ library_id: doc.library_id, document_id: doc.id, revision: doc.revision, sha256: doc.sha256 });
const keyOf = (ref: SharedDocumentReference) => `${ref.library_id}/${ref.document_id}`;
const documentKey = (doc: KnowledgeDocument) => `${doc.library_id}/${doc.id}`;

export function PeerDocumentPicker({ value, disabled, onChange }: { value: KnowledgeDocument[]; disabled: boolean; onChange: (docs: KnowledgeDocument[]) => void }) {
  const [opened, setOpened] = useState(false), [library, setLibrary] = useState('');
  const [libraries, setLibraries] = useState<LibraryPage>({ items: [], complete: true }), [documents, setDocuments] = useState<KnowledgeDocumentPage>({ items: [], complete: true });
  const [libCursors, setLibCursors] = useState(['']), [docCursors, setDocCursors] = useState(['']);
  const [loading, setLoading] = useState(false), [error, setError] = useState('');
  const libCursor = libCursors.at(-1)!, docCursor = docCursors.at(-1)!;
  useEffect(() => {
    if (!opened) return;
    const abort = new AbortController(); setLoading(true); setError(''); setLibraries({ items: [], complete: true }); setDocuments({ items: [], complete: true });
    const signal = AbortSignal.any([abort.signal, AbortSignal.timeout(15000)]);
    void Promise.all([
      request<LibraryPage>(`/agent/knowledge-libraries?${new URLSearchParams({ after: libCursor, limit: '20' })}`, 'GET', undefined, signal),
      library ? request<KnowledgeDocumentPage>(`${documentPath(library)}?${new URLSearchParams({ after: docCursor, limit: '20' })}`, 'GET', undefined, signal) : Promise.resolve({ items: [], complete: true }),
    ]).then(([libs, docs]) => { if (!abort.signal.aborted) { setLibraries(libs); setDocuments(docs); } }).catch(e => { if (!abort.signal.aborted) setError(describeError(e)); }).finally(() => { if (!abort.signal.aborted) setLoading(false); });
    return () => abort.abort();
  }, [opened, library, libCursor, docCursor]);
  return <section className="attachment-library-save" aria-label="共享资料选择">
    <Button type="button" variant="outline" disabled={disabled} onClick={() => setOpened(!opened)}>{opened ? '收起资料选择' : '选择共享资料'}</Button>
    {value.length > 0 && <ul>{value.map(doc => <li className="break-words" key={documentKey(doc)}>{doc.filename} · 版本 {doc.revision} <Button type="button" size="sm" variant="ghost" disabled={disabled} onClick={() => onChange(value.filter(item => documentKey(item) !== documentKey(doc)))}>移除</Button></li>)}</ul>}
    {opened && <>
      <p className="subtle">随消息发送所选文件的准确版本引用，接收方仍需资料库阅读权限。私有附件请先在会话附件中“另存到资料库”。最多选择 4 份已完成索引的资料。</p>
      {error && <p role="alert" className="error-text">{error}</p>}
      <label>资料库<select aria-label="共享资料库" value={library} disabled={disabled || loading} onChange={e => { setLibrary(e.target.value); setDocCursors(['']); }}><option value="">选择资料库</option>{libraries.items.map(lib => <option key={lib.id} value={lib.id} disabled={lib.archived || !lib.documents_configured}>{lib.name} · {lib.kind === 'shared' ? '共享' : '个人'}</option>)}</select></label>
      {(libCursors.length > 1 || !libraries.complete) && <div className="interaction-actions"><Button type="button" size="sm" disabled={disabled || loading || libCursors.length === 1} onClick={() => { setLibCursors(c => c.slice(0, -1)); setLibrary(''); }}>上一页资料库</Button><Button type="button" size="sm" disabled={disabled || loading || !libraries.next_after} onClick={() => { setLibCursors(c => [...c, libraries.next_after!]); setLibrary(''); }}>下一页资料库</Button></div>}
      {loading ? <p role="status">正在读取可访问资料…</p> : library && <div className="attachment-list">{documents.items.map(doc => {
        const selected = value.some(item => documentKey(item) === documentKey(doc));
        return <Button key={documentKey(doc)} type="button" variant={selected ? 'secondary' : 'outline'} disabled={disabled || doc.state !== 'ready' || !selected && value.length >= 4} aria-pressed={selected} onClick={() => onChange(selected ? value.filter(item => documentKey(item) !== documentKey(doc)) : [...value, doc])}><span><strong>{doc.filename}</strong><small>{documentLabel(doc)}{selected ? ' · 已选择' : ''}</small></span></Button>;
      })}{!documents.items.length && <p className="subtle">本页没有资料。</p>}</div>}
      {(docCursors.length > 1 || !documents.complete) && <div className="interaction-actions"><Button type="button" size="sm" disabled={disabled || loading || docCursors.length === 1} onClick={() => setDocCursors(c => c.slice(0, -1))}>上一页资料</Button><Button type="button" size="sm" disabled={disabled || loading || !documents.next_after} onClick={() => setDocCursors(c => [...c, documents.next_after!])}>下一页资料</Button></div>}
    </>}
  </section>;
}

export function PeerSharedDocuments({ references }: { references: SharedDocumentReference[] }) {
  const [documents, setDocuments] = useState<KnowledgeDocument[]>([]), [busy, setBusy] = useState(false), [error, setError] = useState('');
  const action = useRef<AbortController | null>(null), key = JSON.stringify(references);
  useEffect(() => {
    const clear = () => { action.current?.abort(); action.current = null; setDocuments([]); setBusy(false); setError(''); };
    clear(); window.addEventListener('blur', clear); document.addEventListener('visibilitychange', clear);
    return () => { clear(); window.removeEventListener('blur', clear); document.removeEventListener('visibilitychange', clear); };
  }, [key]);
  async function perform(download?: SharedDocumentReference) {
    if (action.current) return;
    const abort = new AbortController(); action.current = abort; setBusy(true); setError(''); setDocuments([]);
    const signal = AbortSignal.any([abort.signal, AbortSignal.timeout(30000)]);
    try {
      const docs = await Promise.all(references.map(async ref => {
        const doc = await request<KnowledgeDocument>(documentPath(ref.library_id, ref.document_id), 'GET', undefined, signal);
        if (doc.library_id !== ref.library_id || doc.id !== ref.document_id || doc.revision !== ref.revision || doc.sha256 !== ref.sha256 || doc.state !== 'ready') throw new ApiError('agent.conversation.shared_document_changed');
        return doc;
      }));
      if (download) {
        const doc = docs.find(item => keyOf(sharedDocumentReference(item)) === keyOf(download))!;
        const blob = await readKnowledgeDocument(doc, signal); signal.throwIfAborted();
        const url = URL.createObjectURL(blob), link = document.createElement('a'); link.href = url; link.download = doc.filename; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
      }
      signal.throwIfAborted(); setDocuments(docs);
    } catch (e) { if (!abort.signal.aborted) setError(describeError(e)); }
    finally { if (action.current === abort) { action.current = null; setBusy(false); } }
  }
  return <section aria-label="共享资料引用"><Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void perform()}>{busy ? '正在核对资料权限…' : `查看共享资料（${references.length}）`}</Button>{error && <p role="alert" className="error-text">{error}</p>}{documents.map(doc => <div key={documentKey(doc)}><p className="break-words">{doc.filename} · 版本 {doc.revision}</p><Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void perform(sharedDocumentReference(doc))}>下载共享原文件</Button></div>)}</section>;
}
