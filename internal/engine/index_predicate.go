package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A persisted predicate keeps original dependencies even when planning removes
// an arm. Both trees use the immutable parent attribute layout.
type indexPredicate struct {
	definition, execution BoundExpr
	dependencies          []ObjectRef
	attributes            []int32
}

// Caller holds engine.mu and supplies final parent/catalog authority.
func (e *Engine) bindIndexPredicate(op *Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *relationDescriptor, raw parser.Expr) (*indexPredicate, error) {
	if raw == nil {
		return nil, nil
	}
	binder := e.indexExpressionBinder(op, input, auth, parent)
	binder.indexPredicateConsumer = true
	definition, err := binder.bindBoolean(raw, filterContext, "WHERE")
	if err != nil {
		return nil, err
	}
	evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: input}
	execution, _, err := preflightCoercionsWithPolicy(&evaluation, definition, true)
	if err != nil {
		return nil, err
	}
	if plannedVolatility(execution) != Immutable {
		return nil, sqlError("42P17", "functions in index predicate must be marked IMMUTABLE")
	}
	var dependencies []ObjectRef
	if _, err = (scalarRenderer{owner: parent, dependencies: &dependencies}).sql(op.Context(), auth.catalog, definition); err != nil {
		return nil, err
	}
	// Match the analyzed catalog shape, not the execution tree after folding.
	// PostgreSQL removes only a standalone nonnull Boolean TRUE predicate.
	if literal, ok := definition.(literalExpression); ok && literal.usage.Type.ID() == sqltype.Bool && !literal.value.IsNull() {
		truth, err := TruthOf(literal.value)
		if err != nil {
			return nil, err
		}
		if truth == True {
			return nil, nil
		}
	}
	return &indexPredicate{definition: definition, execution: execution, attributes: parent.attributes, dependencies: dependencies}, nil
}

// Shared build/maintenance/private-admission contract:
// absent includes; only TRUE includes; errors and cancellation veto publication.
// The caller owns the evaluation frame and row, with no per-row allocation.
func indexPredicateMatches(evaluation *EvalContext, predicate *indexPredicate, row []sqltype.Value) (bool, error) {
	if err := evaluationContext(evaluation); err != nil {
		return false, err
	}
	if predicate == nil {
		return true, nil
	}
	evaluation.Row = row
	value, err := predicate.execution.Eval(evaluation)
	if err != nil {
		return false, err
	}
	truth, err := TruthOf(value)
	return truth == True, err
}

func newLogicalIndexRoot(d *indexDefinition, entries []logicalIndexEntry) *logicalIndexRoot {
	root := &logicalIndexRoot{entries: entries}
	if d.predicate != nil {
		root.membership = make(map[RowID]VersionID, len(entries))
		for _, entry := range entries {
			root.membership[entry.row] = entry.version
		}
	}
	return root
}

// Strong exact implication on unresolved typed trees, not runtime values. Only
// AND is decomposed; an independent fixed conjunct can survive parameters in
// other query atoms. Existing structural identity owns provider/origin semantics.
func partialPredicateImpliedBy(query, predicate BoundExpr) bool {
	if predicate == nil {
		return true
	}
	if query == nil {
		return false
	}
	if p, ok := predicate.(logicalExpression); ok && p.op == parser.LogicalAnd {
		return partialPredicateImpliedBy(query, p.left) && partialPredicateImpliedBy(query, p.right)
	}
	if predicate.Properties().ParameterDependent || plannedVolatility(predicate) != Immutable {
		return false
	}
	return partialPredicateAtom(query, predicate)
}

func partialPredicateAtom(query, predicate BoundExpr) bool {
	if q, ok := query.(logicalExpression); ok && q.op == parser.LogicalAnd {
		return partialPredicateAtom(q.left, predicate) || partialPredicateAtom(q.right, predicate)
	}
	return !query.Properties().ParameterDependent && plannedVolatility(query) == Immutable && sameGroupExpression(query, predicate)
}
