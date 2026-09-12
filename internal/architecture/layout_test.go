package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Applications own policy and orchestration. Concrete storage, transports and
// other service implementations belong behind SDK contracts and composition.
func TestApplicationDoesNotImportAdaptersOrOtherServiceImplementations(t *testing.T) {
	const own = "github.com/domainry/domainry-agent/"
	err := filepath.WalkDir("../application", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(importPath, "github.com/domainry/") {
				continue
			}
			if strings.HasPrefix(importPath, own) {
				local := strings.TrimPrefix(importPath, own)
				for _, adapter := range []string{"internal/infrastructure", "internal/transport", "internal/assembly", "module", "remote", "server", "web"} {
					if local == adapter || strings.HasPrefix(local, adapter+"/") {
						t.Errorf("%s imports concrete adapter %s", path, importPath)
					}
				}
				continue
			}
			module := strings.Split(strings.TrimPrefix(importPath, "github.com/domainry/"), "/")[0]
			// Public deterministic libraries are reusable across module boundaries.
			library := importPath == "github.com/domainry/domainry-tools/timeutil" || importPath == "github.com/domainry/domainry-tools/calculation" || importPath == "github.com/domainry/domainry-knowledge/artifact" || importPath == "github.com/domainry/domainry-knowledge/extraction"
			if !strings.HasSuffix(module, "-sdk") && module != "domainry-foundation" && !library && !strings.HasSuffix(importPath, "/contract") {
				t.Errorf("%s imports another service implementation %s; depend on its public contract", path, importPath)
			}
			if strings.Contains(importPath, "/internal/") {
				t.Errorf("%s bypasses another module's public boundary: %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

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

// Account business protocols belong in Connector SDK / Tools adapters. The
// execution and artifact orchestration must stay generic as families are added.
func TestExecutionDoesNotDependOnAccountBusinessProtocols(t *testing.T) {
	for _, root := range []string{"../application", "../execution", "../assembly/conversation"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				imported, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if strings.HasPrefix(imported, "github.com/domainry/domainry-connector-sdk/") || strings.HasPrefix(imported, "github.com/domainry/domainry-connectors/") || strings.HasPrefix(imported, "github.com/domainry/domainry-tools/internal/") {
					t.Errorf("%s depends on account business adapter/protocol %s", path, imported)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
