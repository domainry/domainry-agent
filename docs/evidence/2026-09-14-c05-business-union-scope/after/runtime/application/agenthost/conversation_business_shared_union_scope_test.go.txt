package agenthost

import (
	"encoding/json"
	"fmt"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func sharedBusinessAllowUnion(p principalmodel.Principal, predicates ...identity.Predicate) principalmodel.Principal {
	b := *p.AccessBundle
	b.DataPolicies = nil
	for _, policy := range p.AccessBundle.DataPolicies {
		if policy.Resource != "customer" || policy.Action != "read" {
			b.DataPolicies = append(b.DataPolicies, policy)
		}
	}
	for i, predicate := range predicates {
		b.DataPolicies = append(b.DataPolicies, identity.DataPolicy{Key: fmt.Sprintf("union-read-%d", i), Resource: "customer", Action: "read", Effect: identity.EffectAllow, DataScopes: []identity.DataScope{identity.DataScopeOwner}, Predicate: predicate})
	}
	p.AccessBundle = &b
	return p
}

func sharedBusinessDenyUnion(p principalmodel.Principal, predicates ...identity.Predicate) principalmodel.Principal {
	b := *p.AccessBundle
	b.Guardrails = nil
	for i, predicate := range predicates {
		b.Guardrails = append(b.Guardrails, identity.Guardrail{Key: fmt.Sprintf("union-deny-%d", i), Resource: "customer", Action: "read", Effect: identity.EffectDeny, Predicate: &predicate})
	}
	p.AccessBundle = &b
	return p
}

func TestSharedBusinessCombinedRulesCoverCompleteOriginalORMQuery(t *testing.T) {
	eq := func(value string) identity.Predicate {
		return identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: value}
	}
	set := func(values ...string) identity.Predicate {
		return identity.Predicate{Fact: "name", Operator: identity.OperatorIn, Value: values}
	}
	and := func(values ...identity.Predicate) identity.Predicate { return identity.Predicate{All: values} }
	not := func(value identity.Predicate) identity.Predicate { return identity.Predicate{Not: &value} }
	for _, scenario := range []string{"multiple allow policies", "query partitions across conjunctions", "producer denial narrows allow", "producer denial union", "double negation and De Morgan"} {
		t.Run(scenario, func(t *testing.T) {
			h, resolver, reads, producer := newConversationBusinessFixture(t)
			enableBusinessEvidence(t, h)
			principals, reader := bindSharedBusinessReader(h, resolver, producer)
			owner := identity.Predicate{Fact: "owner_user_id", Operator: identity.OperatorEqual, Value: producer.UserID}
			wantTotal := int64(2)
			switch scenario {
			case "multiple allow policies":
				principals.original = sharedBusinessReadPredicate(principals.original, and(owner, set("Alpha", "Beta")))
				principals.actual = sharedBusinessAllowUnion(principals.actual, eq("Alpha"), eq("Beta"))
			case "query partitions across conjunctions":
				principals.actual = sharedBusinessReadPredicate(principals.actual, identity.Predicate{Any: []identity.Predicate{and(owner, eq("Alpha")), and(owner, eq("Beta"))}})
			case "producer denial narrows allow":
				principals.original = sharedBusinessReadPredicate(principals.original, and(owner, set("Alpha", "Beta")))
				principals.original = sharedBusinessDenyUnion(principals.original, eq("Beta"))
				principals.actual = sharedBusinessReadPredicate(principals.actual, and(owner, eq("Alpha")))
				wantTotal = 1
			case "producer denial union":
				principals.original = sharedBusinessDenyUnion(principals.original, eq("Gamma"), eq("Delta"))
				principals.actual = sharedBusinessDenyUnion(sharedBusinessReadPredicate(principals.actual, owner), set("Gamma", "Delta"))
			case "double negation and De Morgan":
				principals.original = sharedBusinessReadPredicate(principals.original, and(owner, set("Alpha", "Beta")))
				principals.actual = sharedBusinessReadPredicate(principals.actual, and(owner, not(and(not(not(not(eq("Alpha")))), not(eq("Beta"))))))
			}
			q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "amount"}, PageSize: 1}
			if scenario == "query partitions across conjunctions" {
				q.Filters = []agent.ConversationBusinessFilter{{Field: "name", Operator: "in", Value: json.RawMessage(`["Alpha","Beta"]`)}}
			}
			first, err := h.QueryBusinessRecords(t.Context(), q, producer)
			if err != nil || len(first.Items) != 1 || first.Items[0].ID != "own-1" || first.Total == nil || int64(*first.Total) != wantTotal {
				t.Fatal("original ORM query", first, err)
			}
			evidence := []agent.ConversationBusinessEvidence{sealedBusinessEvidence(t, h, producer, "query_records", q, first)}
			if wantTotal == 2 {
				if first.NextCursor == "" {
					t.Fatal("original second page cursor missing")
				}
				q.Cursor = first.NextCursor
				next, err := h.QueryBusinessRecords(t.Context(), q, producer)
				if err != nil || len(next.Items) != 1 || next.Items[0].ID != "own-2" || next.Total != nil {
					t.Fatal("original second ORM page", next, err)
				}
				evidence = append(evidence, sealedBusinessEvidence(t, h, producer, "query_records", q, next))
			}
			originalBytes, _ := json.Marshal(evidence)
			for _, e := range evidence {
				if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
					t.Fatal("combined reader rules denied original complete scope", err)
				}
			}
			if q.Cursor != "" {
				before := reads.queries
				if _, err := h.QueryBusinessRecords(t.Context(), q, reader); err == nil || before != reads.queries {
					t.Fatal("original cursor authorized reader execution", err)
				}
			}
			restore := principals.actual
			principals.actual = sharedBusinessReadPredicate(principals.actual, identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "own-1"})
			if _, err := h.GetBusinessRecord(t.Context(), agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"name"}}, reader); err != nil {
				t.Fatal("reader lost the visible first row", err)
			}
			for _, e := range evidence {
				if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
					t.Fatal("first row visibility exposed original query scope after narrowing")
				}
			}
			principals.actual = restore
			for _, e := range evidence {
				if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
					t.Fatal("restored complete scope denied", err)
				}
			}
			afterBytes, _ := json.Marshal(evidence)
			if string(originalBytes) != string(afterBytes) {
				t.Fatal("scope proof changed original evidence bytes")
			}
		})
	}
}
