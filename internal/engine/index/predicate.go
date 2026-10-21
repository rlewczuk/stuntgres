package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// A build or command owns this value. It is never part of a published index
// descriptor, logical root, hash binding, lock key or prepared statement.
// These bits describe executable preparation, not a retained rights decision.
type ExecutionPreparation struct {
	predicate                                          expr.Expr
	Ordinary                                           []expr.Expr // Only ordinary validation without an existing ordering/hash programme.
	admission                                          exec.RoutineProgram
	constantsPrepared, predicatePrepared, keysPrepared bool
}

func RecipeEvaluation(evaluation *exec.Context, dependencies []ids.ObjectRef, parent *catalog.Relation) exec.Context {
	local := *evaluation
	local.RoutineGraft = false
	local.RoutineRecipe = true
	local.RoutineChecks = nil
	local.RoutineReferences, local.RoutineConsumer = dependencies, parent
	return local
}

func PrepareIndexRecipe(evaluation *exec.Context, expression expr.Expr, dependencies []ids.ObjectRef, parent *catalog.Relation, admission *exec.RoutineProgram, executable bool) (expr.Expr, error) {
	local := RecipeEvaluation(evaluation, dependencies, parent)
	local.RoutineProgram = admission
	prepared, _, err := exec.PreflightCoercionsWithPolicy(&local, expression, true)
	evaluation.PreparedDomainChecks = local.PreparedDomainChecks
	if err != nil {
		return nil, err
	}
	if executable {
		err = exec.AdmitExpression(&local, prepared, local.Catalog)
	}
	return prepared, err
}

// SQL constant planning is demanded by the first real tuple, before predicate
// truth. Nonconstant key initialization still waits for a true predicate.
func (p *ExecutionPreparation) prepareConstants(evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition, ordering *Ordering, hash *storage.HashBinding) error {
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
		prepared, err := PrepareIndexRecipe(evaluation, source, key.Derived.Dependencies, parent, &p.admission, false)
		if err != nil {
			return err
		}
		if hash != nil {
			hash.Expression = prepared
		} else if ordering != nil {
			ordering.Derived[i] = prepared
		} else {
			if p.Ordinary == nil {
				p.Ordinary = make([]expr.Expr, len(definition.Keys))
			}
			p.Ordinary[i] = prepared
		}
	}
	p.constantsPrepared = true
	return nil
}

func (p *ExecutionPreparation) keyProgram(i int, ordering *Ordering, hash *storage.HashBinding) expr.Expr {
	if hash != nil {
		return hash.Expression
	}
	if ordering != nil {
		return ordering.Derived[i]
	}
	return p.Ordinary[i]
}

func (p *ExecutionPreparation) PrepareKeys(evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition, ordering *Ordering, hash *storage.HashBinding) error {
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
		local := RecipeEvaluation(evaluation, key.Derived.Dependencies, parent)
		local.RoutineProgram = &p.admission
		if err := exec.AdmitExpression(&local, p.keyProgram(i, ordering, hash), local.Catalog); err != nil {
			return err
		}
	}
	p.keysPrepared = true
	return nil
}

func (p *ExecutionPreparation) PrepareOrdinaryKeys(evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition) error {
	return p.PrepareKeys(evaluation, parent, definition, nil, nil)
}

// Shared build/maintenance/private-admission contract:
// absent includes; only TRUE includes; errors and cancellation veto publication.
// The caller owns the evaluation frame and row, with no per-row allocation.
func (p *ExecutionPreparation) PredicateMatches(evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition, row []sqltype.Value, ordering *Ordering, hash *storage.HashBinding) (bool, error) {
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
		prepared, err := PrepareIndexRecipe(evaluation, source, definition.Predicate.Dependencies, parent, &p.admission, true)
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

func NewLogicalIndexRoot(d *catalog.IndexDefinition, entries []storage.IndexEntry) *storage.IndexRoot {
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
