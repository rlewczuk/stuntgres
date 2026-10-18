package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A persisted predicate keeps original dependencies even when planning removes
// an arm. Both trees use the immutable parent attribute layout.
type indexPredicate struct {
	definition, execution BoundExpr
	dependencies          []ids.ObjectRef
	attributes            []int32
}

// A build or command owns this value. It is never part of a published index
// descriptor, logical root, hash binding, lock key or prepared statement.
// These bits describe executable preparation, not a retained rights decision.
type indexExecutionPreparation struct {
	predicate                                          BoundExpr
	ordinary                                           []BoundExpr // Only ordinary validation without an existing ordering/hash programme.
	admission                                          routineAdmissionProgram
	constantsPrepared, predicatePrepared, keysPrepared bool
}

func indexRecipeEvaluation(evaluation *EvalContext, dependencies []ids.ObjectRef, parent *relationDescriptor) EvalContext {
	local := *evaluation
	local.routineGraft = false
	local.routineRecipe = true
	local.routineChecks = nil
	local.routineReferences, local.routineConsumer = dependencies, parent
	return local
}

func prepareIndexRecipe(evaluation *EvalContext, expression BoundExpr, dependencies []ids.ObjectRef, parent *relationDescriptor, admission *routineAdmissionProgram, executable bool) (BoundExpr, error) {
	local := indexRecipeEvaluation(evaluation, dependencies, parent)
	local.routineProgram = admission
	prepared, _, err := preflightCoercionsWithPolicy(&local, expression, true)
	evaluation.preparedDomainChecks = local.preparedDomainChecks
	if err != nil {
		return nil, err
	}
	if executable {
		err = validateExpressionJoinExecution(&local, prepared, local.catalog)
	}
	return prepared, err
}

// SQL constant planning is demanded by the first real tuple, before predicate
// truth. Nonconstant key initialization still waits for a true predicate.
func (p *indexExecutionPreparation) prepareConstants(evaluation *EvalContext, parent *relationDescriptor, definition *indexDefinition, ordering *indexOrdering, hash *hashIndexBinding) error {
	if p.constantsPrepared {
		return nil
	}
	for i, key := range definition.keys {
		if key.derived == nil {
			continue
		}
		source := key.derived.definition
		if !definition.requiresInsertionOutput() {
			// No callable or domain conversion can have disappeared here.
			// Retain the established body-free cooked programme fast path.
			source = key.derived.execution
		}
		prepared, err := prepareIndexRecipe(evaluation, source, key.derived.dependencies, parent, &p.admission, false)
		if err != nil {
			return err
		}
		if hash != nil {
			hash.expression = prepared
		} else if ordering != nil {
			ordering.derived[i] = prepared
		} else {
			if p.ordinary == nil {
				p.ordinary = make([]BoundExpr, len(definition.keys))
			}
			p.ordinary[i] = prepared
		}
	}
	p.constantsPrepared = true
	return nil
}

func (p *indexExecutionPreparation) keyProgram(i int, ordering *indexOrdering, hash *hashIndexBinding) BoundExpr {
	if hash != nil {
		return hash.expression
	}
	if ordering != nil {
		return ordering.derived[i]
	}
	return p.ordinary[i]
}

func (p *indexExecutionPreparation) prepareKeys(evaluation *EvalContext, parent *relationDescriptor, definition *indexDefinition, ordering *indexOrdering, hash *hashIndexBinding) error {
	if p.keysPrepared {
		return nil
	}
	if err := p.prepareConstants(evaluation, parent, definition, ordering, hash); err != nil {
		return err
	}
	for i, key := range definition.keys {
		if key.derived == nil {
			continue
		}
		local := indexRecipeEvaluation(evaluation, key.derived.dependencies, parent)
		local.routineProgram = &p.admission
		if err := validateExpressionJoinExecution(&local, p.keyProgram(i, ordering, hash), local.catalog); err != nil {
			return err
		}
	}
	p.keysPrepared = true
	return nil
}

func (p *indexExecutionPreparation) prepareOrdinaryKeys(evaluation *EvalContext, parent *relationDescriptor, definition *indexDefinition) error {
	return p.prepareKeys(evaluation, parent, definition, nil, nil)
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
	if err := rejectVirtualIndexUses(parent, binder.querySources[0].uses); err != nil {
		return nil, err
	}
	evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: binder.input, catalog: auth.catalog}
	evaluation.routineAuthority, evaluation.routineChecks = &auth, binder.dependencies
	evaluation.routineRecipe = true
	execution, _, err := preflightCoercionsWithPolicy(&evaluation, definition, true)
	if err != nil {
		return nil, err
	}
	if plannedVolatility(execution) != Immutable {
		return nil, sqlError("42P17", "functions in index predicate must be marked IMMUTABLE")
	}
	var dependencies []ids.ObjectRef
	if _, err = (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: parent, dependencies: &dependencies}).sql(op.Context(), auth.catalog, definition); err != nil {
		return nil, err
	}
	// Match the analyzed catalog shape, not the execution tree after folding.
	// PostgreSQL removes only a standalone nonnull Boolean TRUE predicate.
	if literal, ok := definition.(literalExpression); ok && literal.Usage.Type.ID() == sqltype.Bool && !literal.value.IsNull() {
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
func (p *indexExecutionPreparation) predicateMatches(evaluation *EvalContext, parent *relationDescriptor, definition *indexDefinition, row []sqltype.Value, ordering *indexOrdering, hash *hashIndexBinding) (bool, error) {
	if err := evaluationContext(evaluation); err != nil {
		return false, err
	}
	if err := p.prepareConstants(evaluation, parent, definition, ordering, hash); err != nil {
		return false, err
	}
	if definition.predicate == nil {
		return true, nil
	}
	if !p.predicatePrepared {
		source := definition.predicate.definition
		if !definition.requiresInsertionOutput() {
			source = definition.predicate.execution
		}
		prepared, err := prepareIndexRecipe(evaluation, source, definition.predicate.dependencies, parent, &p.admission, true)
		if err != nil {
			return false, err
		}
		p.predicate, p.predicatePrepared = prepared, true
	}
	evaluation.Row = row
	value, err := p.predicate.Eval(evaluation)
	if err != nil {
		return false, err
	}
	truth, err := TruthOf(value)
	return truth == True, err
}

func newLogicalIndexRoot(d *indexDefinition, entries []logicalIndexEntry) *logicalIndexRoot {
	root := &logicalIndexRoot{entries: entries}
	if d.predicate != nil {
		root.membership = make(map[ids.RowID]ids.VersionID, len(entries))
		for _, entry := range entries {
			root.membership[entry.row] = entry.version
		}
	}
	if (d.unique || d.isExclusion()) && d.hasRuntimeDomainKeyConversion() {
		root.rowEntries = make(map[ids.RowID]int, len(entries))
		for i, entry := range entries {
			root.rowEntries[entry.row] = i
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
