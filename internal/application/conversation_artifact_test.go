package application

import (
	"fmt"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/execution"
	"github.com/domainry/domainry-knowledge/artifact"
)

func TestArtifactToolSchemasKeepAuthorityAndStorageHostOwned(t *testing.T) {
	catalog, err := compileConversationTools(agentsdk.ArtifactConversationTools())
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		tool, input string
		valid       bool
	}{
		{"artifact_create", `{"title":"周报","content":{"kind":"markdown","markdown":"本周进度"}}`, true},
		{"artifact_create", `{"title":"费用","content":{"kind":"table","table":{"columns":[{"key":"value","label":"金额","type":"number"}],"rows":[["9007199254740993.01"],[null]]}}}`, true},
		{"artifact_create", `{"title":"费用","content":{"kind":"table","table":{"columns":[{"key":"value","label":"金额","type":"number"}],"rows":[[9007199254740993.01]]}}}`, false},
		{"artifact_create", `{"title":"周报","content":{"kind":"markdown","markdown":"x"},"source_run_id":"run"}`, false},
		{"artifact_create", `{"title":"周报","content":{"kind":"markdown","markdown":"x"},"sources":{"version":1,"runs":[]}}`, false},
		{"artifact_create", `{"title":"周报","content":{"kind":"markdown","markdown":"x"},"body_ref":"content_fake"}`, false},
		{"artifact_edit", `{"id":"art_123","expected_version":2,"patch":{"text":[{"find":"第二节","replace":"新第二节"}]}}`, true},
		{"artifact_edit", `{"id":"art_123","expected_version":2,"patch":{"owner_user_id":"other"}}`, false},
		{"artifact_read", `{"id":"art_123","version":2,"offset":0,"max_bytes":1024}`, true},
		{"artifact_read", `{"id":"art_123","authority":{"user_id":"other"}}`, false},
		{"artifact_export", `{"id":"art_123","version":1,"format":"csv"}`, true},
		{"artifact_export", `{"id":"art_123","version":0,"format":"markdown"}`, false},
		{"artifact_export", `{"id":"art_123","version":1,"format":"csv","path":"/tmp/file"}`, false},
	} {
		err := execution.ValidateJSON(catalog[sample.tool].input, []byte(sample.input))
		if (err == nil) != sample.valid {
			t.Errorf("%s valid=%v: %s: %v", sample.tool, sample.valid, sample.input, err)
		}
	}
}

func TestArtifactReadPagesPreserveUTF8AndExplicitWideTableProjection(t *testing.T) {
	value := agentsdk.ConversationArtifactVersion{Content: agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: strings.Repeat("汉", 100)}}
	first, err := artifactReadPage(value, agentsdk.ConversationArtifactRead{MaxBytes: 256})
	if err != nil || first.NextOffset != 255 || first.Complete {
		t.Fatal("UTF-8 page boundary failed", err)
	}
	second, err := artifactReadPage(value, agentsdk.ConversationArtifactRead{Offset: first.NextOffset, MaxBytes: 256})
	if err != nil || !second.Complete || first.Markdown+second.Markdown != value.Content.Markdown {
		t.Fatal("Markdown pagination lost data", err)
	}
	if _, err = artifactReadPage(value, agentsdk.ConversationArtifactRead{Offset: 1}); err == nil {
		t.Fatal("mid-rune offset accepted")
	}
	table := &agentsdk.ConversationArtifactTable{Rows: [][]*string{{}}}
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("column%d", i)
		table.Columns = append(table.Columns, agentsdk.ConversationArtifactColumn{Key: key, Label: key, Type: "text"})
		text := strings.Repeat("x", 14000)
		table.Rows[0] = append(table.Rows[0], &text)
	}
	value.Content = agentsdk.ConversationArtifactContent{Kind: "table", Table: table}
	if _, _, err = artifact.Encode(value.Content); err != nil {
		t.Fatal("fixture must be a valid stored artifact", err)
	}
	wide, err := artifactReadPage(value, agentsdk.ConversationArtifactRead{})
	if err != nil || !wide.RowTooLarge || wide.Complete || wide.NextOffset != 0 || len(wide.Table.Rows) != 0 || len(wide.Table.Columns) != 50 {
		t.Fatal("wide row was truncated or falsely marked complete", err)
	}
	selected, err := artifactReadPage(value, agentsdk.ConversationArtifactRead{Columns: []string{"column0", "column1"}})
	if err != nil || !selected.Complete || !selected.Projected || len(selected.Table.Rows) != 1 || len(selected.Table.Rows[0]) != 2 || *selected.Table.Rows[0][0] != *table.Rows[0][0] {
		t.Fatal("explicit column projection lost exact values", err)
	}
	if len(value.Content.Table.Columns) != 50 || len(value.Content.Table.Rows[0]) != 50 {
		t.Fatal("paging mutated the saved content")
	}
}
