package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

type analysisResultSource struct {
	agentsdk.ConversationBusinessSource
	started chan struct{}
	release chan struct{}
	denied  atomic.Bool
	once    sync.Once
	mu      sync.Mutex
	proof   string
}

func (*analysisResultSource) BusinessSourceIdentity() string { return "analysis-result-e2e-source" }
func (s *analysisResultSource) AnalysisCatalog(context.Context, reportmodel.AnalysisCatalogRequest, toolsdk.Authority) (reportmodel.AnalysisCatalog, error) {
	if s.denied.Load() {
		return reportmodel.AnalysisCatalog{}, &toolsdk.Error{Class: "forbidden", Code: "analysis.denied"}
	}
	return reportmodel.AnalysisCatalog{Datasets: []reportmodel.AnalysisDataset{{
		Key: "sales", Name: "销售明细", Kind: "business_object", Version: "schema-1",
		Columns:    []reportmodel.AnalysisColumn{{Key: "region", Name: "区域", Type: "text"}, {Key: "total", Name: "金额", Type: "decimal", Unit: "CNY"}},
		References: []reportmodel.AnalysisReference{{Kind: "business_object", ID: "sales", Label: "销售明细", Version: "schema-1"}},
	}}}, nil
}
func (s *analysisResultSource) RunAnalysis(ctx context.Context, request reportmodel.AnalysisRequest, _ toolsdk.Authority) (reportmodel.AnalysisResult, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return reportmodel.AnalysisResult{}, ctx.Err()
	}
	rows := make([]reportmodel.AnalysisRow, 80)
	for index := range rows {
		region, total := fmt.Sprintf("区域-%03d", index+1), fmt.Sprintf("%d.25", index+1)
		value := &total
		issues := []reportmodel.AnalysisCellIssue{}
		if index == len(rows)-1 {
			value = nil
			issues = append(issues, reportmodel.AnalysisCellIssue{Column: "total", Code: "source_null_inputs"})
		}
		rows[index] = reportmodel.AnalysisRow{Values: map[string]*string{"region": &region, "total": value}, InputCounts: map[string]string{"dataset": "1"}, NonNullCounts: map[string]string{"total": map[bool]string{true: "0", false: "1"}[value == nil]}, Anomalies: []string{}, Issues: issues}
	}
	result := reportmodel.AnalysisResult{
		Spec: request, Columns: []reportmodel.AnalysisColumn{{Key: "region", Name: "区域", Type: "text", Unit: "unspecified"}, {Key: "total", Name: "金额", Type: "decimal", Unit: "CNY"}}, Rows: rows,
		Methods:       []reportmodel.AnalysisMethod{{Column: "total", Method: "sum", Nulls: "ignore_null_inputs"}},
		Visualization: reportmodel.AnalysisVisualization{Chart: &reportmodel.AnalysisChartSpec{Type: "bar", XColumn: "region", YColumns: []string{"total"}}},
		Coverage:      reportmodel.AnalysisCoverage{Complete: true, Truncated: false, ReturnedRows: len(rows), RequestedMaxRows: request.MaxRows, Missing: []reportmodel.AnalysisMissing{{Column: "total", Code: "null_value", Count: "1"}, {Column: "total", Code: "source_null_inputs", Count: "1"}}},
		References:    []reportmodel.AnalysisReference{{Kind: "business_object", ID: "sales", Label: "销售明细", Version: "schema-1"}},
		Source:        reportmodel.AnalysisSource{DatasetKey: "sales", DefinitionVersion: "definition-1", DataVersion: "data-1", QueriedAt: "2026-09-12T03:00:00Z", InputCounts: map[string]string{"dataset": "80"}, Scope: "current_subject_filtered_dataset", Complete: true, Proof: "owner-proof"},
	}
	raw, _ := json.Marshal(result)
	digest := sha256.Sum256(raw)
	s.mu.Lock()
	s.proof = hex.EncodeToString(digest[:])
	s.mu.Unlock()
	return result, nil
}
func (s *analysisResultSource) AuthorizeAnalysisResult(_ context.Context, request reportmodel.AnalysisResultAuthorization, _ toolsdk.Authority) error {
	if s.denied.Load() {
		return &toolsdk.Error{Class: "forbidden", Code: "analysis.denied"}
	}
	raw, _ := json.Marshal(request.Result)
	digest := sha256.Sum256(raw)
	s.mu.Lock()
	valid := s.proof != "" && s.proof == hex.EncodeToString(digest[:])
	s.mu.Unlock()
	if !valid {
		return &toolsdk.Error{Class: "forbidden", Code: "analysis.result_invalid"}
	}
	return nil
}

