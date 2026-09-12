import { useEffect, useRef, useState } from "react";
import { renderAsync } from "docx-preview";
import { loadOffice } from "./office-load";
const frameHTML = `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data: blob:; font-src data: blob:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><style>body{margin:0;background:#f5f6f4;color:#25332e} .docx-wrapper{padding:16px!important} a{pointer-events:none} img{max-width:100%} @media(max-width:600px){.docx-wrapper{padding:8px!important} section.docx{padding:24px!important;width:auto!important;min-width:0!important} table{max-width:100%}} </style></head><body><main id="document"></main></body></html>`;
export default function WordViewer({ blob }: { blob: Blob }) {
  const frame = useRef<HTMLIFrameElement>(null); const [ready, setReady] = useState(false), [loading, setLoading] = useState(true), [error, setError] = useState("");
  useEffect(() => {
    if (!ready) return; const abort = new AbortController(); setLoading(true);
    void loadOffice(blob, "docx", abort.signal).then(async () => {
      const host = frame.current?.contentDocument?.getElementById("document"); if (abort.signal.aborted || !host) return;
      // Sandboxed, scriptless iframe: document CSS cannot affect the application;
      // embedded HTML and external resources cannot run or leave this document.
      await renderAsync(blob, host, host, { useBase64URL: true, renderAltChunks: false, renderComments: false, breakPages: true, ignoreLastRenderedPageBreak: false });
      if (abort.signal.aborted) return;
      host.querySelectorAll("a").forEach(a => { a.removeAttribute("href"); a.removeAttribute("target"); });
      setLoading(false);
    }).catch(error => { if (!abort.signal.aborted) { setError(error.message || "Word 文件无法预览，请下载原文件查看。"); setLoading(false); } });
    return () => { abort.abort(); frame.current?.contentDocument?.getElementById("document")?.replaceChildren(); };
  }, [blob, ready]);
  return <div className="file-viewer"><p className="subtle">Word 只读预览 · 复杂排版可能与桌面 Word 有差异。</p>{loading && <p role="status">正在排版文档…</p>}{error && <p role="alert">{error}</p>}<iframe className="word-frame" title="Word 原文件预览" ref={frame} sandbox="allow-same-origin" srcDoc={frameHTML} onLoad={() => setReady(true)} aria-busy={loading} /></div>;
}
