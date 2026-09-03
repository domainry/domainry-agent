package application

import (
	"strings"
	"time"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/internal/dataaccess"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// ResolveDataAuthorization requires the functional grant and data policy for
// the same exact permission key. Agent persists only the resulting closed
// owner/organization boundary; arbitrary policy predicates fail closed.
func ResolveDataAuthorization(principal identitysdk.Principal, permissionKey string, now time.Time, requestID, correlationID, causationID string) (dataaccess.Authorization, error) {
	denied := func() (dataaccess.Authorization, error) {
		return dataaccess.Authorization{}, forbidden("agent.authorization.action_denied")
	}
	permissionKey = strings.TrimSpace(permissionKey)
	separator := strings.LastIndexByte(permissionKey, '.')
	if !principal.Known || separator <= 0 || separator == len(permissionKey)-1 || principal.AccessBundle == nil || !principal.HasPermission(permissionKey) {
		return denied()
	}
	bundle := principal.AccessBundle
	if err := bundle.Validate(now.UTC()); err != nil || strings.TrimSpace(string(bundle.Subject.WorkspaceID)) != strings.TrimSpace(principal.WorkspaceID) || strings.TrimSpace(string(bundle.Subject.SubjectID)) != strings.TrimSpace(principal.UserID) {
		return denied()
	}
	resource, action := permissionKey[:separator], permissionKey[separator+1:]
	resolved := dataaccess.Authorization{
		Principal: modulehost.Principal{
			Known: true, WorkspaceID: strings.TrimSpace(principal.WorkspaceID), UserID: strings.TrimSpace(principal.UserID),
			RoleKey: strings.TrimSpace(principal.RoleKey), AuthorizationRevision: strings.TrimSpace(principal.AuthorizationRevision),
			AuthorizedActionKey: permissionKey, RequestID: strings.TrimSpace(requestID), CorrelationID: strings.TrimSpace(correlationID), CausationID: strings.TrimSpace(causationID),
		},
		PermissionKey: permissionKey,
	}
	found := false
	for _, policy := range bundle.DataPolicies {
		if strings.TrimSpace(string(policy.Resource)) != resource || strings.TrimSpace(string(policy.Action)) != action {
			continue
		}
		if policy.Effect != identitysdk.EffectAllow {
			return denied()
		}
		found = true
		for _, scope := range policy.DataScopes {
			switch scope {
			case identitysdk.DataScopeAll:
				resolved.All = true
			case identitysdk.DataScopeOwner:
				resolved.SubjectIDs = append(resolved.SubjectIDs, string(bundle.Subject.SubjectID))
			case identitysdk.DataScopeOrg:
				resolved.OrganizationIDs = append(resolved.OrganizationIDs, bundle.Subject.OrgID)
			case identitysdk.DataScopeOrgChild:
				resolved.OrganizationIDs = append(resolved.OrganizationIDs, bundle.Subject.OrgScopeIDs...)
			case identitysdk.DataScopeTargetOrg:
				resolved.OrganizationIDs = append(resolved.OrganizationIDs, bundle.Subject.SupportOrgScopeIDs...)
			default:
				return denied()
			}
		}
	}
	if !found {
		return denied()
	}
	for _, guardrail := range bundle.Guardrails {
		resourceMatches := guardrail.Resource == "" || strings.TrimSpace(string(guardrail.Resource)) == resource
		actionMatches := guardrail.Action == "" || strings.TrimSpace(string(guardrail.Action)) == action
		if resourceMatches && actionMatches && guardrail.Predicate != nil && strings.TrimSpace(guardrail.Field) == "" {
			return denied()
		}
	}
	resolved = resolved.Normalized()
	if !resolved.ValidFor(permissionKey, principal.WorkspaceID) {
		return denied()
	}
	return resolved, nil
}
