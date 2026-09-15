package agenthost

import (
	"encoding/json"
	"slices"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	appschema "github.com/domainry/domainry-runtime/runtime/application/appschema"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilemodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func businessQueryScopePrincipal(p principalmodel.Principal, object string, predicate identity.Predicate, all bool) principalmodel.Principal {
	b := *p.AccessBundle
	b.DataPolicies = slices.Clone(b.DataPolicies)
	for i := range b.DataPolicies {
		if b.DataPolicies[i].Resource == identity.ResourceType(object) && b.DataPolicies[i].Action == "read" {
			b.DataPolicies[i].DataScopes = []identity.DataScope{identity.DataScopeOwner}
			if all {
				b.DataPolicies[i].DataScopes = []identity.DataScope{identity.DataScopeAll}
			}
			b.DataPolicies[i].Predicate = predicate
		}
	}
	p.AccessBundle = &b
	return p
}

func TestSharedBusinessExactQueryFiltersLimitOriginalPageScope(t *testing.T) {
	set := identity.Predicate{Fact: "name", Operator: identity.OperatorIn, Value: []string{"Alpha", "Beta"}}
	filter := func(field, op, value string) agent.ConversationBusinessFilter {
		return agent.ConversationBusinessFilter{Field: field, Operator: op, Value: json.RawMessage(value)}
	}
	for _, tc := range []struct {
		name                   string
		filters                []agent.ConversationBusinessFilter
		reader                 identity.Predicate
		producerAll, readerAll bool
		setup                  string
	}{
		{"finite query", []agent.ConversationBusinessFilter{filter("name", "in", `["Alpha","Beta"]`)}, set, false, false, ""},
		{"mixed policy and query", []agent.ConversationBusinessFilter{filter("name", "in", `["Alpha","Beta"]`)}, identity.Predicate{All: []identity.Predicate{{Fact: "owner_user_id", Operator: identity.OperatorEqual, Value: "operator"}, set}}, false, false, ""},
		{"unrestricted producer filtered query", []agent.ConversationBusinessFilter{filter("name", "in", `["Alpha","Beta"]`)}, set, true, false, ""},
		{"exact big integer", []agent.ConversationBusinessFilter{filter("amount", "eq", `9007199254740993`)}, identity.Predicate{Fact: "amount", Operator: identity.OperatorEqual, Value: int64(9007199254740993)}, false, false, ""},
		{"normalized integer string", []agent.ConversationBusinessFilter{filter("amount", "eq", `"9007199254740993"`)}, identity.Predicate{Fact: "amount", Operator: identity.OperatorEqual, Value: int64(9007199254740993)}, false, false, ""},
		{"query exclusion", []agent.ConversationBusinessFilter{filter("name", "not_in", `["Gamma","Hidden"]`)}, identity.Predicate{Fact: "name", Operator: identity.OperatorNotIn, Value: []string{"Gamma"}}, false, false, ""},
		{"null query", []agent.ConversationBusinessFilter{filter("name", "is_null", `null`)}, identity.Predicate{Fact: "name", Operator: identity.OperatorExists, Value: false}, false, false, "null names"},
		{"range implies nonnull", []agent.ConversationBusinessFilter{filter("amount", "gt", `10`)}, identity.Predicate{Fact: "amount", Operator: identity.OperatorExists, Value: true}, false, false, "null amount"},
		{"literal substring implies nonnull", []agent.ConversationBusinessFilter{filter("name", "contains", `"%_"`)}, identity.Predicate{Fact: "name", Operator: identity.OperatorExists, Value: true}, false, false, "literal substring"},
		{"query excludes reader denial", []agent.ConversationBusinessFilter{filter("name", "not_in", `["Gamma"]`)}, identity.Predicate{}, false, true, "reader denial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, resolver, reads, producer := newConversationBusinessFixture(t)
			enableBusinessEvidence(t, h)
			principals, reader := bindSharedBusinessReader(h, resolver, producer)
			third := map[string]any{"name": "Gamma", "amount": int64(9007199254740992)}
			if tc.setup == "null amount" {
				third["amount"] = nil
			}
			if tc.setup == "literal substring" {
				third["name"] = nil
			}
			if err := reads.repository.InsertRecord(t.Context(), producer.WorkspaceID, reads.object, recordmodel.Record{ID: "own-3", OwnerUserID: producer.UserID, Data: third}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"own-1", "own-2"} {
				if tc.setup == "null names" || tc.setup == "literal substring" {
					mutateBusinessEvidenceRow(t, reads, producer, reads.object, id, func(r *recordmodel.Record) {
						if tc.setup == "null names" {
							r.Data["name"] = nil
						} else {
							r.Data["name"] = "Scope%_:" + id
						}
					})
				}
			}
			if tc.producerAll {
				principals.original = businessQueryScopePrincipal(principals.original, "customer", identity.Predicate{}, true)
			}
			principals.actual = businessQueryScopePrincipal(principals.actual, "customer", tc.reader, tc.readerAll)
			if tc.setup == "reader denial" {
				p := identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: "Gamma"}
				principals.actual.AccessBundle.Guardrails = []identity.Guardrail{{Key: "hide-gamma", Resource: "customer", Action: "read", Effect: identity.EffectDeny, Predicate: &p}}
			}
			q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "amount"}, Filters: tc.filters, PageSize: 1}
			first, err := h.QueryBusinessRecords(t.Context(), q, producer)
			if err != nil || len(first.Items) != 1 || first.Items[0].ID != "own-1" || first.Total == nil || *first.Total != 2 || first.NextCursor == "" {
				t.Fatal("original filtered first page", first, err)
			}
			firstEvidence := sealedBusinessEvidence(t, h, producer, "query_records", q, first)
			q.Cursor = first.NextCursor
			next, err := h.QueryBusinessRecords(t.Context(), q, producer)
			if err != nil || len(next.Items) != 1 || next.Items[0].ID != "own-2" || next.Total != nil {
				t.Fatal("original filtered second page", next, err)
			}
			nextEvidence := sealedBusinessEvidence(t, h, producer, "query_records", q, next)
			before := reads.queries
			if _, err := h.QueryBusinessRecords(t.Context(), q, reader); err == nil || reads.queries != before {
				t.Fatal("original cursor gave reader fresh query authority", err)
			}
			evidence := []agent.ConversationBusinessEvidence{firstEvidence, nextEvidence}
			originalBytes, _ := json.Marshal(evidence)
			for _, e := range evidence {
				if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
					t.Fatal("filtered reader denied original page", err)
				}
			}
			principals.actual = businessQueryScopePrincipal(principals.actual, "customer", identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "own-1"}, false)
			if _, err := h.GetBusinessRecord(t.Context(), agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"amount"}}, reader); err != nil {
				t.Fatal("narrow reader must see first row", err)
			}
			for _, e := range evidence {
				if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
					t.Fatal("filtered scope exposed total or second page after narrowing")
				}
			}
			afterBytes, _ := json.Marshal(evidence)
			if string(afterBytes) != string(originalBytes) {
				t.Fatal("reading rewrote original evidence")
			}
		})
	}
}

