package module

import (
	"strings"
	"testing"
)

func TestLibraryKnowledgeConfigurationIsolation(t *testing.T) {
	t.Setenv("TEST_LIBRARY_API_KEY", "fixture-key")
	raw := `[{"library_id":"lib_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","workspace_id":"w","base_url":"https://knowledge.example.test","team_id":"team","kb_id":"kb","api_key_env":"TEST_LIBRARY_API_KEY"}]`
	values, err := knowledgeLibraryEnvironment(raw)
	if err != nil || len(values) != 1 || values[0].Knowledge.APIKey != "fixture-key" {
		t.Fatal("key reference not resolved", err)
	}
	for _, invalid := range []string{raw + `{}`, strings.Replace(raw, `"api_key_env":"TEST_LIBRARY_API_KEY"`, `"api_key":"secret"`, 1), strings.Replace(raw, `TEST_LIBRARY_API_KEY`, `bad-name`, 1)} {
		if _, err := knowledgeLibraryEnvironment(invalid); err == nil {
			t.Fatal("invalid binding config accepted")
		}
	}
	var options ConversationOptions
	if err := assembleLibraryKnowledge(&options, nil, raw, KnowledgeConfig{}); err != nil || len(options.LibraryKnowledge) != 1 {
		t.Fatal(err)
	}
	options = ConversationOptions{}
	if err := assembleLibraryKnowledge(&options, append(values, values...), "", KnowledgeConfig{}); err == nil {
		t.Fatal("same remote KB shared by two bindings")
	}
	options = ConversationOptions{}
	legacy := values[0].Knowledge
	legacy.BaseURL = "https://KNOWLEDGE.example.test:443/"
	if err := assembleLibraryKnowledge(&options, values, "", legacy); err == nil {
		t.Fatal("default source bypasses library membership")
	}
}
