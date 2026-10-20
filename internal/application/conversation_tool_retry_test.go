package application

import (
	"context"
	"errors"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

func TestConversationReadToolRetriesUnavailableFailures(t *testing.T) {
	attempts := 0
	result, err := invokeConversationToolWithRetry(t.Context(), "read", func() (agentsdk.ConversationToolResult, error) {
		attempts++
		if attempts < 3 {
			return agentsdk.ConversationToolResult{}, &agentsdk.Error{Class: "unavailable", Code: "runtime.conversation.business_unavailable"}
		}
		return agentsdk.ConversationToolResult{Status: "completed"}, nil
	})
	if err != nil || attempts != 3 || result.Status != "completed" {
		t.Fatalf("attempts=%d result=%#v err=%v", attempts, result, err)
	}
}

func TestConversationWriteToolDoesNotRetryUnavailableFailures(t *testing.T) {
	attempts := 0
	_, err := invokeConversationToolWithRetry(t.Context(), "write", func() (agentsdk.ConversationToolResult, error) {
		attempts++
		return agentsdk.ConversationToolResult{}, &toolsdk.Error{Class: "unavailable", Code: "provider.unavailable", Retryable: true}
	})
	if err == nil || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestConversationReadToolDoesNotRetryDeterministicFailures(t *testing.T) {
	attempts := 0
	_, err := invokeConversationToolWithRetry(t.Context(), "read", func() (agentsdk.ConversationToolResult, error) {
		attempts++
		return agentsdk.ConversationToolResult{}, &toolsdk.Error{Class: "not_found", Code: "crm.context.get_record_not_found"}
	})
	if err == nil || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestConversationToolFailureCodePreservesOnlyBoundedCodes(t *testing.T) {
	if code := conversationToolFailureCode(&toolsdk.Error{Class: "unavailable", Code: "runtime.conversation.business_unavailable"}); code != "runtime.conversation.business_unavailable" {
		t.Fatalf("code=%q", code)
	}
	if code := conversationToolFailureCode(&toolsdk.Error{Code: "unsafe code with spaces"}); code != "tool_failed" {
		t.Fatalf("unsafe code=%q", code)
	}
	if code := conversationToolFailureCode(context.DeadlineExceeded); code != "tool_timeout" {
		t.Fatalf("deadline code=%q", code)
	}
	if conversationToolRetryable(t.Context(), errors.New("deterministic")) {
		t.Fatal("deterministic failure was retryable")
	}
}
