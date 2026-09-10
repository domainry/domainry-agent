export type PendingMessage = { id: string; text: string; memoryWrite?: boolean; todoWrite?: boolean; artifactWrite?: boolean };
export type Draft = { text: string; memoryWrite?: boolean; todoWrite?: boolean; artifactWrite?: boolean; pending?: PendingMessage };
type StoragePort = Pick<Storage, "getItem" | "setItem" | "removeItem">;

// Unsent text belongs to the local browser and conversation, not model history.
// A pending logical send survives refresh with the same idempotency identity.
export class DraftStore {
  private storage: () => StoragePort;
  private cache = new Map<string, Draft>();
  private prefix: string;
  available = true;
  constructor(storage: () => StoragePort, scope: string) {
    this.storage = storage;
    this.prefix = `domainry-agent:draft:v1:${scope}:`;
  }
  read(id: string): Draft {
    if (!this.available && this.cache.has(id)) return this.cache.get(id)!;
    try {
      const raw = this.storage().getItem(this.prefix + (id || "new"));
      if (raw !== null) {
        const parsed: unknown = JSON.parse(raw);
        if (
          typeof parsed === "object" &&
          parsed !== null &&
          "text" in parsed &&
          typeof parsed.text === "string"
        ) {
          const draft: Draft = { text: parsed.text, ...("artifactWrite" in parsed && parsed.artifactWrite === true ? { artifactWrite: true } : {}), ...("todoWrite" in parsed && parsed.todoWrite === true ? { todoWrite: true } : {}), ...("memoryWrite" in parsed && parsed.memoryWrite === true ? { memoryWrite: true } : {}) };
          if (
            "pending" in parsed &&
            typeof parsed.pending === "object" &&
            parsed.pending !== null &&
            "id" in parsed.pending &&
            "text" in parsed.pending &&
            typeof parsed.pending.id === "string" &&
            typeof parsed.pending.text === "string"
          )
            draft.pending = {
              id: parsed.pending.id,
              text: parsed.pending.text,
              ...("artifactWrite" in parsed.pending && parsed.pending.artifactWrite === true ? { artifactWrite: true } : {}),
              ...("todoWrite" in parsed.pending && parsed.pending.todoWrite === true ? { todoWrite: true } : {}),
              ...("memoryWrite" in parsed.pending && parsed.pending.memoryWrite === true ? { memoryWrite: true } : {}),
            };
          this.cache.set(id, draft);
        }
      } else if (this.available) this.cache.delete(id);
    } catch {
      this.available = false;
    }
    return this.cache.get(id) || { text: "" };
  }
  private save(id: string, draft: Draft) {
    this.cache.set(id, draft);
    try {
      if (!draft.text && !draft.pending && !draft.memoryWrite && !draft.todoWrite && !draft.artifactWrite)
        this.storage().removeItem(this.prefix + (id || "new"));
      else
        this.storage().setItem(
          this.prefix + (id || "new"),
          JSON.stringify(draft),
        );
    } catch {
      this.available = false;
    }
  }
  write(id: string, text: string) {
    this.save(id, { ...this.read(id), text });
  }
  writeMemoryScope(id: string, memoryWrite: boolean) {
    this.save(id, { ...this.read(id), memoryWrite });
  }
  writeTodoScope(id: string, todoWrite: boolean) {
    this.save(id, { ...this.read(id), todoWrite });
  }
  writeArtifactScope(id: string, artifactWrite: boolean) {
    this.save(id, { ...this.read(id), artifactWrite });
  }
  pending(id: string, text: string, memoryWrite = false, todoWrite = false, artifactWrite = false): PendingMessage {
    const draft = this.read(id);
    const pending =
      draft.pending?.text === text
        ? draft.pending
        : { id: crypto.randomUUID(), text, ...(artifactWrite ? { artifactWrite: true } : {}), ...(todoWrite ? { todoWrite: true } : {}), ...(memoryWrite ? { memoryWrite: true } : {}) };
    this.save(id, { ...draft, pending });
    return pending;
  }
  acknowledge(id: string, sent: PendingMessage) {
    const draft = this.read(id);
    if (draft.pending?.id !== sent.id) return;
    this.save(id, { text: draft.text.trim() === sent.text ? "" : draft.text });
  }
  remove(id: string) {
    this.save(id, { text: "" });
  }
}
