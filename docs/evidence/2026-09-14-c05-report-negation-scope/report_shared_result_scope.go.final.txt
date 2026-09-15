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
	validationBudget := 4096
	if !sharedRecordScopeValid(source, depth, &validationBudget) || !sharedRecordScopeValid(reader, depth, &validationBudget) {
		return false
	}
	remaining := 4096
	return sharedRecordScopeImplication(source, reader, depth, &remaining)
}

// Validate the whole compiled tree before proving any branch. These are the
// operators accepted by the Record storage boundary; a matching valid sibling
// cannot hide an unsupported extension or a context-dependent relation.
func sharedRecordScopeValid(scope *record.RecordScopeExpression, depth int, remaining *int) bool {
	if scope == nil {
		return true
	}
	if depth > 32 || *remaining <= 0 || scope.RelationExists {
		return false
	}
	*remaining = *remaining - 1
	switch scope.Operator {
	case "and", "or", "not":
		if scope.FieldKey != "" || len(scope.Values) != 0 || len(scope.Path) != 0 || scope.Operator == "not" && len(scope.Children) != 1 || scope.Operator != "not" && len(scope.Children) < 2 {
			return false
		}
		for i := range scope.Children {
			if !sharedRecordScopeValid(&scope.Children[i], depth+1, remaining) {
				return false
			}
		}
		return true
	case "eq", "in", "prefix", "starts_with", "exists", "not_exists":
		if scope.FieldKey == "" || len(scope.Children) != 0 {
			return false
		}
		switch scope.Operator {
		case "eq", "prefix", "starts_with":
			return len(scope.Values) == 1
		case "exists", "not_exists":
			return len(scope.Values) == 0
		}
		return true
	}
	return false
}

// Record compiles empty issued IN claims to FALSE, including at NULL rows.
// Its negation is TRUE; do not mistake it for a normal nullable comparison.
func sharedRecordScopeConstant(scope *record.RecordScopeExpression) (bool, bool) {
	if scope == nil {
		return true, true
	}
	if scope.Operator == "in" && len(scope.Values) == 0 && len(scope.Path) == 0 {
		return false, true
	}
	if scope.Operator == "not" {
		value, known := sharedRecordScopeConstant(&scope.Children[0])
		return !value, known
	}
	return false, false
}

func sharedRecordScopeNotEquivalent(scope *record.RecordScopeExpression) *record.RecordScopeExpression {
	if scope.Operator != "not" {
		return nil
	}
	child := &scope.Children[0]
	if child.Operator == "not" {
		return &child.Children[0]
	}
	// IS NULL and IS NOT NULL are complementary only at the same direct
	// column. A relation-membership expression is not a nullable-column test.
	if len(child.Path) == 0 && (child.Operator == "exists" || child.Operator == "not_exists") {
		equivalent := *child
		equivalent.Operator = "exists"
		if child.Operator == "exists" {
			equivalent.Operator = "not_exists"
		}
		return &equivalent
	}
	return nil
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
	if value, known := sharedRecordScopeConstant(reader); known && value {
		return true
	}
	if value, known := sharedRecordScopeConstant(source); known && !value {
		return true
	}
	if source == nil || source.RelationExists || reader.RelationExists {
		return false
	}
	if equivalent := sharedRecordScopeNotEquivalent(source); equivalent != nil {
		return sharedRecordScopeImplication(equivalent, reader, depth+1, remaining)
	}
	if equivalent := sharedRecordScopeNotEquivalent(reader); equivalent != nil {
		return sharedRecordScopeImplication(source, equivalent, depth+1, remaining)
	}
	if source.Operator == "not" && reader.Operator == "not" {
		// The supported atom proofs preserve FALSE <= UNKNOWN <= TRUE;
		// AND/OR preserve that order and SQL NOT reverses it. True-row
		// implication alone would be insufficient at nullable fields.
		return sharedRecordScopeImplication(&reader.Children[0], &source.Children[0], depth+1, remaining)
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
		allowed := make(map[string]struct{}, len(reader.Values))
		for _, value := range reader.Values {
			allowed[value] = struct{}{}
		}
		for _, value := range source.Values {
			if _, found := allowed[value]; !found {
				return false
			}
		}
		return true
	}
	// Exact non-relational leaf predicates are safe to compare. Unknown
	// extensions and context-dependent relationship predicates stay blocked.
	if len(source.Children) == 0 && len(reader.Children) == 0 {
		switch source.Operator {
		case "prefix", "starts_with", "exists", "not_exists":
			return reflect.DeepEqual(source, reader)
		}
	}
	return false
}

var _ modulehost.SharedResultScopeAuthorizer = (*ReportModuleQueryHost)(nil)
