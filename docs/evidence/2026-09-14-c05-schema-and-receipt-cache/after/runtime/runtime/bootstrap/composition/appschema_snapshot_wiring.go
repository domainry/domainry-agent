package composition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// RecordSchemaSnapshotProvider projects mutable Runtime schema state.
type RecordSchemaSnapshotProvider struct {
	snapshot   func() appschemamodel.ApplicationSchemaSnapshot
	generation func() uint64

	mu                sync.RWMutex
	cached            bool
	cachedGeneration  uint64
	cachedSnapshot    appschemamodel.ApplicationSchemaSnapshot
	projectedSnapshot map[string]recordSchemaProjectionCacheEntry
}

type recordSchemaProjectionCacheEntry struct {
	snapshot  appschemamodel.ApplicationSchemaSnapshot
	expiresAt time.Time
}

const recordSchemaProjectionCacheLimit = 256

func ensureRecordSchemaSnapshotProvider(records *runtimeAssembly) {
	if records != nil && records.RecordSchemaSnapshotProvider == nil {
		records.RecordSchemaSnapshotProvider = &RecordSchemaSnapshotProvider{
			snapshot: func() appschemamodel.ApplicationSchemaSnapshot { return recordSchemaSnapshot(records) },
			generation: func() uint64 {
				records.mu.RLock()
				defer records.mu.RUnlock()
				return records.schemaGeneration
			},
		}
	}
}

func (s *RecordSchemaSnapshotProvider) Schema() appschemamodel.ApplicationSchemaSnapshot {
	if s == nil || s.snapshot == nil {
		return appschemamodel.ApplicationSchemaSnapshot{}
	}
	if s.generation == nil {
		return s.snapshot()
	}
	generation := s.generation()
	s.mu.RLock()
	if s.cached && s.cachedGeneration == generation {
		snapshot := s.cachedSnapshot
		s.mu.RUnlock()
		return snapshot
	}
	s.mu.RUnlock()

	// Serialize the cold build. Schema construction includes the complete
	// canonical hash, so allowing every concurrent first reader to repeat it
	// defeats the generation cache under race instrumentation and startup load.
	s.mu.Lock()
	defer s.mu.Unlock()
	generation = s.generation()
	if s.cached && s.cachedGeneration == generation {
		return s.cachedSnapshot
	}
	snapshot := s.snapshot()
	if s.generation() != generation {
		// The value is still a valid immutable snapshot from one instant. Do
		// not retain it after a concurrent metadata publication.
		return snapshot
	}
	s.cached = true
	s.cachedGeneration = generation
	s.cachedSnapshot = snapshot
	s.projectedSnapshot = map[string]recordSchemaProjectionCacheEntry{}
	return snapshot
}

func recordSchemaSnapshot(s *runtimeAssembly) appschemamodel.ApplicationSchemaSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	objects := make([]definitionmodel.ObjectSchema, 0, len(s.schema))
	for _, object := range s.schema {
		objects = append(objects, object)
	}
	actions := make([]definitionmodel.ActionSchema, 0, len(s.actions))
	for _, action := range s.actions {
		actions = append(actions, action)
	}
	workflows := make([]definitionmodel.WorkflowSchema, 0, len(s.workflows))
	for _, workflow := range s.workflows {
		workflows = append(workflows, workflow)
	}
	automationRules := make([]automationmodel.AutomationRuleSchema, 0, len(s.automationRules))
	for _, rule := range s.automationRules {
		automationRules = append(automationRules, rule)
	}
	return appschemaservice.BuildSchemaSnapshot(appschemaservice.SchemaSnapshotState{
		TemplateID: s.templateID, TemplateVersion: s.templateVersion, Name: s.name, TimeZone: s.timeZone,
		Objects: objects, Actions: actions, Workflows: workflows, AutomationRules: automationRules,
		Dictionaries: s.dictionaries, Integrations: s.integrations, Reports: s.reports,
		Skills: s.skills, Agents: s.agents, AgentTasks: s.agentTasks, AgentEntrypoints: s.agentEntrypoints, AgentServicePrincipals: s.agentServicePrincipals,
		IdentityProfileExtensions: s.identityProfileExtensions,
	})
}

