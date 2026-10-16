package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A caller-checked query consumer owns its selected equality identity beside
// the existing immutable provider, in the same key allocation. Internal hash
// consumers and index support keep ordinary sqltype.Grouping.
type queryGrouping struct {
	sqltype.Grouping
	routine     ObjectRef
	privateTree bool
}

func privateTreeGrouping(d *sqltype.Descriptor) bool {
	d = d.BaseType()
	for d.IsArray() {
		d = d.Element().BaseType()
	}
	return d.ID() == sqltype.PgNodeTree
}

func (b *expressionBinder) groupingRoutineReference(usage sqltype.Usage) (ObjectRef, bool, error) {
	if privateTreeGrouping(usage.Type) {
		return ObjectRef{}, true, nil
	}
	base := usage.Type.BaseType()
	var declaration *builtinRoutineDeclaration
	if base.IsArray() {
		declaration = builtinOperatorRoutineByDeclaration[builtinOperatorRoutineKey{"=", 2277, 2277}]
	} else {
		var err error
		declaration, _, err = b.comparisonRoutineDeclaration("=", base, base)
		if err != nil {
			return ObjectRef{}, false, err
		}
	}
	if declaration == nil {
		return ObjectRef{}, false, fmt.Errorf("stuntgres: missing query equality routine association")
	}
	_, ref, err := b.captureBuiltinRoutineDeclaration(declaration)
	return ref, false, err
}

func (b *expressionBinder) bindDistinctGrouping(expression BoundExpr) (queryGrouping, error) {
	if expression.CollationIndeterminate() {
		left, right, position := expression.ImplicitCollationConflict()
		d := expressionError("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(left.ID), b.collationName(right.ID)), position).(*sqlstate.Error)
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
	ref, private, err := b.groupingRoutineReference(expression.ResultUsage())
	return queryGrouping{Grouping: family, routine: ref, privateTree: private}, err
}
