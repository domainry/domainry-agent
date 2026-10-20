package application

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationCitationsRequireAnExactCompletedResultMarker(t *testing.T) {
	citation := agentsdk.ConversationCitation{ID: "crm-activity-1-v3", Source: "aurora_crm", Operation: "crm_search_customer_memory", ObjectKey: "activity", RecordID: "activity-1", Excerpt: "客户担心迁移窗口"}
	run := agentsdk.ConversationRun{Steps: []agentsdk.ConversationStepView{{Calls: []agentsdk.ConversationToolView{{Status: "completed", Citations: []agentsdk.ConversationCitation{citation}}}}}}
	if !conversationHasCitableEvidence(run) {
		t.Fatal("completed source-backed result was not recognized")
	}
	if got := conversationCitations(run, "客户担心迁移窗口"); len(got) != 0 {
		t.Fatalf("uncited answer projected citations: %#v", got)
	}
	if err := requireConversationAnswerCitation(run, "客户担心迁移窗口"); conversationModelFailureCode(err, "") != "answer_citation_required" {
		t.Fatalf("uncited source-backed answer error=%v", err)
	}
	got := conversationCitations(run, "客户担心迁移窗口。[[cite:crm-activity-1-v3]]")
	if len(got) != 1 || got[0].ID != citation.ID || got[0].RecordID != citation.RecordID {
		t.Fatalf("exact cited answer=%#v", got)
	}
	if err := requireConversationAnswerCitation(run, "客户担心迁移窗口。[[cite:crm-activity-1-v3]]"); err != nil {
		t.Fatalf("valid cited answer rejected: %v", err)
	}
	run.Steps[0].Calls[0].Status = "failed"
	if conversationHasCitableEvidence(run) {
		t.Fatal("failed result was promoted to citable evidence")
	}
}
