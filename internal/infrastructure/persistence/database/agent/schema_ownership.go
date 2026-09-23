package agent

import "github.com/domainry/domainry-foundation/schemaownership"

const (
	conversationRetention = "conversation deletion, lifecycle retention and subject erasure remove the scoped rows"
	subjectRetention      = "subject erasure removes rows by owner identity; product retention removes the owning aggregate"
)

// SchemaOwnership is the source-owned contract for every physical table
// created by SchemaMigrations. Shared Foundation, Knowledge and Todo tables are
// deliberately excluded because their owning packages publish their own
// contracts.
func SchemaOwnership() []schemaownership.Table {
	return []schemaownership.Table{
		{
			Name: "_agent_runtime_states", Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"workspace_id", "kind", "state_key"},
			BoundedQueryPath: "workspace plus kind/state_key identity; lifecycle scans require workspace and bounded candidate limit",
			DeletionPolicy:   "eligible terminal state is deleted by lifecycle retention and user rows are deleted by subject erasure",
		},
		{
			Name: agentRunTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionProduct, PrimaryKey: []string{"run_kind", "scope_key", "run_id"},
			BoundedQueryPath: "run_kind plus workspace or owner/conversation scope; dedicated claim, status and capacity indexes",
			DeletionPolicy:   "terminal runs follow product retention; conversation and interactive rows participate in scoped deletion and subject erasure",
		},
		{
			Name: "_agent_conversations", Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "conversation_id"},
			BoundedQueryPath: "owner identity plus conversation/client identity; owner list uses updated cursor and enforced limit",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: conversationItemTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "conversation_id", "item_kind", "item_key"},
			BoundedQueryPath: "owner/conversation plus typed sequence, run or subject indexes",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: "_agent_user_memories", Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "memory_id"},
			BoundedQueryPath: "owner plus memory identity; owner-scoped listing",
			DeletionPolicy:   "explicit memory deletion and subject erasure remove the owner-scoped row",
		},
		{
			Name: conversationRunStepTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "conversation_id", "run_id", "record_kind", "step_no", "call_key"},
			BoundedQueryPath: "owner/conversation/run plus record kind and ordered step identity",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: "_agent_conversation_interactions", Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionTechnicalTTL, PrimaryKey: []string{"owner_key", "conversation_id", "run_id", "step_no", "call_key", "kind"},
			BoundedQueryPath: "owner/conversation/run identity or runtime/status/expiry worker index",
			DeletionPolicy:   "expiry processing closes pending interactions; conversation deletion and subject erasure remove retained rows",
		},
		{
			Name: conversationTaskTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionProduct, PrimaryKey: []string{"record_kind", "owner_key", "task_id"},
			BoundedQueryPath: "record kind plus owner/task identity; runtime status claim indexes and cursor limits",
			DeletionPolicy:   "task and follow-up rows follow owning conversation/product retention and subject erasure",
		},
		{
			Name: conversationPeerLinkTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"link_kind", "owner_key", "link_id", "peer_key"},
			BoundedQueryPath: "link kind plus owner/link/peer identity; typed collaboration indexes and enforced list limits",
			DeletionPolicy:   "participant and grant revocation delete typed rows; conversation deletion and subject erasure remove dependent links",
		},
		{
			Name: conversationAgentMessageTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "message_id"},
			BoundedQueryPath: "owner plus message identity or owner/conversation/consumption inbox index",
			DeletionPolicy:   subjectRetention,
		},
		{
			Name: conversationDelegationSubjectTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "delegation_id"},
			BoundedQueryPath: "source or execution owner plus delegation identity; execution-owner cursor is limited",
			DeletionPolicy:   "subject erasure removes rows matching either source or execution owner",
		},
		{
			Name: conversationSourceReleaseTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "release_id"},
			BoundedQueryPath: "owner/delegation/conversation/run boundary or producer/delegation index with enforced limits",
			DeletionPolicy:   "release revocation, conversation cleanup and subject erasure remove source records",
		},
		{
			Name: conversationContractPublicationTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "delegation_id", "revision"},
			BoundedQueryPath: "owner/delegation identity ordered by immutable revision",
			DeletionPolicy:   subjectRetention,
		},
		{
			Name: conversationWorkBudgetTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionProduct, PrimaryKey: []string{"runtime_id", "workspace_id", "root_conversation_id"},
			BoundedQueryPath: "exact runtime/workspace/root-conversation identity",
			DeletionPolicy:   "root conversation deletion, lifecycle retention and root-subject erasure remove the budget row",
		},
		{
			Name: conversationForkTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "conversation_id"},
			BoundedQueryPath: "exact owner/forked-conversation identity",
			DeletionPolicy:   "fork provenance follows conversation deletion and subject erasure",
		},
		{
			Name: conversationCapabilityFeedbackTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "feedback_id"},
			BoundedQueryPath: "owner plus feedback or client identity",
			DeletionPolicy:   "feedback follows the submitting subject and product retention policy",
		},
		{
			Name: conversationImprovementCandidateTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "candidate_id"},
			BoundedQueryPath: "owner plus candidate/client or kind/target/version identity; list is limited",
			DeletionPolicy:   "candidate history follows the submitting subject and product retention policy",
		},
		{
			Name: conversationCapabilityConfigTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionProduct, PrimaryKey: []string{"owner_key", "kind", "target_key"},
			BoundedQueryPath: "owner plus kind/target identity or owner/kind ordered listing",
			DeletionPolicy:   "current capability configuration follows its owner and target lifecycle",
		},
		{
			Name: conversationMemoryChangeTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"owner_key", "memory_id", "revision"},
			BoundedQueryPath: "owner/memory identity ordered by immutable revision",
			DeletionPolicy:   "memory history is removed with explicit memory deletion or subject erasure",
		},
	}
}

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }
