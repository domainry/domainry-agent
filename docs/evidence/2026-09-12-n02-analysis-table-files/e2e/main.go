package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/codeck-backend/kb-search-api/handler"
	common "github.com/codeck-backend/kb-search-api/model/common"
	"github.com/codeck-backend/kb-search-api/pkg/clients"
	"github.com/codeck-backend/kb-search-api/pkg/requestctx"
	searchtypes "github.com/codeck-backend/kb-search-api/pkg/types"
	connector "github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportpersistence "github.com/domainry/domainry-report-sdk/persistence"
	reportmodule "github.com/domainry/domainry-report/module"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	toolsmodule "github.com/domainry/domainry-tools/module"
	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

type artifact struct {
	TenantID   string          `json:"tenant_id"`
	KBID       string          `json:"kb_id"`
	DocID      string          `json:"doc_id"`
	DocVersion int             `json:"doc_version"`
	Generation string          `json:"generation"`
	Tables     []artifactTable `json:"tables"`
}

type artifactTable struct {
	DatasetKey        string                            `json:"dataset_key"`
	TableRef          string                            `json:"table_ref"`
	Name              string                            `json:"name"`
	Sheet             string                            `json:"sheet"`
	DefinitionVersion string                            `json:"definition_version"`
	DataVersion       string                            `json:"data_version"`
	Columns           []searchtypes.AnalysisTableColumn `json:"columns"`
	Rows              [][]*string                       `json:"rows"`
	RowCount          int64                             `json:"row_count"`
	Complete          bool                              `json:"complete"`
}

func (a artifact) item(table artifactTable) searchtypes.AnalysisTableCatalogItem {
	return searchtypes.AnalysisTableCatalogItem{
		DatasetKey: table.DatasetKey, DocID: a.DocID, TableRef: table.TableRef,
		Name: table.Name, Sheet: table.Sheet, DefinitionVersion: table.DefinitionVersion,
		DataVersion: table.DataVersion, Generation: a.Generation, DocVersion: a.DocVersion,
		RowCount: table.RowCount, Complete: table.Complete, Columns: table.Columns,
	}
}

type permissionReader struct {
	denied atomic.Bool
	reads  atomic.Int64
}

func (p *permissionReader) BatchGetDocumentPermissions(_ context.Context, _, _ string, ids []string) (map[string]clients.DocumentPermission, error) {
	p.reads.Add(1)
	result := map[string]clients.DocumentPermission{}
	for _, id := range ids {
		permissions := []string{}
		if p.denied.Load() {
			permissions = []string{"revoked"}
		}
		result[id] = clients.DocumentPermission{DocID: id, PermissionIDs: permissions, PermissionVersion: 1}
	}
	return result, nil
}

type documentReader struct{ artifact artifact }

func (d documentReader) BatchGetDocuments(_ context.Context, _, _ string, ids []string) (map[string]clients.DocMeta, error) {
	result := map[string]clients.DocMeta{}
	for _, id := range ids {
		if id == d.artifact.DocID {
			result[id] = clients.DocMeta{CurrentGeneration: d.artifact.Generation}
		}
	}
	return result, nil
}
func (d documentReader) GetDocument(_ context.Context, _, _, id string) (*clients.DocDetail, error) {
	if id != d.artifact.DocID {
		return nil, nil
	}
	return &clients.DocDetail{DocID: id, CurrentGeneration: d.artifact.Generation}, nil
}

type artifactInvoker struct{ artifact artifact }

func (i artifactInvoker) InvokeCatalogAnalysisTables(_ context.Context, tenant, kb string, documents []clients.AnalysisDocumentRef) (searchtypes.AnalysisTableCatalogResponse, error) {
	if tenant != i.artifact.TenantID || kb != i.artifact.KBID || len(documents) != 1 || documents[0].DocID != i.artifact.DocID || documents[0].Generation != i.artifact.Generation {
		return searchtypes.AnalysisTableCatalogResponse{}, errors.New("source scope mismatch")
	}
	items := make([]searchtypes.AnalysisTableCatalogItem, len(i.artifact.Tables))
	for index, table := range i.artifact.Tables {
		items[index] = i.artifact.item(table)
	}
	return searchtypes.AnalysisTableCatalogResponse{Tables: items}, nil
}

