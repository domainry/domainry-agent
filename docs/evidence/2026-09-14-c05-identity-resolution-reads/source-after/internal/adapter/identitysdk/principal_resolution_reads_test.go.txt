package identitysdkadapter

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityscope "github.com/domainry/domainry-identity-sdk/application"
	authapplication "github.com/domainry/domainry-identity/internal/application/auth"
	identityapplication "github.com/domainry/domainry-identity/internal/application/identity"
	definitionmodel "github.com/domainry/domainry-identity/internal/domain/definition/model"
	identitymodel "github.com/domainry/domainry-identity/internal/domain/identity/model"
	database "github.com/domainry/domainry-identity/internal/infrastructure/persistence/database"
	authpersistence "github.com/domainry/domainry-identity/internal/infrastructure/persistence/database/auth"
	identitypersistence "github.com/domainry/domainry-identity/internal/infrastructure/persistence/database/identity"
	"github.com/domainry/domainry-identity/internal/platform/config"
)

// Count real repository IO, and inject changes during projection IO rather
// than replacing either the principal resolver or its authorization result.
type resolutionReadRepository struct {
	*identitypersistence.SQLIdentityStore
	users, userLists, roles, assignments int
	fail                                 string
	err                                  error
	duringMenus                          func(context.Context) error
}

func (r *resolutionReadRepository) ListIdentityUsers(ctx context.Context, workspace string) ([]identitymodel.IdentityUser, error) {
	r.userLists++
	return r.SQLIdentityStore.ListIdentityUsers(ctx, workspace)
}

type resolutionEligibility struct{ active bool }

func (r *resolutionEligibility) IdentityRoleBindingActive(context.Context, string, string, string, string) (bool, error) {
	return r.active, nil
}

func (r *resolutionReadRepository) GetIdentityUser(ctx context.Context, workspace, id string) (identitymodel.IdentityUser, bool, error) {
	r.users++
	if r.fail == "users" && r.users == 2 {
		return identitymodel.IdentityUser{}, false, r.err
	}
	return r.SQLIdentityStore.GetIdentityUser(ctx, workspace, id)
}

func (r *resolutionReadRepository) ListIdentityRoles(ctx context.Context, workspace string) ([]identitymodel.IdentityRole, error) {
	r.roles++
	if r.fail == "roles" && r.roles == 2 {
		return nil, r.err
	}
	return r.SQLIdentityStore.ListIdentityRoles(ctx, workspace)
}

func (r *resolutionReadRepository) ListIdentityUserRoleAssignments(ctx context.Context, workspace, id string) ([]identitymodel.IdentityUserRoleAssignment, error) {
	r.assignments++
	if r.fail == "assignments" && r.assignments == 2 {
		return nil, r.err
	}
	return r.SQLIdentityStore.ListIdentityUserRoleAssignments(ctx, workspace, id)
}

func (r *resolutionReadRepository) ListIdentityMenus(ctx context.Context, workspace string) ([]identitymodel.IdentityMenu, error) {
	if r.duringMenus != nil {
		if err := r.duringMenus(ctx); err != nil {
			return nil, err
		}
	}
	return r.SQLIdentityStore.ListIdentityMenus(ctx, workspace)
}

type principalReadFixture struct {
	repo     *resolutionReadRepository
	identity *identityapplication.IdentityApplicationService
	resolver sdkPrincipalResolver
	ctx      context.Context
	grants   []identitymodel.IdentityPermissionDefinition
}

