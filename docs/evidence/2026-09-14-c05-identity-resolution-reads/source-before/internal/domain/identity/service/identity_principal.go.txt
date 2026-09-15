package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	identitycontract "github.com/domainry/domainry-identity/internal/domain/identity/contract"
	identitymodel "github.com/domainry/domainry-identity/internal/domain/identity/model"
)

func (s *IdentityDomainService) BuildPrincipal(ctx context.Context, userID string) (identitymodel.Principal, error) {
	scoped, scopeErr := s.withWorkspacePermissions(ctx)
	if scopeErr != nil {
		return identitymodel.Principal{}, scopeErr
	}
	s = scoped

	userID = strings.TrimSpace(userID)
	if userID == "" {
		return identitymodel.Principal{Known: false}, nil
	}
	user, ok, err := s.userByID(ctx, userID)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	if !ok || user.Status != identitymodel.IdentityStatusActive {
		return identitymodel.Principal{UserID: userID, Known: false}, nil
	}
	now := time.Now()
	organizationUnitID, organizationPath, err := s.resolveUserOrganization(ctx, user)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	orgScopeIDs, supportOrgScopeIDs, reportingScopeUserIDs, err := s.resolvePrincipalScopeIDs(ctx, user.ID, organizationUnitID, user.SupportOrgID)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	supportOrgID := strings.TrimSpace(user.SupportOrgID)
	if len(supportOrgScopeIDs) == 0 {
		supportOrgID = ""
	}
	assignments, err := s.repo.ListIdentityUserRoleAssignments(ctx, s.workspace, user.ID)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	roles, err := s.repo.ListIdentityRoles(ctx, s.workspace)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	activeRoleIDs := map[string]struct{}{}
	activeAssignments := []identitymodel.IdentityUserRoleAssignment{}
	for _, assignment := range assignments {
		active, activeErr := s.identityRoleAssignmentActive(ctx, assignment, now)
		if activeErr != nil {
			return identitymodel.Principal{}, activeErr
		}
		if !active {
			continue
		}
		activeRoleIDs[assignment.RoleID] = struct{}{}
		activeAssignments = append(activeAssignments, assignment)
	}
	role := identitymodel.RoleSchema{
		Key:  "identity_effective",
		Name: "Identity Effective",
	}
	activeIdentityRoles := []identitymodel.IdentityRole{}
	activePublishedRoles := []identitymodel.RoleSchema{}
	for _, identityRole := range roles {
		if _, ok := activeRoleIDs[identityRole.ID]; !ok {
			continue
		}
		published, ok := s.publishedRoleDefinition(identityRole)
		if !ok {
			continue
		}
		activeIdentityRoles = append(activeIdentityRoles, identityRole)
		activePublishedRoles = append(activePublishedRoles, published)
		role.Permissions = append(role.Permissions, published.Permissions...)
		role.FieldPermissions = append(role.FieldPermissions, published.FieldPermissions...)
		role.ReferencePermissions = append(role.ReferencePermissions, published.ReferencePermissions...)
		role.ExportRules = append(role.ExportRules, published.ExportRules...)
		role.GrantableRoleKeys = append(role.GrantableRoleKeys, published.GrantableRoleKeys...)
		role.Guardrails = append(role.Guardrails, published.Guardrails...)
	}
	identityCanonicalizeEffectiveRole(&role)
	role.Permissions = s.identityFilterExecutablePermissions(role.Permissions)
	role.Permissions = identityFilterGuardrailDeniedPermissions(role)
	if len(activeIdentityRoles) == 1 {
		published := activePublishedRoles[0]
		published.Key = valueOrDefault(published.Key, activeIdentityRoles[0].Key)
		published.Name = valueOrDefault(published.Name, activeIdentityRoles[0].Label)
		identityCanonicalizeEffectiveRole(&published)
		published.Permissions = s.identityFilterExecutablePermissions(published.Permissions)
		published.Permissions = identityFilterGuardrailDeniedPermissions(published)
		role = published
	}
	authorizationRevision := identityAuthorizationRevision(user, orgScopeIDs, supportOrgScopeIDs, reportingScopeUserIDs, activeAssignments, activeIdentityRoles, role, identityPermissionStateFingerprint(s.PermissionDefinitions()))
	return identitymodel.Principal{
		UserID:                user.ID,
		WorkspaceID:           s.workspace,
		OrgID:                 organizationUnitID,
		OrgScopeIDs:           orgScopeIDs,
		SupportOrgID:          supportOrgID,
		SupportOrgScopeIDs:    supportOrgScopeIDs,
		ReportingScopeUserIDs: reportingScopeUserIDs,
		OrganizationPath:      organizationPath,
		Role:                  role,
		Known:                 true,
		AuthorizationRevision: authorizationRevision,
	}, nil
}

