package agenthost

import (
	"encoding/json"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestSharedBusinessReverseRelationBoundsOriginalPageScope(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		t.Run(map[bool]string{false: "relationship only", true: "relationship and explicit filter"}[filtered], func(t *testing.T) {
			h, resolver, reads, producer := newBusinessRelationFixture(t)
			enableBusinessEvidence(t, h)
			principals, reader := bindSharedBusinessReader(h, resolver, producer)
			predicate := identity.Predicate{Fact: "customer", Operator: identity.OperatorEqual, Value: "own-1"}
			q := agent.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: "own-1", RelationKey: "reverse:order:customer", Fields: []string{"name", "amount"}, PageSize: 1}
			if filtered {
				set := identity.Predicate{Fact: "name", Operator: identity.OperatorIn, Value: []string{"订单甲", "订单乙"}}
				predicate = identity.Predicate{All: []identity.Predicate{predicate, set}}
				q.Filters = []agent.ConversationBusinessFilter{{Field: "name", Operator: "in", Value: json.RawMessage(`["订单甲","订单乙"]`)}}
			}
			principals.actual = businessQueryScopePrincipal(principals.actual, "order", predicate, false)
			first, err := h.QueryRelatedBusinessRecords(t.Context(), q, producer)
			if err != nil || len(first.Items) != 1 || first.Total == nil || *first.Total != 2 || first.NextCursor == "" {
				t.Fatal("original relationship first page", first, err)
			}
			firstEvidence := sealedBusinessEvidence(t, h, producer, "query_related_records", q, first)
			q.Cursor = first.NextCursor
			next, err := h.QueryRelatedBusinessRecords(t.Context(), q, producer)
			if err != nil || len(next.Items) != 1 || next.Items[0].ID != "o2" || next.Total != nil {
				t.Fatal("original relationship continuation", next, err)
			}
			nextEvidence := sealedBusinessEvidence(t, h, producer, "query_related_records", q, next)
			before := reads.queries
			if _, err := h.QueryRelatedBusinessRecords(t.Context(), q, reader); err == nil || reads.queries != before {
				t.Fatal("relationship scope gave reader a producer cursor", err)
			}
			evidence := []agent.ConversationBusinessEvidence{firstEvidence, nextEvidence}
			originalBytes, _ := json.Marshal(evidence)
			for _, e := range evidence {
				if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
					t.Fatal("reader of the exact relationship denied original page", err)
				}
			}
			principals.actual = businessQueryScopePrincipal(principals.actual, "order", identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "o1"}, false)
			if _, err := h.GetBusinessRecord(t.Context(), agent.ConversationBusinessGet{ObjectKey: "order", RecordID: "o1", Fields: []string{"name"}}, reader); err != nil {
				t.Fatal("narrow reader must still see the first record", err)
			}
			for _, e := range evidence {
				if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
					t.Fatal("one visible child exposed the entire historical count or continuation")
				}
			}
			principals.actual = businessQueryScopePrincipal(principals.actual, "order", predicate, false)
			for _, object := range h.objects(t.Context(), principals.original) {
				if object.Key == "order" {
					mutateBusinessEvidenceRow(t, reads, producer, object, "o2", func(r *recordmodel.Record) { r.Data["customer"] = "own-2" })
				}
			}
			if err := h.AuthorizeSharedBusinessResultRead(t.Context(), nextEvidence, reader, producer); err == nil {
				t.Fatal("historical relationship replaced the saved record's current row policy")
			}
			afterBytes, _ := json.Marshal(evidence)
			if string(originalBytes) != string(afterBytes) {
				t.Fatal("relationship reading changed the saved evidence")
			}
		})
	}
}

func TestSharedBusinessForwardRelationUsesAuthenticatedHistoricalTarget(t *testing.T) {
	h, resolver, reads, producer := newBusinessRelationFixture(t)
	enableBusinessEvidence(t, h)
	principals, reader := bindSharedBusinessReader(h, resolver, producer)
	principals.actual = businessQueryScopePrincipal(principals.actual, "customer", identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "own-1"}, false)
	q := agent.ConversationBusinessRelatedQuery{ObjectKey: "order", RecordID: "o1", RelationKey: "forward:customer", Fields: []string{"name"}, PageSize: 1}
	page, err := h.QueryRelatedBusinessRecords(t.Context(), q, producer)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "own-1" || page.Total == nil || *page.Total != 1 || page.HasNext {
		t.Fatal("original unique forward target", page, err)
	}
	e := sealedBusinessEvidence(t, h, producer, "query_related_records", q, page)
	originalBytes, _ := json.Marshal(e)
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
		t.Fatal("reader of the exact forward target denied original result", err)
	}
	for _, object := range h.objects(t.Context(), principals.original) {
		if object.Key == "order" {
			mutateBusinessEvidenceRow(t, reads, producer, object, "o1", func(r *recordmodel.Record) { r.Data["customer"] = "own-2" })
		}
	}
	if err := h.RevalidateBusiness(t.Context(), e, producer); err != nil {
		t.Fatal("producer must retain the exact historical target after changing its relationship", err)
	}
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
		t.Fatal("today's different forward target replaced the authenticated original target", err)
	}
	principals.actual = businessQueryScopePrincipal(principals.actual, "customer", identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "own-2"}, false)
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
		t.Fatal("permission for today's target exposed the historical different target")
	}
	principals.actual = businessQueryScopePrincipal(principals.actual, "customer", identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "own-1"}, false)
	forged := e
	page.Items[0].ID = "own-2"
	forged.Data, _ = json.Marshal(page)
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), forged, reader, producer); err == nil {
		t.Fatal("unauthenticated saved target gained a relationship constraint")
	}
	afterBytes, _ := json.Marshal(e)
	if string(originalBytes) != string(afterBytes) {
		t.Fatal("forward reading rewrote the original evidence")
	}
}

func TestSharedBusinessEmptyForwardResultCannotGuessOriginalTarget(t *testing.T) {
	h, resolver, _, producer := newBusinessRelationFixture(t)
	enableBusinessEvidence(t, h)
	principals, reader := bindSharedBusinessReader(h, resolver, producer)
	q := agent.ConversationBusinessRelatedQuery{ObjectKey: "order", RecordID: "unset", RelationKey: "forward:customer", Fields: []string{"name"}}
	page, err := h.QueryRelatedBusinessRecords(t.Context(), q, producer)
	if err != nil || len(page.Items) != 0 || page.Total == nil || *page.Total != 0 {
		t.Fatal("original empty relationship", page, err)
	}
	e := sealedBusinessEvidence(t, h, producer, "query_related_records", q, page)
	principals.actual = businessQueryScopePrincipal(principals.actual, "customer", identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "own-1"}, false)
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
		t.Fatal("an empty historical result invented its original forward target")
	}
}
