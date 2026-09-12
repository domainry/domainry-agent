package producttest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	module "github.com/domainry/domainry-agent/module"
	"github.com/domainry/domainry-agent/webhost"
)

// VerifyToolSettingsBrowser runs the delivered product, its exact Agent/Skills
// and built frontend. Each owner invokes this from its own assembly tests.
func VerifyToolSettingsBrowser(t *testing.T, newProduct func() (webhost.Product, error)) {
	t.Helper()
	if os.Getenv("AGENT_PRODUCT_TOOLS_BROWSER") != "1" {
		t.Skip("opt-in delivered product Tools browser acceptance")
	}
	t.Setenv("AUTH_DEFAULT_PASSWORD", "Product-Initial-Test-Password!2")
	t.Setenv("AUTH_JWT_SECRET", "product-test-signing-key-32-bytes-long")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "product-test-data-key-32-bytes-long")
	t.Setenv("APP_ENV", "development")
	product, err := newProduct()
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../../..")
	output := filepath.Join(os.Getenv("AGENT_UI_TEST_OUTPUT"), product.Key)
	if err = os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Tools []struct{ Function struct{ Name string } }
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		keys := []string{}
		for _, tool := range in.Tools {
			keys = append(keys, tool.Function.Name)
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": "当前可用工具：" + strings.Join(keys, "、")}, "finish_reason": "stop"}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	defer upstream.Close()
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	options := webhost.ProductOptions{Origin: origin, Files: os.DirFS(filepath.Join(root, "frontend/dist")), Host: webhost.Options{DatabasePath: filepath.Join(t.TempDir(), "product.db"), RuntimeID: product.Key + "-tools-runtime", WorkspaceID: product.Key + "-tools-workspace", ApplicationKey: product.Key, Agent: module.Options{ConversationURL: upstream.URL, ConversationModel: "settings-fixture", ConversationOptions: module.ConversationOptions{Poll: 5 * time.Millisecond}}}}
	var host *webhost.ProductHost
	open := func() {
		t.Helper()
		var err error
		host, err = webhost.OpenProduct(t.Context(), product, options)
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { host.Close(context.Background()) }()
	var gate sync.RWMutex
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__acceptance/restart" {
			gate.Lock()
			defer gate.Unlock()
			if err := host.Close(context.Background()); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			open()
			w.WriteHeader(204)
			return
		}
		gate.RLock()
		defer gate.RUnlock()
		host.Handler.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	scope := ""
	call := func(method, path, body string) {
		t.Helper()
		r, _ := http.NewRequest(method, origin+path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Agent-Scope", scope)
		r.Header.Set("Idempotency-Key", fmt.Sprintf("tools-product-%d", time.Now().UnixNano()))
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			t.Fatalf("bootstrap %s: %d %s", path, resp.StatusCode, raw)
		}
		if path == "/app/session" {
			var s struct{ Scope string }
			if json.NewDecoder(resp.Body).Decode(&s) != nil {
				t.Fatal("session")
			}
			scope = s.Scope
		}
	}
	call("POST", "/auth/login", `{"login":"admin@example.com","password":"Product-Initial-Test-Password!2"}`)
	call("GET", "/app/session", "")
	call("POST", "/auth/password/change", `{"current_password":"Product-Initial-Test-Password!2","new_password":"Product-Changed-Test-Password!3"}`)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	script := filepath.Join(root, "../domainry-agent/frontend/tests/product-tool-settings.browser.mjs")
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), script)
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_PRODUCT_KEY="+product.Key, "AGENT_PRODUCT_TOOLS_OUTPUT="+output)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal("product Tools browser", err)
	}
}