func newPrincipalReadFixture(t *testing.T) principalReadFixture {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "principal.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	sqlStore, err := identitypersistence.NewSQLIdentityStore(t.Context(), store.DB(), store.PersistenceEngine())
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.UpsertIdentityUser(t.Context(), "workspace-primary", identitymodel.IdentityUser{ID: "subject", Name: "Subject", Status: identitymodel.IdentityStatusActive}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"reader", "writer"} {
		if err := sqlStore.UpsertIdentityRole(t.Context(), "workspace-primary", identitymodel.IdentityRole{ID: key + "-id", Key: key, Label: key, Status: identitymodel.IdentityStatusActive}); err != nil {
			t.Fatal(err)
		}
		if err := sqlStore.AssignIdentityUserRole(t.Context(), "workspace-primary", identitymodel.IdentityUserRoleAssignment{UserID: "subject", RoleID: key + "-id", Source: "manual", Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	repo := &resolutionReadRepository{SQLIdentityStore: sqlStore, err: errors.New("final authorization read unavailable")}
	grants := []identitymodel.IdentityPermissionDefinition{
		{Key: "order.read", Resource: "order", ObjectKey: "order", Action: "read", DefinitionStatus: identitymodel.IdentityPermissionDefinitionActive, Enabled: true},
		{Key: "order.write", Resource: "order", ObjectKey: "order", Action: "update", DefinitionStatus: identitymodel.IdentityPermissionDefinitionActive, Enabled: true},
	}
	identity := identityapplication.NewIdentityApplicationService(repo, grants)
	identity.ReplaceRoleDefinitions([]identitymodel.RoleSchema{
		{Key: "reader", Permissions: []identitymodel.RolePermission{{PermissionKey: "order.read", DataScope: identitymodel.IdentityDataScopeAll}}},
		{Key: "writer", Permissions: []identitymodel.RolePermission{{PermissionKey: "order.write", DataScope: identitymodel.IdentityDataScopeOwner}}},
	})
	applications, err := authapplication.NewAuthApplicationRegistrationService(authpersistence.NewAuthStore(sqlStore), "workspace-primary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applications.Register(t.Context(), "principal-test", nil); err != nil {
		t.Fatal(err)
	}
	access := identityapplication.NewIdentityEffectiveAccessApplicationService(identityapplication.IdentityEffectiveAccessDependencies{
		Identity: identity, Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{{Key: "order"}} },
	})
	ctx := identityscope.WithScope(t.Context(), identitysdk.ApplicationScope{WorkspaceID: "workspace-primary", ApplicationKey: "principal-test"})
	return principalReadFixture{repo: repo, identity: identity, resolver: sdkPrincipalResolver{binding: &sdkBinding{identity: identity, access: access, applications: applications, clock: sdkSystemClock{}}}, ctx: ctx, grants: grants}
}

func TestSDKPrincipalResolutionReusesOwnerReadsAndPreservesRoleSelection(t *testing.T) {
	for _, selection := range []struct {
		name, role, session string
		permissions         []string
	}{
		{"restricted", "reader", "", []string{"order.read"}},
		{"role-id-alias", "reader-id", "", []string{"order.read"}},
		{"effective", "", "", []string{"order.read", "order.write"}},
		{"session-display-keeps-union", "", "writer", []string{"order.read", "order.write"}},
	} {
		t.Run(selection.name, func(t *testing.T) {
			f := newPrincipalReadFixture(t)
			result, err := f.resolver.Resolve(f.ctx, identitysdk.PrincipalResolutionRequest{SubjectID: "subject", RoleKey: selection.role, SessionRoleKey: selection.session})
			if err != nil || !reflect.DeepEqual(result.Principal.Permissions, selection.permissions) || len(result.Principal.Roles) != 2 || result.Principal.User.ID != "subject" {
				t.Fatalf("resolution=%+v err=%v", result, err)
			}
			if selection.role != "" && result.Principal.RoleKey != "reader" || selection.session != "" && result.Principal.RoleKey != "writer" {
				t.Fatalf("selected role=%s", result.Principal.RoleKey)
			}
			if result.AccessBundle.AuthorizationRevision == "" || result.Principal.AuthorizationRevision != string(result.AccessBundle.AuthorizationRevision) || result.AccessBundle.Validate(time.Now().UTC()) != nil {
				t.Fatal("bundle revision or validation invalid")
			}
			if f.repo.users != 2 || f.repo.userLists != 2 || f.repo.roles != 2 || f.repo.assignments != 2 {
				t.Fatalf("duplicate IO user=%d user-list=%d roles=%d assignments=%d; want one initial read plus one fresh authorization read", f.repo.users, f.repo.userLists, f.repo.roles, f.repo.assignments)
			}
			t.Logf("real ORM reads: user=%d user-list=%d roles=%d assignments=%d", f.repo.users, f.repo.userLists, f.repo.roles, f.repo.assignments)
		})
	}
}

func TestSDKPrincipalResolutionChecksRevocationAfterProjectionIO(t *testing.T) {
	for _, change := range []string{"assignment", "user", "role", "permission", "published-policy", "expiry", "eligibility", "users-error", "roles-error", "assignments-error", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			f := newPrincipalReadFixture(t)
			eligibility := &resolutionEligibility{active: true}
			if change == "eligibility" {
				f.identity.UseRoleBindingEligibility(eligibility)
				if err := f.repo.RemoveIdentityUserRole(f.ctx, "workspace-primary", "subject", "reader-id"); err != nil {
					t.Fatal(err)
				}
				if err := f.repo.AssignIdentityUserRole(f.ctx, "workspace-primary", identitymodel.IdentityUserRoleAssignment{UserID: "subject", RoleID: "reader-id", Source: "profile_binding", BindingKey: "member", ProfileID: "member-1", Status: "active"}); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			f.repo.duringMenus = func(ioCtx context.Context) error {
				switch change {
				case "assignment":
					return f.repo.RemoveIdentityUserRole(ioCtx, "workspace-primary", "subject", "reader-id")
				case "user":
					return f.repo.SetIdentityUserStatus(ioCtx, "workspace-primary", "subject", identitymodel.IdentityStatusDisabled)
				case "role":
					return f.repo.UpsertIdentityRole(ioCtx, "workspace-primary", identitymodel.IdentityRole{ID: "reader-id", Key: "reader", Status: identitymodel.IdentityStatusDisabled})
				case "permission":
					f.grants[0].Enabled = false
					f.identity.ReplacePermissionDefinitions(f.grants)
				case "published-policy":
					f.identity.ReplaceRoleDefinitions([]identitymodel.RoleSchema{{Key: "reader"}, {Key: "writer"}})
				case "expiry":
					expired := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
					return f.repo.AssignIdentityUserRole(ioCtx, "workspace-primary", identitymodel.IdentityUserRoleAssignment{UserID: "subject", RoleID: "reader-id", Source: "manual", Status: "active", ExpiresAt: &expired})
				case "eligibility":
					eligibility.active = false
				case "users-error":
					f.repo.fail = "users"
				case "roles-error":
					f.repo.fail = "roles"
				case "assignments-error":
					f.repo.fail = "assignments"
				case "cancelled":
					cancel()
				}
				return nil
			}
			_, err := f.resolver.Resolve(ctx, identitysdk.PrincipalResolutionRequest{SubjectID: "subject", RoleKey: "reader"})
			if change == "cancelled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled error=%v", err)
				}
			} else if f.repo.fail != "" {
				if !errors.Is(err, f.repo.err) {
					t.Fatalf("repository error overwritten: %v", err)
				}
			} else {
				var coded *identitysdk.Error
				if !errors.As(err, &coded) || coded.StatusCode != http.StatusConflict || coded.Code != "identity.authorization_revision_stale" {
					t.Fatalf("changed authorization was accepted: %v", err)
				}
			}
		})
	}
}

