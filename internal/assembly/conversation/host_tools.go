package conversation

import (
	"fmt"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/internal/application"
)

// ComposeHostTools runs during deferred host binding. Application execution
// receives only the resulting neutral tool host and published definitions.
func ComposeHostTools(options application.ConversationOptions, composer modulehost.ConversationToolComposer) (application.ConversationOptions, error) {
	definitions := composer.ConversationToolDefinitions()
	seen := map[string]bool{}
	for _, d := range options.ToolDefinitions {
		seen[d.Key] = true
	}
	for _, d := range definitions {
		if d.Key == "" || seen[d.Key] {
			return options, fmt.Errorf("duplicate or empty host tool definition: %s", d.Key)
		}
		seen[d.Key] = true
	}
	options.ToolDefinitions = append(append([]sdk.ConversationToolDefinition{}, options.ToolDefinitions...), definitions...)
	previous := options.AssembleTools
	options.AssembleTools = func(base sdk.ConversationToolHost) (sdk.ConversationToolHost, error) {
		if previous != nil {
			var err error
			base, err = previous(base)
			if err != nil {
				return nil, err
			}
		}
		return composer.AssembleConversationTools(base)
	}
	return options, nil
}
