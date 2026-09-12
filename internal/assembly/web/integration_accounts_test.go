package web

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	gateway "github.com/domainry/domainry-agent-sdk/browsergateway"
	hostdb "github.com/domainry/domainry-agent/internal/infrastructure/persistence/webhost"
	agentmodule "github.com/domainry/domainry-agent/module"
	connector "github.com/domainry/domainry-connector-sdk"
	connectormodule "github.com/domainry/domainry-connectors/module"
	"github.com/domainry/domainry-foundation/modulehttp"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	integrationhost "github.com/domainry/domainry-integration-sdk/modulehost"
	integrationmodule "github.com/domainry/domainry-integration/module"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

const accountInitial = "Initial-Accounts-Test!2"
const accountChanged = "Changed-Accounts-Test!3"
const accountScope = "https://www.googleapis.com/auth/calendar.readonly"

// This deployment fixture composes only public module/SDK entry points. Identity,
// Integration persistence and the Google provider are real; the token issuer is local.
type accountFixture struct {
	providerTransport connector.Transport
	t                 *testing.T
	options           Options
	host              *Host
	owner             *accountOwnerHost
	issuer            *httptest.Server
	mu                sync.Mutex
	challenges        map[string]string
	grants            map[string]string
	tokenGrants       map[string]string
	exchanges, probes int
}
type accountOwnerHost struct {
	db        *sql.DB
	registrar *hostdb.Registrar
	registry  *connector.Registry
}

func (h *accountOwnerHost) Database() integrationhost.Database { return h.db }
func (h *accountOwnerHost) Dialect() integrationhost.Dialect {
	return h.registrar.Renderer.(integrationhost.Dialect)
}
func (h *accountOwnerHost) Migrations() integrationhost.MigrationRegistrar { return h.registrar }
func (h *accountOwnerHost) Providers() integrationhost.ProviderRegistry    { return h.registry }
func (h *accountOwnerHost) SecretCipher() integrationhost.SecretMaterialCipher {
	return accountFixtureCipher{}
}
func (h *accountOwnerHost) RuntimeTriggers() integration.TriggerSink { return h }

type accountFixtureCipher struct{}

func (accountFixtureCipher) EncryptSecretMaterial(_ context.Context, workspace, key, value string) (string, error) {
	block, _ := aes.NewCipher([]byte(strings.Repeat("f", 32)))
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(value), []byte(workspace+"\x00"+key))), nil
}
func (accountFixtureCipher) DecryptSecretMaterial(_ context.Context, workspace, key, value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, _ := aes.NewCipher([]byte(strings.Repeat("f", 32)))
	aead, _ := cipher.NewGCM(block)
	if len(raw) < aead.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(workspace+"\x00"+key))
	return string(plain), err
}

type accountTransport struct{ fixture *accountFixture }

func (tr accountTransport) ExecuteSQL(context.Context, connector.SQLRequest) (connector.SQLResult, error) {
	return connector.SQLResult{}, errors.New("unexpected SQL")
}
func (tr accountTransport) RoundTripHTTP(ctx context.Context, in connector.HTTPRequest) (connector.HTTPResponse, error) {
	f := tr.fixture
	if in.URL != "https://oauth2.googleapis.com/token" {
		if in.URL != "https://www.googleapis.com/oauth2/v2/userinfo" {
			if f.providerTransport != nil {
				return f.providerTransport.RoundTripHTTP(ctx, in)
			}
			return connector.HTTPResponse{}, errors.New("unexpected provider endpoint")
		}
		f.mu.Lock()
		f.probes++
		granted := f.tokenGrants[strings.TrimPrefix(strings.Join(in.SecretHeaders["Authorization"], ""), "Bearer ")]
		f.mu.Unlock()
		allowed := false
		for _, scope := range strings.Fields(granted) {
			allowed = allowed || scope == "openid" || scope == "https://www.googleapis.com/auth/userinfo.email" || scope == "https://www.googleapis.com/auth/userinfo.profile"
		}
		if !allowed {
			return connector.HTTPResponse{StatusCode: 403, Body: []byte(`{"error":{"code":403,"message":"insufficient scope"}}`)}, nil
		}
		return connector.HTTPResponse{StatusCode: 200, Body: []byte(`{"id":"fixture-user"}`)}, nil
	}
	form, err := url.ParseQuery(string(in.Body))
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	for key, value := range in.SecretForm {
		if form.Has(key) {
			return connector.HTTPResponse{}, errors.New("private field collision")
		}
		form.Set(key, value)
	}
	req, err := http.NewRequestWithContext(ctx, in.Method, f.issuer.URL, strings.NewReader(form.Encode()))
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := f.issuer.Client().Do(req)
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return connector.HTTPResponse{StatusCode: response.StatusCode, Body: body, Headers: response.Header}, err
}

