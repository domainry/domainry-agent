package artifact

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func cell(s string) *string { return &s }

func TestVersionContentEditsAreSpecificAndDoNotMutatePriorContent(t *testing.T) {
	original := agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: "# 周报\n## 进展\n完成联调\n## 风险\n待核对费用\n"}
	changed, err := Edit(original, agentsdk.ConversationArtifactPatch{Text: []agentsdk.ConversationArtifactTextEdit{{Find: "## 风险\n待核对费用", Replace: "## 风险\n费用已核对，等待审批"}}})
	if err != nil || !strings.Contains(changed.Markdown, "等待审批") || strings.Contains(original.Markdown, "等待审批") {
		t.Fatal("specific edit changed old content", err)
	}
	for _, find := range []string{"missing", "\n"} {
		if _, err := Edit(original, agentsdk.ConversationArtifactPatch{Text: []agentsdk.ConversationArtifactTextEdit{{Find: find, Replace: "wrong"}}}); err == nil {
			t.Fatal("ambiguous/missing target accepted")
		}
	}
	table := agentsdk.ConversationArtifactContent{Kind: "table", Table: &agentsdk.ConversationArtifactTable{Columns: []agentsdk.ConversationArtifactColumn{{Key: "amount", Label: "金额", Type: "number"}}, Rows: [][]*string{{cell("9007199254740993.01")}, {cell("0.10")}}}}
	changed, err = Edit(table, agentsdk.ConversationArtifactPatch{Cells: []agentsdk.ConversationArtifactCellEdit{{Row: 1, Column: "amount", Value: cell("0.30")}}})
	if err != nil || *changed.Table.Rows[1][0] != "0.30" || *table.Table.Rows[1][0] != "0.10" || *changed.Table.Rows[0][0] != "9007199254740993.01" {
		t.Fatal("table edit lost precision or immutability", err)
	}
	if _, err := Edit(table, agentsdk.ConversationArtifactPatch{Cells: []agentsdk.ConversationArtifactCellEdit{{Row: 0, Column: "amount", Value: cell("1.2")}, {Row: 1, Column: "amount", Value: cell("=SUM(A1)")}}}); err == nil || *table.Table.Rows[0][0] != "9007199254740993.01" {
		t.Fatal("invalid cell edit partially applied")
	}
}

func TestStrictFormatsAndBoundedChartContract(t *testing.T) {
	if _, err := Decode(append([]byte(`{"kind":"markdown","markdown":"`), 0xff, '"', '}')); err == nil {
		t.Fatal("invalid UTF-8 was silently replaced")
	}
	for _, raw := range []string{
		`{"kind":"html","markdown":"<script>run()</script>"}`,
		`{"kind":"markdown","markdown":"ok","script":"run()"}`,
		`{"kind":"markdown","markdown":"ok"} {}`,
		`{"kind":"table","table":{"columns":[{"key":"value","label":"Amount","type":"number"}],"rows":[[1.2]]}}`,
		`{"kind":"chart","table":{"columns":[{"key":"value","label":"Amount","type":"number"}],"rows":[["NaN"]]},"chart":{"type":"line","x_column":"value","y_columns":["value"]}}`,
	} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatal("unsupported or executable document accepted", raw)
		}
	}
	chart := agentsdk.ConversationArtifactContent{Kind: "chart", Table: &agentsdk.ConversationArtifactTable{Columns: []agentsdk.ConversationArtifactColumn{{Key: "day", Label: "日期", Type: "date"}, {Key: "amount", Label: "金额", Type: "number"}}, Rows: [][]*string{{cell("2026-09-10"), cell("123.45")}}}, Chart: &agentsdk.ConversationArtifactChart{Type: "bar", XColumn: "day", YColumns: []string{"amount"}}}
	if _, _, err := Encode(chart); err != nil {
		t.Fatal(err)
	}
	chart.Chart.YColumns = []string{"day"}
	if _, _, err := Encode(chart); err == nil {
		t.Fatal("nonnumeric chart series accepted")
	}
	if _, _, err := Encode(agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: strings.Repeat("x", MaxBytes)}); err == nil {
		t.Fatal("JSON overhead ignored in size limit")
	}
}

func TestCSVExportGuardsFormulaCellsAndPreservesDecimalText(t *testing.T) {
	content := agentsdk.ConversationArtifactContent{Kind: "table", Table: &agentsdk.ConversationArtifactTable{Columns: []agentsdk.ConversationArtifactColumn{{Key: "name", Label: "项目", Type: "text"}, {Key: "amount", Label: "金额", Type: "number"}}, Rows: [][]*string{{cell(" =HYPERLINK(\"https://example.invalid\")"), cell("9007199254740993.01")}, {cell("正常,含逗号"), cell("-0.10")}, {cell("@SUM(A1)"), nil}}}}
	export, err := Export(content, "csv")
	if err != nil || !export.FormulaGuarded || export.Extension != ".csv" {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(export.Data)).ReadAll()
	if err != nil || rows[1][0] != "' =HYPERLINK(\"https://example.invalid\")" || rows[1][1] != "9007199254740993.01" || rows[2][0] != "正常,含逗号" || rows[2][1] != "-0.10" || rows[3][0] != "'@SUM(A1)" || rows[3][1] != "" {
		t.Fatal("CSV export changed data or left formula executable", err)
	}
	if *content.Table.Rows[0][0] != " =HYPERLINK(\"https://example.invalid\")" {
		t.Fatal("export mutated source")
	}
	if _, err := Export(content, "markdown"); err == nil {
		t.Fatal("unsupported conversion accepted")
	}
}