func (i artifactInvoker) InvokeReadAnalysisTable(_ context.Context, tenant, kb string, request searchtypes.AnalysisTableReadRequest) (searchtypes.AnalysisTableReadResponse, error) {
	if tenant != i.artifact.TenantID || kb != i.artifact.KBID || request.DocID != i.artifact.DocID || request.Generation != i.artifact.Generation {
		return searchtypes.AnalysisTableReadResponse{}, &clients.SourceOperationError{Code: "analysis_table_source_changed"}
	}
	for _, table := range i.artifact.Tables {
		if table.DatasetKey != request.DatasetKey {
			continue
		}
		if table.DefinitionVersion != request.DefinitionVersion || table.DataVersion != request.DataVersion {
			return searchtypes.AnalysisTableReadResponse{}, &clients.SourceOperationError{Code: "analysis_table_source_changed"}
		}
		positions := map[string]int{}
		for index, column := range table.Columns {
			positions[column.Key] = index
		}
		projected := make([][]*string, len(table.Rows))
		for rowIndex, row := range table.Rows {
			projected[rowIndex] = make([]*string, len(request.Fields))
			for fieldIndex, field := range request.Fields {
				position, ok := positions[field]
				if !ok {
					return searchtypes.AnalysisTableReadResponse{}, &clients.SourceOperationError{Code: "analysis_table_request_invalid"}
				}
				projected[rowIndex][fieldIndex] = row[position]
			}
		}
		digest := sha256.New()
		encoder := json.NewEncoder(digest)
		_ = encoder.Encode(request.Fields)
		for _, row := range projected {
			_ = encoder.Encode(row)
		}
		page := [][]*string{}
		var next *int
		if request.Limit > 0 {
			end := request.Offset + request.Limit
			if end > len(projected) {
				end = len(projected)
			}
			page = projected[request.Offset:end]
			if end < len(projected) {
				value := end
				next = &value
			}
		}
		return searchtypes.AnalysisTableReadResponse{
			AnalysisTableCatalogItem: i.artifact.item(table), Fields: request.Fields,
			ContentSHA256: hex.EncodeToString(digest.Sum(nil)), Offset: request.Offset,
			Rows: page, NextOffset: next,
		}, nil
	}
	return searchtypes.AnalysisTableReadResponse{}, &clients.SourceOperationError{Code: "analysis_table_not_found"}
}

func apiHandler(fn func(*gin.Context) (interface{}, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := fn(c)
		if err != nil {
			var business *common.HandlerError
			if errors.As(err, &business) {
				c.JSON(http.StatusOK, gin.H{"err_code": business.ErrCode})
				return
			}
			c.JSON(http.StatusOK, gin.H{"err_code": 1099})
			return
		}
		c.JSON(http.StatusOK, gin.H{"err_code": 0, "data": result})
	}
}

