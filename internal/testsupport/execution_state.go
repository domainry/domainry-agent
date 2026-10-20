// Package testsupport exposes deterministic constructors for cross-module
// contract and Runtime host-adapter tests. Product composition must obtain the
// same services from the Agent Binding.
package testsupport

import (
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	application "github.com/domainry/domainry-agent/internal/application"
)

type Clock interface{ Now() time.Time }
type IdentifierGenerator interface{ NewID() string }

type TaskState struct {
	agentpersistence.AgentTaskStateService
	Repository agentpersistence.AgentTaskRunRepository
}

func NewTaskState(repository agentpersistence.AgentTaskRunRepository, clock Clock) agentpersistence.AgentTaskStateService {
	var now func() time.Time
	if clock != nil {
		now = clock.Now
	}
	return &TaskState{AgentTaskStateService: application.NewTaskStateServiceWithClock(repository, now), Repository: repository}
}

func NewInteractiveState(repository agentpersistence.AgentInteractiveRunRepository, clock Clock, ids IdentifierGenerator) agentpersistence.AgentInteractiveStateService {
	var now func() time.Time
	if clock != nil {
		now = clock.Now
	}
	var newID func() string
	if ids != nil {
		newID = ids.NewID
	}
	return application.NewInteractiveStateServiceWithRuntime(repository, now, newID)
}
