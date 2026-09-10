package execution

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type localSchemaLoader struct{}

func (localSchemaLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external schema references are not allowed")
}

// JSON schemas never fetch a URL during execution. A schema may use its own
// local definitions; fetching a remote schema would change a frozen contract.
func CompileSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(raw) == 0 || len(raw) > 64*1024 {
		return nil, fmt.Errorf("invalid schema size")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(localSchemaLoader{})
	compiler.AssertFormat()
	if err = compiler.AddResource("https://agent.invalid/tool.json", doc); err != nil {
		return nil, err
	}
	return compiler.Compile("https://agent.invalid/tool.json")
}

func ValidateJSON(schema *jsonschema.Schema, raw []byte) error {
	if schema == nil {
		return fmt.Errorf("missing schema")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return schema.Validate(value)
}
