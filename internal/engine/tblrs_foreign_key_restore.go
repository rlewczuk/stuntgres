package engine

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// ValidateForeignKeyData checks every row of children against f: each
// complete key must find a parent through f's index in parents, and a FULL
// key must be all or nothing NULL. The first violation is reported with the
// diagnostic view and env allow.
func ValidateForeignKeyData(ctx context.Context, view access.View, env access.Environment, child, parent *catalog.Relation, f catalog.ForeignKeyConstraint, comparisons []table.ForeignKeyComparison, children, parents table.ForeignKeyRows, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	index, ok := view.Catalog.Relation(f.Index)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	key := make([]sqltype.Value, len(comparisons))
	return children.Each(func(row storage.RowVersion) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		any, all := table.ForeignKeyNullState(comparisons, row.Values)
		if any {
			if f.Full && !all {
				return false, table.ForeignKeyDiagnostic(ctx, view, env, child, parent, f, row.Values, false, true, output)
			}
			return false, nil
		}
		if err := table.PrepareForeignKeyChild(ctx, comparisons, row.Values, key, input); err != nil {
			return false, err
		}
		matching, _, err := table.FindForeignKeyParent(ctx, index, comparisons, parents, key, input)
		if err != nil {
			return false, err
		}
		found := matching.Row != 0
		if !found {
			return false, table.ForeignKeyDiagnostic(ctx, view, env, child, parent, f, row.Values, false, false, output)
		}
		return false, nil
	})
}
