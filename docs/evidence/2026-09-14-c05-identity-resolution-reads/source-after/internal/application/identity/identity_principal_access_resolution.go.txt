package identity

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitymodel "github.com/domainry/domainry-identity/internal/domain/identity/model"
)

type IdentityPrincipalAccessResolution struct {
	Principal identitymodel.Principal
	User      identitymodel.IdentityUser
	Roles     []identitymodel.IdentityRole
	Snapshot  identitymodel.IdentityEffectiveAccessSnapshot
}

// ResolvePrincipalAccess is the internal principal-resolution use case, not
// the user-facing effective-access endpoint. It accepts only subject selection;
// callers cannot supply a principal or authorization facts to skip owner reads.
func (s *IdentityEffectiveAccessApplicationService) ResolvePrincipalAccess(ctx context.Context, workspaceID, userID, roleKey string) (IdentityPrincipalAccessResolution, error) {
	var empty IdentityPrincipalAccessResolution
	if s == nil || s.dependencies.Identity == nil || s.dependencies.Objects == nil {
		return empty, internalError("resolve principal access", nil)
	}
	scoped, err := s.dependencies.Identity.ForWorkspace(workspaceID)
	if err != nil {
		return empty, err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, workspaceID)
	roleKey = strings.TrimSpace(roleKey)
	facts, err := scoped.ResolvePrincipalContext(ctx, userID, roleKey)
	if err != nil {
		return empty, err
	}
	if !facts.Principal.Known {
		return empty, &apperror.AppError{Kind: apperror.KindForbidden, Code: "identity.principal_unavailable"}
	}
	snapshot, err := s.snapshotFromPrincipalContext(ctx, scoped, facts.Principal, facts.Assignments, facts.ProjectionRoles, roleKey)
	if err != nil {
		return empty, err
	}
	// Keep the existing fresh revision check, now after projection IO. Do not
	// reuse the first read for this check: revocation, expiry, eligibility and
	// permission changes during resolution must still invalidate the result.
	var current identitymodel.Principal
	if roleKey != "" {
		current, err = scoped.ResolvePrincipalForRole(ctx, facts.Principal.UserID, roleKey)
	} else {
		current, err = scoped.ResolvePrincipal(ctx, facts.Principal.UserID)
	}
	if err != nil {
		return empty, err
	}
	if !current.Known || current.AuthorizationRevision != facts.Principal.AuthorizationRevision {
		return empty, &apperror.AppError{Kind: apperror.KindConflict, Code: "identity.authorization_revision_stale"}
	}
	return IdentityPrincipalAccessResolution{Principal: facts.Principal, User: facts.User, Roles: facts.EffectiveRoles, Snapshot: snapshot}, nil
}
