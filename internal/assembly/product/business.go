package product

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/domainry/domainry-agent-sdk/businessrpc"
	identity "github.com/domainry/domainry-identity-sdk"
	identityremote "github.com/domainry/domainry-identity-sdk/remote"
)

// OpenSharedIdentityFromEnvironment opens a public SDK binding to the existing
// Identity owner. The command owns its lifecycle; products borrow it without
// reconciling the owner's permission snapshots.
func OpenSharedIdentityFromEnvironment(ctx context.Context, application identity.ApplicationRef) (identity.Binding, error) {
	config := identityremote.ConfigFromEnvironment()
	if config.Endpoint == "" {
		return nil, nil
	}
	if !serviceOrigin(config.Endpoint) {
		return nil, fmt.Errorf("Identity endpoint must be an HTTPS or loopback HTTP origin")
	}
	config.HTTPClient = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return identityremote.NewFactory(config).Open(ctx, application)
}

// OpenBusinessFromEnvironment consumes only the Agent SDK business-host ports.
// It never opens Runtime, resolves a business database or creates a local user.
func OpenBusinessFromEnvironment(ctx context.Context, runtimeID string, application identity.ApplicationRef, shared identity.Binding) (*businessrpc.Client, error) {
	values := []string{}
	for _, key := range []string{"AGENT_BUSINESS_ENDPOINT", "AGENT_BUSINESS_SERVICE_TOKEN", "AGENT_BUSINESS_SOURCE_IDENTITY", "AGENT_BUSINESS_CONTRACT_SHA256"} {
		values = append(values, strings.TrimSpace(os.Getenv(key)))
	}
	if strings.Join(values, "") == "" {
		return nil, nil
	}
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf("business service requires endpoint, service token, source identity and expected contract SHA256")
		}
	}
	if shared == nil || shared.Descriptor().Issuer == "" || shared.Descriptor().Audience != string(application.ApplicationKey) {
		return nil, fmt.Errorf("business service requires an existing Identity binding for this application")
	}
	return businessrpc.Open(ctx, businessrpc.ClientOptions{BaseURL: values[0], Token: values[1], ExpectedSourceIdentity: values[2], ExpectedContractSHA256: values[3], Scope: businessrpc.Scope{RuntimeID: runtimeID, WorkspaceID: string(application.WorkspaceID), ApplicationKey: string(application.ApplicationKey), IdentityIssuer: shared.Descriptor().Issuer}})
}

func serviceOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" || strings.Contains(raw, "\\") {
		return false
	}
	local := strings.EqualFold(u.Hostname(), "localhost")
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		local = ip.IsLoopback()
	}
	return u.Scheme == "https" || u.Scheme == "http" && local
}
