package product

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	integration "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-integration-sdk/remote"
	"github.com/domainry/domainry-integration-sdk/saashost"
	integrationmodule "github.com/domainry/domainry-integration/module"
)

// OpenIntegrationFromEnvironment is deployment composition. The product never
// creates an OAuth client or stores its secret; Integration owns that configuration.
func OpenIntegrationFromEnvironment(ctx context.Context, runtimeID string) (integration.Binding, error) {
	base, token, contract := strings.TrimSpace(os.Getenv("INTEGRATION_SAAS_BASE_URL")), strings.TrimSpace(os.Getenv("INTEGRATION_SAAS_TOKEN")), strings.TrimSpace(os.Getenv("INTEGRATION_SAAS_CONTRACT_SHA256"))
	if base == "" && token == "" && contract == "" {
		return nil, nil
	}
	if base == "" || token == "" || contract == "" {
		return nil, fmt.Errorf("Integration SaaS requires base URL, service token and expected contract SHA256")
	}
	factory := integrationmodule.NewSaaSFactory(remote.NewFactory(remote.Options{BaseURL: base, Token: token, CapabilityContractSHA256: contract, HTTPClient: &http.Client{Timeout: 55 * time.Second}}))
	return factory.(saashost.Factory).OpenSaaS(ctx, integration.ApplicationRef{RuntimeID: runtimeID}, nil)
}
