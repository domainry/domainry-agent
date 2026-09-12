package product

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/domainry/domainry-agent-sdk/businessrpc"
	identity "github.com/domainry/domainry-identity-sdk"
)

type sharedDescriptor struct {
	identity.Binding
	value identity.Descriptor
}

func (s sharedDescriptor) Descriptor() identity.Descriptor { return s.value }

func TestBusinessEnvironmentPinsServiceAndIdentity(t *testing.T) {
	keys := []string{"AGENT_BUSINESS_ENDPOINT", "AGENT_BUSINESS_SERVICE_TOKEN", "AGENT_BUSINESS_SOURCE_IDENTITY", "AGENT_BUSINESS_CONTRACT_SHA256"}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	application := identity.ApplicationRef{WorkspaceID: "workspace", ApplicationKey: "application"}
	if client, err := OpenBusinessFromEnvironment(t.Context(), "runtime", application, nil); client != nil || err != nil {
		t.Fatal("unconfigured optional business source", err)
	}
	d := businessrpc.Descriptor{ProtocolVersion: businessrpc.ProtocolVersion, ContractSHA256: businessrpc.ContractSHA256(), Scope: businessrpc.Scope{RuntimeID: "runtime", WorkspaceID: "workspace", ApplicationKey: "application", IdentityIssuer: "https://identity.example.test"}, SourceIdentity: "owned-source"}
	var calls atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != businessrpc.CapabilitiesPath || r.Header.Get("Authorization") != "Bearer isolated-business-service-token" || len(r.Cookies()) != 0 {
			t.Error("unexpected handshake or credential boundary")
			http.Error(w, "invalid", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d)
	}))
	defer service.Close()
	values := []string{service.URL, "isolated-business-service-token", d.SourceIdentity, businessrpc.ContractSHA256()}
	shared := sharedDescriptor{value: identity.Descriptor{Issuer: d.Scope.IdentityIssuer, Audience: "application"}}
	for i, key := range keys {
		t.Setenv(key, values[i])
		if i < len(keys)-1 {
			if _, err := OpenBusinessFromEnvironment(t.Context(), "runtime", application, shared); err == nil {
				t.Fatal("incomplete service config accepted")
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("incomplete config contacted service")
	}
	for _, bad := range []identity.Binding{nil, sharedDescriptor{value: identity.Descriptor{Audience: "application"}}, sharedDescriptor{value: identity.Descriptor{Issuer: d.Scope.IdentityIssuer, Audience: "other"}}} {
		if _, err := OpenBusinessFromEnvironment(t.Context(), "runtime", application, bad); err == nil {
			t.Fatal("unbound Identity accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid Identity contacted service")
	}
	client, err := OpenBusinessFromEnvironment(t.Context(), "runtime", application, shared)
	if err != nil || client.Descriptor() != d || calls.Load() != 1 {
		t.Fatal("real SDK handshake failed", err)
	}
	for _, bad := range []struct{ runtime, issuer string }{{"other", d.Scope.IdentityIssuer}, {"runtime", "https://other-identity.example.test"}} {
		other := sharedDescriptor{value: identity.Descriptor{Issuer: bad.issuer, Audience: "application"}}
		if _, err := OpenBusinessFromEnvironment(t.Context(), bad.runtime, application, other); err == nil || strings.Contains(err.Error(), values[1]) {
			t.Fatal("mismatched service scope accepted or secret exposed", err)
		}
	}
	if calls.Load() != 3 {
		t.Fatal("handshake was retried")
	}
}

func TestSharedIdentityEnvironmentRejectsNonOriginBeforeDispatch(t *testing.T) {
	t.Setenv("IDENTITY_ENDPOINT", "")
	application := identity.ApplicationRef{WorkspaceID: "workspace", ApplicationKey: "application"}
	if binding, err := OpenSharedIdentityFromEnvironment(t.Context(), application); binding != nil || err != nil {
		t.Fatal("unconfigured shared Identity", err)
	}
	for _, endpoint := range []string{"https://user:secret@example.test", "http://identity.example.test", "https://identity.example.test/path", "https://identity.example.test?token=secret", "https://identity.example.test#secret"} {
		t.Setenv("IDENTITY_ENDPOINT", endpoint)
		if _, err := OpenSharedIdentityFromEnvironment(t.Context(), application); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid Identity endpoint admitted or secret exposed", err)
		}
	}
}
