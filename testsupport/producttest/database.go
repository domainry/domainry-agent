package producttest

import (
	"github.com/domainry/domainry-agent/webhost"
	"github.com/domainry/domainry-orm/query"
	"strings"
	"testing"
)

// Real DDL acceptance for the nested Metadata module: preserve full Unicode
// keys, distinguish suffixes beyond an index prefix, and reject a duplicate
// natural key even if an external writer supplies a different row ID.
func verifyMetadataNaturalKey(t *testing.T, h *webhost.ProductHost) {
	t.Helper()
	tx, err := h.Database().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	key := strings.Repeat("界", 254)
	insert := func(id, entityKey string) error {
		statement, args, err := query.NewInsertBuilder(h.Dialect(), "_metadata_localized_texts").Columns(
			"id", "workspace_id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at",
		).Values(id, "schema-test", "entity", entityKey, "title", "zh-CN", "数据库验收 😀", "test", "fixture", "2026-09-11", "2026-09-11").Build()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(t.Context(), statement, args...)
		return err
	}
	if err = insert("first", key+"甲"); err != nil {
		t.Fatal("full Unicode natural key", err)
	}
	if err = insert("second", key+"乙"); err != nil {
		t.Fatal("natural-key suffix conflated", err)
	}
	statement, args, err := query.NewUpdateBuilder(h.Dialect(), "_metadata_localized_texts").Set("text", "数据库验收 😀").Where(query.And(query.Equal("workspace_id", "schema-test"), query.Equal("id", "first"))).Build()
	if err != nil {
		t.Fatal(err)
	}
	result, err := tx.ExecContext(t.Context(), statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("unchanged update must report the matched row: %d %v", count, err)
	}
	if err = insert("third", key+"甲"); err == nil {
		t.Fatal("duplicate natural key accepted")
	}
}
