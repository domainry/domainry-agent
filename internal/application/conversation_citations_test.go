package application

import (
	"encoding/json"
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

func TestBusinessResultCitationsBindTheExactReturnedRecord(t *testing.T) {
	record := agentsdk.ConversationBusinessRecord{
		ID: "opportunity-1", Version: "v3",
		Data: map[string]json.RawMessage{"title": json.RawMessage(`"续约商机"`), "risk": json.RawMessage(`"high"`)},
	}
	citations := businessResultCitations("aurora-runtime", "get_record", "opportunity", record)
	if len(citations) != 1 || citations[0].ID == "" || citations[0].Source != "aurora-runtime" || citations[0].Operation != "get_record" || citations[0].ObjectKey != "opportunity" || citations[0].RecordID != record.ID || citations[0].Title != "续约商机" || citations[0].Excerpt == "" {
		t.Fatalf("business citations=%#v", citations)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := businessEvidenceCitations(agentsdk.ConversationBusinessEvidence{
		Source: "aurora-runtime", Operation: "get_record",
		Input: json.RawMessage(`{"object_key":"opportunity","record_id":"opportunity-1"}`), Data: data,
	})
	if err != nil || !businessCitationsEqual(citations, rebuilt) {
		t.Fatalf("rebuilt citations=%#v err=%v", rebuilt, err)
	}
	record.Data["risk"] = json.RawMessage(`"low"`)
	changed := businessResultCitations("aurora-runtime", "get_record", "opportunity", record)
	if len(changed) != 1 || changed[0].ID == citations[0].ID {
		t.Fatalf("citation ID did not bind returned data: before=%#v after=%#v", citations, changed)
	}
}
