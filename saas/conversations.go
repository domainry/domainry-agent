package saas

import (
	"errors"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
)

const conversationSaaSActionPrefix = "agent.saas.conversation."

func (s *Server) conversationHandler(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.config.Conversations == nil || s.config.ConversationRuntimeID == "" {
			writeError(w, 503, "agent.conversation.unavailable", "")
			return
		}
		var in agentsdk.ConversationRPCRequest
		limit := int64(maxRequestBytes)
		if op == "attachments_upload" {
			limit = 24 << 20
		}
		if err := decodeLimit(r, &in, limit); err != nil {
			writeError(w, 400, "agent.conversation.request_invalid", "")
			return
		}
		// This service API key belongs to one runtime. A client cannot choose another
		// runtime's namespace by forging the invocation envelope.
		if in.Authority.RuntimeID != s.config.ConversationRuntimeID {
			writeError(w, 403, "agent.conversation.runtime_denied", "")
			return
		}
		if s.config.ConversationWorkspaceID != "" && in.Authority.WorkspaceID != s.config.ConversationWorkspaceID {
			writeError(w, 403, "agent.conversation.workspace_denied", "")
			return
		}
		if op == "scheduled_task_start" && in.ScheduledTask.Authority != in.Authority {
			writeError(w, 403, "agent.conversation.scheduled_authority_denied", "")
			return
		}
		if op == "business_event_task_accept" && in.BusinessEventTask.Authority != in.Authority {
			writeError(w, 403, "agent.conversation.business_event_authority_denied", "")
			return
		}
		result, err := agentapplication.InvokeConversation(r.Context(), s.config.Conversations, op, in)
		if err != nil {
			writeConversationServiceError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}
}

func writeConversationServiceError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "agent.conversation.internal"
	var coded *agentsdk.Error
	if errors.As(err, &coded) {
		code = coded.Code
		switch coded.Class {
		case "bad_request":
			status = http.StatusBadRequest
		case "forbidden":
			status = http.StatusForbidden
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		case "rate_limited":
			status = http.StatusTooManyRequests
		case "unavailable":
			status = http.StatusServiceUnavailable
		}
	}
	writeError(w, status, code, "")
}
