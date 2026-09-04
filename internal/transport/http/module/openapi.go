package module

import agentsdk "github.com/domainry/domainry-agent-sdk"

func (s *adapter) OpenAPIOperations() map[string]map[string]any {
	owned := make(map[string]map[string]any, len(s.openAPI))
	for pattern, operation := range s.openAPI {
		owned[pattern] = operation
	}
	return owned
}

func agentOpenAPIOperations() map[string]map[string]any {
	contract, err := agentsdk.CompileAgentHTTPAdapterContract()
	if err != nil {
		panic(err)
	}
	return contract.OpenAPI
}
