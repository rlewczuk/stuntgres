package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Binding runs under engine.mu. Candidate identity and definition are immutable;
// source capture revalidates them rather than reopening names or substituting
// prepared values. All candidates have RelationClassID and SubID zero.
func (q *queryScopeState) PartialIndexCandidate(parent ids.ObjectRef, query expr.Expr) *catalog.Relation {
	if q.Auth == nil {
		return nil
	}
	var chosen *catalog.Relation
	for _, descriptor := range q.Auth.ReadCatalog().Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address || index.SQLIndex.Predicate == nil {
			continue
		}
		if !PartialPredicateImpliedBy(query, index.SQLIndex.Predicate.Definition) {
			continue
		}
		if chosen == nil || index.Address().ObjectID < chosen.Address().ObjectID {
			chosen = index
		}
	}
	return chosen
}

// Strong exact implication on unresolved typed trees, not runtime values. Only
// AND is decomposed; an independent fixed conjunct can survive parameters in
// other query atoms. Existing structural identity owns provider/origin semantics.
func PartialPredicateImpliedBy(query, predicate expr.Expr) bool {
	if predicate == nil {
		return true
	}
	if query == nil {
		return false
	}
	if p, ok := predicate.(plan.LogicalExpr); ok && p.Op == parser.LogicalAnd {
		return PartialPredicateImpliedBy(query, p.Left) && PartialPredicateImpliedBy(query, p.Right)
	}
	if predicate.Properties().ParameterDependent || exec.PlannedVolatility(predicate) != expr.Immutable {
		return false
	}
	return partialPredicateAtom(query, predicate)
}

func partialPredicateAtom(query, predicate expr.Expr) bool {
	if q, ok := query.(plan.LogicalExpr); ok && q.Op == parser.LogicalAnd {
		return partialPredicateAtom(q.Left, predicate) || partialPredicateAtom(q.Right, predicate)
	}
	return !query.Properties().ParameterDependent && exec.PlannedVolatility(query) == expr.Immutable && SameGroupExpression(query, predicate)
}
