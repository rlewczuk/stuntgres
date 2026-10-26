package stored

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// IN batching stores the operator's array-element conversion on each item.
// PostgreSQL stores that conversion around the common-type array instead.
// Reconstruct that presentation without changing the executable operands.
func (r Deparser) scalarArrayItems(ctx context.Context, items []expr.Expr) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("stuntgres: missing scalar-array items")
	}
	cast, converted := items[0].(plan.CoercionExpr)
	converted = converted && cast.Mode == sqltype.Implicit && cast.Child != nil &&
		cast.Child.ResultUsage().Type != cast.Usage.Type
	if converted {
		for _, item := range items[1:] {
			next, ok := item.(plan.CoercionExpr)
			if !ok || next.Mode != sqltype.Implicit || next.Child == nil ||
				next.Child.ResultUsage() != cast.Child.ResultUsage() ||
				next.Usage != cast.Usage || !next.Plan.Equal(cast.Plan) {
				converted = false
				break
			}
		}
	}
	var text strings.Builder
	text.WriteString("ARRAY[")
	for i, item := range items {
		if converted {
			item = item.(plan.CoercionExpr).Child
		}
		if i > 0 {
			text.WriteString(", ")
		}
		operand := r
		operand.ShowImplicit = true
		part, err := operand.SQL(ctx, item, DeparseParent{})
		if err != nil {
			return "", err
		}
		text.WriteString(part)
	}
	text.WriteByte(']')
	if !converted {
		return text.String(), nil
	}
	array, ok := r.View.LookupArray(cast.Usage.Type)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing scalar-array coercion type")
	}
	usage := cast.Usage
	usage.Type = array
	return r.cast(text.String(), usage, false)
}
