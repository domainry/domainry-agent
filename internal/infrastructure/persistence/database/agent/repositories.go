package agent

import agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"

type Repositories struct {
	definitions DefinitionStore
	state       AgentStateStore
	runs        *AgentTaskRunStore
	lifecycle   LifecycleStore
}

func NewRepositories(store *Store) *Repositories {
	if store == nil {
		return &Repositories{}
	}
	return &Repositories{definitions: NewDefinitionStore(store), state: NewAgentStateStore(store), runs: NewAgentTaskRunStore(store), lifecycle: NewLifecycleStore(store)}
}

func (r *Repositories) AgentLifecycleRepository() agentpersistence.AgentLifecycleRepository {
	if r == nil {
		return nil
	}
	return r.lifecycle
}

func (r *Repositories) DefinitionRepository() agentpersistence.DefinitionRepository {
	if r == nil {
		return nil
	}
	return r.definitions
}

func (r *Repositories) AgentStateRepository() agentpersistence.AgentStateRepository {
	if r == nil {
		return nil
	}
	return r.state
}
func (r *Repositories) AgentTaskRunRepository() agentpersistence.AgentTaskRunRepository {
	if r == nil {
		return nil
	}
	return r.runs
}

var _ agentpersistence.Binding = (*Repositories)(nil)