func TestSDKPrincipalResolutionExcludesDisabledRoleFromEffectiveUnion(t *testing.T) {
	f := newPrincipalReadFixture(t)
	if err := f.repo.UpsertIdentityRole(f.ctx, "workspace-primary", identitymodel.IdentityRole{ID: "writer-id", Key: "writer", Status: identitymodel.IdentityStatusDisabled}); err != nil {
		t.Fatal(err)
	}
	result, err := f.resolver.Resolve(f.ctx, identitysdk.PrincipalResolutionRequest{SubjectID: "subject"})
	if err != nil || !reflect.DeepEqual(result.Principal.Permissions, []string{"order.read"}) || len(result.Principal.Roles) != 1 {
		t.Fatalf("disabled role entered effective authorization: result=%+v err=%v", result, err)
	}
}

func TestSDKPrincipalResolutionNeverReusesFactsAcrossRequests(t *testing.T) {
	f := newPrincipalReadFixture(t)
	request := identitysdk.PrincipalResolutionRequest{SubjectID: "subject", RoleKey: "reader"}
	if _, err := f.resolver.Resolve(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.RemoveIdentityUserRole(f.ctx, "workspace-primary", "subject", "reader-id"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.resolver.Resolve(f.ctx, request); err == nil {
		t.Fatal("revoked selected role reused from a previous request")
	}
	result, err := f.resolver.Resolve(f.ctx, identitysdk.PrincipalResolutionRequest{SubjectID: "subject"})
	if err != nil || len(result.Principal.Roles) != 1 || !reflect.DeepEqual(result.Principal.Permissions, []string{"order.write"}) {
		t.Fatalf("current remaining authorization=%+v err=%v", result, err)
	}
	if _, err := f.resolver.Resolve(f.ctx, identitysdk.PrincipalResolutionRequest{SubjectID: "subject", SessionRoleKey: "reader"}); err == nil {
		t.Fatal("revoked bearer role restored")
	}
	if _, err := f.resolver.Resolve(f.ctx, identitysdk.PrincipalResolutionRequest{SubjectID: ""}); err == nil {
		t.Fatal("empty subject became an administrator")
	}
}
