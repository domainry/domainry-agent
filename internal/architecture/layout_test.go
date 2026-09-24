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

// Core Agent code may depend on another module's SDK or a public, reusable
// library package, but selecting another module's runnable implementation is a
// composition-root responsibility. This keeps module mode injectable and SaaS
// mode free to bind the same SDK contract to a remote client.
func TestCoreDoesNotImportForeignModuleImplementations(t *testing.T) {
	roots := []string{"../../module", "../application", "../composition", "../dataaccess", "../execution", "../infrastructure"}
	for _, root := range roots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		} else if err != nil {
			t.Fatal(err)
		}
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
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				const domainry = "github.com/domainry/"
				if !strings.HasPrefix(importPath, domainry) {
					continue
				}
				parts := strings.Split(strings.TrimPrefix(importPath, domainry), "/")
				repository := parts[0]
				if repository == "domainry-agent" || repository == "domainry-foundation" || strings.HasSuffix(repository, "-sdk") || len(parts) < 2 {
					if len(parts) < 2 && repository != "domainry-agent" && repository != "domainry-foundation" && !strings.HasSuffix(repository, "-sdk") {
						t.Errorf("%s selects foreign implementation root %s; inject its SDK boundary from a composition root", path, importPath)
					}
					continue
				}
				switch parts[1] {
				case "internal", "module", "remote", "server", "web", "webhost":
					t.Errorf("%s selects foreign implementation %s; inject its SDK boundary from a composition root", path, importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
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

func TestLifecycleOwnershipDoesNotCrossDomainTables(t *testing.T) {
	subject, err := os.ReadFile(filepath.Join("..", "infrastructure", "persistence", "database", "agent", "subject_lifecycle.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []string{"_agent_user_todos", "_agent_todo_mutations", "_agent_artifacts", "_agent_attachments", "_agent_knowledge_"} {
		if strings.Contains(string(subject), foreign) {
			t.Errorf("Agent subject lifecycle reaches another owner's table %q", foreign)
		}
	}
	conversation, err := os.ReadFile(filepath.Join("..", "infrastructure", "persistence", "database", "agent", "conversation_store.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(conversation)
	start := strings.Index(text, "func (s *ConversationStore) DeleteForRequest")
	if start < 0 {
		t.Fatal("conversation deletion implementation was not found")
	}
	end := strings.Index(text[start:], "func (s *ConversationStore) Messages")
	if end < 0 {
		t.Fatal("conversation deletion implementation end was not found")
	}
	if strings.Contains(text[start:start+end], "deleteConversationAttachments") {
		t.Fatal("Agent conversation deletion crossed into Knowledge persistence")
	}
}

func TestExecutionCapacityStaysInsideAgentSDKAndPersistenceBoundary(t *testing.T) {
	application, err := os.ReadFile(filepath.Join("..", "application", "conversation_service.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(application), "persistence.ConversationCapacityRepository") || strings.Contains(string(application), "domainry-runtime") {
		t.Fatal("conversation capacity must use the SDK persistence port without a Runtime dependency")
	}
	store, err := os.ReadFile(filepath.Join("..", "infrastructure", "persistence", "database", "agent", "conversation_capacity_store.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []string{"domainry-runtime", "domainry-identity", "_runtime_", "_identity_"} {
		if strings.Contains(string(store), foreign) {
			t.Fatalf("Agent capacity persistence crossed owner boundary %q", foreign)
		}
	}
}
