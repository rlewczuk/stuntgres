package table

import (
	"context"
	"fmt"
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

// ResolveTarget is the parent the definition binds to in base: its captured
// target, else the first relation of its name along its captured path. No
// such relation is 42P01; a captured target base no longer holds is
// ErrCatalogReplaced.
func (d RestoredForeignKey) ResolveTarget(base catalog.Read) (ids.ObjectAddress, error) {
	target := d.Target
	if target == (ids.ObjectAddress{}) {
		for _, namespace := range d.Path {
			target = base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: d.Raw.Target.Parts[len(d.Raw.Target.Parts)-1].Text})
			if target != (ids.ObjectAddress{}) {
				break
			}
		}
	}
	if target == (ids.ObjectAddress{}) {
		return ids.ObjectAddress{}, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, catalog.ObjectNameText(d.Raw.Target)))
	}
	if _, ok := base.Find(target); !ok {
		return ids.ObjectAddress{}, ids.ErrCatalogReplaced
	}
	return target, nil
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
