package reportmodulehost

import (
	"context"
	"reflect"

	"github.com/domainry/domainry-foundation/apperror"
	model "github.com/domainry/domainry-report-sdk/model"
	"github.com/domainry/domainry-report-sdk/modulehost"
	record "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// Resolve both current projections through the same Runtime data/field policy
// used by execution, then prove containment without querying historical rows.
func (h *ReportModuleQueryHost) AuthorizeSharedReportResultScope(ctx context.Context, report model.ReportSchema, producer, reader model.ReportSubject) error {
	denied := func() error {
		return stableReportHostError(&apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.permission.denied"})
	}
	if h == nil || h.dependencies.Access == nil || producer.Principal.WorkspaceID != reader.Principal.WorkspaceID {
		return denied()
	}
	source, err := h.reportSourceVersionRequest(ctx, report, RuntimePrincipalFromReportSubject(producer))
	if err != nil {
		return stableReportHostError(err)
	}
	target, err := h.reportSourceVersionRequest(ctx, report, RuntimePrincipalFromReportSubject(reader))
	if err != nil {
		return stableReportHostError(err)
	}
	if source.WorkspaceID != target.WorkspaceID || !reflect.DeepEqual(source.Objects, target.Objects) || len(source.Queries) != len(target.Queries) {
		return denied()
	}
	for alias, original := range source.Queries {
		current, found := target.Queries[alias]
		if !found || !sharedRecordProjectionCovered(original, current) {
			return denied()
		}
	}
	return ctx.Err()
}

func sharedRecordProjectionCovered(source, reader record.RecordListQuery) bool {
	if source.AuthorizationMode != record.RecordQueryAuthorizationUnrestricted && source.AuthorizationMode != record.RecordQueryAuthorizationPredicate || reader.AuthorizationMode != record.RecordQueryAuthorizationUnrestricted && reader.AuthorizationMode != record.RecordQueryAuthorizationPredicate {
		return false
	}
	if source.AuthorizationMode == record.RecordQueryAuthorizationPredicate && source.ScopeExpression == nil || reader.AuthorizationMode == record.RecordQueryAuthorizationPredicate && reader.ScopeExpression == nil {
		return false
	}
	if source.AuthorizationMode == record.RecordQueryAuthorizationUnrestricted && source.ScopeExpression != nil || reader.AuthorizationMode == record.RecordQueryAuthorizationUnrestricted && reader.ScopeExpression != nil {
		return false
	}
	if reader.OwnerOrganizationScopeID != "" && reader.OwnerOrganizationScopeID != source.OwnerOrganizationScopeID {
		return false
	}
	if !sharedRecordScopeCovered(source.ScopeExpression, reader.ScopeExpression, 0) {
		return false
	}
	// Compare the whole query, including fields excluded from JSON. Only the
	// row predicates proved above and data-free diagnostics are removed.
	source.AuthorizationMode, reader.AuthorizationMode = "", ""
	source.ScopeExpression, reader.ScopeExpression = nil, nil
	source.AuthorizationDiagnostic, reader.AuthorizationDiagnostic = nil, nil
	source.OwnerOrganizationScopeID, reader.OwnerOrganizationScopeID = "", ""
	return reflect.DeepEqual(source, reader)
}

func sharedRecordScopeCovered(source, reader *record.RecordScopeExpression, depth int) bool {
	remaining := 4096
	return sharedRecordScopeImplication(source, reader, depth, &remaining)
}

func sharedRecordScopeImplication(source, reader *record.RecordScopeExpression, depth int, remaining *int) bool {
	if *remaining <= 0 {
		return false
	}
	*remaining = *remaining - 1
	if depth > 32 {
		return false
	}
	if reader == nil {
		return true
	}
	if source == nil || source.RelationExists || reader.RelationExists {
		return false
	}
	if reader.Operator == "and" && len(reader.Children) > 0 {
		for i := range reader.Children {
			if !sharedRecordScopeImplication(source, &reader.Children[i], depth+1, remaining) {
				return false
			}
		}
		return true
	}
	if source.Operator == "or" && len(source.Children) > 0 {
		for i := range source.Children {
			if !sharedRecordScopeImplication(&source.Children[i], reader, depth+1, remaining) {
				return false
			}
		}
		return true
	}
	if source.Operator == "and" && len(source.Children) > 0 {
		for i := range source.Children {
			if sharedRecordScopeImplication(&source.Children[i], reader, depth+1, remaining) {
				return true
			}
		}
	}
	if reader.Operator == "or" && len(reader.Children) > 0 {
		for i := range reader.Children {
			if sharedRecordScopeImplication(source, &reader.Children[i], depth+1, remaining) {
				return true
			}
		}
	}
	if (source.Operator == "eq" || source.Operator == "in") && (reader.Operator == "eq" || reader.Operator == "in") && source.FieldKey == reader.FieldKey && reflect.DeepEqual(source.Path, reader.Path) && len(source.Children) == 0 && len(reader.Children) == 0 {
		if source.Operator == "eq" && len(source.Values) != 1 || reader.Operator == "eq" && len(reader.Values) != 1 {
			return false
		}
		for _, value := range source.Values {
			found := false
			for _, allowed := range reader.Values {
				found = found || value == allowed
			}
			if !found {
				return false
			}
		}
		return true
	}
	// Exact non-relational leaf predicates are safe to compare. Unknown
	// extensions and context-dependent relationship predicates stay blocked.
	if len(source.Children) == 0 && len(reader.Children) == 0 {
		switch source.Operator {
		case "lt", "lte", "gt", "gte", "prefix", "starts_with", "is_null", "is_not_null":
			return reflect.DeepEqual(source, reader)
		}
	}
	return false
}

var _ modulehost.SharedResultScopeAuthorizer = (*ReportModuleQueryHost)(nil)
