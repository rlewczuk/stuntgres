package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

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
		declaration = routine.OperatorRoutine("=", 2277, 2277)
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
			group.Conversion, err = sqltype.PlanCoercion(usage, target, sqltype.Implicit)
			if err != nil {
				return queryGrouping{}, err
			}
			group.Converted, err = sqltype.BindGrouping(target)
			if err != nil {
				return queryGrouping{}, err
			}
			group.Convert = true
			usage = target
		}
	}
	group.Routine, group.PrivateTree, err = b.groupingRoutineReference(usage)
	return group, err
}
