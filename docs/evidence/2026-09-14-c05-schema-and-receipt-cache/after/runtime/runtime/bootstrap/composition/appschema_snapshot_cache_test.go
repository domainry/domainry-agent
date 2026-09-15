package composition

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	identity "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestNewRuntimeServicesStateUsesGenerationAwareSchemaProvider(t *testing.T) {
	records := newRuntimeServicesState(t.Context(), manifestmodel.ManifestSchema{}, RuntimeServicesDependencies{})
	if records.RecordSchemaSnapshotProvider == nil || records.RecordSchemaSnapshotProvider.generation == nil {
		t.Fatal("runtime constructor bypassed generation-aware schema provider")
	}
	var builds atomic.Int32
	original := records.RecordSchemaSnapshotProvider.snapshot
	records.RecordSchemaSnapshotProvider.snapshot = func() appschemamodel.ApplicationSchemaSnapshot {
		builds.Add(1)
		return original()
	}
	records.RecordSchemaSnapshotProvider.Schema()
	records.RecordSchemaSnapshotProvider.Schema()
	if got := builds.Load(); got != 1 {
		t.Fatalf("constructed runtime rebuilt stable schema %d times, want 1", got)
	}
}

func TestRecordSchemaSnapshotProviderBuildsColdGenerationOnceUnderConcurrency(t *testing.T) {
	var builds atomic.Int32
	provider := &RecordSchemaSnapshotProvider{
		generation: func() uint64 { return 1 },
		snapshot: func() appschemamodel.ApplicationSchemaSnapshot {
			builds.Add(1)
			time.Sleep(10 * time.Millisecond)
			return appschemamodel.ApplicationSchemaSnapshot{SchemaHash: "stable"}
		},
	}
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if snapshot := provider.Schema(); snapshot.SchemaHash != "stable" {
				t.Errorf("incomplete concurrent snapshot: %+v", snapshot)
			}
		}()
	}
	wait.Wait()
	if got := builds.Load(); got != 1 {
		t.Fatalf("cold generation built %d times, want 1", got)
	}
}

func TestRecordSchemaSnapshotProviderBuildsColdProjectionOnceUnderConcurrency(t *testing.T) {
	records := &runtimeAssembly{
		schema:          map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}},
		actions:         map[string]definitionmodel.ActionSchema{},
		workflows:       map[string]definitionmodel.WorkflowSchema{},
		automationRules: map[string]automationmodel.AutomationRuleSchema{},
	}
	ensureRecordSchemaSnapshotProvider(records)
	p := accessfixture.Attach(principalmodel.Principal{Principal: identity.Principal{Known: true, UserID: "reader", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"customer.read"}, FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true}}})
	var wait sync.WaitGroup
	results := make([]appschemamodel.ApplicationSchemaSnapshot, 32)
	for i := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index] = records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p)
		}(i)
	}
	wait.Wait()
	if len(results[0].Objects) != 1 {
		t.Fatal("valid concurrent projection denied", results[0])
	}
	for index := 1; index < len(results); index++ {
		if len(results[index].Objects) != 1 || &results[index].Objects[0] != &results[0].Objects[0] {
			t.Fatalf("concurrent projection %d was independently built: first=%p current=%p", index, &results[0].Objects[0], &results[index].Objects[0])
		}
	}
}

func TestRecordSchemaSnapshotProviderReusesProjectionAcrossAccessRefresh(t *testing.T) {
	records := &runtimeAssembly{
		schema:          map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}},
		actions:         map[string]definitionmodel.ActionSchema{},
		workflows:       map[string]definitionmodel.WorkflowSchema{},
		automationRules: map[string]automationmodel.AutomationRuleSchema{},
	}
	ensureRecordSchemaSnapshotProvider(records)
	p := accessfixture.Attach(principalmodel.Principal{Principal: identity.Principal{Known: true, UserID: "reader", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"customer.read"}, FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true}}})
	first := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p)
	refreshed := p
	copy := *p.AccessBundle
	copy.ExpiresAt = copy.ExpiresAt.Add(time.Minute)
	refreshed.AccessBundle = &copy
	second := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), refreshed)
	if len(first.Objects) != 1 || len(second.Objects) != 1 || &first.Objects[0] != &second.Objects[0] {
		t.Fatalf("equivalent refreshed access rebuilt projection: first=%p second=%p", &first.Objects[0], &second.Objects[0])
	}
}