func analysisServer(a artifact, permissions *permissionReader) *httptest.Server {
	previous := handler.AnalysisTableDeps
	handler.AnalysisTableDeps = &handler.AnalysisTableDepsConfig{
		Permissions: permissions, Doc: documentReader{artifact: a}, Invoke: artifactInvoker{artifact: a},
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer e2e-secret" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		ctx := requestctx.SetTeamID(c.Request.Context(), a.TenantID)
		ctx = requestctx.SetAPIKeyQueryScope(ctx, a.TenantID, a.KBID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.POST("/v1/kb/analysis/tables/catalog", apiHandler(handler.CatalogAnalysisTables))
	router.POST("/v1/kb/analysis/tables/read", apiHandler(handler.ReadAnalysisTable))
	server := httptest.NewServer(router)
	server.Config.RegisterOnShutdown(func() { handler.AnalysisTableDeps = previous })
	return server
}

type registrar struct{ db *sql.DB }

func (*registrar) Driver() string { return "sqlite" }
func (*registrar) Schema() string { return "" }
func (r *registrar) ApplyOwnedMigrations(ctx context.Context, _ string, migrations []reportmodulehost.SchemaMigration) error {
	for _, migration := range migrations {
		for _, statement := range migration.Statements {
			if _, err := r.db.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return nil
}

type reportHost struct {
	db      *sql.DB
	dialect reportmodulehost.Dialect
	migrate *registrar
	tables  reportmodulehost.AnalysisTableSource
}

func (h *reportHost) Database() reportmodulehost.Database                                 { return h.db }
func (h *reportHost) DatabaseFor(context.Context) reportmodulehost.DBTX                   { return h.db }
func (h *reportHost) Dialect() reportmodulehost.Dialect                                   { return h.dialect }
func (h *reportHost) Migrations() reportmodulehost.MigrationRegistrar                     { return h.migrate }
func (h *reportHost) ReportSubjects() reportmodulehost.SubjectResolver                    { return h }
func (h *reportHost) ReportObjectSQL() reportmodulehost.ObjectSQLExecutor                 { return h }
func (h *reportHost) ReportSourceVersions() reportmodulehost.SourceVersionReader          { return h }
func (h *reportHost) ReportExecutionAudit() reportmodulehost.ExecutionAudit               { return h }
func (h *reportHost) ReportExportAuthorization() reportmodulehost.ExportAuthorization     { return h }
func (h *reportHost) ReportSnapshotTerminals() reportmodulehost.SnapshotTerminalCommitter { return h }
func (h *reportHost) ReportExports() reportmodulehost.ExportGateway                       { return h }
func (h *reportHost) ReportAnalysisTables() reportmodulehost.AnalysisTableSource          { return h.tables }
func (*reportHost) ReportCursorSigningKey() []byte                                        { return []byte("analysis-file-e2e-signing-key") }
func (*reportHost) ReportClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 9, 12, 2, 0, 0, 0, time.UTC) }
}
func (*reportHost) ResolveReportSubject(_ context.Context, authority reportmodel.ReportAuthority) (reportmodel.ReportSubject, error) {
	if authority.AccessToken != "e2e-user" {
		return reportmodel.ReportSubject{}, &reportsdk.Error{StatusCode: 401, Code: "auth.token_invalid"}
	}
	permission := reportsdk.ActionReportQueryExecute
	separator := len("report.query")
	bundle := &identitysdk.AccessBundle{
		FunctionGrants: []identitysdk.FunctionGrant{{Resource: identitysdk.ResourceType(permission[:separator]), Action: identitysdk.Action(permission[separator+1:]), Effect: identitysdk.EffectAllow}},
		DataPolicies:   []identitysdk.DataPolicy{{Key: permission, Resource: identitysdk.ResourceType(permission[:separator]), Action: identitysdk.Action(permission[separator+1:]), Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{identitysdk.DataScopeAll}}},
	}
	return reportmodel.ReportSubject{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user", AccessBundle: bundle}, AccessScopeHash: "e2e-scope"}, nil
}
func (*reportHost) ResolveReportObjectSQLSources(context.Context, reportmodel.ReportSchema, reportmodel.ReportSubject) (map[string]reportmodel.ReportSourceObject, error) {
	return nil, errors.New("Object SQL must not be used for table_file")
}
func (*reportHost) AuthorizeReportObjectSQLPlan(context.Context, reportmodel.ReportSchema, reportmodel.ReportObjectSQLPlan, reportmodel.ReportSubject) error {
	return errors.New("Object SQL must not be used for table_file")
}
func (*reportHost) ExecuteReportObjectSQL(context.Context, reportmodel.ReportObjectSQLExecutionRequest) (reportmodel.ReportObjectSQLExecutionResult, error) {
	return reportmodel.ReportObjectSQLExecutionResult{}, errors.New("Object SQL must not be used for table_file")
}
func (*reportHost) ReadReportSourceVersion(context.Context, reportmodel.ReportSchema, reportmodel.ReportSubject) (reportmodel.ReportSnapshotSourceVersion, error) {
	return reportmodel.ReportSnapshotSourceVersion{}, errors.New("report snapshot source is unused")
}
func (*reportHost) AppendReportExecution(context.Context, reportmodel.ReportSchema, reportmodel.ReportSummary, reportmodel.ReportSubject) error {
	return nil
}
func (*reportHost) AuthorizeReportExportSource(context.Context, string, reportmodel.ReportSubject) error {
	return errors.New("export is unused")
}
func (*reportHost) AuthorizeReportExportField(context.Context, string, string, reportmodel.ReportSubject) (bool, error) {
	return false, errors.New("export is unused")
}
func (*reportHost) CompleteReportSnapshot(context.Context, reportpersistence.SnapshotCompleteRequest, notificationmodel.NotificationIntent) error {
	return errors.New("snapshot is unused")
}
func (*reportHost) FailReportSnapshot(context.Context, reportpersistence.SnapshotFailRequest, notificationmodel.NotificationIntent) error {
	return errors.New("snapshot is unused")
}
func (*reportHost) PrepareReportExport(context.Context, reportmodel.ReportExportPrepareRequest, reportmodel.ReportSchema, reportmodel.ReportExportControlSchema, reportmodel.ReportSubject) (reportmodel.ReportExportJob, error) {
	return reportmodel.ReportExportJob{}, errors.New("export is unused")
}

type toolSource struct{ analyses reportsdk.Analyses }

func (toolSource) BusinessSourceIdentity() string { return "report:knowledge-file-e2e" }
func (s toolSource) AnalysisCatalog(ctx context.Context, request reportmodel.AnalysisCatalogRequest, _ toolsdk.Authority) (reportmodel.AnalysisCatalog, error) {
	return s.analyses.AnalysisCatalog(ctx, request, reportmodel.ReportAuthority{AccessToken: "e2e-user"})
}
func (s toolSource) RunAnalysis(ctx context.Context, request reportmodel.AnalysisRequest, _ toolsdk.Authority) (reportmodel.AnalysisResult, error) {
	return s.analyses.RunAnalysis(ctx, request, reportmodel.ReportAuthority{AccessToken: "e2e-user"})
}
func (s toolSource) AuthorizeAnalysisResult(ctx context.Context, request reportmodel.AnalysisResultAuthorization, _ toolsdk.Authority) error {
	return s.analyses.AuthorizeAnalysisResult(ctx, request, reportmodel.ReportAuthority{AccessToken: "e2e-user"})
}

func main() {
	if len(os.Args) != 2 {
		panic("artifact path required")
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var a artifact
	if json.Unmarshal(raw, &a) != nil || len(a.Tables) != 1 || a.Tables[0].RowCount != 1205 {
		panic("invalid builder artifact")
	}
	permissions := &permissionReader{}
	server := analysisServer(a, permissions)
	defer server.Close()
	knowledge, err := knowledgemodule.NewKnowledge(knowledgemodule.KnowledgeConfig{
		BaseURL: server.URL, APIKey: "e2e-secret", TeamID: a.TenantID, KBID: a.KBID,
		WorkspaceID: "workspace", RuntimeID: "e2e-runtime", AnalysisDocumentIDs: []string{a.DocID}, Client: server.Client(),
	})
	if err != nil {
		panic(err)
	}
	db, err := sql.Open("sqlite", "file:analysis-table-e2e?mode=memory&cache=shared")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		panic(err)
	}
	host := &reportHost{db: db, dialect: dialect.WithSchema(""), tables: knowledge}
	host.migrate = &registrar{db: db}
	binding, err := reportmodule.NewFactory().Open(context.Background(), reportsdk.ApplicationRef{RuntimeID: "e2e-runtime"}, host)
	if err != nil {
		panic(err)
	}
	defer binding.Close(context.Background())
	if err = binding.(reportsdk.ApplicationHostBinder).BindApplicationHost(host); err != nil {
		panic(err)
	}
	analyses := binding.(reportsdk.ApplicationBinding).Queries().(reportsdk.Analyses)
	catalog, err := analyses.AnalysisCatalog(context.Background(), reportmodel.AnalysisCatalogRequest{}, reportmodel.ReportAuthority{AccessToken: "e2e-user"})
	if err != nil || len(catalog.Datasets) != 1 {
		panic(fmt.Sprintf("catalog=%+v error=%v", catalog, err))
	}
	dataset := catalog.Datasets[0]
	request := reportmodel.AnalysisRequest{DatasetKey: dataset.Key, Mode: "aggregate", Measures: []reportmodel.AnalysisMeasure{{Key: "total", Field: "amount", Function: "sum"}, {Key: "rows", Function: "count"}}}
	result, err := analyses.RunAnalysis(context.Background(), request, reportmodel.ReportAuthority{AccessToken: "e2e-user"})
	if err != nil || len(result.Rows) != 1 || result.Rows[0].Values["total"] == nil || *result.Rows[0].Values["total"] != "1000012.29" || result.Source.InputCounts["dataset"] != "1205" || !result.Source.Complete || !result.Coverage.Complete || result.Coverage.Truncated || result.Coverage.ReturnedRows != 1 || result.Visualization.Chart != nil || result.Visualization.OmittedReason == "" || len(result.References) != 1 || result.References[0].Kind != "knowledge_document" || result.References[0].ID != a.DocID || result.References[0].Version != a.Generation {
		panic(fmt.Sprintf("analysis=%+v error=%v", result, err))
	}
	chartRequest := reportmodel.AnalysisRequest{DatasetKey: dataset.Key, Mode: "aggregate", GroupBy: []string{"region"}, Measures: []reportmodel.AnalysisMeasure{{Key: "total", Field: "amount", Function: "sum"}}}
	chartResult, err := analyses.RunAnalysis(context.Background(), chartRequest, reportmodel.ReportAuthority{AccessToken: "e2e-user"})
	if err != nil || chartResult.Visualization.Chart == nil || chartResult.Visualization.Chart.Type != "bar" || chartResult.Visualization.Chart.XColumn != "region" || len(chartResult.Visualization.Chart.YColumns) != 1 || chartResult.Visualization.Chart.YColumns[0] != "total" || chartResult.Coverage.Truncated {
		panic(fmt.Sprintf("chart analysis=%+v error=%v", chartResult, err))
	}
	missingRequest := reportmodel.AnalysisRequest{DatasetKey: dataset.Key, Mode: "table", Filters: []reportmodel.AnalysisFilter{{Field: "note", Operator: "is_null"}}, Select: []string{"note", "amount"}, MaxRows: 500}
	missingResult, err := analyses.RunAnalysis(context.Background(), missingRequest, reportmodel.ReportAuthority{AccessToken: "e2e-user"})
	missingCount := 0
	for _, missing := range missingResult.Coverage.Missing {
		if missing.Column == "note" && missing.Code == "null_value" {
			missingCount, _ = strconv.Atoi(missing.Count)
		}
	}
	if err != nil || !missingResult.Coverage.Complete || missingResult.Coverage.Truncated || missingResult.Coverage.ReturnedRows != 241 || missingCount != 241 {
		panic(fmt.Sprintf("missing analysis=%+v error=%v", missingResult, err))
	}

	adapter := &toolsmodule.AnalysisAdapter{Source: func() toolsmodule.AnalysisSource { return toolSource{analyses: analyses} }, Authorize: func(context.Context, toolsdk.Request) (toolsdk.Authorization, error) {
		return toolsdk.Authorization{Granted: true, Revision: "e2e-authority"}, nil
	}}
	registry := toolsmodule.NewRegistry()
	if err = adapter.Register(registry); err != nil {
		panic(err)
	}
	selected, err := registry.Select([]string{toolsdk.AnalysisRunToolKey})
	if err != nil {
		panic(err)
	}
	definition := toolsmodule.AnalysisDefinitions()[0]
	arguments, _ := json.Marshal(map[string]any{"operation": "run", "spec": request})
	toolRequest := toolsdk.Request{
		Authority:  toolsdk.Authority{Known: true, RuntimeID: "e2e-runtime", WorkspaceID: "workspace", UserID: "user"},
		Definition: definition, Call: toolsdk.Call{Name: definition.Key, ID: "call-e2e", Arguments: string(arguments)},
	}
	toolResult, err := selected.InvokeConversationTool(context.Background(), toolRequest)
	if err != nil || toolResult.Status != "completed" {
		panic(fmt.Sprintf("tool result=%+v error=%v", toolResult, err))
	}
	permissions.denied.Store(true)
	if err = selected.AuthorizeConversationToolResult(context.Background(), toolRequest, toolResult); err == nil {
		panic("revoked table result remained authorized")
	}
	columns := make([]string, len(dataset.Columns))
	for index, column := range dataset.Columns {
		columns[index] = column.Key + ":" + column.Type
	}
	sort.Strings(columns)
	output := map[string]any{
		"ok": true, "dataset_key": dataset.Key, "row_count": result.Source.InputCounts["dataset"],
		"total": *result.Rows[0].Values["total"], "complete": result.Source.Complete,
		"coverage_complete": result.Coverage.Complete, "truncated": result.Coverage.Truncated,
		"chart_type": chartResult.Visualization.Chart.Type, "chart_x": chartResult.Visualization.Chart.XColumn,
		"chart_y": chartResult.Visualization.Chart.YColumns, "missing_note_cells": missingCount,
		"reference_kind": result.References[0].Kind, "reference_id": result.References[0].ID,
		"reference_version": result.References[0].Version, "reference_subresource": result.References[0].Subresource,
		"definition_version": result.Source.DefinitionVersion, "data_version": result.Source.DataVersion,
		"proof_present": result.Source.Proof != "", "columns": columns,
		"permission_reads": permissions.reads.Load(), "revoked_saved_result": true,
		"tool_status": toolResult.Status, "tool_result_bytes": len(toolResult.Content),
	}
	encoded, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(encoded))
}

var _ handler.AnalysisTableInvoker = artifactInvoker{}
var _ reportmodulehost.AnalysisTableHost = (*reportHost)(nil)
var _ connector.Transport = (*knowledgemodule.HTTP)(nil)