func newAccountFixture(t *testing.T) *accountFixture {
	t.Helper()
	t.Setenv("AUTH_DEFAULT_PASSWORD", accountInitial)
	t.Setenv("AUTH_JWT_SECRET", "account-fixture-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "account-fixture-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	f := &accountFixture{t: t, challenges: map[string]string{}, grants: map[string]string{}, tokenGrants: map[string]string{}, options: Options{DatabasePath: filepath.Join(t.TempDir(), "agent.db"), RuntimeID: "accounts-runtime", WorkspaceID: "accounts-workspace", ApplicationKey: "accounts-product", Agent: agentmodule.Options{ConversationProvider: testModel{}}}}
	f.issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.exchanges++
		if r.Method != "POST" || r.ParseForm() != nil || r.PostForm.Get("client_id") != "fixture-client" || r.PostForm.Get("client_secret") != "fixture-client-secret" {
			w.WriteHeader(400)
			return
		}
		code := r.PostForm.Get("code")
		challenge, ok := f.challenges[code]
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		delete(f.challenges, code)
		w.Header().Set("Content-Type", "application/json")
		token := "fixture-access-" + code
		f.tokenGrants[token] = f.grants[code]
		json.NewEncoder(w).Encode(map[string]any{"access_token": token, "refresh_token": "fixture-refresh", "token_type": "Bearer", "expires_in": 3600, "scope": f.grants[code]})
		delete(f.grants, code)
	}))
	t.Cleanup(func() { f.close(); f.issuer.Close() })
	f.open()
	return f
}
func (f *accountFixture) open() {
	t := f.t
	t.Helper()
	db, err := sql.Open("sqlite", f.options.DatabasePath+".integration")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	registrar := &hostdb.Registrar{DB: db, Renderer: dialect.WithSchema("")}
	if err = registrar.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	providers, err := connectormodule.WorkAccountProviders(accountTransport{f})
	if err != nil {
		t.Fatal(err)
	}
	if f.options.WebTools {
		web, e := connectormodule.PublicWebProviders(accountTransport{f})
		if e != nil {
			t.Fatal(e)
		}
		providers.Providers = append(providers.Providers, web.Providers...)
	}
	registry, err := connectormodule.NewFactory(connectormodule.Options{Providers: providers}).Registry()
	if err != nil {
		t.Fatal(err)
	}
	f.owner = &accountOwnerHost{db, registrar, registry}
	f.options.Integration, err = integrationmodule.NewFactory().OpenModule(t.Context(), integration.ApplicationRef{RuntimeID: f.options.RuntimeID}, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	f.host, err = Open(t.Context(), f.options)
	if err != nil {
		t.Fatal(err)
	}
}
func (f *accountFixture) close() {
	if f.host != nil {
		if err := f.host.Close(context.Background()); err != nil {
			f.t.Error(err)
		}
		f.host = nil
	}
	if f.options.Integration != nil {
		if err := f.options.Integration.Close(context.Background()); err != nil {
			f.t.Error(err)
		}
		f.options.Integration = nil
	}
	if f.owner != nil {
		if err := f.owner.db.Close(); err != nil {
			f.t.Error(err)
		}
		f.owner = nil
	}
}
func (f *accountFixture) boundary(origin string, files fs.FS) http.Handler {
	f.t.Helper()
	adapters, err := f.host.IntegrationAdapters()
	if err != nil {
		f.t.Fatal(err)
	}
	toolAdapters, err := f.host.ToolSettingsAdapters()
	if err != nil {
		f.t.Fatal(err)
	}
	adapters = append(adapters, toolAdapters...)
	routes := f.host.AccountSetupRoutes()
	for key, handler := range f.host.ToolSettingsSetupRoutes() {
		routes[key] = handler
	}
	for key, handler := range f.host.ScheduleRoutes() {
		routes[key] = handler
	}
	handler, err := gateway.NewHandler(gateway.Options{Identity: f.host.Identity, Agent: f.host.Agent, RuntimeID: f.options.RuntimeID, WorkspaceID: f.options.WorkspaceID, ApplicationKey: f.options.ApplicationKey, Origin: origin, Files: files, ModuleAdapters: adapters, ApplicationRoutes: routes, NavigationFiles: map[string]string{"/oauth/callback": "oauth-callback.html"}})
	if err != nil {
		f.t.Fatal(err)
	}
	return handler
}
func (f *accountFixture) grant(admin *browser, user, denied string, shared bool) {
	f.t.Helper()
	mutateTestRolePermissions(f.t, f.host, admin, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		out := []identity.ProjectRolePermission{}
		for _, p := range prior {
			if !strings.HasPrefix(p.PermissionKey, "integration.") {
				out = append(out, p)
			}
		}
		for _, route := range integration.IntegrationHTTPAdapterContract().Routes {
			if !accountAction(route.Action.Key) || route.Action.Key == denied {
				continue
			}
			app := strings.Contains(route.Action.Key, "oauth_applications")
			if app && user != "admin" {
				continue
			}
			scope := identity.DataScopeOwner
			if app || shared {
				scope = identity.DataScopeAll
			}
			out = append(out, identity.ProjectRolePermission{PermissionKey: route.Action.Permission.Key, DataScope: scope})
		}
		return out
	}, user)
}
func (f *accountFixture) callback(raw, outcome string) string {
	f.t.Helper()
	target, err := url.Parse(raw)
	if err != nil || target.Host != "accounts.google.com" {
		f.t.Fatal("invalid official authorization target")
	}
	q := target.Query()
	if q.Get("client_id") != "fixture-client" || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" {
		f.t.Fatal("invalid authorization request")
	}
	callback, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		f.t.Fatal(err)
	}
	params := url.Values{"state": {q.Get("state")}}
	if outcome == "reject" {
		params.Set("error", "access_denied")
	} else {
		code := "fixture-code-" + rand.Text()
		f.mu.Lock()
		f.challenges[code] = q.Get("code_challenge")
		f.grants[code] = q.Get("scope")
		f.mu.Unlock()
		params.Set("code", code)
	}
	callback.RawQuery = params.Encode()
	return callback.String()
}
func accountDecode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func accountJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }

