package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
)

type sealedBusinessSourceFixture struct {
	businessSourceFixture
	proof     string
	sealError error
	seals     atomic.Int32
	checks    atomic.Int32
}

func (f *sealedBusinessSourceFixture) SealBusinessEvidence(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) (string, error) {
	f.seals.Add(1)
	if e.HostProof != "" {
		return "", fmt.Errorf("Agent must not supply its own host proof")
	}
	if err := f.businessSourceFixture.RevalidateBusiness(ctx, e, a); err != nil {
		return "", err
	}
	return f.proof, f.sealError
}

func (f *sealedBusinessSourceFixture) RevalidateBusiness(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	f.checks.Add(1)
	if e.HostProof != f.proof {
		return &agentsdk.Error{Class: "forbidden", Code: "host_proof_missing"}
	}
	return f.businessSourceFixture.RevalidateBusiness(ctx, e, a)
}

func TestBusinessHostProofPersistsAcrossModelFailureAndServiceReconstruction(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy := personalReadAuthorizer{}
	source := &sealedBusinessSourceFixture{proof: "opaque-source-integrity-proof"}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var frozen []byte
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		switch n {
		case 1:
			return resultToolCall("get_record", "read", agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "customer-2", Fields: []string{"name"}}), nil
		case 2:
			var result agentsdk.ConversationToolResult
			if err := json.Unmarshal([]byte(in.Messages[len(in.Messages)-1].Content), &result); err != nil {
				t.Error(err)
			}
			var e agentsdk.ConversationBusinessEvidence
			if err := json.Unmarshal(result.Content, &e); err != nil || e.HostProof != source.proof {
				t.Errorf("proof not attached to stored result: %v", err)
			}
			frozen = mustJSON(in)
			return agentsdk.ConversationStepResult{}, fmt.Errorf("model disconnected")
		case 3:
			if string(frozen) != string(mustJSON(in)) {
				t.Error("frozen input changed on resume")
			}
			return (&executionModel{}).answerResult(), nil
		default:
			return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected model call")
		}
	}}
	options := application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source}
	service, err := application.NewConversationService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "sealed-read"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "sealed-read", Message: "读取客户"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.ErrorCode != "provider_failed" {
		t.Fatal(done)
	}
	service.Close()
	service, err = application.NewConversationService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.Status != "completed" {
		t.Fatal(done)
	}
	if source.seals.Load() != 1 || source.checks.Load() < 2 {
		t.Fatalf("seal=%d checks=%d", source.seals.Load(), source.checks.Load())
	}
	source.revoked.Store(true)
	page, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(page.Items) != 2 || page.Items[1].AccessError == "" {
		t.Fatal(page, err)
	}
}

func TestBusinessHostSealingFailureNeverEntersModelAsSource(t *testing.T) {
	for _, tc := range []struct {
		name, proof, code string
		err               error
	}{
		{"source changed", "", "business_access_denied", &agentsdk.Error{Class: "forbidden", Code: "PRIVATE-SEAL-ERROR"}},
		{"oversized proof", strings.Repeat("s", 4097), "business_response_invalid", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			policy := personalReadAuthorizer{}
			source := &sealedBusinessSourceFixture{proof: tc.proof, sealError: tc.err}
			host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				if n == 1 {
					return resultToolCall("get_record", "read", agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "customer-2", Fields: []string{"name"}}), nil
				}
				last := in.Messages[len(in.Messages)-1].Content
				if !strings.Contains(last, tc.code) || strings.Contains(last, "客户乙") || strings.Contains(last, "PRIVATE-SEAL-ERROR") {
					t.Errorf("failed source leaked or wrong feedback: %s", last)
				}
				return (&executionModel{}).answerResult(), nil
			}}
			service, err := application.NewConversationService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "failed-seal"}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "failed-seal", Message: "读取客户"}, a)
			if err != nil {
				t.Fatal(err)
			}
			if done := waitConversation(t, service, c.ID, run.ID); done.Status != "completed" {
				t.Fatal(done)
			}
			if source.seals.Load() != 1 || source.checks.Load() != 0 {
				t.Fatalf("invalid result entered source audit: seals=%d checks=%d", source.seals.Load(), source.checks.Load())
			}
		})
	}
}
