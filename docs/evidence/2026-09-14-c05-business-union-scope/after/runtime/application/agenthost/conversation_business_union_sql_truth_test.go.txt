package agenthost

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilemodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestBusinessCombinedScopeProofPreservesActualSQLTruthAndBindings(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "scope.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE scope_rows (v TEXT COLLATE NOCASE, owner TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{nil, "Alpha", "alpha", "Abe", "Beta", "Gamma", "1", "$subject.id", "$context.fake"} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO scope_rows (v, owner) VALUES (?, ?)`, value, "operator"); err != nil {
			t.Fatal(err)
		}
	}
	h, resolver, _, producerAuthority := newConversationBusinessFixture(t)
	principals, _ := bindSharedBusinessReader(h, resolver, producerAuthority)
	producer, reader := principals.original, principals.actual
	reader.BusinessClaims = map[string]profilemodel.ClaimValue{"literal_a": {Value: "$subject.id"}, "literal_b": {Value: "$context.fake"}}
	eq := func(value any) identity.Predicate {
		return identity.Predicate{Fact: "v", Operator: identity.OperatorEqual, Value: value}
	}
	set := func(values ...any) identity.Predicate {
		return identity.Predicate{Fact: "v", Operator: identity.OperatorIn, Value: values}
	}
	and := func(values ...identity.Predicate) identity.Predicate { return identity.Predicate{All: values} }
	or := func(values ...identity.Predicate) identity.Predicate { return identity.Predicate{Any: values} }
	not := func(value identity.Predicate) identity.Predicate { return identity.Predicate{Not: &value} }
	missing := identity.Predicate{Fact: "v", Operator: identity.OperatorNotEqual, Value: "$context.absent"}
	null := identity.Predicate{Fact: "v", Operator: identity.OperatorExists, Value: false}
	owner := identity.Predicate{Fact: "owner", Operator: identity.OperatorEqual, Value: "operator"}
	subjectOwner := identity.Predicate{Fact: "owner", Operator: identity.OperatorEqual, Value: "$subject.id"}
	for _, tc := range []struct {
		name             string
		reader, producer identity.Predicate
		query            *businessQueryScope
		want             bool
	}{
		{"partitioned conjunctions", or(and(owner, eq("Alpha")), and(owner, eq("Beta"))), and(owner, set("Alpha", "Beta")), nil, true},
		{"actual subjects stay distinct", or(and(subjectOwner, eq("Alpha")), and(subjectOwner, eq("Beta"))), and(subjectOwner, set("Alpha", "Beta")), nil, false},
		{"NULL branch excluded by native neq", or(eq("Alpha"), identity.Predicate{Fact: "v", Operator: identity.OperatorNotEqual, Value: "Gamma"}), or(eq("Alpha"), null), nil, false},
		{"positive IN with NULL", eq("Alpha"), set("Alpha", nil), nil, true},
		{"reader IN with NULL cannot read NULL", set("Alpha", nil), or(eq("Alpha"), null), nil, false},
		{"NOT IN with NULL has no matches", eq("Alpha"), identity.Predicate{Fact: "v", Operator: identity.OperatorNotIn, Value: []any{"Beta", nil}}, nil, true},
		{"native missing neq stays FALSE", eq("Alpha"), and(owner, missing), nil, true},
		{"reader native missing neq grants nothing", missing, eq("Alpha"), nil, false},
		{"explicit NOT of missing neq is TRUE", not(missing), or(eq("Alpha"), null), nil, true},
		{"SQL affinity does not make conjunction empty", eq("other"), and(eq(1), eq("1")), nil, false},
		{"SQL collation does not make conjunction empty", eq("Beta"), and(eq("Alpha"), eq("alpha")), nil, false},
		{"exact bound contradiction has no matches", eq("Beta"), and(eq("Alpha"), not(eq("Alpha"))), nil, true},
		{"claim-looking list literals remain literals", or(eq("$context.literal_a"), eq("$context.literal_b")), set("$subject.id", "$context.fake"), nil, true},
		{"claim-looking scalar is resolved for actual reader", eq("$subject.id"), set("$subject.id", "$context.fake"), nil, false},
		{"query literals preserve producer ownership binding", or(and(owner, eq("$context.literal_a")), and(owner, eq("$context.literal_b"))), subjectOwner, &businessQueryScope{predicate: set("$subject.id", "$context.fake"), principal: principalmodel.Principal{}}, true},
		{"query literal and producer bindings stay separate", or(and(subjectOwner, eq("$context.literal_a")), and(subjectOwner, eq("$context.literal_b"))), owner, &businessQueryScope{predicate: set("$subject.id", "$context.fake"), principal: principalmodel.Principal{}}, false},
		{"double NOT and De Morgan", and(owner, not(and(not(not(not(eq("Alpha")))), not(eq("Beta"))))), and(owner, set("Alpha", "Beta")), nil, true},
		{"negated prefixes keep SQL NULL behavior", not(identity.Predicate{Fact: "v", Operator: identity.OperatorPrefix, Value: "Al"}), not(identity.Predicate{Fact: "v", Operator: identity.OperatorPrefix, Value: "A"}), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := evaluator.RecordFilter{Allow: []identity.Predicate{tc.reader}}
			b := evaluator.RecordFilter{Allow: []identity.Predicate{tc.producer}}
			if got := businessCombinedReadScopeCovers(reader, producer, a, b, tc.query); got != tc.want {
				t.Fatalf("scope coverage=%v want=%v", got, tc.want)
			}
			compile := func(filter evaluator.RecordFilter, principal principalmodel.Principal) evaluator.SQLFilter {
				compiled, err := evaluator.CompileSQLWithContext(filter, recordpolicy.RecordSDKEvaluationContext(principal), func(fact string) (string, bool) { return fact, fact == "v" || fact == "owner" })
				if err != nil {
					t.Fatal(err)
				}
				return compiled
			}
			source, current := compile(b, producer), compile(a, reader)
			clauses, args := []string{source.Clause}, append([]any(nil), source.Args...)
			if tc.query != nil {
				query := compile(evaluator.RecordFilter{Allow: []identity.Predicate{tc.query.predicate}}, tc.query.principal)
				clauses = append(clauses, query.Clause)
				args = append(args, query.Args...)
			}
			args = append(args, current.Args...)
			var excluded int
			// UNKNOWN in the reader scope must count as excluded as well as
			// FALSE. NOT(reader) alone would silently omit the NULL counterexample.
			statement := "SELECT COUNT(*) FROM scope_rows WHERE (" + strings.Join(clauses, ") AND (") + ") AND (" + current.Clause + ") IS NOT TRUE"
			if err := store.DB().QueryRowContext(t.Context(), statement, args...).Scan(&excluded); err != nil {
				t.Fatal(err)
			}
			if tc.want && excluded != 0 || !tc.want && excluded == 0 {
				t.Fatalf("actual SQLite excluded rows=%d coverage=%v", excluded, tc.want)
			}
		})
	}
}