func TestRecordSchemaSnapshotProviderReusesStableProjectionAndInvalidatesEveryMutation(t *testing.T) {
	records := &runtimeAssembly{
		schema:          map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer", Name: "Initial", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}},
		actions:         map[string]definitionmodel.ActionSchema{},
		workflows:       map[string]definitionmodel.WorkflowSchema{},
		automationRules: map[string]automationmodel.AutomationRuleSchema{},
	}
	ensureRecordSchemaSnapshotProvider(records)
	builds := 0
	original := records.RecordSchemaSnapshotProvider.snapshot
	records.RecordSchemaSnapshotProvider.snapshot = func() appschemamodel.ApplicationSchemaSnapshot {
		builds++
		return original()
	}
	p := accessfixture.Attach(principalmodel.Principal{Principal: identity.Principal{Known: true, UserID: "reader", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"customer.read"}, FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true}}})
	first := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p)
	second := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p)
	if builds != 1 || len(first.Objects) != 1 || len(second.Objects) != 1 || first.SchemaHash != second.SchemaHash || &first.Objects[0] != &second.Objects[0] {
		t.Fatalf("stable immutable projection rebuilt: builds=%d first=%+v second=%+v", builds, first, second)
	}
	records.applyManifestMetadata("template", "2", "Updated", "UTC", []definitionmodel.ObjectSchema{{Key: "customer", Name: "Updated", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}, nil, nil, nil, nil, appschemamodel.IntegrationSchema{}, nil, nil, nil, nil)
	updated := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p)
	if builds != 2 || len(updated.Objects) != 1 || updated.Objects[0].Name != "Updated" || updated.SchemaHash == first.SchemaHash {
		t.Fatalf("metadata mutation retained cached projection: builds=%d snapshot=%+v", builds, updated)
	}
	registry := runtimeWorkflowRegistry{records: records}
	registry.Set("review", definitionmodel.WorkflowSchema{Key: "review", Name: "Review"})
	withWorkflow := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p)
	if builds != 3 || len(withWorkflow.Workflows) != 1 || withWorkflow.Workflows[0].Key != "review" {
		t.Fatalf("workflow mutation retained cached projection: builds=%d snapshot=%+v", builds, withWorkflow)
	}
	registry.Delete("review")
	withoutWorkflow := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p)
	if builds != 4 || len(withoutWorkflow.Workflows) != 0 {
		t.Fatalf("workflow deletion retained cached projection: builds=%d snapshot=%+v", builds, withoutWorkflow)
	}
	records.applyManifestAgentMetadata(nil, nil, nil)
	if snapshot := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p); builds != 5 || snapshot.SchemaHash != withoutWorkflow.SchemaHash {
		t.Fatalf("agent metadata generation was not observed: builds=%d snapshot=%+v", builds, snapshot)
	}
}

func TestRecordSchemaSnapshotProviderDoesNotReuseExpiredAccessProjection(t *testing.T) {
	records := &runtimeAssembly{
		schema:          map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}},
		actions:         map[string]definitionmodel.ActionSchema{},
		workflows:       map[string]definitionmodel.WorkflowSchema{},
		automationRules: map[string]automationmodel.AutomationRuleSchema{},
	}
	ensureRecordSchemaSnapshotProvider(records)
	p := accessfixture.Attach(principalmodel.Principal{Principal: identity.Principal{Known: true, UserID: "reader", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"customer.read"}, FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true}}})
	p.AccessBundle.ExpiresAt = time.Now().Add(20 * time.Millisecond)
	if snapshot := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p); len(snapshot.Objects) != 1 {
		t.Fatal("valid projection denied", snapshot)
	}
	time.Sleep(30 * time.Millisecond)
	if snapshot := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), p); len(snapshot.Objects) != 0 {
		t.Fatal("expired access bundle reused cached projection", snapshot)
	}
}

func TestRecordSchemaSnapshotProviderBoundsPrincipalsAndHandlesConcurrentPublication(t *testing.T) {
	records := &runtimeAssembly{
		schema:          map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}},
		actions:         map[string]definitionmodel.ActionSchema{},
		workflows:       map[string]definitionmodel.WorkflowSchema{},
		automationRules: map[string]automationmodel.AutomationRuleSchema{},
	}
	ensureRecordSchemaSnapshotProvider(records)
	principal := func(index int) principalmodel.Principal {
		user := fmt.Sprintf("reader-%d", index)
		return accessfixture.Attach(principalmodel.Principal{Principal: identity.Principal{Known: true, UserID: user, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"customer.read"}, FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true}}})
	}
	for i := 0; i < recordSchemaProjectionCacheLimit+44; i++ {
		if snapshot := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), principal(i)); len(snapshot.Objects) != 1 {
			t.Fatal("valid principal denied", i, snapshot)
		}
	}
	records.RecordSchemaSnapshotProvider.mu.RLock()
	cached := len(records.RecordSchemaSnapshotProvider.projectedSnapshot)
	records.RecordSchemaSnapshotProvider.mu.RUnlock()
	if cached == 0 || cached > recordSchemaProjectionCacheLimit {
		t.Fatal("principal projection cache escaped its bound", cached)
	}
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			for j := 0; j < 10; j++ {
				snapshot := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), principal(index))
				if len(snapshot.Objects) != 1 || snapshot.Objects[0].Key != "customer" {
					t.Errorf("concurrent projection incomplete: %+v", snapshot)
					return
				}
			}
		}(i)
	}
	records.applyManifestMetadata("template", "3", "Concurrent", "UTC", []definitionmodel.ObjectSchema{{Key: "customer", Name: "Concurrent", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}, nil, nil, nil, nil, appschemamodel.IntegrationSchema{}, nil, nil, nil, nil)
	wait.Wait()
	if snapshot := records.RecordSchemaSnapshotProvider.SchemaForPrincipal(t.Context(), principal(0)); len(snapshot.Objects) != 1 || snapshot.Objects[0].Name != "Concurrent" {
		t.Fatal("publication did not invalidate concurrent projection", snapshot)
	}
}
