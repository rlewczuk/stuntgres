package analyze

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *Binder) groupingRoutineReference(usage sqltype.Usage) (ids.ObjectRef, bool, error) {
	if PrivateTreeGrouping(usage.Type) {
		return ids.ObjectRef{}, true, nil
	}
	base := usage.Type.BaseType()
	var declaration *catalog.Procedure
	if base.IsArray() {
		builtin := routine.OperatorRoutine("=", 2277, 2277)
		var err error
		declaration, _, err = b.captureBuiltinRoutineDeclaration(builtin)
		if err != nil {
			return ids.ObjectRef{}, false, err
		}
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
	ref := b.BindingCatalog().Owner().Domain.Ref(b.BindingCatalog().Epoch(), declaration.Address())
	return ref, false, nil
}

func (b *Binder) bindDistinctGrouping(expression expr.Expr) (plan.Grouping, error) {
	if expression.CollationIndeterminate() {
		left, right, position := expression.ImplicitCollationConflict()
		d := expr.ErrorAt("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(left.ID), b.collationName(right.ID)), position).(*sqlstate.Error)
		d.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
		return plan.Grouping{}, d
	}
	family, err := sqltype.BindGrouping(expression.ResultUsage())
	if diagnostic, ok := err.(*sqlstate.Error); ok {
		copy := *diagnostic
		if position := expression.SourcePosition().Character; position > 0 {
			copy.Position = strconv.Itoa(position)
		}
		return plan.Grouping{}, &copy
	}
	if err != nil {
		return plan.Grouping{}, err
	}
	group := plan.Grouping{Grouping: family}
	usage := expression.ResultUsage()
	if !PrivateTreeGrouping(usage.Type) {
		signature, err := sqltype.ResolveEquality(usage.Type.BaseType(), usage.Type.BaseType())
		if err != nil {
			return plan.Grouping{}, err
		}
		if signature.Left != usage.Type.BaseType().ID() {
			target := b.Type(signature.Left).Usage()
			target.Collation = usage.Collation
			group.Conversion, err = sqltype.PlanCoercion(usage, target, sqltype.Implicit)
			if err != nil {
				return plan.Grouping{}, err
			}
			group.Converted, err = sqltype.BindGrouping(target)
			if err != nil {
				return plan.Grouping{}, err
			}
			group.Convert = true
			usage = target
		}
	}
	group.Routine, group.PrivateTree, err = b.groupingRoutineReference(usage)
	return group, err
}
