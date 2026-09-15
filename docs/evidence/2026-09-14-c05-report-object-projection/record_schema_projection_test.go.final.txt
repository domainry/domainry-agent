package composition

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestRecordPolicyUsesCurrentObjectsWithoutBuildingWholeSchema(t *testing.T) {
	services := &runtimeAssembly{schema: map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{{Key: "balance", Name: "Balance", Type: "integer"}}}}}
	builds := 0
	services.RecordSchemaSnapshotProvider = &RecordSchemaSnapshotProvider{snapshot: func() appschemamodel.ApplicationSchemaSnapshot {
		builds++
		return recordSchemaSnapshot(services)
	}}
	policy := newRecordQueryPolicyService(services)
	p := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reader", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"customer.read"}})
	object, err := policy.ObjectForAction(p, "customer", "read")
	if err != nil || object.Capabilities == nil || len(object.Fields) != 1 {
		t.Fatal("current normalized object unavailable", object, err)
	}
	if builds != 0 {
		t.Fatal("record-only policy built and hashed the whole application schema", builds)
	}
	services.mu.Lock()
	services.schema["customer"] = definitionmodel.ObjectSchema{Key: "customer", Name: "Updated", Fields: []definitionmodel.FieldSchema{{Key: "amount", Name: "Amount", Type: "integer"}}}
	services.mu.Unlock()
	object, err = policy.ObjectForAction(p, "customer", "read")
	if err != nil || object.Name != "Updated" || object.Fields[0].Key != "amount" || object.Capabilities == nil || builds != 0 {
		t.Fatal("policy retained obsolete metadata", object, err, builds)
	}
	denied := accessfixture.Attach(p, accessfixture.Bundle{})
	if _, err := policy.ObjectForAction(denied, "customer", "read"); err == nil {
		t.Fatal("object projection bypassed current operation authorization")
	}
}

func TestReportRecordsUseCurrentObjectMetadataWithoutBuildingWholeSchema(t *testing.T) {
	services := &runtimeAssembly{schema: map[string]definitionmodel.ObjectSchema{"customer": {Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{{Key: "balance", Type: "integer"}}}}}
	builds := 0
	services.RecordSchemaSnapshotProvider = &RecordSchemaSnapshotProvider{snapshot: func() appschemamodel.ApplicationSchemaSnapshot {
		builds++
		return recordSchemaSnapshot(services)
	}}
	assertObjects := func(name, field string) {
		t.Helper()
		objects := services.reportRecordSchemaMap()
		if len(objects) != 1 || objects["customer"].Name != name || len(objects["customer"].Fields) != 1 || objects["customer"].Fields[0].Key != field || objects["customer"].Capabilities == nil {
			t.Fatal("report source did not receive current normalized object metadata", objects)
		}
		if builds != 0 {
			t.Fatal("report-only source built and hashed unrelated application metadata", builds)
		}
	}
	assertObjects("Customer", "balance")
	services.applyManifestMetadata("template", "2", "Updated runtime", "UTC", []definitionmodel.ObjectSchema{{Key: "customer", Name: "Updated", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "integer"}}}}, nil, nil, nil, nil, appschemamodel.IntegrationSchema{}, nil, nil, nil, nil)
	assertObjects("Updated", "amount")
	services.applyManifestMetadata("template", "3", "Updated runtime", "UTC", nil, nil, nil, nil, nil, appschemamodel.IntegrationSchema{}, nil, nil, nil, nil)
	objects := services.reportRecordSchemaMap()
	if len(objects) != 0 || builds != 0 {
		t.Fatal("report source retained a removed object", objects, builds)
	}
}
