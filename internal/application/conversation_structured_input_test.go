package application

import (
	"encoding/json"
	sdk "github.com/domainry/domainry-agent-sdk"
	"strings"
	"testing"
)

func TestPeerStructuredInputValidatedBeforeAdmission(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"currency":{"type":"string","enum":["EUR","CNY"]},"amount":{"type":"number","minimum":0}},"required":["currency","amount"],"additionalProperties":false}`)
	for _, tc := range []struct {
		name         string
		data, schema json.RawMessage
		ok           bool
	}{
		{"valid", json.RawMessage(`{"currency":"EUR","amount":15}`), schema, true},
		{"wrong type", json.RawMessage(`{"currency":"EUR","amount":"15"}`), schema, false},
		{"missing field", json.RawMessage(`{"currency":"EUR"}`), schema, false},
		{"negative", json.RawMessage(`{"currency":"CNY","amount":-1}`), schema, false},
		{"invalid data", json.RawMessage(`not-json`), nil, false},
		{"invalid schema", json.RawMessage(`{}`), json.RawMessage(`{"type":"unknown"}`), false},
		{"bounded", json.RawMessage(`"` + strings.Repeat("x", 16384) + `"`), nil, false},
		{"explicit null", json.RawMessage(`null`), json.RawMessage(`{"type":"null"}`), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConversationStructuredInput(&sdk.ConversationStructuredInput{Schema: tc.schema, Data: tc.data})
			if (err == nil) != tc.ok {
				t.Fatal(err)
			}
		})
	}
}
