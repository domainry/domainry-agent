package agenthost

import (
	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilemodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

const (
	businessScopeMaxClauses     = 64
	businessScopeMaxAtoms       = 64
	businessScopeMaxNodes       = 4096
	businessScopeMaxComparisons = 4096
)

// These clauses represent SQL WHERE matches, after NOT has been pushed down
// to atoms. They compare SDK-bound scopes; they never evaluate a saved row or
// provide an authorization identity. Each atom retains its original context.
type businessScopeAtom struct {
	predicate identity.Predicate
	principal principalmodel.Principal
	negative  bool
	digest    string
}

type businessScopeClauses [][]businessScopeAtom
type businessScopeAtomKey struct {
	digest   string
	negative bool
}
type businessScopeComparison struct{ broad, narrow businessScopeAtomKey }
type businessScopeProof struct {
	nodes, comparisons int
	exhausted          bool
	compared           map[businessScopeComparison]bool
}

func businessCombinedReadScopeCovers(reader, producer principalmodel.Principal, a, b evaluator.RecordFilter, query *businessQueryScope) bool {
	proof := &businessScopeProof{compared: map[businessScopeComparison]bool{}}
	allowed, ok := proof.filter(a, reader)
	if !ok {
		return false
	}
	produced, ok := proof.filter(b, producer)
	if !ok {
		return false
	}
	if query != nil {
		constraint, valid := proof.boundPredicate(query.predicate, query.principal, false)
		if !valid {
			return false
		}
		produced, ok = businessScopeConjunction(produced, constraint)
		if !ok {
			return false
		}
	}
	for _, source := range produced {
		covered := false
		for _, scope := range allowed {
			all := true
			for _, required := range scope {
				found := false
				for _, actual := range source {
					if proof.covers(required, actual) {
						found = true
						break
					}
					if proof.exhausted {
						return false
					}
				}
				if !found {
					all = false
					break
				}
			}
			if all {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func (p *businessScopeProof) covers(broad, narrow businessScopeAtom) bool {
	if broad.digest == narrow.digest && broad.negative == narrow.negative {
		return true
	}
	key := businessScopeComparison{broad: businessScopeAtomKey{broad.digest, broad.negative}, narrow: businessScopeAtomKey{narrow.digest, narrow.negative}}
	if covered, found := p.compared[key]; found {
		return covered
	}
	if p.comparisons >= businessScopeMaxComparisons {
		p.exhausted = true
		return false
	}
	p.comparisons++
	covered := businessPredicateCovers(broad.publicPredicate(), broad.principal, narrow.publicPredicate(), narrow.principal, 0)
	p.compared[key] = covered
	return covered
}

func (p *businessScopeProof) filter(filter evaluator.RecordFilter, principal principalmodel.Principal) (businessScopeClauses, bool) {
	clauses := businessScopeClauses{{}}
	if !filter.Unrestricted {
		clauses = nil
		for _, allow := range filter.Allow {
			branch, ok := p.boundPredicate(allow, principal, false)
			if !ok {
				return nil, false
			}
			clauses = append(clauses, branch...)
			if len(clauses) > businessScopeMaxClauses {
				return nil, false
			}
		}
	}
	// The SDK combines denials with OR then negates that complete group.
	// De Morgan therefore requires every NOT(denial), including NULL behavior.
	for _, deny := range filter.Deny {
		branch, ok := p.boundPredicate(deny, principal, true)
		if !ok {
			return nil, false
		}
		clauses, ok = businessScopeConjunction(clauses, branch)
		if !ok {
			return nil, false
		}
	}
	return clauses, true
}

func (p *businessScopeProof) boundPredicate(predicate identity.Predicate, principal principalmodel.Principal, negative bool) (businessScopeClauses, bool) {
	if !p.bounded(predicate, 0) {
		return nil, false
	}
	if _, ok := businessBoundPredicate(predicate, principal); !ok {
		return nil, false
	}
	return p.predicate(predicate, principal, negative)
}

func (p *businessScopeProof) bounded(predicate identity.Predicate, depth int) bool {
	p.nodes++
	if depth > 24 || p.nodes > businessScopeMaxNodes {
		return false
	}
	for _, child := range predicate.All {
		if !p.bounded(child, depth+1) {
			return false
		}
	}
	for _, child := range predicate.Any {
		if !p.bounded(child, depth+1) {
			return false
		}
	}
	return predicate.Not == nil || p.bounded(*predicate.Not, depth+1)
}

func (p *businessScopeProof) predicate(predicate identity.Predicate, principal principalmodel.Principal, negative bool) (businessScopeClauses, bool) {
	if predicate.Not != nil {
		return p.predicate(*predicate.Not, principal, !negative)
	}
	children, conjunction := predicate.All, true
	if len(predicate.Any) > 0 {
		children, conjunction = predicate.Any, false
	}
	if len(children) > 0 {
		conjunction = conjunction != negative
		var clauses businessScopeClauses
		if conjunction {
			clauses = businessScopeClauses{{}}
		}
		for _, child := range children {
			branch, ok := p.predicate(child, principal, negative)
			if !ok {
				return nil, false
			}
			if conjunction {
				clauses, ok = businessScopeConjunction(clauses, branch)
				if !ok {
					return nil, false
				}
			} else {
				clauses = append(clauses, branch...)
				if len(clauses) > businessScopeMaxClauses {
					return nil, false
				}
			}
		}
		return clauses, true
	}
	value, err := evaluator.ResolveValue(predicate.Value, recordpolicy.RecordSDKEvaluationContext(principal))
	if err != nil {
		return nil, false
	}
	if evaluator.IsMissingValue(value) {
		// The SDK translates a missing claim to FALSE even for native neq /
		// not_in. Apply only an outer explicit NOT before normalizing operators.
		if negative {
			return businessScopeClauses{{}}, true
		}
		return nil, true
	}
	switch predicate.Operator {
	case identity.OperatorNotEqual:
		predicate.Operator, negative = identity.OperatorEqual, !negative
	case identity.OperatorNotIn:
		predicate.Operator, negative = identity.OperatorIn, !negative
	}
	if predicate.Operator == identity.OperatorEqual && value == nil {
		// Both x = NULL and NOT(x = NULL) are UNKNOWN, never a WHERE match.
		return nil, true
	}
	if predicate.Operator == identity.OperatorExists {
		want, ok := value.(bool)
		if !ok {
			return nil, false
		}
		predicate.Value = true
		negative = negative != !want
	}
	if predicate.Operator == identity.OperatorIn {
		var values []any
		switch list := value.(type) {
		case []any:
			values = list
		case []string:
			for _, item := range list {
				values = append(values, item)
			}
		default:
			return nil, false
		}
		if len(values) == 0 || len(values) > businessScopeMaxAtoms {
			return nil, false
		}
		p.nodes += len(values)
		if p.nodes > businessScopeMaxNodes {
			return nil, false
		}
		var clauses businessScopeClauses
		if negative {
			clauses = businessScopeClauses{{}}
		}
		for _, item := range values {
			if evaluator.IsMissingValue(item) {
				return nil, false
			}
			// Bind already-resolved values through a context with no identity or
			// grants. A literal "$subject.id" in a list must remain a literal.
			literal := principalmodel.Principal{BusinessClaims: map[string]profilemodel.ClaimValue{"scope_literal": {Value: item}}}
			atom := identity.Predicate{Fact: predicate.Fact, Operator: identity.OperatorEqual, Value: "$context.scope_literal"}
			var branch businessScopeClauses
			if item != nil {
				var ok bool
				branch, ok = businessScopeSingleAtom(atom, literal, negative)
				if !ok {
					return nil, false
				}
			}
			if negative {
				var ok bool
				clauses, ok = businessScopeConjunction(clauses, branch)
				if !ok {
					return nil, false
				}
			} else {
				clauses = append(clauses, branch...)
			}
		}
		return clauses, true
	}
	return businessScopeSingleAtom(predicate, principal, negative)
}

func businessScopeSingleAtom(predicate identity.Predicate, principal principalmodel.Principal, negative bool) (businessScopeClauses, bool) {
	digest, ok := businessBoundPredicate(predicate, principal)
	if !ok {
		return nil, false
	}
	return businessScopeClauses{{{predicate: predicate, principal: principal, negative: negative, digest: digest}}}, true
}

func (a businessScopeAtom) publicPredicate() identity.Predicate {
	if !a.negative {
		return a.predicate
	}
	p := a.predicate
	switch p.Operator {
	case identity.OperatorEqual:
		p.Operator = identity.OperatorNotEqual
	case identity.OperatorExists:
		p.Value = false
	default:
		excluded := p
		p = identity.Predicate{Not: &excluded}
	}
	return p
}

func businessScopeConjunction(left, right businessScopeClauses) (businessScopeClauses, bool) {
	var out businessScopeClauses
	for _, a := range left {
		for _, b := range right {
			clause := append([]businessScopeAtom(nil), a...)
			contradiction := false
			for _, atom := range b {
				duplicate := false
				for _, existing := range clause {
					if existing.digest == atom.digest {
						if existing.negative != atom.negative {
							contradiction = true
						}
						duplicate = true
						break
					}
				}
				if contradiction {
					break
				}
				if !duplicate {
					clause = append(clause, atom)
				}
				if len(clause) > businessScopeMaxAtoms {
					return nil, false
				}
			}
			// Only an exact bound P AND NOT(P) has no TRUE rows. Different
			// literal values/types may compare equal under SQL affinity/collation.
			if contradiction {
				continue
			}
			out = append(out, clause)
			if len(out) > businessScopeMaxClauses {
				return nil, false
			}
		}
	}
	return out, true
}
