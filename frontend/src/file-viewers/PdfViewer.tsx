import { useEffect, useRef, useState } from "react";
import { getDocument, GlobalWorkerOptions, type PDFDocumentProxy } from "pdfjs-dist";
import workerURL from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { Button } from "../components/ui/button";
GlobalWorkerOptions.workerSrc = workerURL;
export default function PdfViewer({ blob }: { blob: Blob }) {
  const canvas = useRef<HTMLCanvasElement>(null), viewport = useRef<HTMLDivElement>(null);
  const [pdf, setPDF] = useState<PDFDocumentProxy | null>(null), [page, setPage] = useState(1), [zoom, setZoom] = useState(1), [width, setWidth] = useState(800), [error, setError] = useState("");
  const [rendered, setRendered] = useState(false);
  useEffect(() => { const observer = new ResizeObserver(entries => setWidth(Math.max(200, entries[0].contentRect.width - 32))); observer.observe(viewport.current!); return () => observer.disconnect(); }, []);
  useEffect(() => {
    let active = true, task: ReturnType<typeof getDocument> | undefined;
    void blob.arrayBuffer().then(data => { if (!active) return; task = getDocument({ data: new Uint8Array(data), cMapUrl: "/file-viewer/pdfjs/cmaps/", cMapPacked: true, standardFontDataUrl: "/file-viewer/pdfjs/standard_fonts/", wasmUrl: "/file-viewer/pdfjs/wasm/", useWorkerFetch: true });
      return task.promise.then(value => { if (active) setPDF(value); });
    }).catch(() => { if (active) setError("PDF 无法打开，文件可能已损坏或需要密码。可下载原文件查看。"); });
    return () => { active = false; void task?.destroy(); };
  }, [blob]);
  useEffect(() => {
    if (!pdf) return; let active = true, task: { cancel(): void } | undefined; setRendered(false);
    void pdf.getPage(page).then(value => {
      if (!active || !canvas.current) return;
      const base = value.getViewport({ scale: 1 }), scale = Math.min(width / base.width, 2) * zoom;
      const view = value.getViewport({ scale }), ratio = Math.min(window.devicePixelRatio || 1, 2);
      const element = canvas.current; element.width = Math.ceil(view.width * ratio); element.height = Math.ceil(view.height * ratio); element.style.width = `${view.width}px`; element.style.height = `${view.height}px`;
      const rendering = value.render({ canvas: element, viewport: view, transform: [ratio, 0, 0, ratio, 0, 0] }); task = rendering;
      return rendering.promise.then(() => { if (active) setRendered(true); });
    }).catch(error => { if (active && error?.name !== "RenderingCancelledException") setError("此页无法显示，请下载原文件查看。"); });
    return () => { active = false; task?.cancel(); };
  }, [pdf, page, zoom, width]);
  return <div className="file-viewer"><nav className="file-toolbar" aria-label="PDF 翻页"><Button variant="outline" disabled={!pdf || page <= 1} onClick={() => setPage(p => p - 1)}>上一页</Button><span>第 {page} / {pdf?.numPages || "—"} 页</span><Button variant="outline" disabled={!pdf || page >= pdf.numPages} onClick={() => setPage(p => p + 1)}>下一页</Button><label>缩放 <select aria-label="缩放" value={zoom} onChange={event => setZoom(Number(event.target.value))}><option value={0.75}>75%</option><option value={1}>适合宽度</option><option value={1.5}>150%</option><option value={2}>200%</option></select></label></nav>
    {error && <p role="alert">{error}</p>}<div className="file-canvas" ref={viewport} aria-busy={!rendered && !error}><canvas ref={canvas} aria-label={`PDF 第 ${page} 页`} data-rendered={rendered} /></div></div>;
}
