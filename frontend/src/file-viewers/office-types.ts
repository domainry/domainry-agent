import type { CSSProperties } from "react";
export type DisplayCell = { row: number; col: number; text: string; formula?: string; style: CSSProperties };
export type DisplayMerge = { top: number; left: number; bottom: number; right: number };
export type DisplaySheet = { name: string; rows: number; cols: number; cells: DisplayCell[]; merges: DisplayMerge[]; widths: Record<number, number>; heights: Record<number, number>; hiddenRows: number[]; hiddenCols: number[] };
export type OfficeResult = { sheets?: DisplaySheet[] };
