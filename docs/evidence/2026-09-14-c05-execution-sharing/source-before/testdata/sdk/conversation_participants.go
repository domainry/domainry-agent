package agentsdk

import (
	"slices"
	"strings"
)

// A participant grant applies only to its recorded delegation. Identity's
// current role policy is still required; it never transfers tool permissions.
// Supported scopes are view, communicate, manage and delivery_read. Other scopes require
// their execution/receipt sharing contracts before they can be granted.
type ConversationDelegationParticipant struct {
	// Recorded by the server from the actual granting identity. A client
	// cannot choose it; readers must recheck this publisher's current role.
	Publisher  *ConversationAuthority `json:"publisher,omitempty"`
	UserID     string                 `json:"user_id"`
	Operations []string               `json:"operations"`
	Revision   int64                  `json:"revision,omitempty"`
}

// Clients choose recipients and scopes, not the server's grant revision.
type ConversationDelegationParticipantInput struct {
	UserID     string   `json:"user_id"`
	Operations []string `json:"operations"`
}

func ConversationParticipantOperations() []string {
	return []string{"view", "communicate", "manage", "delivery_read"}
}

// ParticipantPublisherVerified checks the stored provenance, not current
// Identity permission. That permission must still be checked by the host.
func ParticipantPublisherVerified(grant ConversationDelegationParticipant, owner string, reader ConversationAuthority) bool {
	p := grant.Publisher
	return grant.Revision > 0 && p != nil && p.Known && p.UserID == owner && p.RuntimeID == reader.RuntimeID && p.WorkspaceID == reader.WorkspaceID && p.RoleKey != "" && len(p.RoleKey) <= 255 && strings.TrimSpace(p.RoleKey) == p.RoleKey && !strings.ContainsAny(p.RoleKey, "\x00\r\n\t")
}

// ParticipantGrantNeedsPublication distinguishes newly shared access from
// revocation. Removing scopes must remain possible without reading old data.
func ParticipantGrantNeedsPublication(d ConversationDelegation, input ConversationDelegationParticipantInput, publisher ConversationAuthority) bool {
	for _, old := range d.Participants {
		if old.UserID != input.UserID || old.Revision < 1 {
			continue
		}
		onlyExisting := true
		for _, op := range input.Operations {
			onlyExisting = onlyExisting && slices.Contains(old.Operations, op)
		}
		if onlyExisting && len(input.Operations) < len(old.Operations) {
			return false
		}
		return !onlyExisting || old.Publisher == nil || *old.Publisher != publisher
	}
	return true
}
