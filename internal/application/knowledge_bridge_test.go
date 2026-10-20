package application

import (
	"context"
	"testing"
	"time"

	knowledge "github.com/domainry/domainry-knowledge-sdk/contract"
)

type knowledgeBridgeSecurityScanner struct{}

func (*knowledgeBridgeSecurityScanner) ScanKnowledgeDocument(context.Context, knowledge.KnowledgeDocumentSecurityInput) (knowledge.KnowledgeDocumentSecurityResult, error) {
	return knowledge.KnowledgeDocumentSecurityResult{Clean: true, Scanner: "test.scanner", ReportSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
}

func TestKnowledgeOptionsForwardsDocumentSecurityScanner(t *testing.T) {
	scanner := &knowledgeBridgeSecurityScanner{}
	options := knowledgeOptions(ConversationOptions{DocumentSecurity: scanner, DocumentWorkTimeout: 75 * time.Minute, DocumentUploadIdleTimeout: 36 * time.Hour})
	if options.DocumentSecurity != scanner || options.DocumentWorkTimeout != 75*time.Minute || options.DocumentUploadIdleTimeout != 36*time.Hour {
		t.Fatal("knowledge document worker configuration was not forwarded")
	}
}
