package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	sdk "github.com/domainry/domainry-agent-sdk"
	module "github.com/domainry/domainry-agent/module"
	gateway "github.com/domainry/domainry-agent/web"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identity "github.com/domainry/domainry-identity/module"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Product is startup composition. No role-specific rules live in this host.
type Product struct {
	CalendarTools, MailTools, WebTools bool
	CalendarWriteTools, MailWriteTools bool
	ReportTools                        bool
	AnalysisTools                      bool
	Key                                string
	UI                                 any
	Agent                              sdk.AgentSchema
	Skills                             []sdk.SkillSchema
	Tools                              []sdk.ConversationToolDefinition
	Prepare                            func(context.Context, *Host) (Binding, error)
}

// Binding belongs to one host instance; a Product definition can be reused.
type Binding struct {
	Handler       http.Handler
	AssembleTools func(sdk.ConversationToolHost) (sdk.ConversationToolHost, error)
}
type ProductOptions struct {
	Host   Options
	Origin string
	Files  fs.FS
	Model  string
}
type ProductHost struct {
	*Host
	Handler http.Handler
}

func OpenProduct(ctx context.Context, p Product, o ProductOptions) (*ProductHost, error) {
	if p.Key == "" || p.Prepare == nil {
		return nil, fmt.Errorf("product definition required")
	}
	o.Host.CalendarTools = o.Host.CalendarTools || p.CalendarTools
	o.Host.MailTools = o.Host.MailTools || p.MailTools
	o.Host.WebTools = o.Host.WebTools || p.WebTools
	o.Host.CalendarWriteTools = o.Host.CalendarWriteTools || p.CalendarWriteTools
	o.Host.MailWriteTools = o.Host.MailWriteTools || p.MailWriteTools
	o.Host.ReportTools = o.Host.ReportTools || p.ReportTools
	o.Host.AnalysisTools = o.Host.AnalysisTools || p.AnalysisTools
	o.Host.ToolDefinitions = p.Tools
	if o.Host.Agent.ConversationOptions.MaxArgumentBytes == 0 && !o.Host.CalendarWriteTools && !o.Host.MailWriteTools {
		o.Host.Agent.ConversationOptions.MaxArgumentBytes = 64 * 1024
	}
	o.Host.Agent.ConversationOptions.Agent = &p.Agent
	o.Host.Agent.ConversationOptions.Skills = p.Skills
	o.Host.Agent.ConversationOptions.ToolDefinitions = p.Tools
	var binding Binding
	o.Host.Agent.ConversationOptions.AssembleTools = func(base sdk.ConversationToolHost) (sdk.ConversationToolHost, error) {
		if binding.AssembleTools == nil {
			return nil, fmt.Errorf("product tools were not assembled")
		}
		return binding.AssembleTools(base)
	}
	o.Host.Prepare = func(ctx context.Context, h *Host) error {
		var err error
		binding, err = p.Prepare(ctx, h)
		return err
	}
	h, err := Open(ctx, o.Host)
	if err != nil {
		return nil, err
	}
	routes := map[string]http.Handler{"/app/product/": binding.Handler, "GET /app/product/config": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.UI)
	})}
	for pattern, handler := range h.ToolSettingsSetupRoutes() {
		routes[pattern] = handler
	}
	for pattern, handler := range h.AccountSetupRoutes() {
		routes[pattern] = handler
	}
	adapters, err := h.IntegrationAdapters()
	if err != nil {
		h.Close(ctx)
		return nil, err
	}
	navigation := map[string]string{}
	if len(adapters) > 0 {
		navigation["/oauth/callback"] = "oauth-callback.html"
	}
	toolAdapters, err := h.ToolSettingsAdapters()
	if err != nil {
		h.Close(ctx)
		return nil, err
	}
	adapters = append(adapters, toolAdapters...)
	handler, err := gateway.NewHandler(gateway.Options{CookiePrefix: strings.ReplaceAll(p.Key, "-", "_"), Identity: h.Identity, Agent: h.Agent, RuntimeID: o.Host.RuntimeID, WorkspaceID: o.Host.WorkspaceID, ApplicationKey: o.Host.ApplicationKey, Origin: o.Origin, Model: o.Model, Files: o.Files, ApplicationRoutes: routes, ModuleAdapters: adapters, NavigationFiles: navigation})
	if err != nil {
		h.Close(ctx)
		return nil, err
	}
	return &ProductHost{Host: h, Handler: handler}, nil
}
func RunProduct(p Product, defaultAddress string) error {
	if strings.TrimSpace(os.Getenv("IDENTITY_ENDPOINT")) == "" {
		for _, key := range []string{"AUTH_JWT_SECRET", "IDENTITY_DATA_SECRET_KEY", "AUTH_DEFAULT_PASSWORD"} {
			if len(strings.TrimSpace(os.Getenv(key))) < 16 {
				return fmt.Errorf("%s must contain at least 16 characters", key)
			}
		}
		if os.Getenv("AUTH_JWT_SECRET") == os.Getenv("IDENTITY_DATA_SECRET_KEY") {
			return fmt.Errorf("signing and data keys must differ")
		}
	}
	env := func(key, fallback string) string {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	address := env("SAAS_ADDRESS", defaultAddress)
	origin := env("SAAS_ORIGIN", "http://"+address)
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		if !strings.HasPrefix(origin, "https://") || os.Getenv("APP_ENV") != "production" {
			return fmt.Errorf("public listener requires HTTPS origin and APP_ENV=production")
		}
	}
	frontend := env("SAAS_FRONTEND", "frontend/dist")
	if _, err := os.Stat(filepath.Join(frontend, "index.html")); err != nil {
		return fmt.Errorf("build product frontend first: %w", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	opts := module.OptionsFromEnvironment()
	opts.ConversationEnabled = true
	runtimeID := env("SAAS_RUNTIME_ID", p.Key)
	workspaceID, applicationKey := env("SAAS_WORKSPACE_ID", p.Key+"-workspace"), env("SAAS_APPLICATION_KEY", p.Key)
	application := identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(workspaceID), ApplicationKey: identitysdk.ApplicationKey(applicationKey)}
	sharedIdentity, err := OpenSharedIdentityFromEnvironment(ctx, application)
	if err != nil {
		return err
	}
	if sharedIdentity != nil {
		defer sharedIdentity.Close(context.Background())
	}
	business, err := OpenBusinessFromEnvironment(ctx, runtimeID, application, sharedIdentity)
	if err != nil {
		return err
	}
	if business != nil {
		opts.ConversationOptions.Business = business
	}
	integration, err := OpenIntegrationFromEnvironment(ctx, runtimeID)
	if err != nil {
		return err
	}
	if integration != nil {
		defer integration.Close(context.Background())
	}
	app, err := OpenProduct(ctx, p, ProductOptions{Host: Options{DatabaseDriver: env("SAAS_DATABASE_DRIVER", "sqlite"), DatabaseDSN: os.Getenv("SAAS_DATABASE_DSN"), DatabaseSchema: os.Getenv("SAAS_DATABASE_SCHEMA"), StoragePath: os.Getenv("SAAS_STORAGE_PATH"), DatabasePath: env("SAAS_DB", "data/"+p.Key+".db"), RuntimeID: runtimeID, WorkspaceID: workspaceID, ApplicationKey: applicationKey, Agent: opts, Identity: identity.OptionsFromEnvironment(), IdentityBinding: sharedIdentity, Integration: integration, WebConnectionKey: env("INTEGRATION_WEB_CONNECTION_KEY", "")}, Origin: origin, Files: os.DirFS(frontend), Model: opts.ConversationModel})
	if err != nil {
		return err
	}
	defer app.Close(context.Background())
	server := &http.Server{Addr: address, Handler: app.Handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	cert, key := os.Getenv("SAAS_TLS_CERT"), os.Getenv("SAAS_TLS_KEY")
	if (cert == "") != (key == "") {
		return fmt.Errorf("both TLS files required")
	}
	go func() {
		if cert != "" {
			done <- server.ListenAndServeTLS(cert, key)
		} else {
			done <- server.ListenAndServe()
		}
	}()
	log.Printf("%s: %s", p.Key, origin)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		return server.Shutdown(shutdown)
	}
}
