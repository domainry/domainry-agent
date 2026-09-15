package agenthost

import (
	"context"
	"strconv"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilemodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// This principal is a local literal-binding context only: it has no Identity
// subject, AccessBundle or grants and never crosses an execution/read port.
type businessQueryScope struct {
	predicate identity.Predicate
	principal principalmodel.Principal
}

func (h *ConversationBusinessHost) businessReadQueryScopeCovers(ctx context.Context, reader, producer principalmodel.Principal, object string, filters []agent.ConversationBusinessFilter) bool {
	return h.businessReadConstrainedQueryScopeCovers(ctx, reader, producer, object, filters, nil)
}

func (h *ConversationBusinessHost) businessReadConstrainedQueryScopeCovers(ctx context.Context, reader, producer principalmodel.Principal, object string, filters []agent.ConversationBusinessFilter, required *recordmodel.RecordFilterExpression) bool {
	if businessReadScopeCovers(reader, producer, object) {
		return true
	}
	if len(filters) == 0 && required == nil {
		return false
	}
	for _, schema := range h.objects(ctx, producer) {
		if schema.Key != object {
			continue
		}
		definitions := map[string]agent.ConversationBusinessField{}
		types := map[string]string{}
		for _, field := range schema.Fields {
			definitions[field.Key] = businessField(field, object, producer)
			types[field.Key] = field.Type
		}
		expression, err := businessQueryFilterExpression(schema, definitions, filters, required)
		if err != nil || expression == nil {
			return false
		}
		nodes := expression.Children
		if expression.Operator != "and" {
			nodes = append(nodes, *expression)
		}
		query := &businessQueryScope{principal: principalmodel.Principal{BusinessClaims: map[string]profilemodel.ClaimValue{}}}
		predicates := make([]identity.Predicate, 0, len(nodes))
		for i, node := range nodes {
			predicate := identity.Predicate{Fact: node.Field}
			value := node.Value
			switch node.Operator {
			case "eq":
				predicate.Operator = identity.OperatorEqual
			case "ne":
				predicate.Operator = identity.OperatorNotEqual
			case "in", "not_in":
				predicate.Operator = identity.OperatorIn
				if node.Operator == "not_in" {
					predicate.Operator = identity.OperatorNotIn
				}
				value = node.Values
			case "is_null", "is_not_null":
				predicate.Operator, value = identity.OperatorExists, node.Operator == "is_not_null"
			case "gt", "gte", "lt", "lte", "contains":
				// These filters imply nonnull. Ordered/substring semantics are
				// not SDK policy atoms; retain only this weaker, sound constraint.
				predicate.Operator, value = identity.OperatorExists, true
			default:
				return false
			}
			if types[node.Field] == "currency" || types[node.Field] == "percent" {
				// Runtime encodes exact decimals for the active database. A raw
				// SDK atom is not that encoded comparison; nullness is unchanged.
				if predicate.Operator != identity.OperatorExists {
					predicate.Operator, value = identity.OperatorExists, true
				}
			}
			key := "query_literal_" + strconv.Itoa(i)
			query.principal.BusinessClaims[key] = profilemodel.ClaimValue{Value: value}
			predicate.Value = "$context." + key
			predicates = append(predicates, predicate)
		}
		if len(predicates) == 1 {
			query.predicate = predicates[0]
		} else {
			query.predicate = identity.Predicate{All: predicates}
		}
		return businessReadScopeWithQuery(reader, producer, object, query)
	}
	return false
}

// Called only after authenticating the original input/page and checking both
// principals' current relationship access. Reverse keys fix the original
// foreign-key constraint. A forward reference can change, so its current
// parent value must never substitute for an authenticated historical target.
func (h *ConversationBusinessHost) businessReadRelatedQueryScopeCovers(ctx context.Context, reader, producer principalmodel.Principal, q agent.ConversationBusinessRelatedQuery, page agent.ConversationBusinessRelatedPage) bool {
	if h.businessReadQueryScopeCovers(ctx, reader, producer, page.ObjectKey, q.Filters) {
		return true
	}
	for _, relation := range businessRelations(h.schema.ForPrincipal(ctx, producer), producer, q.ObjectKey) {
		if relation.Key != q.RelationKey || relation.ObjectKey != page.ObjectKey {
			continue
		}
		var required recordmodel.RecordFilterExpression
		switch relation.Direction {
		case "reverse":
			required = recordmodel.RecordFilterExpression{Field: relation.field, Operator: "eq", Value: q.RecordID}
		case "forward":
			// The source query's unique-ID constraint permits at most one
			// result. Only a complete, authenticated singleton proves which
			// original ID constrained that query; an empty page cannot.
			if page.Total == nil || *page.Total != 1 || len(page.Items) != 1 || page.HasNext || page.NextCursor != "" || page.Items[0].ID == "" {
				return false
			}
			required = recordmodel.RecordFilterExpression{Field: "id", Operator: "eq", Value: page.Items[0].ID}
		default:
			return false
		}
		return h.businessReadConstrainedQueryScopeCovers(ctx, reader, producer, page.ObjectKey, q.Filters, &required)
	}
	return false
}

// The producer's policy and the query constraints form a conjunction, but each
// retains its own binding context. Never overwrite producer claims to bind a
// literal query string, or rebind that string as a Subject/business reference.
func businessPredicateCoversQuery(broad identity.Predicate, broadPrincipal principalmodel.Principal, narrow *identity.Predicate, narrowPrincipal principalmodel.Principal, query *businessQueryScope, depth int) bool {
	if depth > 24 {
		return false
	}
	if _, ok := businessBoundPredicate(broad, broadPrincipal); !ok {
		return false
	}
	if narrow != nil {
		if _, ok := businessBoundPredicate(*narrow, narrowPrincipal); !ok {
			return false
		}
	}
	if query != nil {
		if _, ok := businessBoundPredicate(query.predicate, query.principal); !ok {
			return false
		}
	}
	if narrow != nil && businessPredicateCovers(broad, broadPrincipal, *narrow, narrowPrincipal, depth) {
		return true
	}
	if query != nil && businessPredicateCovers(broad, broadPrincipal, query.predicate, query.principal, depth) {
		return true
	}
	if len(broad.All) > 0 {
		for _, child := range broad.All {
			if !businessPredicateCoversQuery(child, broadPrincipal, narrow, narrowPrincipal, query, depth+1) {
				return false
			}
		}
		return true
	}
	if narrow != nil && len(narrow.Any) > 0 {
		for _, child := range narrow.Any {
			if !businessPredicateCoversQuery(broad, broadPrincipal, &child, narrowPrincipal, query, depth+1) {
				return false
			}
		}
		return true
	}
	for _, child := range broad.Any {
		if businessPredicateCoversQuery(child, broadPrincipal, narrow, narrowPrincipal, query, depth+1) {
			return true
		}
	}
	if narrow != nil {
		for _, child := range narrow.All {
			if businessPredicateCoversQuery(broad, broadPrincipal, &child, narrowPrincipal, query, depth+1) {
				return true
			}
		}
	}
	return false
}
