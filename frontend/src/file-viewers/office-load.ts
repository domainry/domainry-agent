import type { OfficeResult } from "./office-types";
export async function loadOffice(blob: Blob, kind: string, signal: AbortSignal): Promise<OfficeResult> {
  const data = await blob.arrayBuffer(); signal.throwIfAborted();
  return new Promise((resolve, reject) => {
    const worker = new Worker(new URL("./office-worker.ts", import.meta.url), { type: "module" });
    const done = (error?: Error, value?: OfficeResult) => { clearTimeout(timer); worker.terminate(); signal.removeEventListener("abort", abort); if (error) reject(error); else resolve(value!); };
    const abort = () => done(new DOMException("Aborted", "AbortError"));
    const timer = setTimeout(() => done(Error("文件预览处理超时，请下载原文件查看。")), 20000);
    signal.addEventListener("abort", abort, { once: true });
    worker.onerror = () => done(Error("文件预览失败，请下载原文件查看。"));
    worker.onmessage = event => event.data.error ? done(Error(event.data.error)) : done(undefined, event.data);
    worker.postMessage({ data, kind }, [data]);
  });
}
