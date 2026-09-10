package provider

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	connector "github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
)

func (k *Knowledge) PutKnowledgeDocument(ctx context.Context, in agentsdk.KnowledgeDocumentContent, a agentsdk.ConversationAuthority) error {
	if !k.config.DocumentManagement {
		return knowledgeFailure("document_management_unavailable")
	}
	_, err := connector.Call(ctx, knowledgeGateway{k, a}, httpapi.PutDocument, httpapi.PutDocumentInput{DocID: in.DocumentID, Filename: in.Filename, Content: in.Data})
	if err != nil {
		return knowledgeConnectorError(err)
	}
	return nil
}

func (k *Knowledge) InspectKnowledgeDocument(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentState, error) {
	out, err := connector.Call(ctx, knowledgeGateway{k, a}, httpapi.DocumentStatus, httpapi.DocumentInput{DocID: id})
	if err != nil {
		return agentsdk.KnowledgeDocumentState{}, knowledgeConnectorError(err)
	}
	if out.DocID != id || out.Provider != httpapi.ProviderKey || out.KBID != k.config.KBID {
		return agentsdk.KnowledgeDocumentState{}, knowledgeFailure("response_invalid")
	}
	return agentsdk.KnowledgeDocumentState{DocumentID: id, Exists: out.Exists, IndexStatus: out.IndexStatus}, nil
}

func (k *Knowledge) DeleteKnowledgeDocument(ctx context.Context, id string, a agentsdk.ConversationAuthority) error {
	if !k.config.DocumentManagement {
		return knowledgeFailure("document_management_unavailable")
	}
	_, err := connector.Call(ctx, knowledgeGateway{k, a}, httpapi.DeleteDocument, httpapi.DocumentInput{DocID: id})
	if err != nil {
		return knowledgeConnectorError(err)
	}
	return nil
}

var _ agentsdk.KnowledgeDocumentSource = (*Knowledge)(nil)
