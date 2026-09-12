// Package webhost composes independently deployed products from modules.
// Business services, prompts, Skills and tool selections belong to the caller.
package product

import (
	"context"
	internal "github.com/domainry/domainry-agent/internal/assembly/web"
)

type Options = internal.Options
type ToolAccountRequirement = internal.ToolAccountRequirement
type Host = internal.Host

func Open(ctx context.Context, options Options) (*Host, error) { return internal.Open(ctx, options) }