func TestSharedBusinessRelatedQueryFiltersUseTargetObjectScope(t *testing.T) {
	h, resolver, reads, producer := newBusinessRelationFixture(t)
	enableBusinessEvidence(t, h)
	principals, reader := bindSharedBusinessReader(h, resolver, producer)
	principals.actual = businessQueryScopePrincipal(principals.actual, "order", identity.Predicate{Fact: "name", Operator: identity.OperatorIn, Value: []string{"订单甲", "订单乙"}}, false)
	q := agent.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: "own-1", RelationKey: "reverse:order:customer", Fields: []string{"name", "amount"}, PageSize: 1, Filters: []agent.ConversationBusinessFilter{{Field: "name", Operator: "in", Value: json.RawMessage(`["订单甲","订单乙"]`)}}}
	first, err := h.QueryRelatedBusinessRecords(t.Context(), q, producer)
	if err != nil || len(first.Items) != 1 || first.Total == nil || *first.Total != 2 || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	firstEvidence := sealedBusinessEvidence(t, h, producer, "query_related_records", q, first)
	q.Cursor = first.NextCursor
	next, err := h.QueryRelatedBusinessRecords(t.Context(), q, producer)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != "o2" {
		t.Fatal(next, err)
	}
	nextEvidence := sealedBusinessEvidence(t, h, producer, "query_related_records", q, next)
	before := reads.queries
	if _, err := h.QueryRelatedBusinessRecords(t.Context(), q, reader); err == nil || reads.queries != before {
		t.Fatal("reader executed producer relation cursor", err)
	}
	for _, e := range []agent.ConversationBusinessEvidence{firstEvidence, nextEvidence} {
		if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
			t.Fatal("target-object filtered scope denied original related page", err)
		}
	}
	principals.actual = businessQueryScopePrincipal(principals.actual, "order", identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "o1"}, false)
	for _, e := range []agent.ConversationBusinessEvidence{firstEvidence, nextEvidence} {
		if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
			t.Fatal("narrow target scope exposed relation total/second page")
		}
	}
}

