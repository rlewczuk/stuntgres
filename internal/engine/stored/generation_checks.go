package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// CHECK recipes belong to the completed destination, not a copied source.
func RefreshGenerationChecks(d *catalog.Relation, catalog catalog.Read) error {
	for i := range d.Constraints.Check {
		next, changed, err := MapExpression(catalog, d.Constraints.Check[i].Expression, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil, func(node expr.Expr) (expr.Expr, bool, error) {
			column, ok := node.(plan.ColumnExpr)
			if !ok || column.Origin.Address.ClassID != d.Address().ClassID || column.Origin.Address.ObjectID != d.Address().ObjectID {
				return node, false, nil
			}
			slot := -1
			for i, attribute := range d.Attributes {
				if attribute == column.Origin.Address.SubID {
					slot = i
					break
				}
			}
			if slot < 0 {
				return nil, false, fmt.Errorf("stuntgres: missing destination CHECK attribute")
			}
			column.Slot = slot
			column.Virtual = nil
			column.VirtualOwner, column.VirtualAttribute = nil, 0
			column.RowStart, column.RowWidth, column.QueryOccurrence = 0, 0, 0
			column = column.WithGeneration(d, slot, 0)
			return column, true, nil
		})
		if err != nil {
			return err
		}
		if changed {
			d.Constraints.Check[i].Expression = next
		}
	}
	return nil
}
