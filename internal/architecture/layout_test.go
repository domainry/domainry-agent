package architecture

import (
	"os"
	"strings"
	"testing"
)

func TestPersistenceImplementationStaysInternal(t *testing.T) {
	for _, path := range []string{"../../persistence", "../../infrastructure"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Agent implementation exposes public persistence package %s", path)
		}
	}
}

func TestModuleUsesTaggedDependencies(t *testing.T) {
	content, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "replace ") || strings.Contains(string(content), "../domainry-") {
		t.Fatal("Agent must consume released module tags, not local directory replacements")
	}
}
