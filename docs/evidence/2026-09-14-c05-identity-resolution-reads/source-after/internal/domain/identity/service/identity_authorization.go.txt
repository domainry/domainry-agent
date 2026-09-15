package service

import (
	"context"
	"sort"
	"strings"
	"time"

	identitymodel "github.com/domainry/domainry-identity/internal/domain/identity/model"
)

// IdentityAuthorization is the stable read boundary used by authentication and
// domain-facing HTTP composition. Callers do not access IdentityDomainService state
// or its repository directly.
// IdentityProjection is the stable ctx-first domain lookup boundary. Business
// services depend on this contract instead of IdentityRepository or
// IdentityDomainService internals.
func (s *IdentityDomainService) ResolvePrincipal(ctx context.Context, userID string) (identitymodel.Principal, error) {
	if err := ctx.Err(); err != nil {
		return identitymodel.Principal{}, err
	}
	return s.BuildPrincipal(ctx, userID)
}

// ResolveEffectiveRoles returns the active published projection roles that
// currently contribute to the user's authorization. It deliberately excludes
// expired, revoked, future, binding-ineligible, disabled,
// and unpublished role facts so application projections cannot infer access
// from stale assignment rows or the role projection.
func (s *IdentityDomainService) ResolveEffectiveRoles(ctx context.Context, userID string) ([]identitymodel.IdentityRole, error) {
	assignments, err := s.ResolveEffectiveRoleAssignments(ctx, userID)
	if err != nil {
		return nil, err
	}
	roles, err := s.repo.ListIdentityRoles(ctx, s.workspace)
	if err != nil {
		return nil, err
	}
	return s.effectiveRolesFromAssignments(assignments, roles), nil
}

func (s *IdentityDomainService) effectiveRolesFromAssignments(assignments []identitymodel.IdentityUserRoleAssignment, roles []identitymodel.IdentityRole) []identitymodel.IdentityRole {
	activeRoleIDs := make(map[string]struct{}, len(assignments))
	for _, assignment := range assignments {
		activeRoleIDs[assignment.RoleID] = struct{}{}
	}
	effective := make([]identitymodel.IdentityRole, 0, len(activeRoleIDs))
	for _, role := range roles {
		if _, assigned := activeRoleIDs[role.ID]; !assigned {
			continue
		}
		if role.Status != "" && role.Status != identitymodel.IdentityStatusActive {
			continue
		}
		if _, published := s.publishedRoleDefinition(role); !published {
			continue
		}
		effective = append(effective, role)
	}
	sort.Slice(effective, func(left, right int) bool {
		return effective[left].Key+effective[left].ID < effective[right].Key+effective[right].ID
	})
	return effective
}

func (s *IdentityDomainService) ResolvePrincipalForRole(ctx context.Context, userID, roleKey string) (identitymodel.Principal, error) {
	if err := ctx.Err(); err != nil {
		return identitymodel.Principal{}, err
	}
	return s.BuildPrincipalForRole(ctx, userID, roleKey)
}

// ResolveEffectiveRoleAssignments returns only assignments that contribute to
// the principal built for this request. Expired, revoked, and ineligible
// binding assignments are excluded.
func (s *IdentityDomainService) ResolveEffectiveRoleAssignments(ctx context.Context, userID string) ([]identitymodel.IdentityUserRoleAssignment, error) {
	now := time.Now()
	assignments, err := s.repo.ListIdentityUserRoleAssignments(ctx, s.workspace, userID)
	if err != nil {
		return nil, err
	}
	out := make([]identitymodel.IdentityUserRoleAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		if strings.TrimSpace(assignment.UserID) != strings.TrimSpace(userID) {
			continue
		}
		active, activeErr := s.identityRoleAssignmentActive(ctx, assignment, now)
		if activeErr != nil {
			return nil, activeErr
		}
		if active {
			out = append(out, assignment)
		}
	}
	return out, nil
}

func (s *IdentityDomainService) ResolveEffectivePermissions(ctx context.Context, userID string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.EffectivePermissionKeys(ctx, userID)
}

func (s *IdentityDomainService) ResolveEffectiveMenus(ctx context.Context, userID string) ([]identitymodel.IdentityMenu, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.EffectiveMenus(ctx, userID)
}

func (s *IdentityDomainService) FindUser(ctx context.Context, userID string) (identitymodel.IdentityUser, bool, error) {
	if err := ctx.Err(); err != nil {
		return identitymodel.IdentityUser{}, false, err
	}
	return s.repo.GetIdentityUser(ctx, s.workspace, userID)
}

func (s *IdentityDomainService) FindOrganizationUnit(ctx context.Context, organizationUnitID string) (identitymodel.IdentityOrganizationUnit, bool, error) {
	if err := ctx.Err(); err != nil {
		return identitymodel.IdentityOrganizationUnit{}, false, err
	}
	organizationUnits, err := s.repo.ListIdentityOrganizationUnits(ctx, s.workspace)
	if err != nil {
		return identitymodel.IdentityOrganizationUnit{}, false, err
	}
	organizationUnitID = strings.TrimSpace(organizationUnitID)
	for _, organizationUnit := range organizationUnits {
		if strings.TrimSpace(organizationUnit.ID) == organizationUnitID {
			return organizationUnit, true, nil
		}
	}
	return identitymodel.IdentityOrganizationUnit{}, false, nil
}

func (s *IdentityDomainService) ListProjectionUsers(ctx context.Context) ([]identitymodel.IdentityUser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.repo.ListIdentityUsers(ctx, s.workspace)
}

func (s *IdentityDomainService) ListProjectionRoles(ctx context.Context) ([]identitymodel.IdentityRole, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.repo.ListIdentityRoles(ctx, s.workspace)
}

func (s *IdentityDomainService) ListProjectionUserRoleAssignments(ctx context.Context, userID string) ([]identitymodel.IdentityUserRoleAssignment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.repo.ListIdentityUserRoleAssignments(ctx, s.workspace, userID)
}