func TestExternalAccountsCurrentIdentityOwnershipAndRestart(t *testing.T) {
	f := newAccountFixture(t)
	files := fstest.MapFS{"index.html": {Data: []byte("chat")}, "oauth-callback.html": {Data: []byte("static callback")}}
	handler := f.boundary("http://127.0.0.1:8091", files)
	admin := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	admin.call("GET", "/integration/connection-accounts", "", 401)
	admin.login("admin@example.com", accountInitial)
	admin.call("GET", "/integration/connection-accounts", "", 403)
	admin.changePassword(accountInitial, accountChanged)
	admin.call("GET", "/integration/connection-accounts", "", 403)
	admin.call("POST", "/app/product/account-setup", `{}`, 200)
	admin.call("POST", "/auth/refresh", `{}`, 200)
	admin.call("GET", "/integration/connections", "", 404)
	admin.call("GET", "/integration/secrets", "", 404)
	app := integration.OAuthApplicationInput{ConnectorKey: "google_workspace", ProviderKey: "google", Name: "Work Google", ClientID: "fixture-client", ClientSecret: "fixture-client-secret", RedirectURI: "http://127.0.0.1:8091/oauth/callback", Scopes: []string{accountScope, "openid"}, Enabled: true}
	saved := admin.call("PUT", "/integration/oauth-applications/work-google", accountJSON(app), 200)
	if strings.Contains(saved.Body.String(), "fixture-client-secret") {
		t.Fatal("write-only secret returned")
	}
	user := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	user.login("system_administrator@example.com", accountInitial)
	user.changePassword(accountInitial, accountChanged)
	userID := user.readSession()["user_id"].(string)
	f.grant(admin, userID, "", false)
	user.call("POST", "/auth/refresh", `{}`, 200)
	user.call("GET", "/integration/oauth-applications", "", 403)
	user.call("POST", "/app/product/account-setup", `{}`, 403)
	start := func(b *browser, scope integration.ConnectionAccountScope) integration.OAuthAuthorizationSession {
		return accountDecode[integration.OAuthAuthorizationSession](t, b.call("POST", "/integration/oauth-authorizations", accountJSON(integration.OAuthAuthorizationInput{ApplicationKey: "work-google", Name: "Personal work", Scope: scope, Scopes: []string{accountScope, "openid"}}), 200))
	}
	session := start(user, integration.ConnectionAccountScopePersonal)
	admin.call("GET", "/integration/oauth-authorizations/"+session.ID, "", 400)
	callback, _ := url.Parse(f.callback(session.AuthorizationURL, "connect"))
	input := integration.OAuthAuthorizationCallback{State: callback.Query().Get("state"), Code: callback.Query().Get("code")}
	admin.call("POST", "/integration/oauth-authorizations/callback", accountJSON(input), 400)
	done := accountDecode[integration.OAuthAuthorizationSession](t, user.call("POST", "/integration/oauth-authorizations/callback", accountJSON(input), 200))
	if done.Status != "connected" || done.Account == nil || done.Account.OwnerUserID != userID {
		t.Fatal("personal ownership missing", done.Status)
	}
	key := done.Account.Key
	admin.call("GET", "/integration/connection-accounts/"+key, "", 400)
	user.call("POST", "/integration/connection-accounts/"+key+"/test", `{}`, 200)
	priorScope := user.scope
	user.scope = admin.scope
	user.call("GET", "/integration/connection-accounts", "", 409)
	user.scope = priorScope
	for _, path := range []string{"/oauth/callback?code=private&state=private", "/app/session", "/integration/connection-accounts"} {
		request := httptest.NewRequest("GET", "http://127.0.0.1:8091"+path, nil)
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := 403
		if strings.HasPrefix(path, "/oauth/callback?") {
			want = 200
			if response.Body.String() != "static callback" || response.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("callback reflected query or allowed referrer")
			}
		}
		if response.Code != want {
			t.Fatal("cross-site navigation boundary", path, response.Code)
		}
	}
	f.grant(admin, userID, integration.ActionIntegrationConnectionAccountsGet, false)
	user.call("GET", "/integration/connection-accounts/"+key, "", 403)
	f.close()
	f.open()
	handler = f.boundary("http://127.0.0.1:8091", files)
	admin.handler = handler
	user.handler = handler
	admin.login("admin@example.com", accountChanged)
	user.login("system_administrator@example.com", accountChanged)
	user.call("GET", "/integration/connection-accounts/"+key, "", 403)
	f.grant(admin, userID, "", false)
	user.call("POST", "/auth/refresh", `{}`, 200)
	user.call("GET", "/integration/connection-accounts/"+key, "", 200)
	replay := accountDecode[integration.OAuthAuthorizationSession](t, user.call("POST", "/integration/oauth-authorizations/callback", accountJSON(input), 200))
	if replay.Account == nil || replay.Account.Key != key {
		t.Fatal("durable callback receipt changed")
	}
	revoked := accountDecode[integration.ConnectionAccount](t, user.call("POST", "/integration/connection-accounts/"+key+"/revoke", accountJSON(map[string]string{"expected_updated_at": done.Account.UpdatedAt}), 200))
	if revoked.Status != "revoked" {
		t.Fatal("revoke not persisted")
	}
	user.call("POST", "/integration/connection-accounts/"+key+"/test", `{}`, 400)
	shared := start(admin, integration.ConnectionAccountScopeWorkspace)
	cb, _ := url.Parse(f.callback(shared.AuthorizationURL, "connect"))
	sharedDone := accountDecode[integration.OAuthAuthorizationSession](t, admin.call("POST", "/integration/oauth-authorizations/callback", accountJSON(integration.OAuthAuthorizationCallback{State: cb.Query().Get("state"), Code: cb.Query().Get("code")}), 200))
	if sharedDone.Account == nil {
		t.Fatal("shared account missing")
	}
	user.call("GET", "/integration/connection-accounts/"+sharedDone.Account.Key, "", 400)
	user.call("POST", "/integration/connection-accounts/"+sharedDone.Account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": sharedDone.Account.UpdatedAt}), 400)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.exchanges != 2 || f.probes != 1 {
		t.Fatal("unexpected provider dispatch counts", f.exchanges, f.probes)
	}
}

