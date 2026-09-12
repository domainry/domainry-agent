package conversation

import (
	"errors"
	"reflect"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
)

type hostComposer struct {
	definitions []sdk.ConversationToolDefinition
	assemble    func(sdk.ConversationToolHost) (sdk.ConversationToolHost, error)
}

func (c hostComposer) ConversationToolDefinitions() []sdk.ConversationToolDefinition {
	return c.definitions
}
func (c hostComposer) AssembleConversationTools(h sdk.ConversationToolHost) (sdk.ConversationToolHost, error) {
	return c.assemble(h)
}

type composedHost struct{ sdk.ConversationToolHost }

func TestHostToolCompositionPreservesEarlierAssemblyAndErrors(t *testing.T) {
	base, earlier, final := &composedHost{}, &composedHost{}, &composedHost{}
	order := []string{}
	wantError := errors.New("assembly unavailable")
	for _, failure := range []string{"", "earlier", "host"} {
		t.Run(failure, func(t *testing.T) {
			order = nil
			options := application.ConversationOptions{AssembleTools: func(h sdk.ConversationToolHost) (sdk.ConversationToolHost, error) {
				if h != base {
					t.Fatal("earlier assembly did not receive base host")
				}
				order = append(order, "earlier")
				if failure == "earlier" {
					return nil, wantError
				}
				return earlier, nil
			}}
			composer := hostComposer{assemble: func(h sdk.ConversationToolHost) (sdk.ConversationToolHost, error) {
				if h != earlier {
					t.Fatal("host composer bypassed earlier tool assembly")
				}
				order = append(order, "host")
				if failure == "host" {
					return nil, wantError
				}
				return final, nil
			}}
			options, err := ComposeHostTools(options, composer)
			if err != nil {
				t.Fatal(err)
			}
			got, err := options.AssembleTools(base)
			if failure == "" && (err != nil || got != final) || failure != "" && (!errors.Is(err, wantError) || got != nil) {
				t.Fatalf("host=%v error=%v", got, err)
			}
			want := []string{"earlier", "host"}
			if failure == "earlier" {
				want = want[:1]
			}
			if !reflect.DeepEqual(order, want) {
				t.Fatalf("assembly order: %v", order)
			}
		})
	}
}

func TestHostToolDefinitionsRejectAmbiguityBeforeAssembly(t *testing.T) {
	for _, keys := range [][]string{{""}, {"existing"}, {"report", "report"}} {
		definitions := []sdk.ConversationToolDefinition{}
		for _, key := range keys {
			definitions = append(definitions, sdk.ConversationToolDefinition{Key: key})
		}
		_, err := ComposeHostTools(application.ConversationOptions{ToolDefinitions: []sdk.ConversationToolDefinition{{Key: "existing"}}}, hostComposer{definitions: definitions})
		if err == nil {
			t.Fatalf("ambiguous definitions accepted: %v", keys)
		}
	}
}
