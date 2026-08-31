package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/domainry/domainry-agent-sdk/modulehost"
)

type AnalysisQuery struct {
	Intent     string
	SQL        string
	MetricSpec map[string]any
	DryRun     bool
	MaxRows    int
	Principal  modulehost.Principal
}

type AnalysisService struct {
	host  modulehost.AnalysisHost
	audit modulehost.AuditHost
}

func NewAnalysisService(host modulehost.AnalysisHost, audit modulehost.AuditHost) *AnalysisService {
	return &AnalysisService{host: host, audit: audit}
}

func (s *AnalysisService) Query(ctx context.Context, request AnalysisQuery) (map[string]any, error) {
	if s == nil || s.host == nil || s.audit == nil {
		return nil, unavailable("agent.analysis.unavailable")
	}
	request.Intent, request.SQL = strings.TrimSpace(request.Intent), strings.TrimSpace(request.SQL)
	if request.Intent == "" {
		return nil, badRequest("agent_analysis.intent_required")
	}
	if reason := analysisSQLRejectReason(request.SQL); reason != "" {
		return nil, badRequest(reason)
	}
	catalog, err := s.host.ResolveAnalysisCatalog(ctx, request.Principal)
	if err != nil {
		return nil, err
	}
	objectKey, reportKey := analysisSpecString(request.MetricSpec, "object_key"), analysisSpecString(request.MetricSpec, "report_key")
	if objectKey != "" && !containsString(catalog.ObjectKeys, objectKey) {
		return nil, forbidden("agent_analysis.object_denied")
	}
	if reportKey != "" && !containsString(catalog.ReportKeys, reportKey) {
		return nil, forbidden("agent_analysis.report_denied")
	}
	maxRows := request.MaxRows
	if maxRows <= 0 || maxRows > 100 {
		maxRows = 100
	}
	masked := append([]string(nil), catalog.MaskedFields[objectKey]...)
	sort.Strings(masked)
	queryRef := analysisQueryRef(request.Intent, request.SQL, request.MetricSpec, request.Principal.WorkspaceID, request.Principal.RoleKey)
	suggestion := analysisProposalSuggestion(request.MetricSpec, request.Intent, queryRef)
	metadata := map[string]any{
		"query_ref": queryRef, "object_key": objectKey, "report_key": reportKey, "masked_fields": masked,
		"has_sql": request.SQL != "", "dry_run": request.DryRun, "max_rows": maxRows,
		"gateway": "agent_analysis_query_gateway", "scope": "server_principal_scoped",
		"workspace_id": request.Principal.WorkspaceID, "user_id": request.Principal.UserID, "role": request.Principal.RoleKey,
		"requesting_user": request.Principal.UserID, "service_role": "agent_service_user", "automation_user": "agent_automation",
	}
	if len(suggestion) > 0 {
		metadata["proposal_suggestion"] = suggestion
	}
	if request.DryRun || request.SQL != "" || objectKey == "" {
		event := "agent_analysis_query_validated"
		_ = s.audit.AppendAgentAudit(ctx, modulehost.AuditRequest{Event: event, ObjectKey: objectKey, Summary: "Agent analysis query validated", Principal: request.Principal, Metadata: metadata})
		scopeNote := "Validated with the current workspace, role, and visible schema. Raw SQL execution remains disabled until a permission-aware executor is bound."
		htmlFragment := analysisValidationHTML(request.Intent, queryRef, reportKey, scopeNote) + analysisProposalHTML(suggestion)
		return analysisResponse("validated", "gateway_validation", request.Principal, queryRef, objectKey, reportKey, masked, suggestion, htmlFragment, scopeNote, event, nil, 0, false), nil
	}
	page, err := s.host.ListAnalysisRecords(ctx, objectKey, analysisFilters(request.MetricSpec), maxRows, request.Principal)
	if err != nil {
		return nil, err
	}
	event := "agent_analysis_query_executed"
	_ = s.audit.AppendAgentAudit(ctx, modulehost.AuditRequest{Event: event, ObjectKey: objectKey, Summary: "Agent analysis query executed through scoped record APIs", Principal: request.Principal, Metadata: metadata})
	scopeNote := "Result rows were read through generated record APIs using the current principal, data scope, and field visibility."
	htmlFragment := analysisReportHTML(request.Intent, queryRef, objectKey, reportKey, page.Items, scopeNote) + analysisProposalHTML(suggestion)
	return analysisResponse("completed", "scoped_record_query", request.Principal, queryRef, objectKey, reportKey, masked, suggestion, htmlFragment, scopeNote, event, page.Items, page.Total, page.HasNext), nil
}

