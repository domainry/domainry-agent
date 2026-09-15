package reportmodulehost

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	identity "github.com/domainry/domainry-identity-sdk"
	ormschema "github.com/domainry/domainry-orm/schema"
	report "github.com/domainry/domainry-report-sdk/model"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definition "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principal "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	record "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestSharedReportSDKNegationAndNullScopesUseActualORMProjection(t *testing.T) {
	eq := func(value string) identity.Predicate {
		return identity.Predicate{Fact: "name", Operator: identity.OperatorEqual, Value: value}
	}
	not := func(p identity.Predicate) identity.Predicate { return identity.Predicate{Not: &p} }
	for _, tc := range []struct {
		name           string
		source, reader identity.Predicate
		guardrail      bool
		ids            []string
	}{
		{"identical neq", identity.Predicate{Fact: "name", Operator: identity.OperatorNotEqual, Value: "Gamma"}, not(eq("Gamma")), false, []string{"a", "b", "h"}},
		{"weaker exclusion", identity.Predicate{Fact: "name", Operator: identity.OperatorNotIn, Value: []string{"Gamma", "Hidden"}}, identity.Predicate{Fact: "name", Operator: identity.OperatorNotIn, Value: []string{"Gamma"}}, false, []string{"a", "b"}},
		{"composed reader exclusions", not(identity.Predicate{Any: []identity.Predicate{eq("Gamma"), eq("Hidden")}}), identity.Predicate{All: []identity.Predicate{not(eq("Gamma")), not(eq("Hidden"))}}, false, []string{"a", "b"}},
		{"double negation", not(not(eq("Alpha"))), eq("Alpha"), false, []string{"a"}},
		{"source and reader guardrails", identity.Predicate{Any: []identity.Predicate{eq("Gamma"), eq("Hidden")}}, eq("Gamma"), true, []string{"a", "b"}},
		{"identical exists", identity.Predicate{Fact: "name", Operator: identity.OperatorExists, Value: true}, identity.Predicate{Fact: "name", Operator: identity.OperatorExists, Value: true}, false, []string{"a", "b", "g", "h"}},
		{"identical not exists", identity.Predicate{Fact: "name", Operator: identity.OperatorExists, Value: false}, identity.Predicate{Fact: "name", Operator: identity.OperatorExists, Value: false}, false, []string{"n"}},
		{"negated not exists", not(identity.Predicate{Fact: "name", Operator: identity.OperatorExists, Value: false}), identity.Predicate{Fact: "name", Operator: identity.OperatorExists, Value: true}, false, []string{"a", "b", "g", "h"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "shared-report.db")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.EnsureApplicationSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			object := definition.ObjectSchema{Key: "sale", Name: "Sale", Fields: []definition.FieldSchema{{Key: "name", Type: "text"}}}
			columns := []ormschema.ColumnDefinition{}
			for _, key := range []string{"workspace_id", "id", "created_at", "updated_at", "owner_user_id", "owner_org_id"} {
				columns = append(columns, ormschema.Column(key, ormschema.TextKey(96)))
			}
			columns = append(columns, ormschema.Column("deleted", ormschema.Boolean()).DefaultValue(false), ormschema.Column("name", ormschema.LongText()))
			statement, _, err := ormschema.NewTable(store.SQLRenderer, object.Key).Columns(columns...).PrimaryKey("workspace_id", "id").Build()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			repository := recordstore.NewRecordStore(store)
			for _, row := range []struct {
				id   string
				name any
			}{{"a", "Alpha"}, {"b", "Beta"}, {"g", "Gamma"}, {"h", "Hidden"}, {"n", nil}} {
				if err := repository.InsertRecord(t.Context(), "workspace", object, record.Record{ID: row.id, OwnerUserID: "producer", Data: map[string]any{"name": row.name}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := repository.InsertRecord(t.Context(), "foreign-workspace", object, record.Record{ID: "foreign", OwnerUserID: "producer", Data: map[string]any{"name": "Alpha"}}); err != nil {
				t.Fatal(err)
			}
			policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definition.ObjectSchema { return []definition.ObjectSchema{object} }})
			objects := func() map[string]definition.ObjectSchema { return map[string]definition.ObjectSchema{"sale": object} }
			application := recordapplication.NewRecordApplicationService(recordapplication.RecordApplicationDependencies{Repository: repository, QueryPolicy: policy, SchemaMap: objects})
			adapter := NewReportRecordAdapter(application, repository, objects)
			h := NewReportModuleQueryHost(ReportModuleQueryHostDependencies{Access: adapter})
			makePrincipal := func(user string, predicate identity.Predicate) principal.Principal {
				p := accessfixture.Attach(principal.Principal{Principal: identity.Principal{Known: true, WorkspaceID: "workspace", UserID: user}}, accessfixture.Bundle{Key: "report-reader", Permissions: []string{"sale.read"}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{"sale.read"}, identity.DataScopeOwner), FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "sale", FieldKey: "name", Read: true}}})
				if tc.guardrail {
					p.AccessBundle.Guardrails = []identity.Guardrail{{Key: "exclude", Resource: "sale", Action: "read", Effect: identity.EffectDeny, Predicate: &predicate}}
					if user == "reader" {
						p.AccessBundle.DataPolicies[0].DataScopes = []identity.DataScope{identity.DataScopeAll}
						p.AccessBundle.DataPolicies[0].Predicate = identity.Predicate{}
					}
				} else {
					p.AccessBundle.DataPolicies[0].Predicate = predicate
				}
				if err := p.AccessBundle.Validate(time.Now()); err != nil {
					t.Fatal("invalid SDK scope fixture", user, err)
				}
				return p
			}
			source, reader := makePrincipal("producer", tc.source), makePrincipal("reader", tc.reader)
			original, err := adapter.ListReportRecordsForPrincipal(t.Context(), "sale", record.RecordListQuery{Page: 1, PageSize: 10, Sort: []record.RecordSortRule{{Field: "id", Direction: "asc"}}}, source)
			if err != nil {
				t.Fatal("real source query", err)
			}
			ids := make([]string, 0, len(original.Items))
			for _, row := range original.Items {
				ids = append(ids, row.ID)
			}
			if !slices.Equal(ids, tc.ids) || original.Total != len(tc.ids) {
				t.Fatal("real SQL scope/NULL/workspace semantics", ids, original.Total, tc.ids)
			}
			r := report.ReportSchema{ObjectSQLV1: &report.ReportObjectSQLSchema{SQL: `SELECT s.name AS name FROM sale s LIMIT 10`, ResultSchema: []report.ReportResultColumnSchema{{Key: "name", Type: "text", Kind: "dimension"}}}}
			sourceSubject, readerSubject := reportSubjectFromPrincipal(source, "source"), reportSubjectFromPrincipal(reader, "reader")
			originalBytes, _ := json.Marshal(original)
			if err := h.AuthorizeSharedReportResultScope(t.Context(), r, sourceSubject, readerSubject); err != nil {
				t.Fatal("equivalent or broader real SDK reader scope denied original source", err)
			}
			b := *reader.AccessBundle
			b.DataPolicies = slices.Clone(b.DataPolicies)
			b.DataPolicies[0].DataScopes = nil
			b.DataPolicies[0].Predicate = identity.Predicate{Fact: "id", Operator: identity.OperatorEqual, Value: "unreadable"}
			if err := b.Validate(time.Now()); err != nil {
				t.Fatal("invalid narrower SDK fixture", err)
			}
			reader.AccessBundle = &b
			narrow, err := adapter.ListReportRecordsForPrincipal(t.Context(), "sale", record.RecordListQuery{Page: 1, PageSize: 10}, reader)
			if err != nil || len(narrow.Items) != 0 || narrow.Total != 0 {
				t.Fatal("real narrower query did not exclude original rows", narrow, err)
			}
			if err := h.AuthorizeSharedReportResultScope(t.Context(), r, sourceSubject, reportSubjectFromPrincipal(reader, "narrow")); err == nil {
				t.Fatal("narrower reader accepted original scope")
			}
			afterBytes, _ := json.Marshal(original)
			if string(originalBytes) != string(afterBytes) {
				t.Fatal("scope proof rewrote original rows")
			}
		})
	}
}
