import { useEffect, useRef, useState } from "react";
import { Brain, Pencil, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { request, type Memory } from "./api";
import { describeError } from "./errors";

export type MemorySource = { title: string; content: string };
export function MemoryDialog({
  source,
  onClose,
  onSaved,
}: {
  source: MemorySource | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [items, setItems] = useState<Memory[]>([]);
  const [editing, setEditing] = useState<Memory | null>(null);
  const [fromMessage, setFromMessage] = useState(!!source);
  const [savedNotice, setSavedNotice] = useState("");
  const [title, setTitle] = useState(source?.title || "");
  const [content, setContent] = useState(source?.content || "");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const newID = useRef(crypto.randomUUID());
  const titleBytes = new TextEncoder().encode(title.trim()).length;
  const contentBytes = new TextEncoder().encode(content.trim()).length;
  const valid =
    !!title.trim() &&
    !!content.trim() &&
    titleBytes <= 128 &&
    contentBytes <= 512;
  useEffect(() => {
    const controller = new AbortController();
    request<Memory[]>(
      "/agent/conversations/memories",
      "GET",
      undefined,
      controller.signal,
    )
      .then((v) => setItems(v || []))
      .catch((e) => {
        if (!controller.signal.aborted) setError(describeError(e));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, []);
  function reset() {
    setFromMessage(false);
    setEditing(null);
    setTitle("");
    setContent("");
    newID.current = crypto.randomUUID();
  }
  async function mutate(action: () => Promise<void>) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (e) {
      setError(describeError(e));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent className="memory-dialog">
        <DialogHeader>
          <DialogTitle>个人记忆</DialogTitle>
          <DialogDescription>
            保存希望在新会话中沿用的偏好。开启会话的「使用个人记忆」后，后续对话会使用这些内容。
          </DialogDescription>
        </DialogHeader>
        {error && (
          <p role="alert" className="text-destructive text-sm">
            {error}
          </p>
        )}
        {savedNotice && (
          <p role="status" className="composer-notice">
            {savedNotice}
          </p>
        )}
        <div className="memory-list">
          {loading ? (
            <p className="subtle">正在读取记忆…</p>
          ) : items.length ? (
            items.map((memory) => (
              <div className="memory-card" key={memory.id}>
                <div>
                  <strong>{memory.title}</strong>
                  <p>{memory.content}</p>
                </div>
                <Switch
                  aria-label={`启用记忆 ${memory.title}`}
                  checked={memory.enabled}
                  disabled={busy}
                  onCheckedChange={(enabled) =>
                    void mutate(async () => {
                      const updated = await request<Memory>(
                        `/agent/conversations/memories/${memory.id}`,
                        "PUT",
                        {
                          title: memory.title,
                          content: memory.content,
                          enabled,
                          expected_revision: memory.revision,
                        },
                      );
                      setItems((v) =>
                        v.map((m) => (m.id === updated.id ? updated : m)),
                      );
                      if (editing?.id === updated.id) setEditing(updated);
                    })
                  }
                />
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`编辑记忆 ${memory.title}`}
                  disabled={busy}
                  onClick={() => {
                    setEditing(memory);
                    setTitle(memory.title);
                    setContent(memory.content);
                    setError("");
                  }}
                >
                  <Pencil size={15} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`删除记忆 ${memory.title}`}
                  disabled={busy}
                  onClick={() =>
                    void mutate(async () => {
                      await request(
                        `/agent/conversations/memories/${memory.id}?expected_revision=${memory.revision}`,
                        "DELETE",
                      );
                      setItems((v) => v.filter((m) => m.id !== memory.id));
                      if (editing?.id === memory.id) reset();
                    })
                  }
                >
                  <Trash2 size={15} />
                </Button>
              </div>
            ))
          ) : (
            <p className="subtle">还没有个人记忆。会话历史会独立保存。</p>
          )}
        </div>
        <form
          className="dialog-form"
          onSubmit={(event) => {
            event.preventDefault();
            if (!valid) return;
            void mutate(async () => {
              const saved = await request<Memory>(
                `/agent/conversations/memories/${editing?.id || newID.current}`,
                "PUT",
                {
                  title: title.trim(),
                  content: content.trim(),
                  enabled: editing?.enabled ?? true,
                  expected_revision: editing?.revision || 0,
                },
              );
              setItems((v) =>
                editing
                  ? v.map((m) => (m.id === saved.id ? saved : m))
                  : [...v, saved],
              );
              reset();
              setSavedNotice(
                "个人记忆已保存。开启会话的记忆开关后，后续对话会使用最新内容。",
              );
              onSaved();
            });
          }}
        >
          <div className="memory-editor-heading">
            <span>
              <Brain size={15} />
              {editing
                ? `编辑「${editing.title}」`
                : fromMessage
                  ? "从消息保存为个人记忆"
                  : "新增个人记忆"}
            </span>
            {editing && (
              <Button
                type="button"
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={reset}
              >
                取消编辑
              </Button>
            )}
          </div>
          {fromMessage && !editing && (
            <p className="subtle">
              已填入消息原文，请确认或提炼后保存。保存前不会写入个人记忆。
            </p>
          )}
          <label htmlFor="memory-title">名称</label>
          <Input
            id="memory-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            disabled={busy}
            required
            placeholder="例如：回复偏好"
          />
          <label htmlFor="memory-content">内容</label>
          <Textarea
            id="memory-content"
            rows={4}
            value={content}
            onChange={(e) => setContent(e.target.value)}
            disabled={busy}
            required
            placeholder="例如：请使用简体中文，先给结论。"
          />
          <p
            className={
              titleBytes > 128 || contentBytes > 512
                ? "text-destructive text-xs"
                : "subtle"
            }
            aria-live="polite"
          >
            名称 {titleBytes}/128 字节 · 内容 {contentBytes}/512 字节
            {contentBytes > 512 ? "，请提炼后保存" : ""}
          </p>
          <Button
            type="submit"
            disabled={
              busy || loading || !valid || (!editing && items.length >= 32)
            }
          >
            {editing ? "保存修改" : "保存记忆"}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}
