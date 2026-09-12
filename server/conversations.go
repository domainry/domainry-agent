package server

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
		if op == "attachments_upload" || op == "documents_upload" {
			limit = 24 << 20
		} // Bounded 16 MiB base64 plus RPC metadata.
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
		result, err := agentapplication.InvokeConversation(r.Context(), s.config.Conversations, op, in)
		if err != nil {
			status, code := 500, "agent.conversation.internal"
			var coded *agentsdk.Error
			if errors.As(err, &coded) {
				code = coded.Code
				switch coded.Class {
				case "bad_request":
					status = 400
				case "forbidden":
					status = 403
				case "not_found":
					status = 404
				case "conflict":
					status = 409
				case "rate_limited":
					status = 429
				case "unavailable":
					status = 503
				}
			}
			writeError(w, status, code, "")
			return
		}
		writeJSON(w, 200, result)
	}
}
