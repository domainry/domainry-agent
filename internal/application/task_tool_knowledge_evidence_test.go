package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

func taskKnowledgeFixture() *agentmodel.AgentTaskKnowledgeEvidence {
	return &agentmodel.AgentTaskKnowledgeEvidence{LibraryID: "library-a", Operation: "search", ScopeSHA256: strings.Repeat("a", 64), Citations: []agentmodel.AgentTaskKnowledgeCitationEvidence{{ID: "cite-a", Provider: "knowledge", DocumentID: "doc-a", DocumentVersion: 3, ChunkID: "chunk-a"}}}
}
func TestTaskKnowledgeReceiptComesFromHostAndIsBoundToActualTool(t *testing.T) {
	request := TaskToolInvocation{Tool: agentsdk.AgentToolKnowledgeSearch, Input: map[string]any{"library_id": "library-a", "query": "question"}}
	result := modulehost.TaskToolResult{Tool: request.Tool, Status: "executed", Knowledge: taskKnowledgeFixture()}
	receipt, err := taskKnowledgeReceipt(request, result)
	if err != nil || receipt.Citations[0].DocumentVersion != 3 {
		t.Fatal(receipt, err)
	}
	receipt.Citations[0].DocumentID = "mutated"
	if result.Knowledge.Citations[0].DocumentID != "doc-a" {
		t.Fatal("host evidence was aliased")
	}
	for _, change := range []func(*TaskToolInvocation, *modulehost.TaskToolResult){
		func(_ *TaskToolInvocation, r *modulehost.TaskToolResult) {
			r.Knowledge = nil
			r.Output = map[string]any{"knowledge": taskKnowledgeFixture()}
		},
		func(_ *TaskToolInvocation, r *modulehost.TaskToolResult) { r.Knowledge.LibraryID = "other" },
		func(_ *TaskToolInvocation, r *modulehost.TaskToolResult) { r.Knowledge.Operation = "fetch" },
		func(_ *TaskToolInvocation, r *modulehost.TaskToolResult) { r.Knowledge.ScopeSHA256 = "bad" },
		func(_ *TaskToolInvocation, r *modulehost.TaskToolResult) { r.Status = "failed" },
		func(q *TaskToolInvocation, r *modulehost.TaskToolResult) {
			q.Tool = agentsdk.AgentToolKnowledgeRead
			r.Tool = q.Tool
			q.Input = map[string]any{"library_id": "library-a", "document_id": "other"}
			r.Knowledge.Operation = "fetch"
		},
		func(q *TaskToolInvocation, r *modulehost.TaskToolResult) {
			q.Tool = agentsdk.AgentToolGetRecord
			r.Tool = q.Tool
		},
	} {
		q := TaskToolInvocation{Tool: request.Tool, Input: map[string]any{"library_id": "library-a", "query": "question"}}
		r := modulehost.TaskToolResult{Tool: q.Tool, Status: "executed", Knowledge: taskKnowledgeFixture()}
		change(&q, &r)
		if _, err = taskKnowledgeReceipt(q, r); err == nil {
			t.Fatal("unverified receipt accepted", q, r)
		}
	}
}
func TestTaskKnowledgeServicePersistsHostReceiptAndDropsItOnFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		state := &taskToolStateStub{run: agentmodel.AgentTaskRun{ID: "task-1", WorkspaceID: "workspace-1", TaskKey: "read", TaskVersion: "1", Status: agentmodel.AgentTaskRunRunning, Lease: agentmodel.AgentTaskLease{Owner: "worker", FencingToken: 1}}}
		ledger := &taskToolLedgerStub{}
		host := &taskToolHostStub{authorization: &modulehost.TaskAuthorization{Principal: modulehost.Principal{Known: true, UserID: "service", WorkspaceID: "workspace-1"}, Task: agentsdk.AgentTaskDefinition{Key: "read", Version: "1"}, AllowedTools: []string{agentsdk.AgentToolKnowledgeSearch}}, result: modulehost.TaskToolResult{Status: "executed", Tool: agentsdk.AgentToolKnowledgeSearch, Output: map[string]any{"citations": []any{}}, Knowledge: taskKnowledgeFixture()}}
		if failed {
			host.err = errors.New("current source access denied")
		}
		result, err := NewTaskToolService(state, ledger, host).Invoke(context.Background(), TaskToolInvocation{WorkspaceID: "workspace-1", TaskRunID: "task-1", Tool: agentsdk.AgentToolKnowledgeSearch, Input: map[string]any{"library_id": "library-a", "query": "question"}})
		if failed {
			if err == nil || ledger.finish.Status != "failed" || ledger.finish.Knowledge != nil {
				t.Fatal("failed read retained trusted citations", ledger.finish, err)
			}
		} else {
			if err != nil || ledger.finish.Knowledge == nil || ledger.finish.Knowledge.Citations[0].DocumentID != "doc-a" {
				t.Fatal(ledger.finish, err)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "scope_sha256") || strings.Contains(string(raw), `"knowledge":`) {
				t.Fatal("internal receipt leaked into public tool response", string(raw))
			}
		}
	}
}
