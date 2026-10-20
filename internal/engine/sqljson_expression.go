package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindSQLJSON binds a SQL/JSON constructor. The query form of JSON_ARRAY is a
// subquery; the other forms bind as a constructor whose RETURNING clause the
// engine applies as an assignment coercion.
func (b *expressionBinder) bindSQLJSON(raw parser.SQLJSONExpr) (expr.Expr, error) {
	if functions.SQLJSONKind(raw.Kind) == 0 {
		return nil, fmt.Errorf("invalid SQL/JSON constructor")
	}
	if raw.Query != nil {
		return b.bindSQLJSONArrayQuery(raw)
	}
	node, returning, err := functions.BindSQLJSON(b, raw)
	if err != nil {
		return nil, err
	}
	if returning.Type == nil {
		return node, nil
	}
	if err = b.captureTypeDiagnosticInput(returning.Type); err != nil {
		return nil, err
	}
	coercionPlan, err := functions.JSONReturningCoercion(node.ResultUsage(), returning, raw.Output)
	if err != nil {
		return nil, err
	}
	info, err := b.coercionInfo(b.info(returning, raw.Pos, coercionProperties(node, coercionPlan)), node)
	if err != nil {
		return nil, err
	}
	return plan.CoercionExpr{info, node, coercionPlan, sqltype.Assignment, false}, nil
}
