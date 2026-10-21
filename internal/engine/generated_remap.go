package engine

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// Replacement candidates own detached vectors; the source and retained TEMP
// roots are never modified. Only owner-dependent stored cells are recomputed.
func recomputeRemappedGenerations(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress, eval exec.Context, changed map[ids.ObjectAddress]map[int32]bool) error {
	for source, target := range mapping {
		if source == target {
			continue
		}
		descriptor, ok := next.Relation(target)
		if !ok || len(descriptor.ColumnMeta.Generations) == 0 {
			continue
		}
		filtered := *descriptor
		filtered.ColumnMeta.Generations = nil
		for attribute, definition := range descriptor.ColumnMeta.Generations {
			if definition.Kind != catalog.GenerationStored || !plan.GenerationHasTableOID(definition.Planned) {
				continue
			}
			if filtered.ColumnMeta.Generations == nil {
				filtered.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration)
			}
			filtered.ColumnMeta.Generations[attribute] = definition
		}
		if len(filtered.ColumnMeta.Generations) == 0 {
			continue
		}
		rows := relations[target]
		if rows == nil {
			return fmt.Errorf("stuntgres: missing generated replacement storage")
		}
		schema := next.Get(descriptor.Namespace()).Name()
		historical := table.ValidCheckTraversal(descriptor)
		requiredNotNull := table.NewAlterRowValidator(descriptor, schema).NotNull
		if scope := scopeOf(eval.Host); scope != nil {
			stage := scope.engine()
			stage.mu.Lock()
			err := stage.captureCheckCommentSnapshotLocked(scope, historical)
			if err == nil {
				err = stage.captureCheckVisibilitySnapshotLocked(scope, historical)
			}
			stage.mu.Unlock()
			if err != nil {
				return err
			}
		}
		var comparisons map[int]sqltype.Grouping
		if len(changed[target]) != 0 {
			comparisons = make(map[int]sqltype.Grouping, len(filtered.ColumnMeta.Generations))
			for attribute := range filtered.ColumnMeta.Generations {
				if _, tracked := changed[target][attribute]; !tracked {
					continue
				}
				slot := slices.Index(descriptor.Attributes, attribute)
				if slot < 0 {
					return ids.ErrCatalogReplaced
				}
				comparison, err := sqltype.BindGrouping(descriptor.Columns[slot].Usage)
				if err != nil {
					return err
				}
				comparisons[slot] = comparison
			}
		}
		for i := range rows.Versions {
			if err := ctx.Err(); err != nil {
				return err
			}
			row := append([]sqltype.Value(nil), rows.Versions[i].Values...)
			if err := table.EvaluateGenerations(ctx, &filtered, row, &eval); err != nil {
				return err
			}
			for _, slot := range requiredNotNull {
				column := descriptor.Columns[slot]
				value, err := table.LogicalColumnValue(descriptor, slot, row, &eval)
				if err != nil {
					return err
				}
				if value.IsNull() {
					detail, err := exec.FailingRowDetail(row, eval.Output, nil, nil, descriptor)
					if err != nil {
						return err
					}
					return notNullDetailError(schema, descriptor.Name(), column.Name, detail)
				}
			}
			constraint, err := table.ValidateCheckRow(historical, row, &eval)
			if err != nil {
				return err
			}
			if constraint != nil {
				return table.CheckDetailError(schema, descriptor.Name(), constraint.Name, "")
			}
			for slot, comparison := range comparisons {
				before := rows.Versions[i].Values[slot]
				if before == row[slot] {
					continue
				}
				equal, err := comparison.Equal(ctx, before, row[slot])
				if err != nil {
					return err
				}
				if !equal {
					if changed[target] == nil {
						changed[target] = make(map[int32]bool)
					}
					changed[target][descriptor.Attributes[slot]] = true
				}
			}
			rows.Versions[i].Values = row
			rows.Versions[i].IndexOutputs = nil
		}
	}
	return ctx.Err()
}