func TestBusinessCombinedScopeProofRejectsUntranslatableAndUnboundedTrees(t *testing.T) {
	h, resolver, _, producer := newConversationBusinessFixture(t)
	principals, _ := bindSharedBusinessReader(h, resolver, producer)
	eq := identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: "Alpha"}
	for _, scenario := range []string{"unsupported branch", "relation branch", "clause expansion", "deep negation", "node budget", "comparison budget"} {
		t.Run(scenario, func(t *testing.T) {
			predicate := eq
			narrow := eq
			switch scenario {
			case "unsupported branch":
				predicate = identity.Predicate{Any: []identity.Predicate{eq, {Fact: "name", Operator: identity.OperatorContains, Value: "A"}}}
			case "relation branch":
				predicate = identity.Predicate{Any: []identity.Predicate{eq, {Fact: "name", Operator: identity.OperatorEqual, Value: "Alpha", Path: []identity.RelationSegment{{Direction: identity.RelationForward, Reference: "customer", TargetResource: "customer"}}}}}
			case "clause expansion":
				predicate = identity.Predicate{Any: make([]identity.Predicate, businessScopeMaxClauses+1)}
				for i := range predicate.Any {
					predicate.Any[i] = eq
				}
			case "deep negation":
				for i := 0; i < 26; i++ {
					child := predicate
					predicate = identity.Predicate{Not: &child}
				}
			case "node budget":
				predicate = identity.Predicate{All: make([]identity.Predicate, businessScopeMaxNodes+1)}
				for i := range predicate.All {
					predicate.All[i] = eq
				}
			case "comparison budget":
				predicate = identity.Predicate{All: make([]identity.Predicate, 64)}
				narrow = identity.Predicate{Any: make([]identity.Predicate, 32)}
				for i := range predicate.All {
					predicate.All[i] = identity.Predicate{Fact: fmt.Sprintf("fact_%d", i), Operator: identity.OperatorExists, Value: true}
				}
				for i := range narrow.Any {
					narrow.Any[i] = identity.Predicate{All: make([]identity.Predicate, 64)}
					for j := range narrow.Any[i].All {
						narrow.Any[i].All[j] = identity.Predicate{Fact: fmt.Sprintf("fact_%d", j), Operator: identity.OperatorEqual, Value: i}
					}
				}
			}
			if businessCombinedReadScopeCovers(principals.actual, principals.original, evaluator.RecordFilter{Allow: []identity.Predicate{predicate}}, evaluator.RecordFilter{Allow: []identity.Predicate{narrow}}, nil) {
				t.Fatal("incomplete/unsupported scope proof granted reading")
			}
		})
	}
}
