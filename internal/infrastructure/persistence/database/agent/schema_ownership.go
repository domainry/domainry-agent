package agent

import "github.com/domainry/domainry-foundation/schemaownership"

const (
	conversationRetention = "conversation deletion, lifecycle retention and subject erasure remove the scoped rows"
	subjectRetention      = "subject erasure removes rows by owner identity; product retention removes the owning aggregate"
)

// SchemaOwnership is the source-owned contract for every physical table
// created by SchemaMigrations. Shared Foundation and product-module tables are
// deliberately excluded because their owning packages publish their own contracts.
func SchemaOwnership() []schemaownership.Table {
	return []schemaownership.Table{
		{
			Name: "_agent_runtime_states", Owner: MigrationOwner, Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "workspace plus kind/state_key identity; lifecycle scans require workspace and bounded candidate limit",
			DeletionPolicy:   "eligible terminal state is deleted by lifecycle retention and user rows are deleted by subject erasure",
		},
		{
			Name: agentRunTable, Owner: MigrationOwner, Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionProduct, PrimaryKey: []string{"id"},
			BoundedQueryPath: "run_kind plus workspace or owner/conversation scope; dedicated claim, status and capacity indexes",
			DeletionPolicy:   "terminal runs follow product retention; conversation and interactive rows participate in scoped deletion and subject erasure",
		},
		{
			Name: "_agent_conversations", Owner: MigrationOwner, Kind: schemaownership.KindEntity, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"}, EntityIDColumn: "conversation_id", EntityIDPrefix: "conv", BusinessKey: []string{"conversation_id"},
			BoundedQueryPath: "owner identity plus conversation/client identity; owner list uses updated cursor and enforced limit",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: conversationItemTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/conversation plus typed sequence, run or subject indexes",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: "_agent_user_memories", Owner: MigrationOwner, Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner plus memory identity; owner-scoped listing",
			DeletionPolicy:   "explicit memory deletion and subject erasure remove the owner-scoped row",
		},
		{
			Name: conversationRunStepTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/conversation/run plus ordered step identity; hot results only",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: conversationStepInputTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/conversation/run plus immutable step request or source-only projection",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: conversationToolCatalogTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/conversation plus exact SHA-256 catalog identity",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: conversationToolCallTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/conversation/run plus ordered step/call receipt identity",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: conversationStepSourceTable, Owner: MigrationOwner, Kind: schemaownership.KindRelation, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/conversation/run/step plus source ordinal; at most 64 edges per step or 256 for initial input",
			DeletionPolicy:   conversationRetention,
		},
		{
			Name: "_agent_conversation_interactions", Owner: MigrationOwner, Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionTechnicalTTL, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/conversation/run identity or runtime/status/expiry worker index",
			DeletionPolicy:   "expiry processing closes pending interactions; conversation deletion and subject erasure remove retained rows",
		},
		{
			Name: conversationTaskTable, Owner: MigrationOwner, Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionProduct, PrimaryKey: []string{"id"},
			BoundedQueryPath: "record kind plus owner/task identity; runtime status claim indexes and cursor limits",
			DeletionPolicy:   "task and follow-up rows follow owning conversation/product retention and subject erasure",
		},
		{
			Name: conversationPeerLinkTable, Owner: MigrationOwner, Kind: schemaownership.KindRelation, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "link kind plus owner/link/peer identity; typed collaboration indexes and enforced list limits",
			DeletionPolicy:   "participant and grant revocation delete typed rows; conversation deletion and subject erasure remove dependent links",
		},
		{
			Name: conversationAgentMessageTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner plus message identity or owner/conversation/consumption inbox index",
			DeletionPolicy:   subjectRetention,
		},
		{
			Name: conversationDelegationSubjectTable, Owner: MigrationOwner, Kind: schemaownership.KindRelation, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "source or execution owner plus delegation identity; execution-owner cursor is limited",
			DeletionPolicy:   "subject erasure removes rows matching either source or execution owner",
		},
		{
			Name: conversationSourceReleaseTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/delegation/conversation/run boundary or producer/delegation index with enforced limits",
			DeletionPolicy:   "release revocation, conversation cleanup and subject erasure remove source records",
		},
		{
			Name: conversationContractPublicationTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/delegation identity ordered by immutable revision",
			DeletionPolicy:   subjectRetention,
		},
		{
			Name: conversationWorkBudgetTable, Owner: MigrationOwner, Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionProduct, PrimaryKey: []string{"id"},
			BoundedQueryPath: "exact runtime/workspace/root-conversation identity",
			DeletionPolicy:   "root conversation deletion, lifecycle retention and root-subject erasure remove the budget row",
		},
		{
			Name: conversationForkTable, Owner: MigrationOwner, Kind: schemaownership.KindRelation, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "exact owner/forked-conversation identity",
			DeletionPolicy:   "fork provenance follows conversation deletion and subject erasure",
		},
		{
			Name: conversationCapabilityFeedbackTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner plus feedback or client identity",
			DeletionPolicy:   "feedback follows the submitting subject and product retention policy",
		},
		{
			Name: conversationImprovementCandidateTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner plus candidate/client or kind/target/version identity; list is limited",
			DeletionPolicy:   "candidate history follows the submitting subject and product retention policy",
		},
		{
			Name: conversationCapabilityConfigTable, Owner: MigrationOwner, Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionProduct, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner plus kind/target identity or owner/kind ordered listing",
			DeletionPolicy:   "current capability configuration follows its owner and target lifecycle",
		},
		{
			Name: conversationMemoryChangeTable, Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/memory identity ordered by immutable revision",
			DeletionPolicy:   "memory history is removed with explicit memory deletion or subject erasure",
		},
		{
			Name: "_agent_artifacts", Owner: MigrationOwner, Kind: schemaownership.KindEntity, WorkspaceScope: schemaownership.ScopeExplicitMixed,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"}, EntityIDColumn: "artifact_id", EntityIDPrefix: "agart", BusinessKey: []string{"artifact_id"},
			BoundedQueryPath: "owner plus artifact identity or bounded owner creation cursor",
			DeletionPolicy:   "explicit artifact deletion, conversation lifecycle and subject erasure remove the artifact head",
		},
		{
			Name: "_agent_artifact_versions", Owner: MigrationOwner, Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeExplicitMixed,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner/artifact identity plus bounded descending immutable version history",
			DeletionPolicy:   "artifact deletion and subject erasure remove immutable versions",
		},
		{
			Name: "_agent_artifact_mutations", Owner: MigrationOwner, Kind: schemaownership.KindIdempotency, WorkspaceScope: schemaownership.ScopeExplicitMixed,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
			BoundedQueryPath: "owner plus exact idempotency client digest",
			DeletionPolicy:   "artifact retention and subject erasure remove mutation receipts",
		},
	}
}

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }
