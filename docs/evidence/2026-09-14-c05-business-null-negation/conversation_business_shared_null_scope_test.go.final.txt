package agenthost

import (
	"encoding/json"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestSharedBusinessSQLNullNegationMustNotCoverOriginalORMPage(t *testing.T) {
	for _, tc := range []struct {
		name              string
		native, guardrail bool
	}{
		{"explicit NOT", false, false}, {"native unequal", true, false},
		{"explicit deny", false, true}, {"native deny", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, resolver, reads, producer := newConversationBusinessFixture(t)
			enableBusinessEvidence(t, h)
			principals, reader := bindSharedBusinessReader(h, resolver, producer)
			if err := reads.repository.InsertRecord(t.Context(), producer.WorkspaceID, reads.object, recordmodel.Record{ID: "zz-null-name", OwnerUserID: producer.UserID, Data: map[string]any{"name": nil, "amount": int64(1)}}); err != nil {
				t.Fatal(err)
			}
			present := identity.Predicate{Fact: "name", Operator: identity.OperatorExists, Value: true}
			gamma := identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: "Gamma"}
			alpha := identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: "Alpha"}
			exclusion := identity.Predicate{Not: &gamma}
			notAlpha := identity.Predicate{Not: &alpha}
			if tc.native {
				exclusion = identity.Predicate{Fact: "name", Operator: identity.OperatorNotEqual, Value: "Gamma"}
				notAlpha = identity.Predicate{Fact: "name", Operator: identity.OperatorNotEqual, Value: "Alpha"}
			}
			if tc.guardrail {
				sourceDeny := identity.Predicate{All: []identity.Predicate{present, notAlpha}}
				readerDeny := identity.Predicate{All: []identity.Predicate{gamma, notAlpha}}
				principals.original.AccessBundle.Guardrails = []identity.Guardrail{{Key: "deny-other-nonnull", Resource: "customer", Action: "read", Effect: identity.EffectDeny, Predicate: &sourceDeny}}
				principals.actual.AccessBundle.Guardrails = []identity.Guardrail{{Key: "deny-gamma", Resource: "customer", Action: "read", Effect: identity.EffectDeny, Predicate: &readerDeny}}
			} else {
				principals.original = sharedBusinessReadPredicate(principals.original, identity.Predicate{Any: []identity.Predicate{alpha, {Not: &present}}})
				principals.actual = sharedBusinessReadPredicate(principals.actual, identity.Predicate{Any: []identity.Predicate{alpha, exclusion}})
			}
			q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "amount"}, PageSize: 1}
			original, err := h.QueryBusinessRecords(t.Context(), q, producer)
			if err != nil || len(original.Items) != 1 || original.Items[0].ID != "own-1" || original.Total == nil || *original.Total != 2 || original.NextCursor == "" {
				t.Fatal("actual producing Alpha and NULL scope", original, err)
			}
			readerQuery := q
			readerQuery.PageSize = 25
			current, err := h.QueryBusinessRecords(t.Context(), readerQuery, reader)
			if err != nil || len(current.Items) == 0 {
				t.Fatal("actual reader exclusion", current, err)
			}
			for _, r := range current.Items {
				if r.ID == "zz-null-name" {
					t.Fatal("SQL exclusion unexpectedly included NULL", current)
				}
			}
			if _, err := h.GetBusinessRecord(t.Context(), agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"name", "amount"}}, reader); err != nil {
				t.Fatal("reader must retain visible first row", err)
			}
			e := sealedBusinessEvidence(t, h, producer, "query_records", q, original)
			bytesBefore, _ := json.Marshal(e)
			if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
				t.Fatal("SQL NOT/deny coverage leaked original total containing NULL excluded from actual reader query")
			}
			principals.actual = businessQueryScopePrincipal(principals.actual, "customer", identity.Predicate{}, true)
			principals.actual.AccessBundle.Guardrails = nil
			if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
				t.Fatal("restored reader covering Alpha and NULL denied original page", err)
			}
			bytesAfter, _ := json.Marshal(e)
			if string(bytesBefore) != string(bytesAfter) {
				t.Fatal("scope proof rewrote original evidence")
			}
		})
	}
}
