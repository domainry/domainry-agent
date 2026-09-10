import { useState } from "react";
import { Button } from "./components/ui/button";
import { ArtifactDialog } from "./ArtifactDialog.tsx";

type Reference = { id: string; version: number; title: string };
function references(text: string): Reference[] {
  try {
    const data = JSON.parse(text);
    const candidates = data.artifact ? [data.artifact] : data.export ? [{ id: data.export.artifact_id, version: data.export.version, title: data.export.filename }] : Array.isArray(data.items) ? data.items : [];
    return candidates.filter((item: Reference) => item && typeof item.id === "string" && typeof item.title === "string" && Number.isSafeInteger(item.version) && item.version > 0).slice(0, 50);
  } catch { return []; }
}
export function artifactResult(text?: string) {
  const items = references(text || "");
  return items.length ? <ArtifactResult items={items} /> : null;
}
function ArtifactResult({ items }: { items: Reference[] }) {
  const [selected, setSelected] = useState<Reference | null>(null);
  return <div className="artifact-result">{items.map(item => <div className="todo-toolbar" key={`${item.id}:${item.version}`}><strong>{item.title}</strong><small className="subtle">版本 {item.version}</small><Button size="sm" variant="outline" onClick={() => setSelected(item)}>查看此版本</Button></div>)}{selected && <ArtifactDialog initial={selected} onClose={() => setSelected(null)} />}</div>;
}
