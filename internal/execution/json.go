package execution

import (
	"encoding/json"
	shared "github.com/domainry/domainry-tools-sdk/schema"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func CompileSchema(raw json.RawMessage) (*jsonschema.Schema, error) { return shared.CompileSchema(raw) }
func ValidateJSON(s *jsonschema.Schema, raw []byte) error           { return shared.ValidateJSON(s, raw) }
