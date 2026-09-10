package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestKnowledgeReceiptsRecheckCurrentRemoteAccessAndSource(t *testing.T) {
	var permission, document atomic.Bool
	permission.Store(true)
	document.Store(true)
	var calls atomic.Int32
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			Permissions []string `json:"permission_ids"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid request")
		}
		if fail.Load() {
			http.Error(w, "PRIVATE-UPSTREAM-ERROR", 503)
			return
		}
		if len(request.Permissions) == 0 || !document.Load() {
			fmt.Fprint(w, `{"items":[]}`)
			return
		}
		// Illustrative schema only. Integer and source fields must survive JSON
		// normalization and a persistence round trip without float64 loss.
		fmt.Fprint(w, `{"items":[{"doc_id":"finance","title":"费用规则","url":"https://example.com/rule?a=1&b=2","amount":9007199254740993,"content":"许可内容"}]}`)
	}))
	defer upstream.Close()
	k := newTestKnowledge(t, upstream)
	k.config.PermissionIDs = func(context.Context, agentsdk.ConversationAuthority) ([]string, error) {
		if permission.Load() {
			return []string{"finance"}, nil
		}
		return nil, nil
	}
	a := knowledgeAuthority()
	for _, operation := range []string{"search", "fetch"} {
		t.Run(operation, func(t *testing.T) {
			var saved agentsdk.ConversationKnowledgeResult
			var err error
			if operation == "search" {
				saved, err = k.SearchKnowledge(t.Context(), "费用规则", a)
			} else {
				saved, err = k.ReadKnowledge(t.Context(), "finance", a)
			}
			if err != nil || !strings.Contains(string(saved.Data), "9007199254740993") {
				t.Fatal("source precision lost", err)
			}
			persisted, _ := json.Marshal(saved)
			_ = json.Unmarshal(persisted, &saved)
			if err = k.RevalidateKnowledge(t.Context(), saved, a); err != nil {
				t.Fatal("unchanged persisted result rejected", err)
			}
			for _, revoked := range []*atomic.Bool{&permission, &document} {
				revoked.Store(false)
				err = k.RevalidateKnowledge(t.Context(), saved, a)
				revoked.Store(true)
				var coded *agentsdk.Error
				if !errors.As(err, &coded) || coded.Code != "agent.conversation.knowledge_source_changed" {
					t.Fatal("revoked source reused", err)
				}
			}
			for _, field := range []string{"runtime", "workspace", "user", "source", "library"} {
				other := a
				copy := saved
				switch field {
				case "runtime":
					other.RuntimeID = "other"
				case "workspace":
					other.WorkspaceID = "other"
				case "user":
					other.UserID = "other"
				case "source":
					copy.ScopeSHA256 = "forged"
				case "library":
					copy.LibraryID = "lib_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				}
				before := calls.Load()
				if k.RevalidateKnowledge(t.Context(), copy, other) == nil || calls.Load() != before {
					t.Fatal("foreign receipt reached upstream")
				}
			}
			fail.Store(true)
			err = k.RevalidateKnowledge(t.Context(), saved, a)
			fail.Store(false)
			if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("outage bypassed revalidation or leaked response", err)
			}
		})
	}
}

func TestKnowledgeBusinessFailureCannotBecomeEvidence(t *testing.T) {
	for _, operation := range []string{"search", "fetch"} {
		t.Run(operation, func(t *testing.T) {
			var missing atomic.Bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if missing.Load() {
					// The HTTP 200 / 1004 envelope was observed on the live service.
					// Extra data deliberately checks that errors cannot supply evidence.
					fmt.Fprint(w, `{"err_code":1004,"err_msg":"PRIVATE-UPSTREAM-ERROR","data":{"content":"PRIVATE-FAILED-EVIDENCE"}}`)
					return
				}
				fmt.Fprint(w, `{"err_code":0,"data":{"content":"fixture source","number":9007199254740993}}`)
			}))
			defer upstream.Close()
			k := newTestKnowledge(t, upstream)
			a := knowledgeAuthority()
			read := func() (agentsdk.ConversationKnowledgeResult, error) {
				if operation == "search" {
					return k.SearchKnowledge(t.Context(), "fixture", a)
				}
				return k.ReadKnowledge(t.Context(), "fixture", a)
			}
			saved, err := read()
			if err != nil || !strings.Contains(string(saved.Data), "9007199254740993") {
				t.Fatal("success envelope lost evidence or precision", err)
			}
			missing.Store(true)
			result, err := read()
			var coded *agentsdk.Error
			if !errors.As(err, &coded) || coded.Code != "agent.conversation.knowledge_not_found" || coded.Class != "not_found" {
				t.Fatal("business failure not classified", err)
			}
			if len(result.Data) != 0 || len(result.Citations) != 0 || result.ScopeSHA256 != "" || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("business failure produced evidence or exposed upstream content")
			}
			if err = k.RevalidateKnowledge(t.Context(), saved, a); !errors.As(err, &coded) || coded.Code != "agent.conversation.knowledge_not_found" {
				t.Fatal("deleted source remained reusable", err)
			}
		})
	}
}