func recordSchemaObjects(s *runtimeAssembly) []definitionmodel.ObjectSchema {
	s.mu.RLock()
	defer s.mu.RUnlock()
	objects := make([]definitionmodel.ObjectSchema, 0, len(s.schema))
	for _, object := range s.schema {
		objects = append(objects, object)
	}
	return appschemaservice.ProjectSchemaObjects(objects)
}

func (s *RecordSchemaSnapshotProvider) SchemaForPrincipal(_ context.Context, principal principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	if s == nil || s.generation == nil {
		return appschemaservice.SnapshotForPrincipal(s.Schema(), principal)
	}
	now := time.Now()
	key, expiresAt, cacheable := recordSchemaProjectionKey(principal, now)
	if !cacheable {
		return appschemaservice.SnapshotForPrincipal(s.Schema(), principal)
	}
	for {
		generation := s.generation()
		s.mu.RLock()
		entry, found := s.projectedSnapshot[key]
		current := s.cached && s.cachedGeneration == generation
		s.mu.RUnlock()
		if current && found && now.Before(entry.expiresAt) {
			return entry.snapshot
		}

		base := s.Schema()
		generation = s.generation()
		s.mu.Lock()
		if !s.cached || s.cachedGeneration != generation {
			s.mu.Unlock()
			continue
		}
		// Serialize projection misses as well as base-schema misses. A single
		// freshly authenticated principal can arrive through several concurrent
		// request paths; each must reuse the same immutable visibility projection.
		now = time.Now()
		if entry, found = s.projectedSnapshot[key]; found && now.Before(entry.expiresAt) {
			s.mu.Unlock()
			return entry.snapshot
		}
		snapshot := appschemaservice.SnapshotForPrincipal(base, principal)
		if s.generation() != generation || !now.Before(expiresAt) {
			s.mu.Unlock()
			return snapshot
		}
		if s.projectedSnapshot == nil || len(s.projectedSnapshot) >= recordSchemaProjectionCacheLimit {
			s.projectedSnapshot = map[string]recordSchemaProjectionCacheEntry{}
		}
		s.projectedSnapshot[key] = recordSchemaProjectionCacheEntry{snapshot: snapshot, expiresAt: expiresAt}
		s.mu.Unlock()
		return snapshot
	}
}

// SnapshotForPrincipal depends on the current policy bundle plus explicit
// system scope. Request IDs, correlation IDs and business values do not affect
// this schema projection. The complete policy bundle is hashed so revocation
// and field/data rule changes cannot reuse a prior projection. Expiry is kept
// on the cache entry instead of the key: a refreshed, otherwise identical
// bundle may reuse the projection only until the earlier entry expires.
func recordSchemaProjectionKey(principal principalmodel.Principal, now time.Time) (string, time.Time, bool) {
	expiresAt := time.Time{}
	var accessBundle *identitysdk.AccessBundle
	if principal.AccessBundle != nil {
		copy := *principal.AccessBundle
		expiresAt = copy.ExpiresAt
		if expiresAt.IsZero() || !expiresAt.After(now) {
			return "", expiresAt, false
		}
		copy.ExpiresAt = time.Time{}
		accessBundle = &copy
	}
	payload, err := json.Marshal(struct {
		Known              bool                       `json:"known"`
		AccessBundle       *identitysdk.AccessBundle  `json:"access_bundle,omitempty"`
		SystemScope        principalmodel.SystemScope `json:"system_scope"`
		SystemCapabilities []string                   `json:"system_capabilities,omitempty"`
	}{principal.Known, accessBundle, principal.SystemScope, principal.SystemCapabilities})
	if err != nil {
		return "", time.Time{}, false
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), expiresAt, true
}
