package agent

import agentrepository "github.com/domainry/domainry-agent-sdk/repository"

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

func (r *Repositories) AgentLifecycleRepository() agentrepository.AgentLifecycleRepository {
	if r == nil {
		return nil
	}
	return r.lifecycle
}

func (r *Repositories) DefinitionRepository() agentrepository.DefinitionRepository {
	if r == nil {
		return nil
	}
	return r.definitions
}

func (r *Repositories) AgentStateRepository() agentrepository.AgentStateRepository {
	if r == nil {
		return nil
	}
	return r.state
}
func (r *Repositories) AgentTaskRunRepository() agentrepository.AgentTaskRunRepository {
	if r == nil {
		return nil
	}
	return r.runs
}

var _ agentrepository.Binding = (*Repositories)(nil)
