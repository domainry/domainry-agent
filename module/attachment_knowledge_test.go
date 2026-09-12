package module

import (
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

func TestAttachmentKnowledgeConfigurationOwnsPrivateScopeAndRejectsKBReuse(t *testing.T) {
	t.Setenv("TEST_ATTACHMENT_SOURCE_KEY", "synthetic-secret")
	raw := `[{"workspace_id":"w","base_url":"https://knowledge.example.test","team_id":"team","kb_id":"kb","api_key_env":"TEST_ATTACHMENT_SOURCE_KEY","response_mapping":{"search":{"items":"/hits","many":true,"doc_id":"/doc_id","excerpt":"/body"},"fetch":{"items":"/data","doc_id":"/doc_id","excerpt":"/body"}}}]`
	values, err := attachmentKnowledgeEnvironment(raw)
	if err != nil || len(values) != 1 || values[0].APIKey != "synthetic-secret" {
		t.Fatal("environment key resolution failed", err)
	}
	for _, bad := range []string{raw + `{}`, `[]`, `null`, strings.Replace(raw, `"api_key_env":"TEST_ATTACHMENT_SOURCE_KEY"`, `"api_key":"secret"`, 1), strings.Replace(raw, `"workspace_id":"w"`, `"workspace_id":"w","permission_ids":["public"]`, 1)} {
		if _, err = attachmentKnowledgeEnvironment(bad); err == nil {
			t.Fatal("unsafe attachment binding accepted")
		}
	}
	var options ConversationOptions
	if err = assembleAttachmentKnowledge(&options, values, "", "runtime"); err != nil {
		t.Fatal(err)
	}
	source := options.AttachmentKnowledge[0].Knowledge
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "w", UserID: "alice"}
	conversation := "conv_" + strings.Repeat("a", 32)
	first, err := source.ResolveAttachmentKnowledge(t.Context(), conversation, a)
	if err != nil {
		t.Fatal(err)
	}
	if first.Source.KnowledgeDocumentMaxBytes() != 10<<20 {
		t.Fatal("upstream limit lost")
	}
	for _, other := range []agentsdk.ConversationAuthority{{Known: true, RuntimeID: "foreign", WorkspaceID: "w", UserID: "alice"}, {Known: true, RuntimeID: "runtime", WorkspaceID: "foreign", UserID: "alice"}, {RuntimeID: "runtime", WorkspaceID: "w", UserID: "alice"}} {
		if _, err = source.ResolveAttachmentKnowledge(t.Context(), conversation, other); err == nil {
			t.Fatal("foreign authority accepted")
		}
	}
	if _, err = source.ResolveAttachmentKnowledge(t.Context(), "forged", a); err == nil {
		t.Fatal("invalid parent locator accepted")
	}
	rotated := values[0]
	rotated.APIKey = "rotated"
	rotated.BaseURL = "https://KNOWLEDGE.example.test:443/"
	other, err := provider.NewAttachmentKnowledge(rotated, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := other.ResolveAttachmentKnowledge(t.Context(), conversation, a)
	if err != nil || resolved.PermissionID != first.PermissionID || resolved.Source.KnowledgeDocumentAccessPolicySHA256() != first.Source.KnowledgeDocumentAccessPolicySHA256() || other.AttachmentKnowledgeSourceIdentity() != source.AttachmentKnowledgeSourceIdentity() {
		t.Fatal("key rotation or URL alias changed private identity", err)
	}
	for _, kind := range []string{"default", "library", "catalog", "attachment"} {
		t.Run(kind, func(t *testing.T) {
			var existing ConversationOptions
			base, err := provider.NewKnowledge(values[0])
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "default":
				existing.Knowledge = base
			case "library":
				existing.LibraryKnowledge = []LibraryKnowledgeBinding{{WorkspaceID: "w", LibraryID: "lib_" + strings.Repeat("b", 32), Source: base}}
			case "catalog":
				if err = assembleKnowledgeDatasources(&existing, []KnowledgeDatasourceConfig{{Key: "reserved", Name: "Reserved", Knowledge: values[0]}}, "", "runtime"); err != nil {
					t.Fatal(err)
				}
			case "attachment":
				foreign := rotated
				foreign.WorkspaceID = "another-workspace"
				if err = assembleAttachmentKnowledge(&existing, []KnowledgeConfig{values[0], foreign}, "", "runtime"); err == nil {
					t.Fatal("attachment KB reused across workspaces")
				}
				return
			}
			if err = assembleAttachmentKnowledge(&existing, []KnowledgeConfig{rotated}, "", "runtime"); err == nil {
				t.Fatal("attachment KB reused by", kind)
			}
		})
	}
}
