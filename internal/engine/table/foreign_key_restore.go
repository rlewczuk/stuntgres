package table

import (
	"context"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type RestoredForeignKey struct {
	Raw        parser.ForeignKeyConstraint
	Path       []ids.ObjectAddress
	Target     ids.ObjectAddress
	Supporting ids.ObjectAddress // Exact captured primary for physical self-FK or omitted-reference inherited replay.
	Inherited  *RestoredInheritedForeignKey
	BoundOID   ids.OID
	BoundSlot  int // Actual append position in this sole stage, verified against boundOID.
}

type RestoredForeignKeys struct {
	Child       ids.ObjectRef
	Auth        ddl.Authority // The declaring principal; root staging recovers it.
	Definitions []RestoredForeignKey
}

func StripRestoreForeignKeys(raw *parser.CreateTable) {
	elements := raw.Elements[:0]
	for _, element := range raw.Elements {
		if element.ForeignKey != nil {
			continue
		}
		if element.Column != nil {
			constraints := element.Column.Constraints[:0]
			for _, constraint := range element.Column.Constraints {
				if constraint.ForeignKey == nil {
					constraints = append(constraints, constraint)
				}
			}
			element.Column.Constraints = constraints
		}
		elements = append(elements, element)
	}
	raw.Elements = elements
}

// ValidateForeignKeyData checks every row of children against f: each
// complete key must find a parent through f's index in parents, and a FULL
// key must be all or nothing NULL. The first violation is reported with the
// diagnostic view and env allow.
func ValidateForeignKeyData(ctx context.Context, view access.View, env access.Environment, child, parent *catalog.Relation, f catalog.ForeignKeyConstraint, comparisons []ForeignKeyComparison, children, parents ForeignKeyRows, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	index, ok := view.Catalog.Relation(f.Index)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	key := make([]sqltype.Value, len(comparisons))
	return children.Each(func(row storage.RowVersion) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		any, all := ForeignKeyNullState(comparisons, row.Values)
		if any {
			if f.Full && !all {
				return false, ForeignKeyDiagnostic(ctx, view, env, child, parent, f, row.Values, false, true, output)
			}
			return false, nil
		}
		if err := PrepareForeignKeyChild(ctx, comparisons, row.Values, key, input); err != nil {
			return false, err
		}
		matching, _, err := FindForeignKeyParent(ctx, index, comparisons, parents, key, input)
		if err != nil {
			return false, err
		}
		found := matching.Row != 0
		if !found {
			return false, ForeignKeyDiagnostic(ctx, view, env, child, parent, f, row.Values, false, false, output)
		}
		return false, nil
	})
}
