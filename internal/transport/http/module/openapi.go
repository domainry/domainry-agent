package module

import (
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (s *surface) OpenAPIOperations() map[string]map[string]any {
	all := agentsdk.HTTPSurfaceOpenAPIOperations()
	owned := make(map[string]map[string]any, len(s.routes))
	for _, route := range s.routes {
		if operation := all[strings.TrimSpace(route.Pattern)]; len(operation) != 0 {
			owned[route.Pattern] = operation
		}
	}
	return owned
}

func agentOpenAPIOperations() map[string]map[string]any {
	return agentsdk.HTTPSurfaceOpenAPIOperations()
}
