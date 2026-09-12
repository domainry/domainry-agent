package application

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type catalogAvailabilityFunc func(context.Context, agentsdk.ConversationAuthority, string) (bool, error)

func (f catalogAvailabilityFunc) ConversationToolAvailable(ctx context.Context, a agentsdk.ConversationAuthority, key string) (bool, error) {
	return f(ctx, a, key)
}

type catalogOnlyHost struct {
	agentsdk.ConversationToolHost
	definitions    []agentsdk.ConversationToolDefinition
	inspectContext func(context.Context)
}

func (h catalogOnlyHost) ConversationTools(ctx context.Context, _ agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	if h.inspectContext != nil {
		h.inspectContext(ctx)
	}
	return h.definitions, nil
}

func TestConversationCatalogBoundsHostAuthorizationDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	host := catalogOnlyHost{definitions: []agentsdk.ConversationToolDefinition{agentsdk.PersonalConversationTools()[1]}, inspectContext: func(actual context.Context) {
		if deadline, ok := actual.Deadline(); !ok || time.Until(deadline) > 30*time.Second || time.Until(deadline) < 29*time.Second {
			t.Error("catalog owner call did not receive its bounded deadline")
		}
	}}
	s := &ConversationService{options: ConversationOptions{ToolHost: host}}
	if _, _, err := s.executionCatalog(ctx, agentsdk.ConversationAuthority{}); err != nil {
		t.Fatal(err)
	}
	s.options.ToolAvailability = catalogAvailabilityFunc(func(check context.Context, _ agentsdk.ConversationAuthority, _ string) (bool, error) {
		if deadline, ok := check.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
			t.Error("connection check was not bounded")
		}
		return true, nil
	})
	if _, _, err := s.executionCatalog(ctx, agentsdk.ConversationAuthority{}); err != nil {
		t.Fatal(err)
	}
}

func TestConversationCatalogAvailabilityPreservesRegisteredContractAndOwnedSnapshot(t *testing.T) {
	var definitions []agentsdk.ConversationToolDefinition
	for _, d := range agentsdk.PersonalConversationTools() {
		if d.Key == "time_now" || d.Key == "calculate" || d.Key == "memory_save" {
			definitions = append(definitions, d)
		}
	}
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "owner"}
	connected := false
	s := &ConversationService{options: ConversationOptions{ToolHost: catalogOnlyHost{definitions: definitions}, ToolAvailability: catalogAvailabilityFunc(func(ctx context.Context, current agentsdk.ConversationAuthority, key string) (bool, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second || current != a {
			t.Fatal("availability lost bounded deadline or authority")
		}
		return key != "calculate" || connected, nil
	})}}
	first, compiled, err := s.executionCatalog(t.Context(), a)
	if err != nil || len(first) != 2 || first[0].Key != "memory_save" || first[1].Key != "time_now" || len(compiled) != 2 {
		t.Fatalf("filtered catalog=%+v err=%v", first, err)
	}
	for _, d := range first {
		for _, registered := range definitions {
			if d.Key == registered.Key && !reflect.DeepEqual(d, registered) {
				t.Fatal("availability changed schemas, action, effects or execution limits")
			}
		}
	}
	frozen, _ := json.Marshal(first)
	connected = true
	second, _, err := s.executionCatalog(t.Context(), a)
	if err != nil || len(second) != 3 || second[0].Key != "calculate" {
		t.Fatal("connection restoration not reflected", err)
	}
	// A host retains its registration buffers; past model snapshots stay owned.
	definitions[0].InputSchema[0] = 'X'
	again, _ := json.Marshal(first)
	if string(frozen) != string(again) {
		t.Fatal("host mutation changed frozen catalog")
	}
}

func TestConversationCatalogDoesNotHideInvalidRegistrationOrLeakAvailabilityErrors(t *testing.T) {
	definition := agentsdk.PersonalConversationTools()[1]
	checks := 0
	s := &ConversationService{options: ConversationOptions{ToolHost: catalogOnlyHost{definitions: []agentsdk.ConversationToolDefinition{definition, definition}}, ToolAvailability: catalogAvailabilityFunc(func(context.Context, agentsdk.ConversationAuthority, string) (bool, error) {
		checks++
		return false, nil
	})}}
	if _, _, err := s.executionCatalog(t.Context(), agentsdk.ConversationAuthority{}); err == nil || checks != 0 {
		t.Fatal("disabled state masked invalid duplicate registration")
	}
	s.options.ToolHost = catalogOnlyHost{definitions: []agentsdk.ConversationToolDefinition{definition}}
	s.options.ToolAvailability = catalogAvailabilityFunc(func(context.Context, agentsdk.ConversationAuthority, string) (bool, error) {
		return false, errors.New("credential=secret-account-material")
	})
	_, _, err := s.executionCatalog(t.Context(), agentsdk.ConversationAuthority{})
	var coded *agentsdk.Error
	if !errors.As(err, &coded) || coded.Code != "agent.conversation.tool_availability_failed" || coded.Message != "" {
		t.Fatalf("unsafe availability error: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = s.conversationToolAvailable(ctx, agentsdk.ConversationAuthority{}, definition.Key); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled availability continued", err)
	}
}
