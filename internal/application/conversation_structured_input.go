package application

import (
	"encoding/json"
	sdk "github.com/domainry/domainry-agent-sdk"
)

func validateConversationStructuredInput(in *sdk.ConversationStructuredInput) error {
	if in == nil {
		return nil
	}
	if len(in.Data) == 0 || len(in.Data) > 16384 || !json.Valid(in.Data) || len(in.Schema) > 16384 {
		return conversationFailure("bad_request", "structured_input_invalid")
	}
	if len(in.Schema) > 0 {
		compiled, err := compileConversationSchema(in.Schema)
		if err != nil {
			return conversationFailure("bad_request", "input_schema_invalid")
		}
		if validateToolJSON(compiled, in.Data) != nil {
			return conversationFailure("bad_request", "input_schema_mismatch")
		}
	}
	return nil
}
