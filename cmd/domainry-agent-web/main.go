// Command domainry-agent-web hosts the chat UI with in-process Identity and
// Agent modules. It is separate from the service-to-service SaaS entrypoint.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	productassembly "github.com/domainry/domainry-agent/internal/assembly/product"
	webassembly "github.com/domainry/domainry-agent/internal/assembly/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	webhttp "github.com/domainry/domainry-agent/web"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	externalOptions, err := externalIdentityOptions()
	if err != nil {
		return err
	}
	if externalOptions.ExternalIdentity != nil && strings.TrimSpace(os.Getenv("IDENTITY_ENDPOINT")) != "" {
		return errors.New("configure only one shared or external Identity source")
	}
	if externalOptions.ExternalIdentity == nil && strings.TrimSpace(os.Getenv("IDENTITY_ENDPOINT")) == "" {
		// Identity supports development defaults; a real-user host requires explicit
		// stable secrets instead. They are never written to logs or browser config.
		for _, key := range []string{"AUTH_JWT_SECRET", "IDENTITY_DATA_SECRET_KEY", "AUTH_DEFAULT_PASSWORD"} {
			if len(strings.TrimSpace(os.Getenv(key))) < 16 {
				return fmt.Errorf("%s must contain at least 16 characters", key)
			}
		}
		if os.Getenv("AUTH_JWT_SECRET") == os.Getenv("IDENTITY_DATA_SECRET_KEY") {
			return errors.New("Identity signing and data keys must differ")
		}
	}
	address := env("AGENT_WEB_ADDRESS", "127.0.0.1:8091")
	origin := env("AGENT_WEB_ORIGIN", "http://"+address)
	listenHost, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(listenHost); ip == nil || !ip.IsLoopback() {
		if !strings.HasPrefix(origin, "https://") {
			return errors.New("non-loopback web host requires an HTTPS public origin")
		}
		if value := os.Getenv("APP_ENV"); value != "production" && value != "prod" {
			return errors.New("non-loopback web host requires APP_ENV=production")
		}
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	path := env("AGENT_WEB_DB", filepath.Join(cache, "domainry-agent", "web", "agent.db"))
	frontend := env("AGENT_WEB_FRONTEND", filepath.Join("frontend", "dist"))
	if _, err := os.Stat(filepath.Join(frontend, "index.html")); err != nil {
		return fmt.Errorf("build the chat UI with npm --prefix frontend run build: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runtimeID, workspaceID, applicationKey := env("AGENT_WEB_RUNTIME_ID", "agent-web"), env("AGENT_WEB_WORKSPACE_ID", "agent-workspace"), env("AGENT_WEB_APPLICATION_KEY", "domainry-agent-web")
	agentOptions := agentmodule.OptionsFromEnvironment()
	application := identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(workspaceID), ApplicationKey: identitysdk.ApplicationKey(applicationKey)}
	sharedIdentity, err := productassembly.OpenSharedIdentityFromEnvironment(ctx, application)
	if err != nil {
		return err
	}
	if sharedIdentity != nil {
		defer sharedIdentity.Close(context.Background())
	}
	business, err := productassembly.OpenBusinessFromEnvironment(ctx, runtimeID, application, sharedIdentity)
	if err != nil {
		return err
	}
	if business != nil {
		agentOptions.ConversationOptions.Business = business
	}
	integration, err := productassembly.OpenIntegrationFromEnvironment(ctx, runtimeID)
	if err != nil {
		return err
	}
	if integration != nil {
		defer integration.Close(context.Background())
	}
	var knowledgePermissions map[string]string
	if raw := strings.TrimSpace(os.Getenv("AGENT_WEB_KNOWLEDGE_PERMISSIONS")); raw != "" {
		if len(raw) > 65536 || json.Unmarshal([]byte(raw), &knowledgePermissions) != nil || knowledgePermissions == nil {
			return errors.New("AGENT_WEB_KNOWLEDGE_PERMISSIONS must be a JSON object mapping local permission names to upstream permission IDs")
		}
	}
	options := webassembly.Options{DatabasePath: path, RuntimeID: runtimeID, WorkspaceID: workspaceID, ApplicationKey: applicationKey, Agent: agentOptions, Identity: identitymodule.OptionsFromEnvironment(), IdentityBinding: sharedIdentity, KnowledgePermissions: knowledgePermissions, ExternalIdentity: externalOptions.ExternalIdentity, ExternalRoles: externalOptions.ExternalRoles, Integration: integration}
	options.CalendarTools = true
	options.MailTools = true
	options.WebTools = true
	options.CalendarWriteTools = true
	options.MailWriteTools = true
	options.ReportTools = true
	options.WebConnectionKey = env("INTEGRATION_WEB_CONNECTION_KEY", "")
	host, err := webassembly.Open(ctx, options)
	if err != nil {
		return err
	}
	defer host.Close(context.Background())
	adapters, err := host.IntegrationAdapters()
	if err != nil {
		return err
	}
	navigation := map[string]string{}
	if len(adapters) > 0 {
		navigation["/oauth/callback"] = "oauth-callback.html"
	}
	toolAdapters, err := host.ToolSettingsAdapters()
	if err != nil {
		return err
	}
	adapters = append(adapters, toolAdapters...)
	routes := host.AccountSetupRoutes()
	if routes == nil {
		routes = map[string]http.Handler{}
	}
	for pattern, handler := range host.ToolSettingsSetupRoutes() {
		routes[pattern] = handler
	}
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: runtimeID, WorkspaceID: workspaceID, ApplicationKey: applicationKey, Origin: origin, Model: agentOptions.ConversationModel, Files: os.DirFS(frontend), ModuleAdapters: adapters, NavigationFiles: navigation, ApplicationRoutes: routes})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	certificate, key := os.Getenv("AGENT_WEB_TLS_CERT"), os.Getenv("AGENT_WEB_TLS_KEY")
	if (certificate == "") != (key == "") {
		return errors.New("both AGENT_WEB_TLS_CERT and AGENT_WEB_TLS_KEY are required for TLS")
	}
	go func() {
		if certificate != "" {
			done <- server.ListenAndServeTLS(certificate, key)
		} else {
			done <- server.ListenAndServe()
		}
	}()
	fmt.Printf("Agent web: %s\nIdentity: %s\nSQLite: %s\n", origin, host.Identity.Descriptor().Mode, path)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
