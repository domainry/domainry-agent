import { Unzip, UnzipInflate } from "fflate";
import ExcelJS from "exceljs";
import { format } from "numfmt";
import type { DisplayCell, DisplaySheet } from "./office-types";

// Count actual expanded bytes before Office libraries allocate their document models.
// The caller also owns a hard worker timeout and terminates on close.
export function checkOfficeArchive(data: Uint8Array, kind: string) {
  const names = new Set<string>(); let expanded = 0;
  const unzip = new Unzip(entry => {
    if (names.size >= 4096 || names.has(entry.name) || entry.name.split("/").includes("..") || /vbaProject|externalLinks/i.test(entry.name)) throw Error("不支持含宏、外部工作簿链接或异常压缩结构的文件。");
    names.add(entry.name);
    entry.ondata = (error, chunk) => { if (error) throw error; expanded += chunk.length; if (expanded > 32 * 1024 * 1024) throw Error("文件展开后超过 32 MB，请下载原文件查看。"); };
    entry.start();
  });
  unzip.register(UnzipInflate);
  for (let offset = 0; offset < data.length; offset += 16384) unzip.push(data.subarray(offset, offset + 16384), offset + 16384 >= data.length);
  if (!names.has("[Content_Types].xml") || !names.has(kind === "xlsx" ? "xl/workbook.xml" : "word/document.xml")) throw Error("文件格式与扩展名不符，无法预览。");
}
const color = (value?: Partial<ExcelJS.Color>) => value?.argb && /^[a-f\d]{8}$/i.test(value.argb) ? `#${value.argb.slice(2)}` : undefined;
function displayCell(cell: ExcelJS.Cell): DisplayCell {
  let value = cell.value, formula: string | undefined;
  if (value && typeof value === "object" && "formula" in value) { formula = value.formula; value = value.result ?? null; }
  else if (value && typeof value === "object" && "sharedFormula" in value) { formula = cell.formula; value = value.result ?? null; }
  let text = value === null ? formula ? "（无保存的计算结果）" : "" : value instanceof Date ? format(cell.numFmt || "yyyy-mm-dd", value) : typeof value === "object" ? "richText" in value ? value.richText.map(p => p.text).join("") : "text" in value ? value.text : "error" in value ? value.error : "" : typeof value === "number" ? format(cell.numFmt || "General", value) : String(value);
  if (text.length > 32768) throw Error("单元格内容过长，请下载原文件查看。");
  const f = cell.font, a = cell.alignment, fill = cell.fill;
  return { row: Number(cell.row), col: Number(cell.col), text, formula, style: {
    fontWeight: f?.bold ? "bold" : undefined, fontStyle: f?.italic ? "italic" : undefined,
    textDecoration: f?.underline ? "underline" : f?.strike ? "line-through" : undefined,
    color: color(f?.color), backgroundColor: fill?.type === "pattern" ? color(fill.fgColor) : undefined,
    fontSize: f?.size ? Math.max(12, Math.min(40, f.size * 4 / 3)) : undefined,
    textAlign: a?.horizontal === "center" || a?.horizontal === "right" ? a.horizontal : typeof value === "number" ? "right" : "left",
    verticalAlign: a?.vertical === "middle" || a?.vertical === "bottom" ? a.vertical : "top", whiteSpace: a?.wrapText ? "pre-wrap" : "pre",
  } };
}
export async function readWorkbook(data: ArrayBuffer): Promise<DisplaySheet[]> {
  const workbook = new ExcelJS.Workbook(); await workbook.xlsx.load(data);
  if (workbook.worksheets.length > 64) throw Error("工作表超过 64 个，请下载原文件查看。");
  let count = 0;
  return workbook.worksheets.filter(sheet => sheet.state === "visible").map(sheet => {
    if (sheet.rowCount > 100000 || sheet.columnCount > 4096) throw Error("工作表超出在线预览范围，请下载原文件查看。");
    const result: DisplaySheet = { name: sheet.name, rows: sheet.rowCount, cols: sheet.columnCount, cells: [], merges: [], widths: {}, heights: {}, hiddenRows: [], hiddenCols: [] };
    sheet.eachRow(row => { if (row.hidden) result.hiddenRows.push(row.number); if (row.height) result.heights[row.number] = Math.min(400, row.height * 4 / 3);
      row.eachCell(cell => { if (++count > 250000) throw Error("非空单元格超过 25 万个，请下载原文件查看。"); if (!cell.isMerged || cell.address === cell.master.address) result.cells.push(displayCell(cell)); });
    });
    (sheet.columns || []).forEach((column, index) => { if (column.hidden) result.hiddenCols.push(index + 1); result.widths[index + 1] = Math.max(48, Math.min(600, (column.width || 14) * 7 + 5)); });
    const parseAddress = (a: string) => { const match = /^([A-Z]+)([1-9][0-9]*)$/.exec(a); if (!match) throw Error("合并单元格地址无效。"); return { row: Number(match[2]), col: [...match[1]].reduce((v, c) => v * 26 + c.charCodeAt(0) - 64, 0) }; };
    for (const merged of sheet.model.merges) { const [start, end = start] = merged.split(":"); const a = parseAddress(start), b = parseAddress(end); result.merges.push({ top: a.row, left: a.col, bottom: b.row, right: b.col }); }
    return result;
  });
}
self.onmessage = async event => {
  try { const { data, kind } = event.data; checkOfficeArchive(new Uint8Array(data), kind); self.postMessage(kind === "xlsx" ? { sheets: await readWorkbook(data) } : {}); }
  catch (error) { self.postMessage({ error: error instanceof Error ? error.message : "文件无法预览。" }); }
};
