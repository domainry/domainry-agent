package dataaccess

import (
	"sort"
	"strings"

	"github.com/domainry/domainry-agent-sdk/modulehost"
)

// Authorization is the explicit application-to-repository record boundary for
// one exact permission. All deliberately means persistence must add no range
// predicate beyond the mandatory workspace boundary.
type Authorization struct {
	Principal       modulehost.Principal
	PermissionKey   string
	All             bool
	SubjectIDs      []string
	OrganizationIDs []string
}

func (authorization Authorization) Normalized() Authorization {
	authorization.PermissionKey = strings.TrimSpace(authorization.PermissionKey)
	authorization.Principal.WorkspaceID = strings.TrimSpace(authorization.Principal.WorkspaceID)
	authorization.Principal.UserID = strings.TrimSpace(authorization.Principal.UserID)
	if authorization.All {
		authorization.SubjectIDs = nil
		authorization.OrganizationIDs = nil
		return authorization
	}
	authorization.SubjectIDs = normalizedValues(authorization.SubjectIDs)
	authorization.OrganizationIDs = normalizedValues(authorization.OrganizationIDs)
	return authorization
}

func (authorization Authorization) ValidFor(permissionKey, workspaceID string) bool {
	authorization = authorization.Normalized()
	permissionKey, workspaceID = strings.TrimSpace(permissionKey), strings.TrimSpace(workspaceID)
	if permissionKey == "" || workspaceID == "" || authorization.PermissionKey != permissionKey || authorization.Principal.WorkspaceID != workspaceID || !authorization.Principal.HasAuthorizedAction(permissionKey) {
		return false
	}
	return authorization.All || len(authorization.SubjectIDs) > 0 || len(authorization.OrganizationIDs) > 0
}

func normalizedValues(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
