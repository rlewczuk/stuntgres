package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A caller-checked query consumer owns its selected equality identity beside
// the existing immutable provider, in the same key allocation. Internal hash
// consumers and index support keep ordinary sqltype.Grouping.
type queryGrouping struct {
	sqltype.Grouping
	routine     ids.ObjectRef
	privateTree bool
	conversion  sqltype.CoercionPlan
	converted   sqltype.Grouping
	convert     bool
}

func (g queryGrouping) Equal(ctx context.Context, a, b sqltype.Value) (bool, error) {
	if !g.convert {
		return g.Grouping.Equal(ctx, a, b)
	}
	left, err := g.conversion.Apply(ctx, a, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return false, err
	}
	right, err := g.conversion.Apply(ctx, b, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return false, err
	}
	return g.converted.Equal(ctx, left, right)
}

func (g queryGrouping) Hash(ctx context.Context, value sqltype.Value) (uint64, error) {
	if !g.convert {
		return g.Grouping.Hash(ctx, value)
	}
	converted, err := g.conversion.Apply(ctx, value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		return 0, err
	}
	return g.converted.Hash(ctx, converted)
}

func privateTreeGrouping(d *sqltype.Descriptor) bool {
	d = d.BaseType()
	for d.IsArray() {
		d = d.Element().BaseType()
	}
	return d.ID() == sqltype.PgNodeTree
}

func (b *expressionBinder) groupingRoutineReference(usage sqltype.Usage) (ids.ObjectRef, bool, error) {
	if privateTreeGrouping(usage.Type) {
		return ids.ObjectRef{}, true, nil
	}
	base := usage.Type.BaseType()
	var declaration *catalog.BuiltinRoutineDeclaration
	if base.IsArray() {
		declaration = builtinOperatorRoutineByDeclaration[builtinOperatorRoutineKey{"=", 2277, 2277}]
	} else {
		var err error
		declaration, _, err = b.comparisonRoutineDeclaration("=", base, base)
		if err != nil {
			return ids.ObjectRef{}, false, err
		}
	}
	if declaration == nil {
		return ids.ObjectRef{}, false, fmt.Errorf("stuntgres: missing query equality routine association")
	}
	_, ref, err := b.captureBuiltinRoutineDeclaration(declaration)
	return ref, false, err
}

func (b *expressionBinder) bindDistinctGrouping(expression expr.Expr) (queryGrouping, error) {
	if expression.CollationIndeterminate() {
		left, right, position := expression.ImplicitCollationConflict()
		d := expr.ErrorAt("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(left.ID), b.collationName(right.ID)), position).(*sqlstate.Error)
		d.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
		return queryGrouping{}, d
	}
	family, err := sqltype.BindGrouping(expression.ResultUsage())
	if diagnostic, ok := err.(*sqlstate.Error); ok {
		copy := *diagnostic
		if position := expression.SourcePosition().Character; position > 0 {
			copy.Position = strconv.Itoa(position)
		}
		return queryGrouping{}, &copy
	}
	if err != nil {
		return queryGrouping{}, err
	}
	group := queryGrouping{Grouping: family}
	usage := expression.ResultUsage()
	if !privateTreeGrouping(usage.Type) {
		signature, err := sqltype.ResolveEquality(usage.Type.BaseType(), usage.Type.BaseType())
		if err != nil {
			return queryGrouping{}, err
		}
		if signature.Left != usage.Type.BaseType().ID() {
			target := b.typ(signature.Left).Usage()
			target.Collation = usage.Collation
			group.conversion, err = sqltype.PlanCoercion(usage, target, sqltype.Implicit)
			if err != nil {
				return queryGrouping{}, err
			}
			group.converted, err = sqltype.BindGrouping(target)
			if err != nil {
				return queryGrouping{}, err
			}
			group.convert = true
			usage = target
		}
	}
	group.routine, group.privateTree, err = b.groupingRoutineReference(usage)
	return group, err
}
