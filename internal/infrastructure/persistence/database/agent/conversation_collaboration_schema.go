package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const (
	conversationPeerLinkTable           = "_agent_peer_links"
	conversationPeerLinkKindInstance    = "peer_instance"
	conversationPeerLinkKindDelegation  = "delegation"
	conversationPeerLinkKindParticipant = "participant"
	conversationPeerLinkKindUseGrant    = "use_grant"
)
const conversationAgentMessageTable = "_agent_peer_messages"

func conversationCollaborationMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 20, Name: "agent_peer_collaboration"}
	tables := []*ormschema.TableBuilder{
		// Current peer state shares one typed aggregate. scope_key preserves the
		// old family-specific uniqueness without making unrelated empty headers
		// collide: agent/delegation ID for grants and participants, conversation
		// ID for delegations, and agent ID for peer instances.
		ormschema.NewTable(d, conversationPeerLinkTable).IfNotExists().Columns(
			required("link_kind", ormschema.TextKey(32)),
			required("owner_key", ormschema.TextKey(64)),
			required("link_id", ormschema.TextKey(96)),
			required("peer_key", ormschema.TextKey(64)).DefaultValue(""),
			required("scope_key", ormschema.TextKey(191)),
			required("owner_user_id", ormschema.TextKey(255)).DefaultValue(""),
			required("conversation_id", ormschema.TextKey(96)).DefaultValue(""),
			required("source_conversation_id", ormschema.TextKey(96)).DefaultValue(""),
			required("root_conversation_id", ormschema.TextKey(96)).DefaultValue(""),
			required("status", ormschema.TextKey(32)).DefaultValue(""),
			required("revision", ormschema.BigInt()).DefaultValue(0),
			required("created_at", ormschema.BigInt()).DefaultValue(0),
			required("updated_at", ormschema.BigInt()).DefaultValue(0),
			required("payload_json", ormschema.LongText()).DefaultValue("{}"),
		).PrimaryKey("link_kind", "owner_key", "link_id", "peer_key").Unique("link_kind", "owner_key", "scope_key", "peer_key"),
		ormschema.NewTable(d, conversationAgentMessageTable).IfNotExists().Columns(
			required("runtime_id", ormschema.TextKey(255)), required("authority_json", ormschema.LongText()), required("agent_json", ormschema.LongText()),
			required("owner_key", ormschema.TextKey(64)), required("message_id", ormschema.TextKey(96)),
			required("delegation_id", ormschema.TextKey(96)), required("conversation_id", ormschema.TextKey(96)),
			required("created_at", ormschema.BigInt()), required("consumed_run_id", ormschema.TextKey(96)),
			required("payload_json", ormschema.LongText()),
		).PrimaryKey("owner_key", "message_id"),
	}
	for _, table := range tables {
		statement, _, err := table.Build()
		if err != nil {
			return m, err
		}
		m.Statements = append(m.Statements, statement)
	}
	for _, index := range []*ormschema.IndexBuilder{
		ormschema.NewIndex(d, "idx_agent_peer_link_delegation_source_v20", conversationPeerLinkTable).Columns("link_kind", "owner_key", "source_conversation_id", "created_at", "link_id"),
		ormschema.NewIndex(d, "idx_agent_peer_link_delegation_root_v20", conversationPeerLinkTable).Columns("link_kind", "root_conversation_id", "link_id"),
		ormschema.NewIndex(d, "idx_agent_peer_link_participant_v20", conversationPeerLinkTable).Columns("link_kind", "peer_key", "created_at", "link_id"),
		ormschema.NewIndex(d, "idx_agent_peer_link_use_grant_v20", conversationPeerLinkTable).Columns("link_kind", "peer_key", "link_id"),
		ormschema.NewIndex(d, "idx_agent_peer_inbox_v20", conversationAgentMessageTable).Columns("owner_key", "conversation_id", "consumed_run_id", "created_at"),
	} {
		statement, _, err := index.Build()
		if err != nil {
			return m, err
		}
		m.Statements = append(m.Statements, statement)
	}
	return m, nil
}