func analysisResponse(status, mode string, principal modulehost.Principal, queryRef, objectKey, reportKey string, masked []string, suggestion map[string]any, htmlFragment, scopeNote, event string, rows []modulehost.AnalysisRecord, total int, truncated bool) map[string]any {
	card := "message_card"
	if rows != nil {
		card = "report_card"
	}
	result := map[string]any{
		"query_ref": queryRef, "status": status, "execution_mode": mode, "html_fragment": htmlFragment,
		"rendered_report":   map[string]any{"format": "html_fragment", "card_type": card, "html_fragment": htmlFragment, "query_ref": queryRef, "report_center_ref": reportKey, "rendering_owner": "agent", "canonical": true},
		"report_provenance": map[string]any{"query_ref": queryRef, "source_object_key": objectKey, "row_count": len(rows), "total": total, "truncated": truncated, "audit_event_key": event, "governance": "server_principal_scoped", "rendering_owner": "agent", "export_audit_required_for_export": true, "export_audit_handoff": "report_center"},
		"scope_note":        scopeNote, "report_center_ref": reportKey, "proposal_suggestion": suggestion,
		"workspace_id": principal.WorkspaceID, "role": principal.RoleKey, "object_key": objectKey, "report_key": reportKey,
		"masked_fields": masked, "truncated": truncated, "audit_event_key": event,
	}
	if rows != nil {
		result["rows"], result["row_count"], result["total"] = rows, len(rows), total
	}
	return result
}

func analysisSQLRejectReason(sqlText string) string {
	lower := strings.ToLower(strings.TrimSpace(sqlText))
	if lower == "" {
		return ""
	}
	if strings.Count(lower, ";") > 1 || strings.Contains(strings.TrimSuffix(lower, ";"), ";") {
		return "agent_analysis.sql_multi_statement_denied"
	}
	if !strings.HasPrefix(lower, "select ") && !strings.HasPrefix(lower, "with ") && !strings.HasPrefix(lower, "explain ") {
		return "agent_analysis.sql_readonly_required"
	}
	for _, token := range strings.FieldsFunc(lower, func(value rune) bool { return !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '_' }) {
		switch token {
		case "insert", "update", "delete", "merge", "upsert", "replace", "create", "alter", "drop", "truncate", "grant", "revoke", "call", "load", "load_file", "into":
			return "agent_analysis.sql_readonly_required"
		}
	}
	return ""
}

func analysisSpecString(spec map[string]any, key string) string {
	text, _ := spec[key].(string)
	return strings.TrimSpace(text)
}
func analysisFilters(spec map[string]any) map[string]any {
	value, _ := spec["filters"].(map[string]any)
	return cloneTaskMap(value)
}
func analysisQueryRef(intent, sqlText string, spec map[string]any, workspaceID, role string) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{intent, sqlText, analysisSpecString(spec, "object_key"), analysisSpecString(spec, "report_key"), workspaceID, role}, "|")))
	return "agent_query:" + hex.EncodeToString(hash[:])[:16]
}

func analysisValidationHTML(intent, queryRef, reportKey, scopeNote string) string {
	return `<article data-card-type="message_card" class="agent-card agent-card-message"><h3>` + html.EscapeString(analysisTitle(intent, "Analysis query")) + `</h3><p>The query was validated by Agent against Runtime-provided scoped catalog facts.</p>` + analysisReportCenterHTML(reportKey) + `<p data-scope-note="analysis_gateway">` + html.EscapeString(scopeNote) + `</p><a data-reference="` + html.EscapeString(queryRef) + `">` + html.EscapeString(queryRef) + `</a></article>`
}

