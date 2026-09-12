// Package webhost is the public facade for product-neutral SaaS composition.
package webhost

import (
	"context"
	assembly "github.com/domainry/domainry-agent/internal/assembly/product"
	transport "github.com/domainry/domainry-agent/internal/transport/http/product"
	tools "github.com/domainry/domainry-tools/module"
	"net/http"
)

type Options = assembly.Options
type ToolAccountRequirement = assembly.ToolAccountRequirement
type Host = assembly.Host
type Product = assembly.Product
type Binding = assembly.Binding
type ProductOptions = assembly.ProductOptions
type ProductHost = assembly.ProductHost

func Open(ctx context.Context, options Options) (*Host, error) { return assembly.Open(ctx, options) }
func OpenProduct(ctx context.Context, p Product, o ProductOptions) (*ProductHost, error) {
	return assembly.OpenProduct(ctx, p, o)
}
func RunProduct(p Product, defaultAddress string) error {
	return assembly.RunProduct(p, defaultAddress)
}
func ConfigureRecords(key string, ui any, agentJSON, skillsJSON []byte, specs ...tools.RecordSpec) (Product, error) {
	return assembly.ConfigureRecords(key, ui, agentJSON, skillsJSON, specs...)
}
func RecordRoutes(runtime string, adapters ...*tools.RecordAdapter) http.Handler {
	return transport.RecordRoutes(runtime, adapters...)
}
