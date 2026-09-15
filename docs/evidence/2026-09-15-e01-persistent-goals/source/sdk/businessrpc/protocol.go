// Package businessrpc transports the existing business host ports. It owns no
// business data or execution state. Only trusted application hosts may use it.
package businessrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	sdk "github.com/domainry/domainry-agent-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

const (
	ProtocolVersion  = "domainry-agent-business-host-v1"
	CapabilitiesPath = "/_domainry/agent-business/v1/capabilities"
	CallPath         = "/_domainry/agent-business/v1/call"
	MaxRequestBytes  = 1 << 20
	MaxResponseBytes = 1 << 20
)

// Backend is the complete existing business-host profile. An optional business
// operation is still governed by the backend's current tool authorization.
// Implementations must resolve Authority through their current Identity owner;
// the service token authenticates the caller, not a user's enduring permission.
type Backend interface {
	sdk.ConversationBusinessSource
	sdk.ConversationBusinessRelationSource
	sdk.ConversationBusinessActionSource
	sdk.ConversationBusinessWorkflowSource
	sdk.ConversationBusinessEvidenceSealer
	sdk.ConversationToolAuthorizer
	sdk.ConversationExecutionAuthorizer
}

type Scope struct {
	RuntimeID      string `json:"runtime_id"`
	WorkspaceID    string `json:"workspace_id"`
	ApplicationKey string `json:"application_key"`
	IdentityIssuer string `json:"identity_issuer"`
}
type Descriptor struct {
	ProtocolVersion string `json:"protocol_version"`
	ContractSHA256  string `json:"contract_sha256"`
	Scope           Scope  `json:"scope"`
	SourceIdentity  string `json:"source_identity"`
}
type request struct {
	Descriptor Descriptor                `json:"binding"`
	Operation  string                    `json:"operation"`
	Authority  sdk.ConversationAuthority `json:"authority"`
	Payload    json.RawMessage           `json:"payload"`
}
type response struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error *wireError      `json:"error,omitempty"`
}
type wireError struct {
	Class string `json:"class"`
	Code  string `json:"code"`
}

type method struct {
	key           string
	input, output reflect.Type
}

var methods = []method{
	{"business_shared_result_read", reflect.TypeFor[sharedReadInput[sdk.ConversationBusinessEvidence]](), reflect.TypeFor[bool]()},
	{"report_shared_result_read", reflect.TypeFor[sharedReadInput[reportmodel.ReportQueryResultAuthorization]](), reflect.TypeFor[bool]()},
	{"report_shared_catalog_read", reflect.TypeFor[sharedReadInput[reportmodel.ReportCatalogReadAuthorization]](), reflect.TypeFor[bool]()},
	{"analysis_shared_result_read", reflect.TypeFor[sharedReadInput[reportmodel.AnalysisResultAuthorization]](), reflect.TypeFor[bool]()},
	{"analysis_shared_catalog_read", reflect.TypeFor[sharedReadInput[reportmodel.AnalysisCatalogReadAuthorization]](), reflect.TypeFor[bool]()},

	{"business_result_read", reflect.TypeFor[sdk.ConversationBusinessEvidence](), reflect.TypeFor[bool]()},
	{"report_result_read", reflect.TypeFor[reportmodel.ReportQueryResultAuthorization](), reflect.TypeFor[bool]()},
	{"report_catalog_read", reflect.TypeFor[reportmodel.ReportCatalogReadAuthorization](), reflect.TypeFor[bool]()},
	{"analysis_result_read", reflect.TypeFor[reportmodel.AnalysisResultAuthorization](), reflect.TypeFor[bool]()},
	{"analysis_catalog_read", reflect.TypeFor[reportmodel.AnalysisCatalogReadAuthorization](), reflect.TypeFor[bool]()},
	{"catalog", reflect.TypeFor[sdk.ConversationBusinessCatalogQuery](), reflect.TypeFor[sdk.ConversationBusinessCatalogPage]()},
	{"query", reflect.TypeFor[sdk.ConversationBusinessQuery](), reflect.TypeFor[sdk.ConversationBusinessRecordPage]()},
	{"get", reflect.TypeFor[sdk.ConversationBusinessGet](), reflect.TypeFor[sdk.ConversationBusinessRecord]()},
	{"related", reflect.TypeFor[sdk.ConversationBusinessRelatedQuery](), reflect.TypeFor[sdk.ConversationBusinessRelatedPage]()},
	{"revalidate", reflect.TypeFor[sdk.ConversationBusinessEvidence](), reflect.TypeFor[bool]()},
	{"seal", reflect.TypeFor[sdk.ConversationBusinessEvidence](), reflect.TypeFor[string]()},
	{"action_authorize", reflect.TypeFor[sdk.ConversationBusinessAction](), reflect.TypeFor[sdk.ConversationToolAuthorization]()},
	{"action_invoke", reflect.TypeFor[sdk.ConversationBusinessActionRequest](), reflect.TypeFor[sdk.ConversationBusinessActionResult]()},
	{"action_reconcile", reflect.TypeFor[sdk.ConversationBusinessActionRequest](), reflect.TypeFor[sdk.ConversationBusinessActionResult]()},
	{"action_revalidate", reflect.TypeFor[sdk.ConversationBusinessEvidence](), reflect.TypeFor[bool]()},
	{"workflow_authorize", reflect.TypeFor[sdk.ConversationWorkflowStart](), reflect.TypeFor[sdk.ConversationToolAuthorization]()},
	{"workflow_start", reflect.TypeFor[sdk.ConversationWorkflowStartRequest](), reflect.TypeFor[sdk.ConversationWorkflowReceipt]()},
	{"workflow_reconcile", reflect.TypeFor[sdk.ConversationWorkflowStartRequest](), reflect.TypeFor[sdk.ConversationWorkflowReceipt]()},
	{"workflow_get", reflect.TypeFor[sdk.ConversationWorkflowGet](), reflect.TypeFor[sdk.ConversationWorkflowState]()},
	{"workflow_revalidate", reflect.TypeFor[sdk.ConversationBusinessEvidence](), reflect.TypeFor[bool]()},
	{"tool_authorize", reflect.TypeFor[sdk.ConversationToolRequest](), reflect.TypeFor[sdk.ConversationToolAuthorization]()},
	{"report_summary", reflect.TypeFor[reportmodel.ReportSummaryRequest](), reflect.TypeFor[reportmodel.ReportSummary]()},
	{"report_object_sql", reflect.TypeFor[reportmodel.ReportObjectSQLRequest](), reflect.TypeFor[reportmodel.ReportSummary]()},
	{"report_catalog", reflect.TypeFor[reportmodel.ReportCatalogRequest](), reflect.TypeFor[reportmodel.ReportCatalog]()},
	{"report_query", reflect.TypeFor[reportmodel.ReportObjectSQLRequest](), reflect.TypeFor[reportmodel.ReportQueryResult]()},
	{"report_authorize_result", reflect.TypeFor[reportmodel.ReportQueryResultAuthorization](), reflect.TypeFor[bool]()},
	{"analysis_catalog", reflect.TypeFor[reportmodel.AnalysisCatalogRequest](), reflect.TypeFor[reportmodel.AnalysisCatalog]()},
	{"analysis_run", reflect.TypeFor[reportmodel.AnalysisRequest](), reflect.TypeFor[reportmodel.AnalysisResult]()},
	{"analysis_authorize_result", reflect.TypeFor[reportmodel.AnalysisResultAuthorization](), reflect.TypeFor[bool]()},
}

