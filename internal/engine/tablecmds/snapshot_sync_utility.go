package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// The historical row frontier is not an expression snapshot. Collect possible
// cooked execution before the genuine first runtime preflight/Eval, preserving
// the sole-stage parent's evaluation versus later-child binding/error order.
func prepareAlterExpressionSnapshot(scope *core.StatementScope, read catalog.Read, next *catalog.Relation, additions []table.AddedColumn, transformations []index.ColumnTransformation, generations []int32, addedIndexes []ids.ObjectAddress, rowWork, execute bool, checks ...[]catalog.CheckConstraint) error {
	e := scope.Engine()
	e.Mu.Lock()
	for _, added := range additions {
		noteAddedSnapshotDemand(scope, read, next, added)
	}
	if rowWork {
		for _, transformation := range transformations {
			scope.NoteUtilityExpressionDemandLocked(read, transformation.Expression)
			for _, dependency := range transformation.Dependencies {
				scope.NoteUtilityRoutineDemandLocked(read, dependency.Ref.Address)
			}
		}
		for _, attribute := range generations {
			scope.NoteUtilityExpressionDemandLocked(read, next.ColumnMeta.Generations[attribute].Planned)
		}
		for _, selected := range checks {
			scope.NoteUtilityWriteDemandLocked(read, nil, nil, selected)
		}
	}
	for _, address := range addedIndexes {
		if descriptor, ok := read.Relation(address); ok {
			scope.NoteUtilityIndexDemandLocked(read, descriptor)
		}
	}
	// An index rebuild decision can depend on runtime DEFAULT/domain preflight.
	// The original actual selector stays late: no new eager catalog scan. Only
	// an expression entry that can precede that unresolved execution retains
	// possible roots; metadata/no-row paths do not enter merely for this flag.
	possibleRewrite := len(generations) != 0
	for _, transformation := range transformations {
		possibleRewrite = possibleRewrite || transformation.Rewrite
	}
	for _, added := range additions {
		if added.Expression != nil {
			p := added.Expression.Properties()
			possibleRewrite = possibleRewrite || p.Volatility > expr.Stable || p.RuntimeDomainConversion
		}
	}
	// A first index is already a typed candidate here, but its parent's
	// HasIndexes history is normalized only at metadata transfer. Classify
	// that known inventory before preflight fixes the active pair as well.
	if execute && (next.History.HasIndexes || len(addedIndexes) != 0) && (possibleRewrite || len(transformations) != 0) {
		scope.NoteOpaqueUtilityExportDemandLocked()
	}
	if state := scope.Inheritance.Alter; state != nil && !state.Descendant {
		for _, target := range state.Targets {
			if target.Address() == next.Address() {
				continue
			}
			prepared := state.Prepared[target.Address()]
			if prepared == nil || prepared.PhaseZeroOnly {
				for _, action := range state.Actions {
					switch action.Action {
					case parser.AddColumn, parser.AlterColumnType, parser.SetExpression, parser.AddCheck, parser.AddUnique, parser.AddExclusion:
						scope.NoteOpaqueUtilityExportDemandLocked()
					}
				}
				continue
			}
			childIndexExecution := false
			for _, step := range prepared.Steps {
				noteAddedSnapshotDemand(scope, read, step.Next, step.Added)
				if node := step.Added.Expression; node != nil {
					p := node.Properties()
					childIndexExecution = childIndexExecution || p.Volatility > expr.Stable || p.RuntimeDomainConversion
				}
				if step.Next != nil && step.Next.ColumnMeta.Generations[step.Added.Attribute].Kind == catalog.GenerationStored {
					childIndexExecution = true
				}
			}
			for _, step := range prepared.Inline {
				noteAddedSnapshotDemand(scope, read, step.Next, step.Added)
				if node := step.Added.Expression; node != nil {
					p := node.Properties()
					childIndexExecution = childIndexExecution || p.Volatility > expr.Stable || p.RuntimeDomainConversion
				}
			}
			if target.History.HasIndexes && childIndexExecution {
				scope.NoteOpaqueUtilityExportDemandLocked()
			}
			for _, action := range prepared.Actions {
				// These executable phases are intentionally not cooked into
				// InheritancePreparedNode; preserve later binding/error order.
				switch action.Action {
				case parser.AlterColumnType, parser.SetExpression, parser.AddUnique, parser.AddExclusion:
					scope.NoteOpaqueUtilityExportDemandLocked()
				}
			}
			// Validation recipes are cooked by the existing prewalk. Reading
			// their retained candidates neither rebinds nor surfaces errors early.
			if stored := e.PrivateStorageLocked(scope.PendingCatalog(), target.Address()); stored != nil && len(stored.Versions) != 0 {
				scope.NoteUtilityWriteDemandLocked(read, nil, nil, prepared.Checks.Scans)
				for _, action := range prepared.Actions {
					if action.Action == parser.ValidateConstraint {
						if selected, err := table.SelectAlterConstraintValidation(target, action.Constraint.Text); err == nil && selected.Check != nil {
							scope.NoteUtilityWriteDemandLocked(read, nil, nil, []catalog.CheckConstraint{*selected.Check})
						}
					}
					if action.Action == parser.AlterColumnType || action.Action == parser.SetExpression || action.Action == parser.AddColumn {
						scope.NoteUtilityWriteDemandLocked(read, nil, target.ColumnMeta.Generations, target.Constraints.Check)
					}
				}
			}
		}
	}
	if scope.EventCommand != nil && execute {
		scope.NoteUtilityEventDemandLocked(scope.EventCommand.Tag, false, true, possibleRewrite)
	}
	e.Mu.Unlock()
	if execute {
		return scope.AcquireUtilityExpressionSnapshot()
	}
	return nil
}

func noteAddedSnapshotDemand(scope *core.StatementScope, read catalog.Read, next *catalog.Relation, added table.AddedColumn) {
	scope.NoteUtilityExpressionDemandLocked(read, added.Expression)
	scope.NoteUtilityTypeDemandLocked(read, added.Value.Type())
	for _, dependency := range added.Dependencies {
		scope.NoteUtilityRoutineDemandLocked(read, dependency.Address)
	}
	if next != nil && added.Declaration.Generation != nil {
		definition := next.ColumnMeta.Generations[added.Attribute]
		scope.NoteUtilityExpressionDemandLocked(read, definition.Planned)
		for _, dependency := range definition.Dependencies {
			scope.NoteUtilityRoutineDemandLocked(read, dependency.Address)
		}
	}
}
