package agenthost

import (
	"slices"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

func TestSharedBusinessPageScopeCannotUseVisibleFirstRowToAuthorizeHiddenTotal(t *testing.T) {
	h, resolver, _, producer := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, PageSize: 1}
	page, err := h.QueryBusinessRecords(t.Context(), q, producer)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "own-1" || page.Total == nil || *page.Total != 2 {
		t.Fatal(page, err)
	}
	e := sealedBusinessEvidence(t, h, producer, "query_records", q, page)
	principals, reader := bindSharedBusinessReader(h, resolver, producer)
	b := *principals.actual.AccessBundle
	b.DataPolicies = slices.Clone(b.DataPolicies)
	for i := range b.DataPolicies {
		if b.DataPolicies[i].Resource == "customer" && b.DataPolicies[i].Action == "read" {
			b.DataPolicies[i].DataScopes = []identity.DataScope{identity.DataScopeOwner}
			b.DataPolicies[i].Predicate = identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "own-1"}
		}
	}
	principals.actual.AccessBundle = &b
	if _, err := h.GetBusinessRecord(t.Context(), agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: page.Items[0].ID, Fields: q.Fields}, reader); err != nil {
		t.Fatal("fixture must permit saved first row", err)
	}
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
		t.Fatal("visible first row authorized producer total/cursor outside reader scope")
	}
	if businessReadScopeCovers(principals.actual, principals.original, "customer") {
		t.Fatal("different subject-bound restricted scopes considered equal")
	}
}

func TestSharedBusinessNegatedAndUnionScopeCoverageUsesBoundSubjects(t *testing.T) {
	h, resolver, _, producer := newConversationBusinessFixture(t)
	principals, _ := bindSharedBusinessReader(h, resolver, producer)
	p := principals.original
	atom := func(value string) identity.Predicate {
		return identity.Predicate{Fact: "owner_user_id", Operator: identity.OperatorEqual, Value: value}
	}
	set := func(values ...string) identity.Predicate {
		return identity.Predicate{Fact: "owner_user_id", Operator: identity.OperatorIn, Value: values}
	}
	not := func(value identity.Predicate) identity.Predicate { return identity.Predicate{Not: &value} }
	union := func(values ...identity.Predicate) identity.Predicate { return identity.Predicate{Any: values} }
	exclude := func(values ...string) identity.Predicate {
		return identity.Predicate{Fact: "owner_user_id", Operator: identity.OperatorNotIn, Value: values}
	}
	unequal := func(value string) identity.Predicate {
		return identity.Predicate{Fact: "owner_user_id", Operator: identity.OperatorNotEqual, Value: value}
	}
	for _, test := range []struct {
		name          string
		broad, narrow identity.Predicate
		want          bool
	}{
		{"negated excluded subset", not(set("a")), not(set("a", "b")), true},
		{"negated reverse denied", not(set("a", "b")), not(set("a")), false},
		{"union covers set", union(atom("a"), atom("b"), atom("c")), set("a", "b"), true},
		{"union cannot cover unseen value", union(atom("a"), atom("b")), set("a", "c"), false},
		{"negated set covers union", not(set("a", "b")), not(union(atom("a"), atom("b"), atom("c"))), true},
		{"negated union covers set", not(union(atom("a"), atom("b"))), not(set("a", "b", "c")), true},
		{"native exclusions widen", exclude("a"), exclude("a", "b"), true},
		{"native exclusions cannot widen in reverse", exclude("a", "b"), exclude("a"), false},
		{"native unequal covers exclusions", unequal("a"), exclude("a", "b"), true},
		{"native exclusions cover explicit negation", exclude("a"), not(set("a", "b")), true},
		{"explicit negation covers native exclusions", not(set("a")), exclude("a", "b"), true},
		{"native unequal cannot hide extra allowed value", unequal("a"), unequal("b"), false},
		{"mixed typed conjunction does not become empty", atom("other"), identity.Predicate{All: []identity.Predicate{{Fact: "owner_user_id", Operator: identity.OperatorEqual, Value: 1}, atom("1")}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := businessPredicateCovers(test.broad, p, test.narrow, p, 0); got != test.want {
				t.Fatalf("scope coverage=%v want=%v", got, test.want)
			}
			if !test.want {
				return
			}
			// Check the claimed implication using the source SDK evaluator, rather
			// than duplicating the implementation's set-comparison rules.
			for _, value := range []any{"a", "b", "c", "other", "1", 1, nil} {
				facts := identity.ResourceFacts{"owner_user_id": value}
				ctx := recordpolicy.RecordSDKEvaluationContext(p)
				broad, err := evaluator.EvaluatePredicateWithContext(test.broad, facts, ctx)
				if err != nil {
					t.Fatal(err)
				}
				narrow, err := evaluator.EvaluatePredicateWithContext(test.narrow, facts, ctx)
				if err != nil || narrow && !broad {
					t.Fatalf("SDK contradicts claimed coverage for %v: broad=%v narrow=%v error=%v", value, broad, narrow, err)
				}
			}
		})
	}
	if businessPredicateCovers(not(atom("$subject.id")), principals.actual, not(atom("$subject.id")), p, 0) {
		t.Fatal("different actual subjects acquired identical negated ownership")
	}
	if businessPredicateCovers(unequal("$subject.id"), principals.actual, unequal("$subject.id"), p, 0) {
		t.Fatal("different actual subjects acquired identical native unequal ownership")
	}
}

