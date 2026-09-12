package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/modulecapability"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitycapability "github.com/domainry/domainry-identity/capability"
)

// Protocol fixture uses Identity's actual source-owned capability disclosure.
// It does not impersonate a production account or connect to a real endpoint.
type identityFixture struct {
	revoked, unavailable, forged atomic.Bool
	checks                       atomic.Int32
	pauseAt                      atomic.Int32
	entered, release             chan struct{}
}

func configureIdentityFixture(t *testing.T) *identityFixture {
	t.Helper()
	state := &identityFixture{entered: make(chan struct{}), release: make(chan struct{})}
	binding, err := identitycapability.Open(identitycapability.Inputs{})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := binding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := modulecapability.NewHTTPHandler(binding, func(r *http.Request) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	issuer := "http://" + server.Listener.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /identity/discovery", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(identitysdk.Descriptor{ProtocolVersion: identitysdk.CurrentProtocolVersion, BundleVersion: identitysdk.CurrentPolicyBundleVersion, AuthorizationVersion: identitysdk.CurrentAuthorizationContractVersion, Mode: identitysdk.DeploymentModeSaaS, Issuer: issuer, Capabilities: []string{"authentication", "challenge_authentication", "action_assurance", "token_verification", "authorization", "principal_resolution", "identity_projection", "application_registration", "permission_reconciliation"}})
	})
	mux.HandleFunc("POST /identity/principal/resolve", func(w http.ResponseWriter, r *http.Request) {
		var in identitysdk.PrincipalResolutionRequest
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Application.WorkspaceID != "workspace" || in.Application.ApplicationKey != "agent-runtime" || r.Header.Get("Cookie") != "" {
			t.Error("principal request bypassed configured application scope")
			http.Error(w, "invalid", 400)
			return
		}
		if state.checks.Add(1) == state.pauseAt.Load() {
			close(state.entered)
			select {
			case <-state.release:
			case <-r.Context().Done():
				return
			}
		}
		if state.unavailable.Load() {
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]string{"code": "identity.application_credential_invalid"})
			return
		}
		user := string(in.SubjectID)
		if state.forged.Load() {
			user = "another-user"
		}
		json.NewEncoder(w).Encode(identitysdk.PrincipalResolution{
			Principal:    identitysdk.Principal{Known: !state.revoked.Load(), WorkspaceID: "workspace", UserID: user, RoleKey: in.RoleKey},
			AccessBundle: identitysdk.AccessBundle{ContractVersion: identitysdk.CurrentPolicyBundleVersion, ExpiresAt: time.Now().Add(time.Minute), Subject: identitysdk.Subject{WorkspaceID: "workspace", SubjectID: in.SubjectID}},
		})
	})
	mux.Handle("/", capabilities)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/identity/discovery" && r.Header.Get("Authorization") != "Bearer identity-service-key" {
			http.Error(w, "denied", 403)
			return
		}
		mux.ServeHTTP(w, r)
	})
	server.Start()
	t.Cleanup(server.Close)
	for key, value := range map[string]string{"IDENTITY_ENDPOINT": server.URL, "IDENTITY_WORKSPACE_ID": "workspace", "IDENTITY_AUDIENCE": "agent-runtime", "IDENTITY_ISSUER": issuer, "IDENTITY_SERVICE_ACCESS_TOKEN": "identity-service-key", "IDENTITY_CAPABILITY_CONTRACT_SHA256": summary.Identity.ContractSHA256} {
		t.Setenv(key, value)
	}
	return state
}
