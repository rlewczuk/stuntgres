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
	keyInitializations                                 []exec.RoutineInitialization
	predicateInitialization                            exec.RoutineInitialization
}

func RecipeEvaluation(evaluation *exec.Context, dependencies []ids.ObjectRef, parent *catalog.Relation) exec.Context {
	local := *evaluation
	local.ClearNativeBodySource()
	local.RoutineGraft = false
	local.RoutineRecipe = true
	local.RoutineChecks = nil
	local.RoutineReferences, local.RoutineConsumer = dependencies, parent
	return local
}

func PrepareIndexRecipe(evaluation *exec.Context, expression expr.Expr, dependencies []ids.ObjectRef, parent *catalog.Relation, admission *exec.RoutineProgram, executable bool) (expr.Expr, error) {
	if err := exec.BeginRoutineInitialization(evaluation); err != nil {
		return nil, err
	}
	local := RecipeEvaluation(evaluation, dependencies, parent)
	local.RoutineProgram = admission
	expression, err := refreshIndexRecipe(&local, parent, dependencies, expression)
	if err != nil {
		return nil, err
	}
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
	if ordering != nil {
		ordering.BorrowSupport(evaluation)
	}
	if p.constantsPrepared {
		return nil
	}
	if owner := evaluation.Catalog.Owner(); owner != nil && owner.Sepgsql != nil && p.keyInitializations == nil {
		for _, selected := range definition.Keys {
			if selected.Derived == nil {
				continue
			}
			// Plain attribute keys have no executable recipe to initialize.
			// Mint all actual recipe owners before the first prewalk.
			host := evaluation.LiveHost()
			if host == nil {
				return ids.ErrCatalogReplaced
			}
			p.keyInitializations = make([]exec.RoutineInitialization, len(definition.Keys))
			for i, key := range definition.Keys {
				if key.Derived != nil {
					var err error
					p.keyInitializations[i], err = host.NewRoutineInitialization()
					if err != nil {
						return err
					}
				}
			}
			break
		}
	}
	if ordering != nil {
		ordering.initializations = p.keyInitializations
		ordering.parent = parent
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
		local := p.keyEvaluation(evaluation, parent, definition, i)
		prepared, err := PrepareIndexRecipe(&local, source, key.Derived.Dependencies, parent, &p.admission, false)
		evaluation.PreparedDomainChecks = local.PreparedDomainChecks
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
	if ordering != nil {
		ordering.BorrowSupport(evaluation)
	}
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
		local := p.keyEvaluation(evaluation, parent, definition, i)
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
		if owner := evaluation.Catalog.Owner(); owner != nil && owner.Sepgsql != nil {
			host := evaluation.LiveHost()
			if host == nil {
				return false, ids.ErrCatalogReplaced
			}
			var err error
			p.predicateInitialization, err = host.NewRoutineInitialization()
			if err != nil {
				return false, err
			}
		}
		source := definition.Predicate.Definition
		if !definition.RequiresInsertionOutput() {
			source = definition.Predicate.Execution
		}
		local := RecipeEvaluation(evaluation, definition.Predicate.Dependencies, parent)
		local.RoutineInitialization = p.predicateInitialization
		local.InitializationPhase = exec.IndexRecipeInitialization
		prepared, err := PrepareIndexRecipe(&local, source, definition.Predicate.Dependencies, parent, &p.admission, true)
		evaluation.PreparedDomainChecks = local.PreparedDomainChecks
		if err != nil {
			return false, err
		}
		p.predicate, p.predicatePrepared = prepared, true
	}
	local := RecipeEvaluation(evaluation, definition.Predicate.Dependencies, parent)
	local.RoutineInitialization = p.predicateInitialization
	local.InitializationPhase = exec.IndexRecipeInitialization
	local.Row = row
	value, err := p.predicate.Eval(&local)
	if err != nil {
		return false, err
	}
	truth, err := plan.TruthOf(value)
	return truth == plan.True, err
}

func (p *ExecutionPreparation) keyEvaluation(evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition, ordinal int) exec.Context {
	local := RecipeEvaluation(evaluation, definition.Keys[ordinal].Derived.Dependencies, parent)
	local.InitializationPhase = exec.IndexRecipeInitialization
	if p.keyInitializations != nil {
		local.RoutineInitialization = p.keyInitializations[ordinal]
	}
	return local
}

// ExtractKey borrows only this operation's prepared expression instance. The
// immutable HashBinding and published storage roots retain no runtime proof.
func (p *ExecutionPreparation) ExtractKey(slot int, expression expr.Expr, values []sqltype.Value, evaluation *exec.Context, parent *catalog.Relation, definition *catalog.IndexDefinition, ordinal int) (sqltype.Value, error) {
	if slot >= 0 {
		return values[slot], nil
	}
	local := p.keyEvaluation(evaluation, parent, definition, ordinal)
	return extractIndexKey(slot, expression, values, &local)
}

func NewLogicalIndexRoot(d *catalog.IndexDefinition, entries []storage.IndexEntry) *storage.IndexRoot {
	return logicalIndexRoot(d, entries, newConstruction(d))
}

// Merge/transport callers supply their real history; a new wrapper is not a
// complete build merely because its membership vector is materialized.
func logicalIndexRoot(d *catalog.IndexDefinition, entries []storage.IndexEntry, history any) *storage.IndexRoot {
	root := &storage.IndexRoot{Construction: history, Entries: entries}
	if d.Predicate != nil {
		root.Membership = make(map[ids.RowID]ids.VersionID, len(entries))
		for _, entry := range entries {
			root.SetMember(entry.Row, entry.Version)
		}
	}
	if (d.Unique || d.IsExclusion()) && d.HasRuntimeDomainKeyConversion() {
		root.RowEntries = make(map[ids.RowID]int, len(entries))
		for i := range entries {
			root.SetRowEntry(i)
		}
	}
	return root
}