func TestSharedBusinessScopeCoversCommonAndOrAndKeepsReaderDenials(t *testing.T) {
	h, resolver, _, producer := newConversationBusinessFixture(t)
	principals, _ := bindSharedBusinessReader(h, resolver, producer)
	a, b := principals.actual, principals.original
	if !businessReadScopeCovers(a, b, "customer") {
		t.Fatal("unrestricted reader should cover original owner scope")
	}
	read := *a.AccessBundle
	read.Guardrails = []identity.Guardrail{{Key: "hide-owner", Resource: "customer", Action: "read", Effect: identity.EffectDeny, Predicate: &identity.Predicate{Fact: "owner_user_id", Operator: identity.OperatorEqual, Value: producer.UserID}}}
	a.AccessBundle = &read
	if businessReadScopeCovers(a, b, "customer") {
		t.Fatal("unrestricted allow swallowed reader denial")
	}
	atom := identity.Predicate{Fact: "owner_user_id", Operator: identity.OperatorEqual, Value: producer.UserID}
	other := identity.Predicate{Fact: "owner_user_id", Operator: identity.OperatorEqual, Value: "other"}
	and := identity.Predicate{All: []identity.Predicate{atom, {Fact: "id", Operator: identity.OperatorEqual, Value: "own-1"}}}
	or := identity.Predicate{Any: []identity.Predicate{atom, other}}
	group := identity.Predicate{Fact: "owner_user_id", Operator: identity.OperatorIn, Value: []string{producer.UserID, "other"}}
	for _, pair := range [][2]identity.Predicate{{atom, and}, {or, atom}, {or, and}, {group, atom}, {group, and}} {
		if !businessPredicateCovers(pair[0], b, pair[1], b, 0) || businessPredicateCovers(pair[1], b, pair[0], b, 0) {
			t.Fatal("scope implication direction was reversed", pair)
		}
	}
}

func TestSharedBusinessNegatedScopesReadOriginalORMPagesWithoutForeignCursorAuthority(t *testing.T) {
	for _, test := range []struct {
		name                         string
		nativeProducer, nativeReader bool
	}{
		{"explicit negations", false, false},
		{"native exclusions", true, true},
		{"native producer and explicit reader", true, false},
		{"explicit producer and native reader", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			exerciseSharedBusinessNegatedORMPages(t, test.nativeProducer, test.nativeReader)
		})
	}
}

func exerciseSharedBusinessNegatedORMPages(t *testing.T, nativeProducer, nativeReader bool) {
	t.Helper()
	h, resolver, reads, producer := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	principals, reader := bindSharedBusinessReader(h, resolver, producer)
	denyOutside := func(native bool, ids ...string) identity.Guardrail {
		inside := identity.Predicate{Fact: "id", Operator: identity.OperatorIn, Value: ids}
		outside := identity.Predicate{Not: &inside}
		if native {
			outside = identity.Predicate{Fact: "id", Operator: identity.OperatorNotIn, Value: ids}
		}
		return identity.Guardrail{Key: "outside-approved-records", Resource: "customer", Action: "read", Effect: identity.EffectDeny, Predicate: &outside}
	}
	original := *principals.original.AccessBundle
	original.Guardrails = []identity.Guardrail{denyOutside(nativeProducer, "own-1", "own-2")}
	principals.original.AccessBundle = &original
	actual := *principals.actual.AccessBundle
	actual.Guardrails = []identity.Guardrail{denyOutside(nativeReader, "own-1", "own-2", "other-owner")}
	principals.actual.AccessBundle = &actual
	q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "amount"}, PageSize: 1, Sort: []agent.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}
	first, err := h.QueryBusinessRecords(t.Context(), q, producer)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "own-1" || first.Total == nil || *first.Total != 2 || first.NextCursor == "" {
		t.Fatal("original ORM page not scoped to two owned records", first, err)
	}
	firstEvidence := sealedBusinessEvidence(t, h, producer, "query_records", q, first)
	q.Cursor = first.NextCursor
	next, err := h.QueryBusinessRecords(t.Context(), q, producer)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != "own-2" || next.Total != nil {
		t.Fatal("original ORM cursor did not produce the second record", next, err)
	}
	nextEvidence := sealedBusinessEvidence(t, h, producer, "query_records", q, next)
	before := reads.queries
	if _, err := h.QueryBusinessRecords(t.Context(), q, reader); err == nil || reads.queries != before {
		t.Fatal("actual reader gained authority to execute an original producer cursor", err)
	}
	for _, evidence := range []agent.ConversationBusinessEvidence{firstEvidence, nextEvidence} {
		if err := h.AuthorizeSharedBusinessResultRead(t.Context(), evidence, reader, producer); err != nil {
			t.Fatal("broader actual reader could not read an exact original ORM page", err)
		}
	}
	actual.Guardrails = []identity.Guardrail{denyOutside(nativeReader, "own-1")}
	if _, err := h.GetBusinessRecord(t.Context(), agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"name"}}, reader); err != nil {
		t.Fatal("narrower reader must still be able to read the saved first row", err)
	}
	for _, evidence := range []agent.ConversationBusinessEvidence{firstEvidence, nextEvidence} {
		if err := h.AuthorizeSharedBusinessResultRead(t.Context(), evidence, reader, producer); err == nil {
			t.Fatal("negated narrower current scope revealed an original total or unseen page")
		}
	}
}
