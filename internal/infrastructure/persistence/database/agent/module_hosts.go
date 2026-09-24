package agent

import (
	"context"
	"fmt"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	knowledgemodulehost "github.com/domainry/domainry-knowledge-sdk/modulehost"
	ormdriver "github.com/domainry/domainry-orm/driver"
	"github.com/domainry/domainry-orm/sqlhost"
	todomodulehost "github.com/domainry/domainry-todo-sdk/modulehost"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

// KnowledgeModuleHost lends the shared pool and Agent-owned source readers to
// Knowledge. Knowledge opens and owns its own persistence behind the binding.
type KnowledgeModuleHost struct {
	conversation *ConversationStore
	migrations   agentmodulehost.MigrationRegistrar
}

func NewKnowledgeModuleHost(conversation *ConversationStore, migrations agentmodulehost.MigrationRegistrar) *KnowledgeModuleHost {
	return &KnowledgeModuleHost{conversation: conversation, migrations: migrations}
}

func (host *KnowledgeModuleHost) Database() agentmodulehost.Database {
	return host.conversation.store.Database()
}
func (host *KnowledgeModuleHost) Dialect() agentmodulehost.Dialect {
	return host.conversation.store.Renderer()
}
func (host *KnowledgeModuleHost) Migrations() agentmodulehost.MigrationRegistrar {
	return host.migrations
}
func (host *KnowledgeModuleHost) Profile() ormdriver.Profile {
	return host.conversation.store.Profile()
}
func (host *KnowledgeModuleHost) Conversation(ctx context.Context, database knowledgemodulehost.DB, id string, authority agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	return host.conversation.get(ctx, database, id, authority)
}
func (host *KnowledgeModuleHost) Run(ctx context.Context, database knowledgemodulehost.DB, conversationID, runID string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	row, err := host.conversation.runRow(ctx, database, conversationID, runID, authority)
	return row.Run, err
}
func (host *KnowledgeModuleHost) ArtifactStore() sharedartifact.ManagedStore {
	return host.conversation.artifactStore
}
func (host *KnowledgeModuleHost) ArtifactContentStore() sharedartifact.ContentStore {
	return host.conversation.artifactContent
}
func (host *KnowledgeModuleHost) ArtifactContentWriter() sharedartifact.ContentWriter {
	return host.conversation.artifactWriter
}

// TodoModuleHost exposes only source-reference authorization from Agent. Todo
// receives no Agent Store and constructs its own private persistence.
type TodoModuleHost struct {
	conversation *ConversationStore
	migrations   todomodulehost.MigrationRegistrar
}

func NewTodoModuleHost(conversation *ConversationStore, migrations todomodulehost.MigrationRegistrar) *TodoModuleHost {
	return &TodoModuleHost{conversation: conversation, migrations: migrations}
}

func (host *TodoModuleHost) Database() sqlhost.Database { return host.conversation.store.Database() }
func (host *TodoModuleHost) Dialect() todomodulehost.Dialect {
	return host.conversation.store.Renderer()
}
func (host *TodoModuleHost) Profile() ormdriver.Profile { return host.conversation.store.Profile() }
func (host *TodoModuleHost) Migrations() todomodulehost.MigrationRegistrar {
	return host.migrations
}
func (host *TodoModuleHost) AuthorizeTodoSource(ctx context.Context, database todomodulehost.DB, source string, authority toolsdk.Authority) error {
	if host == nil || host.conversation == nil {
		return fmt.Errorf("Todo Agent source host is unavailable")
	}
	_, err := host.conversation.get(ctx, database, source, authority)
	return err
}
