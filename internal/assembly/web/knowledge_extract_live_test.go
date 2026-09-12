package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	agentpersistence "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	"github.com/domainry/domainry-orm/query"
)

func TestLiveKnowledgeExtractionFileFormatsIdentityHTTP(t *testing.T) {
	if os.Getenv("AGENT_EXTRACTION_FORMAT_LIVE") != "1" {
		t.Skip("requires explicit synthetic PDF/Word/Excel upload and real model acceptance")
	}
	root := os.Getenv("AGENT_LIVE_EVIDENCE_DIR")
	if !filepath.IsAbs(root) || provider.KnowledgeConfigFromEnvironment().TeamID != "1470194374940573696" {
		t.Fatal("verified team and durable evidence directory required")
	}
	formats := []string{"pdf", "docx"}
	if selected := os.Getenv("AGENT_EXTRACTION_FORMATS"); selected != "" {
		formats = strings.Split(selected, ",")
	}
	seen := map[string]bool{}
	for _, format := range formats {
		if (format != "pdf" && format != "docx") || seen[format] {
			t.Fatal("invalid selected synthetic format")
		}
		seen[format] = true
		t.Run(format, func(t *testing.T) {
			t.Setenv("AGENT_LIVE_EVIDENCE_DIR", filepath.Join(root, format))
			runManagedPrivateDocumentIdentityHTTP(t, true, "personal", format)
		})
	}
}

// Observe persisted results in this new test host using ORM, after the real
// public conversation has completed. These are not model-provided assertions.
func verifyLiveKnowledgeExtraction(t *testing.T, host *Host, format string, conversationID, runID, libraryID, docID string) []agentsdk.KnowledgeExtractionResult {
	t.Helper()
	renderer, err := agentpersistence.Renderer("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	statement, args, err := query.NewSelectBuilder(renderer, "_agent_conversation_tool_calls").Columns("payload_json").Where(query.And(query.Equal("conversation_id", conversationID), query.Equal("run_id", runID))).Build()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := host.db.QueryContext(t.Context(), statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	results := []agentsdk.KnowledgeExtractionResult{}
	fields := map[string]agentsdk.KnowledgeExtractionCell{}
	items := map[string]map[string]string{}
	checkLocation := func(cell agentsdk.KnowledgeExtractionCell, address string, row int) {
		t.Helper()
		if format != "xlsx" {
			return
		}
		if len(cell.Evidence) == 0 || cell.Evidence[0].Location == nil || *cell.Evidence[0].Location != (agentsdk.DocumentLocation{Sheet: "Synthetic Invoice", Row: row, Cell: address}) {
			t.Fatalf("actual XLSX cell location missing: field=%s expected=%s", cell.Key, address)
		}
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var call persistence.ConversationToolExecution
		if json.Unmarshal(raw, &call) != nil {
			t.Fatal("invalid persisted tool record")
		}
		if call.Call.Name != "knowledge_extract" || call.Result == nil || call.Result.Status != "completed" {
			continue
		}
		var result agentsdk.KnowledgeExtractionResult
		if json.Unmarshal(call.Result.Content, &result) != nil || result.LibraryID != libraryID || result.DocumentID != docID || result.Data.Coverage.OriginalComplete {
			t.Fatal("invalid extraction provenance")
		}
		results = append(results, result)
		for _, field := range result.Data.Fields {
			if field.Status == "valid" || field.Status == "missing" {
				fields[field.Key] = field
			}
		}
		for _, table := range result.Data.Tables {
			if table.Key != "items" {
				continue
			}
			for _, row := range table.Rows {
				values := map[string]string{}
				for _, cell := range row {
					if cell.Status == "valid" && cell.Value != nil {
						values[cell.Key] = *cell.Value
						if len(cell.Evidence) == 0 || cell.Evidence[0].CitationID == "" {
							t.Fatal("table value lacks source")
						}
					}
				}
				if len(values) == 3 {
					physicalRow := 10
					if values["item"] == "Paper" {
						physicalRow = 11
					}
					for _, cell := range row {
						column := map[string]string{"item": "A", "quantity": "B", "price": "C"}[cell.Key]
						checkLocation(cell, column+map[int]string{10: "10", 11: "11"}[physicalRow], physicalRow)
					}
					items[values["item"]] = values
				}
			}
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if raw, e := json.MarshalIndent(results, "", "  "); e == nil {
		if e = os.WriteFile(filepath.Join(os.Getenv("AGENT_LIVE_EVIDENCE_DIR"), "extraction-results.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if len(results) == 0 {
		t.Fatal("real model never completed knowledge_extract")
	}
	for key, expected := range map[string]string{"customer": "Qinghe Fixture", "amount": "9007199254740993.25", "signed_date": "2026-09-11", "approval": "true"} {
		field := fields[key]
		if field.Status != "valid" || field.Value == nil || *field.Value != expected || len(field.Evidence) == 0 || field.Evidence[0].CitationID == "" {
			t.Fatalf("real extracted field %s not verified: status=%s", key, field.Status)
		}
		address := map[string]string{"customer": "B3", "amount": "B4", "signed_date": "B5", "approval": "B6"}[key]
		row := map[string]int{"customer": 3, "amount": 4, "signed_date": 5, "approval": 6}[key]
		checkLocation(field, address, row)
	}
	if email := fields["email"]; email.Status != "missing" || email.Value != nil {
		t.Fatal("missing email was invented or omitted from schema")
	}
	if len(items) != 2 || items["Pencil"]["quantity"] != "2" || items["Pencil"]["price"] != "1.2" || items["Paper"]["quantity"] != "3" || items["Paper"]["price"] != "12.5" {
		t.Fatal("real parsed table not extracted exactly")
	}
	return results
}
