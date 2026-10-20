import type { ReactNode } from "react";
import { KnowledgeWorkspace as KnowledgeFeature } from "@domainry/knowledge-ui";
import { describeError } from "./errors.ts";
import { agentKnowledgeClient } from "./knowledge-client.ts";

export function KnowledgeWorkspace({ navigation }: { navigation?: ReactNode }) {
  return <KnowledgeFeature client={agentKnowledgeClient} navigation={navigation} formatError={describeError} />;
}
