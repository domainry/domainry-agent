// Package playground provides a local browser fixture for the production
// Conversation HTTP adapter. The fixed test principal is confined to loopback.
package playground

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agenthttp "github.com/domainry/domainry-agent/internal/transport/http/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func NewHandler(service agentsdk.ConversationService, host, model string, files fs.FS) (http.Handler, error) {
	adapter, err := agenthttp.NewConversationAdapter(service, "agent-playground")
	if err != nil {
		return nil, err
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(random[:])
	mux := http.NewServeMux()
	mux.Handle("/agent/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := identitysdk.Principal{Known: true, WorkspaceID: "playground-workspace", UserID: "playground-user", RoleKey: "member"}
		adapter.Handler().ServeHTTP(w, r.WithContext(identitysdk.WithRequestIdentity(r.Context(), identitysdk.RequestIdentity{Principal: principal})))
	}))
	statusHandler := func(w http.ResponseWriter, r *http.Request) {
		ready := false
		if status, ok := service.(agentsdk.ConversationStatusProvider); ok {
			ready = status.ConversationReady(r.Context()) == nil
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ready": ready, "model": model, "mode": "local", "scope": "local-playground", "runtime_id": "agent-playground", "workspace_id": "playground-workspace", "user_id": "playground-user", "name": "本地测试用户", "must_change_password": false})
	}
	mux.HandleFunc("GET /playground/status", statusHandler)
	mux.HandleFunc("GET /app/session", statusHandler)
	mux.HandleFunc("GET /app/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"mode": "local", "workspace_id": "playground-workspace"})
	})
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.FileServer(http.FS(files)).ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "Local playground origin required", 403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{Name: "agent_playground", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		} else {
			cookie, err := r.Cookie("agent_playground")
			if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) != 1 {
				http.Error(w, "Open the local playground page first", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	}), nil
}
