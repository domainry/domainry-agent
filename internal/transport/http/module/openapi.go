package module

import (
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (s *surface) OpenAPIOperations() map[string]map[string]any {
	all := agentsdk.HTTPSurfaceOpenAPIOperations()
	owned := make(map[string]map[string]any, len(s.routes))
	for _, route := range s.routes {
		pattern := strings.TrimSpace(route.Pattern())
		if operation := all[pattern]; len(operation) != 0 {
			owned[pattern] = operation
		}
	}
	return owned
}

func agentOpenAPIOperations() map[string]map[string]any {
	return agentsdk.HTTPSurfaceOpenAPIOperations()
}
