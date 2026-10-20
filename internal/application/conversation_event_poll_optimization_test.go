package application

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type eventPollReadProbe struct {
	privatePeerSources
	page        sdk.ConversationEventPage
	sourceReads int
	ownerDenied bool
}

func (r *eventPollReadProbe) Events(context.Context, string, string, int64, int, sdk.ConversationAuthority) (sdk.ConversationEventPage, error) {
	if r.ownerDenied {
		return sdk.ConversationEventPage{}, conversationFailure("forbidden", "run_owner_denied")
	}
	return r.page, nil
}

func (r *eventPollReadProbe) ConversationSourceSnapshot(context.Context, sdk.ConversationRunReference, sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	r.sourceReads++
	return persistence.ConversationSourceSnapshot{}, conversationFailure("forbidden", "source_revoked")
}

func TestEventPollReleasesOwnerScopedSnapshotsWithoutHistoricalTraversal(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	repo := &eventPollReadProbe{page: sdk.ConversationEventPage{Items: []sdk.ConversationEvent{}, NextSeq: 7}}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: repo}
	page, err := s.Events(t.Context(), "conversation", "run", 7, 100, a)
	if err != nil || page.NextSeq != 7 || len(page.Items) != 0 || repo.sourceReads != 0 {
		t.Fatal("empty poll walked the entire source graph", page, err, repo.sourceReads)
	}
	repo.ownerDenied = true
	if _, err := s.Events(t.Context(), "conversation", "run", 7, 100, a); err == nil || repo.sourceReads != 0 {
		t.Fatal("empty poll bypassed run ownership", err)
	}
	repo.ownerDenied = false
	repo.page.Items = []sdk.ConversationEvent{{Seq: 8, Type: "step.text.delta", Data: map[string]any{"text": "private business data"}}}
	if page, err := s.Events(t.Context(), "conversation", "run", 7, 100, a); err != nil || len(page.Items) != 1 || repo.sourceReads != 0 {
		t.Fatal("nonempty poll rescanned historical sources", page, err, repo.sourceReads)
	}
	repo.ownerDenied = true
	if page, err := s.Events(t.Context(), "conversation", "run", 7, 100, a); err == nil || len(page.Items) != 0 || repo.sourceReads != 0 {
		t.Fatal("nonempty poll bypassed run ownership", page, err)
	}
}

type messagePageReadProbe struct{ privatePeerSources }

func (*messagePageReadProbe) Messages(context.Context, string, sdk.ConversationMessageQuery, sdk.ConversationAuthority) (sdk.ConversationMessagePage, error) {
	return sdk.ConversationMessagePage{Items: []sdk.ConversationMessage{{Role: "user", Content: "message"}}}, nil
}

func TestMessagePageOwnerRevisionCheckGatesTheWholeRead(t *testing.T) {
	denied := errors.New("owner revision changed during message read")
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	starts, finishes := 0, 0
	s := &ConversationService{runtimeID: a.RuntimeID, repo: &messagePageReadProbe{}, options: ConversationOptions{BeginSourceAuthorizationScope: func(ctx context.Context) (context.Context, func() error) {
		starts++
		return ctx, func() error { finishes++; return denied }
	}}}
	page, err := s.Messages(t.Context(), "conversation", sdk.ConversationMessageQuery{}, a)
	if !errors.Is(err, denied) || len(page.Items) != 0 || starts != 1 || finishes != 1 {
		t.Fatal("message page escaped final owner check", page, err, starts, finishes)
	}
}