func TestSharedBusinessQueryScopePreservesLiteralClaimsAndExactInteger(t *testing.T) {
	h, resolver, _, producer := newConversationBusinessFixture(t)
	principals, _ := bindSharedBusinessReader(h, resolver, producer)
	for _, literal := range []string{"$subject.id", "$context.query_literal_0", " Alpha "} {
		queryJSON, _ := json.Marshal(literal)
		filters := []agent.ConversationBusinessFilter{{Field: "name", Operator: "eq", Value: queryJSON}}
		actual := businessQueryScopePrincipal(principals.actual, "customer", identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: "Alpha"}, false)
		if literal == "$subject.id" {
			actual = businessQueryScopePrincipal(actual, "customer", identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: "$subject.id"}, false)
		}
		actual.BusinessClaims = map[string]profilemodel.ClaimValue{"query_literal_0": {Value: "Alpha"}, "literal_policy": {Value: literal}}
		original := principals.original
		original.BusinessClaims = map[string]profilemodel.ClaimValue{"query_literal_0": {Value: "Alpha"}}
		before, _ := json.Marshal([]principalmodel.Principal{actual, original})
		if h.businessReadQueryScopeCovers(t.Context(), actual, original, "customer", filters) {
			t.Fatal("literal query acquired bound identity/claim or trimmed-text scope", literal)
		}
		after, _ := json.Marshal([]principalmodel.Principal{actual, original})
		if string(before) != string(after) {
			t.Fatal("scope proof changed actual identity/claims")
		}
		actual = businessQueryScopePrincipal(actual, "customer", identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: "$context.literal_policy"}, false)
		if !h.businessReadQueryScopeCovers(t.Context(), actual, original, "customer", filters) {
			t.Fatal("exact literal policy could not cover the literal query", literal)
		}
	}
	actual := businessQueryScopePrincipal(principals.actual, "customer", identity.Predicate{Fact: "amount", Operator: identity.OperatorEqual, Value: float64(9007199254740992)}, false)
	if h.businessReadQueryScopeCovers(t.Context(), actual, principals.original, "customer", []agent.ConversationBusinessFilter{{Field: "amount", Operator: "eq", Value: json.RawMessage(`9007199254740993`)}}) {
		t.Fatal("rounded float authorized the adjacent exact integer")
	}
	query := &businessQueryScope{predicate: identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: "Alpha"}, principal: principalmodel.Principal{}}
	bad := identity.Predicate{All: []identity.Predicate{{Fact: "name", Operator: identity.OperatorEqual, Value: "Alpha"}, {Fact: "name", Operator: identity.Operator("gt"), Value: 1}}}
	if businessPredicateCoversQuery(bad, actual, nil, principals.original, query, 0) {
		t.Fatal("untranslated parent/child scope gained coverage through the query")
	}
}

func TestSharedBusinessQueryScopeDoesNotTreatRawDecimalAsDatabaseComparison(t *testing.T) {
	h, resolver, reads, producer := newConversationBusinessFixture(t)
	principals, _ := bindSharedBusinessReader(h, resolver, producer)
	object := reads.object
	object.Fields = slices.Clone(object.Fields)
	for i := range object.Fields {
		if object.Fields[i].Key == "amount" {
			object.Fields[i].Type = "currency"
		}
	}
	// This is a metadata projection proof test, not a currency storage test.
	// The proof must avoid database IO and raw equality even when the SDK
	// predicate and normalized decimal text appear identical.
	h.schema = appschema.NewApplicationSchemaQueryApplicationService(businessSchemaSnapshot{appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{object}}}, nil)
	q := []agent.ConversationBusinessFilter{{Field: "amount", Operator: "eq", Value: json.RawMessage(`"1.00"`)}}
	actual := businessQueryScopePrincipal(principals.actual, "customer", identity.Predicate{Fact: "amount", Operator: identity.OperatorEqual, Value: "1.00"}, false)
	before := reads.queries
	if h.businessReadQueryScopeCovers(t.Context(), actual, principals.original, "customer", q) {
		t.Fatal("raw exact-decimal text gained an encoded database equality proof")
	}
	actual = businessQueryScopePrincipal(actual, "customer", identity.Predicate{Fact: "amount", Operator: identity.OperatorExists, Value: true}, false)
	if !h.businessReadQueryScopeCovers(t.Context(), actual, principals.original, "customer", q) {
		t.Fatal("decimal comparison did not preserve its nonnull constraint")
	}
	if reads.queries != before {
		t.Fatal("literal proof context performed database IO")
	}
}
