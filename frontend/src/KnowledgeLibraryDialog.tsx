import { KnowledgeLibraryManagerDialog } from "@domainry/knowledge-ui";
import { describeError } from "./errors.ts";
import { agentKnowledgeClient } from "./knowledge-client.ts";

export function KnowledgeLibraryDialog({ onClose }: { onClose(): void }) {
  return <KnowledgeLibraryManagerDialog client={agentKnowledgeClient} formatError={describeError} onClose={onClose} />;
}