func analysisResultModel(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Messages) == 0 {
			t.Error("invalid model request")
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		tool := func(name, id string, args any) {
			raw, _ := json.Marshal(args)
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}, "")
			write(map[string]any{}, "tool_calls")
		}
		last := payload.Messages[len(payload.Messages)-1]
		if last.Role != "tool" {
			tool("analysis_run", "analysis-e2e", map[string]any{"operation": "run", "spec": map[string]any{"dataset_key": "sales", "mode": "aggregate", "group_by": []string{"region"}, "measures": []any{map[string]any{"key": "total", "function": "sum", "field": "total"}}, "max_rows": 100}})
		} else {
			switch {
			case last.ToolCallID == "analysis-e2e":
				var preview struct {
					Representation string                               `json:"representation"`
					Reference      agentsdk.ConversationResultReference `json:"reference"`
					Status         string                               `json:"status"`
				}
				if json.Unmarshal([]byte(last.Content), &preview) != nil || preview.Representation != "stored_result_preview" || preview.Status != "completed" || preview.Reference.CallID != "analysis-e2e" {
					t.Errorf("stored analysis reference missing: %s", last.Content)
					write(map[string]any{"content": "处理失败。"}, "stop")
					break
				}
				tool("tool_result_read", "analysis-read-0", map[string]any{"reference": preview.Reference, "offset": 0, "max_bytes": 8192})
			case strings.HasPrefix(last.ToolCallID, "analysis-read-"):
				parts := map[int]string{}
				next, total, complete := 0, 0, false
				var reference agentsdk.ConversationResultReference
				for _, message := range payload.Messages {
					if message.Role != "tool" || !strings.HasPrefix(message.ToolCallID, "analysis-read-") {
						continue
					}
					var receipt agentsdk.ConversationToolResult
					var page agentsdk.ConversationResultSlice
					if json.Unmarshal([]byte(message.Content), &receipt) != nil || receipt.Status != "completed" || json.Unmarshal(receipt.Content, &page) != nil {
						t.Errorf("result page invalid: %s", message.Content)
						continue
					}
					parts[page.Offset], next, total, complete, reference = page.JSONText, page.NextOffset, page.TotalBytes, page.Complete, page.Reference
				}
				if !complete {
					tool("tool_result_read", fmt.Sprintf("analysis-read-%d", len(parts)), map[string]any{"reference": reference, "offset": next, "max_bytes": 8192})
					break
				}
				var joined strings.Builder
				for offset := 0; offset < total; {
					part, ok := parts[offset]
					if !ok {
						t.Errorf("result page gap at %d", offset)
						break
					}
					joined.WriteString(part)
					offset += len([]byte(part))
				}
				var original agentsdk.ConversationToolResult
				var envelope struct {
					Result reportmodel.AnalysisResult `json:"result"`
				}
				if json.Unmarshal([]byte(joined.String()), &original) != nil || json.Unmarshal(original.Content, &envelope) != nil || envelope.Result.Visualization.Chart == nil || !envelope.Result.Coverage.Complete || envelope.Result.Coverage.Truncated || len(envelope.Result.References) != 1 || len(envelope.Result.Rows) != 80 {
					t.Error("analysis metadata missing after complete result read")
					write(map[string]any{"content": "处理失败。"}, "stop")
					break
				}
				rows := make([][]*string, len(envelope.Result.Rows))
				for index, row := range envelope.Result.Rows {
					rows[index] = []*string{row.Values["region"], row.Values["total"]}
				}
				content := agentsdk.ConversationArtifactContent{Kind: "chart", Table: &agentsdk.ConversationArtifactTable{Columns: []agentsdk.ConversationArtifactColumn{{Key: "region", Label: "区域", Type: "text"}, {Key: "total", Label: "金额", Type: "number"}}, Rows: rows}, Chart: &agentsdk.ConversationArtifactChart{Type: envelope.Result.Visualization.Chart.Type, XColumn: envelope.Result.Visualization.Chart.XColumn, YColumns: envelope.Result.Visualization.Chart.YColumns}}
				tool("artifact_create", "chart-create-e2e", map[string]any{"title": "销售区域分析", "content": content})
			case last.ToolCallID == "chart-create-e2e":
				var receipt agentsdk.ConversationToolResult
				var created struct {
					Artifact agentsdk.ConversationArtifact `json:"artifact"`
				}
				if json.Unmarshal([]byte(last.Content), &receipt) != nil || receipt.Status != "completed" || json.Unmarshal(receipt.Content, &created) != nil || created.Artifact.ID == "" {
					t.Errorf("artifact receipt missing: %s", last.Content)
					write(map[string]any{"content": "处理失败。"}, "stop")
					break
				}
				tool("artifact_export", "chart-export-e2e", map[string]any{"id": created.Artifact.ID, "version": created.Artifact.Version, "format": "csv"})
			default:
				write(map[string]any{"content": "分析图表已保存并生成受控 CSV 下载。"}, "stop")
			}
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestAnalysisLongRunStructuredChartArtifactExportAndCurrentPermission(t *testing.T) {
	const initial, changed = "Initial-Analysis-Result-Test!2", "Changed-Analysis-Result-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "analysis-result-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "analysis-result-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := analysisResultModel(t)
	defer model.Close()
	source := &analysisResultSource{started: make(chan struct{}), release: make(chan struct{})}
	options := Options{AnalysisTools: true, DatabasePath: filepath.Join(t.TempDir(), "analysis-result.db"), RuntimeID: "analysis-result-runtime", WorkspaceID: "analysis-result-workspace", ApplicationKey: "analysis-result-app", Agent: agentmodule.Options{ConversationURL: model.URL, ConversationModel: "analysis-result-fixture", ConversationOptions: agentmodule.ConversationOptions{Business: source, Poll: 5 * time.Millisecond, MaxSteps: 8, ContextBytes: 256 * 1024}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close(context.Background()) }()
	boundary := func() http.Handler {
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "analysis-result-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("analysis")}}})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	b := &browser{t: t, handler: boundary(), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		for _, definition := range append(toolsdk.AnalysisDefinitions(), agentsdk.ArtifactConversationTools()...) {
			previous = append(previous, identitysdk.ProjectRolePermission{PermissionKey: definition.ActionKey, DataScope: identitysdk.DataScopeOwner})
		}
		return previous
	})
	var conversation agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"analysis-result-e2e"}`, 200).Body.Bytes(), &conversation)
	base := "/agent/conversations/" + conversation.ID
	startedAt := time.Now()
	var run agentsdk.ConversationRun
	request := agentsdk.ConversationSend{ClientMessageID: "analysis-chart-export", Message: "分析销售区域，保存图表并导出 CSV", WriteScope: &agentsdk.ConversationWriteScope{PersonalArtifacts: true}}
	raw, _ := json.Marshal(request)
	_ = json.Unmarshal(b.call("POST", base+"/messages", string(raw), 202).Body.Bytes(), &run)
	acceptedStatus := run.Status
	if run.Terminal() || time.Since(startedAt) > time.Second {
		t.Fatalf("long analysis blocked send: status=%s elapsed=%s", run.Status, time.Since(startedAt))
	}
	select {
	case <-source.started:
	case <-time.After(5 * time.Second):
		t.Fatal("background worker did not start analysis")
	}
	current := accountDecode[agentsdk.ConversationRun](t, b.call("GET", base+"/runs/"+run.ID, "", 200))
	if current.Status != "running" {
		t.Fatalf("durable background state=%s", current.Status)
	}
	close(source.release)
	deadline := time.Now().Add(30 * time.Second)
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		run = accountDecode[agentsdk.ConversationRun](t, b.call("GET", base+"/runs/"+run.ID, "", 200))
	}
	if run.Status != "completed" || len(run.Steps) < 4 {
		t.Fatalf("run=%+v", run)
	}
	var analysisReference *agentsdk.ConversationResultReference
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Name == "analysis_run" && call.ResultTruncated && call.ResultReference != nil {
				copy := *call.ResultReference
				analysisReference = &copy
			}
		}
	}
	if analysisReference == nil {
		t.Fatal("large structured result did not expose a stable read reference")
	}
	var saved strings.Builder
	for offset := 0; ; {
		body, _ := json.Marshal(agentsdk.ConversationResultRead{Reference: *analysisReference, Offset: offset, MaxBytes: 8192})
		slice := accountDecode[agentsdk.ConversationResultSlice](t, b.call("POST", base+"/runs/"+run.ID+"/result", string(body), 200))
		saved.WriteString(slice.JSONText)
		if slice.Complete {
			break
		}
		offset = slice.NextOffset
	}
	var storedReceipt agentsdk.ConversationToolResult
	var storedEnvelope struct {
		Result reportmodel.AnalysisResult `json:"result"`
	}
	if json.Unmarshal([]byte(saved.String()), &storedReceipt) != nil || json.Unmarshal(storedReceipt.Content, &storedEnvelope) != nil || storedEnvelope.Result.Visualization.Chart == nil || storedEnvelope.Result.Coverage.Truncated || len(storedEnvelope.Result.References) != 1 || len(storedEnvelope.Result.Coverage.Missing) != 2 {
		t.Fatal("stored structured analysis result changed")
	}
	page := accountDecode[agentsdk.ConversationArtifactPage](t, b.call("GET", "/agent/artifacts", "", 200))
	if len(page.Items) != 1 || page.Items[0].Kind != "chart" || page.Items[0].SourceRunID != run.ID {
		t.Fatalf("artifact=%+v", page)
	}
	version := accountDecode[agentsdk.ConversationArtifactVersion](t, b.call("GET", "/agent/artifacts/"+page.Items[0].ID, "", 200))
	if version.Content.Chart == nil || len(version.Content.Table.Rows) != 80 || version.Content.Table.Rows[79][1] != nil {
		t.Fatalf("chart=%+v", version)
	}
	var exported agentsdk.ConversationArtifactExport
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Name == "artifact_export" {
				var receipt struct {
					Export agentsdk.ConversationArtifactExport `json:"export"`
				}
				if json.Unmarshal([]byte(call.ResultPreview), &receipt) == nil {
					exported = receipt.Export
				}
			}
		}
	}
	if exported.ID == "" || exported.Format != "csv" || !strings.HasSuffix(exported.Filename, ".csv") {
		t.Fatalf("export=%+v", exported)
	}
	download := b.call("GET", "/agent/artifact-exports/"+exported.ID+"/download", "", 200).Body.String()
	if !strings.Contains(download, "区域,金额") || !strings.Contains(download, "区域-001,1.25") {
		t.Fatalf("csv=%q", download[:min(len(download), 256)])
	}
	source.denied.Store(true)
	b.call("GET", "/agent/artifact-exports/"+exported.ID+"/download", "", 503)
	b.call("POST", "/agent/artifacts/"+page.Items[0].ID+"/exports", `{"client_id":"revoked-export","version":1,"format":"csv"}`, 503)
	revokedRead, _ := json.Marshal(agentsdk.ConversationResultRead{Reference: *analysisReference, Offset: 0, MaxBytes: 8192})
	b.call("POST", base+"/runs/"+run.ID+"/result", string(revokedRead), 403)
	source.denied.Store(false)
	t.Logf("PASS accepted=%s background=%s rows=80 chart=%s export=%s source rechecks deny revoked result/download/export", acceptedStatus, current.Status, version.Content.Chart.Type, exported.SHA256)
	servePersonalToolAcceptance(t, host, options, map[string]func(){"revoke-analysis": func() { source.denied.Store(true) }, "restore-analysis": func() { source.denied.Store(false) }})
}