func (s *IdentityDomainService) BuildPrincipalForRole(ctx context.Context, userID string, roleKey string) (identitymodel.Principal, error) {
	scoped, scopeErr := s.withWorkspacePermissions(ctx)
	if scopeErr != nil {
		return identitymodel.Principal{}, scopeErr
	}
	s = scoped

	userID = strings.TrimSpace(userID)
	roleKey = strings.TrimSpace(roleKey)
	if userID == "" {
		return identitymodel.Principal{Known: false}, nil
	}
	user, ok, err := s.userByID(ctx, userID)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	if !ok || user.Status != identitymodel.IdentityStatusActive || roleKey == "" {
		return identitymodel.Principal{UserID: userID, Known: false}, nil
	}
	now := time.Now()
	organizationUnitID, organizationPath, err := s.resolveUserOrganization(ctx, user)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	orgScopeIDs, supportOrgScopeIDs, reportingScopeUserIDs, err := s.resolvePrincipalScopeIDs(ctx, user.ID, organizationUnitID, user.SupportOrgID)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	supportOrgID := strings.TrimSpace(user.SupportOrgID)
	if len(supportOrgScopeIDs) == 0 {
		supportOrgID = ""
	}
	userAssignments, err := s.repo.ListIdentityUserRoleAssignments(ctx, s.workspace, user.ID)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	roles, err := s.repo.ListIdentityRoles(ctx, s.workspace)
	if err != nil {
		return identitymodel.Principal{}, err
	}
	var identityRole identitymodel.IdentityRole
	found := false
	for _, role := range roles {
		if (role.Status == "" || role.Status == identitymodel.IdentityStatusActive) && (role.ID == roleKey || role.Key == roleKey) {
			identityRole = role
			found = true
			break
		}
	}
	if !found {
		return identitymodel.Principal{UserID: user.ID, Known: false}, nil
	}
	assigned := false
	var activeAssignment identitymodel.IdentityUserRoleAssignment
	for _, assignment := range userAssignments {
		active, activeErr := s.identityRoleAssignmentActive(ctx, assignment, now)
		if activeErr != nil {
			return identitymodel.Principal{}, activeErr
		}
		if assignment.RoleID == identityRole.ID && active {
			assigned = true
			activeAssignment = assignment
			break
		}
	}
	if !assigned {
		return identitymodel.Principal{UserID: user.ID, Known: false}, nil
	}
	published, ok := s.publishedRoleDefinition(identityRole)
	if !ok {
		return identitymodel.Principal{UserID: user.ID, Known: false}, nil
	}
	published.Key = valueOrDefault(published.Key, identityRole.Key)
	published.Name = valueOrDefault(published.Name, identityRole.Label)
	// BuildPrincipal and BuildPrincipalForRole are two entry points into the
	// same authorization fact. Keep every set-like role collection canonical so
	// a bearer-session request and a durable worker reauthorization produce the
	// same RoleSchema and authorization revision.
	identityCanonicalizeEffectiveRole(&published)
	published.Permissions = s.identityFilterExecutablePermissions(published.Permissions)
	published.Permissions = identityFilterGuardrailDeniedPermissions(published)
	authorizationRevision := identityAuthorizationRevision(user, orgScopeIDs, supportOrgScopeIDs, reportingScopeUserIDs, []identitymodel.IdentityUserRoleAssignment{activeAssignment}, []identitymodel.IdentityRole{identityRole}, published, identityPermissionStateFingerprint(s.PermissionDefinitions()))
	return identitymodel.Principal{
		UserID:                user.ID,
		WorkspaceID:           s.workspace,
		OrgID:                 organizationUnitID,
		OrgScopeIDs:           orgScopeIDs,
		SupportOrgID:          supportOrgID,
		SupportOrgScopeIDs:    supportOrgScopeIDs,
		ReportingScopeUserIDs: reportingScopeUserIDs,
		OrganizationPath:      organizationPath,
		Role:                  published,
		Known:                 true,
		AuthorizationRevision: authorizationRevision,
	}, nil
}