func analysisReportHTML(intent, queryRef, objectKey, reportKey string, rows []modulehost.AnalysisRecord, scopeNote string) string {
	var builder strings.Builder
	builder.WriteString(`<article data-card-type="report_card" class="agent-card agent-card-report"><h3>` + html.EscapeString(analysisTitle(intent, "Analysis result")) + `</h3>` + analysisReportCenterHTML(reportKey) + `<div data-chart-type="table">`)
	if len(rows) == 0 {
		builder.WriteString(`<div data-chart-item data-label="Rows" data-value="0"></div>`)
	}
	for index, row := range rows {
		if index >= 10 {
			break
		}
		builder.WriteString(`<div data-chart-item data-label="` + html.EscapeString(valueOrDefault(row.ID, fmt.Sprintf("row_%d", index+1))) + `" data-value="` + html.EscapeString(analysisRowValue(row)) + `"></div>`)
	}
	builder.WriteString(`</div><p data-scope-note="analysis_gateway">` + html.EscapeString(scopeNote) + `</p><a data-reference="` + html.EscapeString(queryRef) + `">` + html.EscapeString(queryRef) + `</a></article>`)
	return builder.String()
}

func analysisRowValue(row modulehost.AnalysisRecord) string {
	keys := make([]string, 0, len(row.Data))
	for key := range row.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, 3)
	for _, key := range keys {
		if len(values) == 3 {
			break
		}
		value := strings.TrimSpace(fmt.Sprint(row.Data[key]))
		if value != "" {
			values = append(values, key+"="+truncateUTF8(value, 60))
		}
	}
	return valueOrDefault(strings.Join(values, ", "), "record")
}

func analysisTitle(intent, fallback string) string {
	return valueOrDefault(truncateUTF8(strings.TrimSpace(intent), 80), fallback)
}
func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
func analysisReportCenterHTML(reportKey string) string {
	if strings.TrimSpace(reportKey) == "" {
		return ""
	}
	return `<a data-report-center-ref="` + html.EscapeString(reportKey) + `" data-reference="report:` + html.EscapeString(reportKey) + `">Open report center reference</a>`
}

func analysisProposalSuggestion(spec map[string]any, intent, queryRef string) map[string]any {
	followUp, _ := spec["follow_up"].(map[string]any)
	if len(followUp) == 0 {
		return nil
	}
	proposed := map[string]any{}
	for _, key := range []string{"action_binding", "workflow_binding", "diff", "affected_records"} {
		if value, found := followUp[key]; found {
			proposed[key] = value
		}
	}
	return map[string]any{
		"title":      valueOrDefault(analysisSpecString(followUp, "title"), "Create follow-up task"),
		"summary":    valueOrDefault(analysisSpecString(followUp, "summary"), intent),
		"kind":       valueOrDefault(analysisSpecString(followUp, "kind"), "analysis_follow_up"),
		"risk_level": valueOrDefault(analysisSpecString(followUp, "risk_level"), "medium"),
		"run_mode":   "suggested_write", "reference": valueOrDefault(analysisSpecString(followUp, "reference"), queryRef),
		"rollback": analysisSpecString(followUp, "rollback"), "approval": "approval_required", "proposed": proposed,
	}
}

func analysisProposalHTML(suggestion map[string]any) string {
	if len(suggestion) == 0 {
		return ""
	}
	return `<article data-card-type="proposal_card" class="agent-card agent-card-proposal"><h3>` + html.EscapeString(fmt.Sprint(suggestion["title"])) + `</h3><p>` + html.EscapeString(fmt.Sprint(suggestion["summary"])) + `</p><button data-action="create_proposal">Create follow-up proposal</button></article>`
}
