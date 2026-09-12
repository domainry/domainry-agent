import { lazy, Suspense, useEffect, useState } from "react";
import { Download, RefreshCw } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { Button } from "./components/ui/button";
import { describeError } from "./errors.ts";
import { readPreviewFile, type FileTarget, type PreviewFile } from "./file-preview-state.ts";
const PDF = lazy(() => import("./file-viewers/PdfViewer"));
const Word = lazy(() => import("./file-viewers/WordViewer"));
const Sheet = lazy(() => import("./file-viewers/SpreadsheetViewer"));

export function FilePreviewDialog({ target, title, location, onClose }: { target: FileTarget; title: string; location?: { sheet?: string; row?: number; cell?: string }; onClose(): void }) {
  const [file, setFile] = useState<PreviewFile | null>(null), [error, setError] = useState(""), [refresh, setRefresh] = useState(0), [downloading, setDownloading] = useState(false);
  const key = JSON.stringify(target);
  useEffect(() => {
    const reload = () => { setFile(null); if (document.visibilityState !== "hidden") setRefresh(n => n + 1); };
    window.addEventListener("focus", reload); document.addEventListener("visibilitychange", reload);
    return () => { window.removeEventListener("focus", reload); document.removeEventListener("visibilitychange", reload); };
  }, []);
  useEffect(() => {
    const abort = new AbortController(); setFile(null); setError("");
    void readPreviewFile(JSON.parse(key), AbortSignal.any([abort.signal, AbortSignal.timeout(30000)])).then(value => { if (!abort.signal.aborted) setFile(value); }).catch(error => { if (!abort.signal.aborted) setError(describeError(error)); });
    return () => abort.abort();
  }, [key, refresh]);
  async function download() {
    if (downloading) return; setDownloading(true);
    try { const current = await readPreviewFile(target, AbortSignal.timeout(30000)); const url = URL.createObjectURL(current.blob), a = document.createElement("a"); a.href = url; a.download = current.filename; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000); }
    catch (error) { setFile(null); setError(describeError(error)); }
    finally { setDownloading(false); }
  }
  return <Dialog open onOpenChange={open => { if (!open) onClose(); }}><DialogContent className="file-preview-dialog"><DialogHeader><DialogTitle>{file?.filename || title}</DialogTitle><DialogDescription>原文件在线预览</DialogDescription></DialogHeader>
    <div className="file-toolbar"><Button variant="outline" disabled={downloading} onClick={() => void download()}><Download size={15} />{downloading ? "正在下载…" : "下载原文件"}</Button><Button variant="ghost" onClick={() => setRefresh(n => n + 1)}><RefreshCw size={15} />重新打开</Button></div>
    {error ? <p role="alert" className="error-text">{error}</p> : !file ? <p role="status">正在读取原文件…</p> : <Suspense fallback={<p role="status">正在加载查看器…</p>}>
      {file.format === "pdf" ? <PDF blob={file.blob} /> : file.format === "docx" ? <Word blob={file.blob} /> : file.format === "xlsx" ? <Sheet blob={file.blob} location={location} /> : <p>此格式暂不支持在线预览，请下载原文件查看。旧版 Word／Excel 可另存为 .docx／.xlsx 后预览。</p>}
    </Suspense>}
  </DialogContent></Dialog>;
}