// identityFilterExecutablePermissions compiles RoleSchema grants against the
// current database-backed PermissionDefinition snapshot. RoleSchema remains
// the grant authority, while an unknown, retired, or administratively disabled
// Permission can never enter a newly resolved principal.
func (s *IdentityDomainService) identityFilterExecutablePermissions(grants []identitymodel.RolePermission) []identitymodel.RolePermission {
	definitions := s.PermissionDefinitions()
	filtered := make([]identitymodel.RolePermission, 0, len(grants))
	for _, grant := range grants {
		key := strings.TrimSpace(grant.PermissionKey)
		definition, found := definitions[key]
		if !found || definition.DefinitionStatus != identitymodel.IdentityPermissionDefinitionActive || !definition.Enabled || !grant.DataScope.Valid() {
			continue
		}
		grant.PermissionKey = key
		filtered = append(filtered, grant)
	}
	return identityCanonicalRolePermissions(filtered)
}

func identityAuthorizationRevision(user identitymodel.IdentityUser, orgScopeIDs, supportOrgScopeIDs, reportingScopeUserIDs []string, assignments []identitymodel.IdentityUserRoleAssignment, roles []identitymodel.IdentityRole, role identitymodel.RoleSchema, permissionStateFingerprint string) string {
	sort.Slice(assignments, func(left, right int) bool {
		return identityCanonicalJSON(assignments[left]) < identityCanonicalJSON(assignments[right])
	})
	sort.Slice(roles, func(left, right int) bool {
		return identityCanonicalJSON(roles[left]) < identityCanonicalJSON(roles[right])
	})
	encoded, _ := json.Marshal(struct {
		User                  identitymodel.IdentityUser                 `json:"user"`
		OrgScopeIDs           []string                                   `json:"org_scope_ids"`
		SupportOrgScopeIDs    []string                                   `json:"support_org_scope_ids"`
		ReportingScopeUserIDs []string                                   `json:"reporting_scope_user_ids"`
		Assignments           []identitymodel.IdentityUserRoleAssignment `json:"assignments"`
		Roles                 []identitymodel.IdentityRole               `json:"roles"`
		EffectiveRole         identitymodel.RoleSchema                   `json:"effective_role"`
		PermissionState       string                                     `json:"permission_state"`
	}{user, orgScopeIDs, supportOrgScopeIDs, reportingScopeUserIDs, assignments, roles, role, permissionStateFingerprint})
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

// identityPermissionStateFingerprint captures the authorization-relevant
// current database snapshot without coupling bearer revisions to labels or
// timestamps. Any key becoming unknown, disabled, retired, or active changes
// the fingerprint, including when that key is not granted by the current role.
func identityPermissionStateFingerprint(definitions map[string]identitymodel.IdentityPermissionDefinition) string {
	type state struct {
		Key     string `json:"key"`
		Status  string `json:"status"`
		Enabled bool   `json:"enabled"`
	}
	values := make([]state, 0, len(definitions))
	for key, definition := range definitions {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values = append(values, state{Key: key, Status: strings.TrimSpace(definition.DefinitionStatus), Enabled: definition.Enabled})
	}
	sort.Slice(values, func(left, right int) bool { return values[left].Key < values[right].Key })
	encoded, _ := json.Marshal(values)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func (s *IdentityDomainService) resolveUserOrganization(ctx context.Context, user identitymodel.IdentityUser) (string, string, error) {
	organizationUnitID := strings.TrimSpace(user.OrgID)
	if organizationUnitID == "" {
		return "", "", nil
	}
	organizationUnits, err := s.repo.ListIdentityOrganizationUnits(ctx, s.workspace)
	if err != nil {
		return "", "", err
	}
	for _, organizationUnit := range organizationUnits {
		if strings.TrimSpace(organizationUnit.ID) == organizationUnitID &&
			(organizationUnit.Status == "" || organizationUnit.Status == identitymodel.IdentityStatusActive) {
			return organizationUnitID, strings.TrimSpace(organizationUnit.Path), nil
		}
	}
	return "", "", nil
}

// resolvePrincipalScopeIDs expands current organization and reporting trees
// from their stable adjacency facts. Paths remain derived Identity metadata and
// are deliberately not used as authorization input.
func (s *IdentityDomainService) resolvePrincipalScopeIDs(ctx context.Context, userID, orgID, supportOrgID string) ([]string, []string, []string, error) {
	orgChildren := map[string][]string{}
	activeOrgChildren := map[string][]string{}
	activeOrgIDs := map[string]bool{}
	if strings.TrimSpace(orgID) != "" || strings.TrimSpace(supportOrgID) != "" {
		organizationUnits, err := s.repo.ListIdentityOrganizationUnits(ctx, s.workspace)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, organizationUnit := range organizationUnits {
			organizationUnitID := strings.TrimSpace(organizationUnit.ID)
			if organizationUnitID == "" {
				continue
			}
			active := organizationUnit.Status == "" || organizationUnit.Status == identitymodel.IdentityStatusActive
			activeOrgIDs[organizationUnitID] = active
			parentID := identityParentID(organizationUnit.ParentID)
			if parentID == "" {
				continue
			}
			orgChildren[parentID] = append(orgChildren[parentID], organizationUnitID)
			if active {
				activeOrgChildren[parentID] = append(activeOrgChildren[parentID], organizationUnitID)
			}
		}
	}
	orgScopeIDs := identityTreeScopeIDs(strings.TrimSpace(orgID), orgChildren)
	supportOrgScopeIDs := []string{}
	supportOrgID = strings.TrimSpace(supportOrgID)
	if activeOrgIDs[supportOrgID] {
		supportOrgScopeIDs = identityTreeScopeIDs(supportOrgID, activeOrgChildren)
	}

	users, err := s.repo.ListIdentityUsers(ctx, s.workspace)
	if err != nil {
		return nil, nil, nil, err
	}
	reportingChildren := map[string][]string{}
	for _, user := range users {
		managerID := strings.TrimSpace(user.ManagerUserID)
		if managerID == "" || strings.TrimSpace(user.ID) == "" {
			continue
		}
		reportingChildren[managerID] = append(reportingChildren[managerID], strings.TrimSpace(user.ID))
	}
	reportingScopeUserIDs := identityTreeScopeIDs(strings.TrimSpace(userID), reportingChildren)
	return orgScopeIDs, supportOrgScopeIDs, reportingScopeUserIDs, nil
}

func identityTreeScopeIDs(rootID string, children map[string][]string) []string {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return []string{}
	}
	seen := map[string]bool{}
	queue := []string{rootID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == "" || seen[current] {
			continue
		}
		seen[current] = true
		queue = append(queue, children[current]...)
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func (s *IdentityDomainService) EffectivePermissionKeys(ctx context.Context, userID string) ([]string, error) {
	principal, err := s.BuildPrincipal(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !principal.Known {
		return []string{}, nil
	}
	return identityUniqueSortedStrings(identitymodel.RolePermissionKeys(principal.Role.Permissions)), nil
}

func identityCanonicalizeEffectiveRole(role *identitymodel.RoleSchema) {
	role.Permissions = identityCanonicalRolePermissions(role.Permissions)
	sort.Slice(role.FieldPermissions, func(left, right int) bool {
		return identityCanonicalJSON(role.FieldPermissions[left]) < identityCanonicalJSON(role.FieldPermissions[right])
	})
	sort.Slice(role.ReferencePermissions, func(left, right int) bool {
		return identityCanonicalJSON(role.ReferencePermissions[left]) < identityCanonicalJSON(role.ReferencePermissions[right])
	})
	// Overlapping roles may publish the same reference policy. The SDK requires
	// one policy per reference; repeated identical restrictions add no authority.
	// Keep different policies intact so validation still rejects conflicts.
	references := make([]identitymodel.ReferencePermission, 0, len(role.ReferencePermissions))
	lastReference := ""
	for _, reference := range role.ReferencePermissions {
		canonical := identityCanonicalJSON(reference)
		if canonical != lastReference {
			references = append(references, reference)
			lastReference = canonical
		}
	}
	role.ReferencePermissions = references
	sort.Slice(role.ExportRules, func(left, right int) bool {
		return identityCanonicalJSON(role.ExportRules[left]) < identityCanonicalJSON(role.ExportRules[right])
	})
	sort.Slice(role.Guardrails, func(left, right int) bool {
		return identityCanonicalJSON(role.Guardrails[left]) < identityCanonicalJSON(role.Guardrails[right])
	})
	role.GrantableRoleKeys = identityUniqueSortedStrings(role.GrantableRoleKeys)
}

func identityFilterGuardrailDeniedPermissions(role identitymodel.RoleSchema) []identitymodel.RolePermission {
	out := make([]identitymodel.RolePermission, 0, len(role.Permissions))
	for _, permission := range role.Permissions {
		if !identitycontract.IdentityRoleGuardrailDeniesPermission(role, permission.PermissionKey) {
			out = append(out, permission)
		}
	}
	return out
}

func identityCanonicalRolePermissions(values []identitymodel.RolePermission) []identitymodel.RolePermission {
	type permissionKey struct {
		key   string
		scope identitymodel.IdentityDataScope
	}
	byKey := map[permissionKey]identitymodel.RolePermission{}
	for _, value := range values {
		value.PermissionKey = strings.TrimSpace(value.PermissionKey)
		if value.PermissionKey == "" || !value.DataScope.Valid() {
			continue
		}
		key := permissionKey{key: value.PermissionKey, scope: value.DataScope}
		current := byKey[key]
		value.AuditDenial = value.AuditDenial || current.AuditDenial
		byKey[key] = value
	}
	result := make([]identitymodel.RolePermission, 0, len(byKey))
	for _, value := range byKey {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].PermissionKey == result[right].PermissionKey {
			return result[left].DataScope < result[right].DataScope
		}
		return result[left].PermissionKey < result[right].PermissionKey
	})
	return result
}

func (s *IdentityDomainService) identityRoleAssignmentActive(ctx context.Context, assignment identitymodel.IdentityUserRoleAssignment, now time.Time) (bool, error) {
	if !identityAssignmentActive(assignment, now) {
		return false, nil
	}
	if strings.TrimSpace(assignment.BindingKey) == "" && strings.TrimSpace(assignment.ProfileID) == "" {
		return true, nil
	}
	if strings.TrimSpace(assignment.BindingKey) == "" || strings.TrimSpace(assignment.ProfileID) == "" || s.bindingEligibility == nil {
		return false, nil
	}
	return s.bindingEligibility.IdentityRoleBindingActive(ctx, s.workspace, assignment.BindingKey, assignment.ProfileID, assignment.UserID)
}

func identityUniqueSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func identityCanonicalJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func cloneIdentityPolicyExpression(value *identitymodel.IdentityPolicyExpression) *identitymodel.IdentityPolicyExpression {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Path = append([]identitymodel.IdentityPolicyRelationSegment(nil), value.Path...)
	cloned.Values = append([]string(nil), value.Values...)
	cloned.Children = make([]identitymodel.IdentityPolicyExpression, len(value.Children))
	for index := range value.Children {
		child := cloneIdentityPolicyExpression(&value.Children[index])
		cloned.Children[index] = *child
	}
	return &cloned
}

func cloneContextualFieldPolicies(values []identitymodel.ContextualFieldPolicyRule) []identitymodel.ContextualFieldPolicyRule {
	out := append([]identitymodel.ContextualFieldPolicyRule(nil), values...)
	for index := range out {
		out[index].Actions = append([]string(nil), values[index].Actions...)
		out[index].Predicate = cloneIdentityPolicyExpression(values[index].Predicate)
		if values[index].MaskStrategy != nil {
			mask := *values[index].MaskStrategy
			out[index].MaskStrategy = &mask
		}
	}
	return out
}
