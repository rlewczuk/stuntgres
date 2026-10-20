package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A build or command owns this value. It is never part of a published index
// descriptor, logical root, hash binding, lock key or prepared statement.
// These bits describe executable preparation, not a retained rights decision.
type indexExecutionPreparation struct {
	predicate                                          expr.Expr
	ordinary                                           []expr.Expr // Only ordinary validation without an existing ordering/hash programme.
	admission                                          routineAdmissionProgram
	constantsPrepared, predicatePrepared, keysPrepared bool
}

func indexRecipeEvaluation(evaluation *exec.Context, dependencies []ids.ObjectRef, parent *catalog.Relation) exec.Context {
	local := *evaluation
	local.RoutineGraft = false
	local.RoutineRecipe = true
	local.RoutineChecks = nil
	local.RoutineReferences, local.RoutineConsumer = dependencies, parent
	return local
}

func prepareIndexRecipe(evaluation *exec.Context, expression expr.Expr, dependencies []ids.ObjectRef, parent *catalog.Relation, admission *routineAdmissionProgram, executable bool) (expr.Expr, error) {
	local := indexRecipeEvaluation(evaluation, dependencies, parent)
	local.RoutineProgram = admission
	prepared, _, err := preflightCoercionsWithPolicy(&local, expression, true)
	evaluation.PreparedDomainChecks = local.PreparedDomainChecks
	if err != nil {
		return nil, err
	}
	if executable {
		err = validateExpressionJoinExecution(&local, prepared, local.Catalog)
	}
	return prepared, err
}

// SQL constant planning is demanded by the first real tuple, before predicate
// truth. Nonconstant key initialization still waits for a true predicate.
func (p *indexExecutionPreparation) prepareConstants(evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition, ordering *indexOrdering, hash *storage.HashBinding) error {
	if p.constantsPrepared {
		return nil
	}
	for i, key := range definition.Keys {
		if key.Derived == nil {
			continue
		}
		source := key.Derived.Definition
		if !definition.RequiresInsertionOutput() {
			// No callable or domain conversion can have disappeared here.
			// Retain the established body-free cooked programme fast path.
			source = key.Derived.Execution
		}
		prepared, err := prepareIndexRecipe(evaluation, source, key.Derived.Dependencies, parent, &p.admission, false)
		if err != nil {
			return err
		}
		if hash != nil {
			hash.Expression = prepared
		} else if ordering != nil {
			ordering.derived[i] = prepared
		} else {
			if p.ordinary == nil {
				p.ordinary = make([]expr.Expr, len(definition.Keys))
			}
			p.ordinary[i] = prepared
		}
	}
	p.constantsPrepared = true
	return nil
}

func (p *indexExecutionPreparation) keyProgram(i int, ordering *indexOrdering, hash *storage.HashBinding) expr.Expr {
	if hash != nil {
		return hash.Expression
	}
	if ordering != nil {
		return ordering.derived[i]
	}
	return p.ordinary[i]
}

func (p *indexExecutionPreparation) prepareKeys(evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition, ordering *indexOrdering, hash *storage.HashBinding) error {
	if p.keysPrepared {
		return nil
	}
	if err := p.prepareConstants(evaluation, parent, definition, ordering, hash); err != nil {
		return err
	}
	for i, key := range definition.Keys {
		if key.Derived == nil {
			continue
		}
		local := indexRecipeEvaluation(evaluation, key.Derived.Dependencies, parent)
		local.RoutineProgram = &p.admission
		if err := validateExpressionJoinExecution(&local, p.keyProgram(i, ordering, hash), local.Catalog); err != nil {
			return err
		}
	}
	p.keysPrepared = true
	return nil
}

func (p *indexExecutionPreparation) prepareOrdinaryKeys(evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition) error {
	return p.prepareKeys(evaluation, parent, definition, nil, nil)
}

// Caller holds engine.mu and supplies final parent/catalog authority.
func (e *Engine) bindIndexPredicate(op *session.Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation, raw parser.Expr) (*catalog.IndexPredicate, error) {
	if raw == nil {
		return nil, nil
	}
	binder := e.indexExpressionBinder(op, input, auth, parent)
	binder.Consumer.IndexPredicateConsumer = true
	definition, err := binder.BindBoolean(raw, analyze.FilterContext, "WHERE")
	if err != nil {
		return nil, err
	}
	if err := rejectVirtualIndexUses(parent, binder.Query.Sources[0].Uses); err != nil {
		return nil, err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: binder.Input, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.RoutineAuthority, evaluation.RoutineChecks = &auth, binder.Dependencies
	evaluation.RoutineRecipe = true
	execution, _, err := preflightCoercionsWithPolicy(&evaluation, definition, true)
	if err != nil {
		return nil, err
	}
	if exec.PlannedVolatility(execution) != expr.Immutable {
		return nil, sqlError("42P17", "functions in index predicate must be marked IMMUTABLE")
	}
	var dependencies []ids.ObjectRef
	if _, err = (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.Owner, roles: auth.Roles}, owner: parent, dependencies: &dependencies}).sql(op.Context(), auth.Catalog, definition); err != nil {
		return nil, err
	}
	// Match the analyzed catalog shape, not the execution tree after folding.
	// PostgreSQL removes only a standalone nonnull Boolean TRUE predicate.
	if literal, ok := definition.(plan.LiteralExpr); ok && literal.Usage.Type.ID() == sqltype.Bool && !literal.Value.IsNull() {
		truth, err := plan.TruthOf(literal.Value)
		if err != nil {
			return nil, err
		}
		if truth == plan.True {
			return nil, nil
		}
	}
	return &catalog.IndexPredicate{Definition: definition, Execution: execution, Attributes: parent.Attributes, Dependencies: dependencies}, nil
}

// Shared build/maintenance/private-admission contract:
// absent includes; only TRUE includes; errors and cancellation veto publication.
// The caller owns the evaluation frame and row, with no per-row allocation.
func (p *indexExecutionPreparation) predicateMatches(evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition, row []sqltype.Value, ordering *indexOrdering, hash *storage.HashBinding) (bool, error) {
	if err := exec.EvaluationContext(evaluation); err != nil {
		return false, err
	}
	if err := p.prepareConstants(evaluation, parent, definition, ordering, hash); err != nil {
		return false, err
	}
	if definition.Predicate == nil {
		return true, nil
	}
	if !p.predicatePrepared {
		source := definition.Predicate.Definition
		if !definition.RequiresInsertionOutput() {
			source = definition.Predicate.Execution
		}
		prepared, err := prepareIndexRecipe(evaluation, source, definition.Predicate.Dependencies, parent, &p.admission, true)
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
	truth, err := plan.TruthOf(value)
	return truth == plan.True, err
}

func newLogicalIndexRoot(d *catalog.IndexDefinition, entries []storage.IndexEntry) *storage.IndexRoot {
	root := &storage.IndexRoot{Entries: entries}
	if d.Predicate != nil {
		root.Membership = make(map[ids.RowID]ids.VersionID, len(entries))
		for _, entry := range entries {
			root.Membership[entry.Row] = entry.Version
		}
	}
	if (d.Unique || d.IsExclusion()) && d.HasRuntimeDomainKeyConversion() {
		root.RowEntries = make(map[ids.RowID]int, len(entries))
		for i, entry := range entries {
			root.RowEntries[entry.Row] = i
		}
	}
	return root
}
