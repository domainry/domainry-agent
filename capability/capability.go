// Package capability exposes Agent's source-owned capability contract without
// opening model providers, persistence, workers, or HTTP.
package capability

import (
	internalcapability "github.com/domainry/domainry-agent/internal/capability"
	"github.com/domainry/domainry-foundation/modulecapability"
)

type Inputs struct{}

func Open(Inputs) (*modulecapability.StaticBinding, error) {
	return internalcapability.NewBinding()
}
