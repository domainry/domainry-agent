import type { ResultReference } from "./execution-state.ts";

export type ResultSlice = { reference: ResultReference; json_text: string; offset: number; next_offset: number; total_bytes: number; complete: boolean };
const encoder = new TextEncoder();
// Format tokens without reserializing parsed numbers: JSON.parse/stringify
// would silently round large numeric filter parameters in an otherwise valid
// and correctly hashed source result.
function formatJSON(raw:string):string {
  JSON.parse(raw);
  let out="",depth=0,quoted=false,escaped=false;
  for(let i=0;i<raw.length;i++) {
    if(depth>64||out.length>4*1024*1024)return raw;
    const c=raw[i];
    if(quoted){out+=c;if(escaped)escaped=false;else if(c==="\\")escaped=true;else if(c==='"')quoted=false;continue;}
    if(c==='"'){quoted=true;out+=c;continue;}
    if(/\s/.test(c))continue;
    if(c==="{"||c==="["){out+=c;depth++;if(raw[i+1]!=="}"&&raw[i+1]!=="]")out+="\n"+"  ".repeat(depth);}
    else if(c==="}"||c==="]"){depth--;if(raw[i-1]!=="{"&&raw[i-1]!=="[")out+="\n"+"  ".repeat(depth);out+=c;}
    else if(c===",")out+=",\n"+"  ".repeat(depth);
    else if(c===":")out+=": ";
    else out+=c;
  }
  return out;
}
export async function readStoredResult(reference: ResultReference, read: (offset: number) => Promise<ResultSlice>, signal: AbortSignal): Promise<string> {
  let offset = 0, total = -1;
  const parts: string[] = [];
  for (let pages = 0; pages < 1024; pages++) {
    signal.throwIfAborted();
    const page = await read(offset);
    signal.throwIfAborted();
    if (!page || typeof page.json_text !== "string" || typeof page.complete !== "boolean" || !page.reference || Object.keys(reference).some(key => page.reference[key as keyof ResultReference] !== reference[key as keyof ResultReference])) throw new Error("结果引用已变化，请重新打开处理记录。");
    const bytes = encoder.encode(page.json_text).length;
    if (![page.offset, page.next_offset, page.total_bytes].every(Number.isSafeInteger) || page.offset !== offset || bytes > 8192 || page.next_offset !== offset + bytes || page.total_bytes < 1 || page.total_bytes > 2 * 1024 * 1024 || page.next_offset > page.total_bytes || (total >= 0 && total !== page.total_bytes) || page.complete !== (page.next_offset === page.total_bytes) || !page.complete && bytes === 0) throw new Error("结果分页不完整，请重新读取。");
    parts.push(page.json_text); offset = page.next_offset; total = page.total_bytes;
    if (page.complete) {
      const raw = parts.join("");
      const digest = await crypto.subtle.digest("SHA-256", encoder.encode(raw));
      const hash = Array.from(new Uint8Array(digest), v => v.toString(16).padStart(2,"0")).join("");
      signal.throwIfAborted();
      if (hash !== reference.sha256) throw new Error("完整结果校验失败，请刷新处理记录。");
      return formatJSON(raw);
    }
  }
  throw new Error("结果分页超过读取上限，请缩小分析范围。");
}