// ContractSHA256 covers the versioned semantics, envelope and all existing DTO
// shapes. Changing a shared DTO cannot silently retain wire compatibility.
func ContractSHA256() string { return contractSHA }

var contractSHA = func() string {
	parts := []any{ProtocolVersion, CapabilitiesPath, CallPath, MaxRequestBytes, MaxResponseBytes, "trusted-scoped-delegation;current-execution-and-tool-policy;report-owner-authorization;analysis-owner-full-dataset-and-version-proof;analysis-safe-errors-v1;analysis-result-roundtrip-limit-v1;recursive-shapes-v1;preserve-json-numbers;no-retry;write-unknown-on-transport-failure", shape(reflect.TypeFor[request]()), shape(reflect.TypeFor[response]())}
	for _, m := range methods {
		parts = append(parts, []any{m.key, shape(m.input), shape(m.output)})
	}
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}()

func shape(t reflect.Type) any {
	return shapeWithin(t, map[reflect.Type]int{})
}

// Recursive DTOs contribute an explicit reference to their active ancestor.
// Acyclic shapes retain the previous representation, and repeated sibling
// types are expanded independently so no field changes disappear from the hash.
func shapeWithin(t reflect.Type, path map[reflect.Type]int) any {
	if t == reflect.TypeFor[json.RawMessage]() {
		return "raw-json"
	}
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return "rfc3339-time"
	}
	if ancestor, ok := path[t]; ok {
		return []any{"recursive-ref", ancestor}
	}
	path[t] = len(path)
	defer delete(path, t)
	switch t.Kind() {
	case reflect.Pointer:
		return []any{"pointer", shapeWithin(t.Elem(), path)}
	case reflect.Slice, reflect.Array:
		return []any{t.Kind().String(), shapeWithin(t.Elem(), path)}
	case reflect.Map:
		return []any{"map", shapeWithin(t.Key(), path), shapeWithin(t.Elem(), path)}
	case reflect.Struct:
		fields := []any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() || f.Tag.Get("json") == "-" {
				continue
			}
			name := f.Tag.Get("json")
			if name == "" {
				name = f.Name
			}
			fields = append(fields, []any{name, f.Anonymous, shapeWithin(f.Type, path)})
		}
		return []any{"object", fields}
	default:
		return t.Kind().String()
	}
}
func canonical(s string, max int) bool {
	if s == "" || len(s) > max || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
func (s Scope) valid() bool {
	return canonical(s.RuntimeID, 256) && canonical(s.WorkspaceID, 256) && canonical(s.ApplicationKey, 256) && canonical(s.IdentityIssuer, 2048)
}
func validAuthority(a sdk.ConversationAuthority, s Scope) bool {
	return a.Known && a.RuntimeID == s.RuntimeID && a.WorkspaceID == s.WorkspaceID && canonical(a.UserID, 256) && (a.RoleKey == "" || canonical(a.RoleKey, 256))
}
func newDescriptor(scope Scope, source string) Descriptor {
	return Descriptor{ProtocolVersion: ProtocolVersion, ContractSHA256: ContractSHA256(), Scope: scope, SourceIdentity: source}
}
func failure(class string) error {
	return &sdk.Error{Class: class, Code: "agent.business_host." + class}
}
