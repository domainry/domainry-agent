package service

import (
	"context"
	"strings"

	identitymodel "github.com/domainry/domainry-identity/internal/domain/identity/model"
)

// IdentityPrincipalContext is produced by one owner-controlled resolution.
// Its projections reuse those exact reads; it is never a cross-request cache
// or a substitute for revalidating current authorization before returning it.
type IdentityPrincipalContext struct {
	Principal       identitymodel.Principal
	User            identitymodel.IdentityUser
	EffectiveRoles  []identitymodel.IdentityRole
	Assignments     []identitymodel.IdentityUserRoleAssignment
	ProjectionRoles []identitymodel.IdentityRole
}

func (s *IdentityDomainService) ResolvePrincipalContext(ctx context.Context, userID, roleKey string) (IdentityPrincipalContext, error) {
	if err := ctx.Err(); err != nil {
		return IdentityPrincipalContext{}, err
	}
	var facts IdentityPrincipalContext
	var err error
	if roleKey = strings.TrimSpace(roleKey); roleKey != "" {
		facts.Principal, err = s.buildPrincipalForRole(ctx, userID, roleKey, &facts)
	} else {
		facts.Principal, err = s.buildPrincipal(ctx, userID, &facts)
	}
	return facts, err
}

func (s *IdentityDomainService) capturePrincipalContext(facts *IdentityPrincipalContext, userID string, assignments []identitymodel.IdentityUserRoleAssignment, roles []identitymodel.IdentityRole) {
	if facts == nil {
		return
	}
	for _, assignment := range assignments {
		if strings.TrimSpace(assignment.UserID) == strings.TrimSpace(userID) {
			facts.Assignments = append(facts.Assignments, assignment)
		}
	}
	facts.ProjectionRoles = append([]identitymodel.IdentityRole(nil), roles...)
	facts.EffectiveRoles = s.effectiveRolesFromAssignments(facts.Assignments, roles)
}