func (*accountOwnerHost) Trigger(context.Context, integration.TriggerRequest) (integration.RuntimeExecutionReceipt, error) {
	return integration.RuntimeExecutionReceipt{}, errors.New("unexpected Runtime trigger")
}

func TestExternalAccountsGatewayRejectsUnsafeNavigationAndRoutes(t *testing.T) {
	f := newAccountFixture(t)
	adapters, err := f.host.IntegrationAdapters()
	if err != nil {
		t.Fatal(err)
	}
	options := gateway.Options{Identity: f.host.Identity, Agent: f.host.Agent, RuntimeID: f.options.RuntimeID, WorkspaceID: f.options.WorkspaceID, ApplicationKey: f.options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}, "oauth-callback.html": {Data: []byte("static")}}, ModuleAdapters: adapters}
	for _, path := range []string{"/app", "/auth", "/agent", "/integration", "/integration/callback", "/app/product/landing", "/oauth/{code}", "/oauth/%2e%2e", "/oauth/../callback", "/oauth/\ncallback", "/oauth/\tcallback", "/oauth/callback?code=x"} {
		t.Run(path, func(t *testing.T) {
			current := options
			current.NavigationFiles = map[string]string{path: "oauth-callback.html"}
			if _, err := gateway.NewHandler(current); err == nil {
				t.Fatal("unsafe static landing accepted")
			}
		})
	}
	bad := accountTestAdapter{Adapter: adapters[0], routes: adapters[0].Routes()}
	noPermission := bad.routes[0]
	noPermission.Action.Permission = nil
	bad.routes = []modulehttp.Route{noPermission}
	options.ModuleAdapters = []modulehttp.Adapter{bad}
	if _, err := gateway.NewHandler(options); err == nil {
		t.Fatal("owner command mounted without permission")
	}
	options.ModuleAdapters = append(adapters, adapters...)
	if _, err := gateway.NewHandler(options); err == nil {
		t.Fatal("duplicate owner routes mounted")
	}
}

type accountTestAdapter struct {
	modulehttp.Adapter
	routes []modulehttp.Route
}

func (a accountTestAdapter) Routes() []modulehttp.Route { return a.routes }
