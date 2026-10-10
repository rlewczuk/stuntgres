package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindDistinctGrouping(expression BoundExpr) (sqltype.Grouping, error) {
	if expression.CollationIndeterminate() {
		left, right, position := expression.ImplicitCollationConflict()
		d := expressionError("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(left.ID), b.collationName(right.ID)), position).(*sqlstate.Error)
		d.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
		return sqltype.Grouping{}, d
	}
	family, err := sqltype.BindGrouping(expression.ResultUsage())
	if diagnostic, ok := err.(*sqlstate.Error); ok {
		copy := *diagnostic
		if position := expression.SourcePosition().Character; position > 0 {
			copy.Position = strconv.Itoa(position)
		}
		return sqltype.Grouping{}, &copy
	}
	return family, err
}
