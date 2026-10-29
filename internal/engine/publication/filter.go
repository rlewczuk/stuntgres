package publication

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// ValidateFilter inspects the normal bound tree, including expanded virtual
// columns. Semantic edges are distinct from the payload's full lifetime refs.
func ValidateFilter(ctx context.Context, v catalog.Read, p *stored.Policy, position parser.Position) ([]ids.ObjectAddress, error) {
	if p == nil {
		return nil, nil
	}
	invalid := func(detail string) error {
		e := failure("0A000", "invalid publication WHERE expression")
		e.Detail = detail
		if position.Character > 0 {
			e.Position = fmt.Sprint(position.Character)
		}
		return e
	}
	plan.WalkExpression(p.Expression(), func(node expr.Expr) {
		pos := node.SourcePosition()
		if pos.Character > 0 && (position.Character == 0 || pos.Character < position.Character) {
			position = pos
		}
	})
	var failureDetail string
	var inspect func(expr.Expr)
	inspect = func(root expr.Expr) {
		plan.WalkExpression(root, func(node expr.Expr) {
			if failureDetail != "" {
				return
			}
			switch n := node.(type) {
			case plan.SubqueryExpr:
				failureDetail = "Only columns, constants, built-in operators, built-in data types, built-in collations, and immutable built-in functions are allowed."
			case plan.ColumnExpr:
				if n.Origin.Address.SubID < 0 {
					failureDetail = "System columns are not allowed."
					return
				}
				if n.Virtual != nil {
					inspect(n.Virtual)
				}
			case plan.SelectedOperatorExpr:
				if n.Operator != nil && !routine.OperatorSeeded(n.Operator) {
					failureDetail = "User-defined operators are not allowed."
					return
				}
			}
			if usage := node.ResultUsage(); usage.Type != nil {
				// A normal ROW constructor has an occurrence-owned RECORD shape,
				// not builtin inventory provenance or a user-defined catalog type.
				// Its executable members are inspected by the same tree walk.
				if usage.Type.IsUserDefined() && !(usage.Type.ID() == 2249 && usage.Type.IsComposite()) {
					failureDetail = "User-defined types are not allowed."
					return
				}
				if usage.Collation.ID != 0 {
					if c, ok := v.Get(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(usage.Collation.ID)}).(*catalog.Collation); ok && !c.Seeded() {
						failureDetail = "User-defined collations are not allowed."
						return
					}
				}
			}
			info := expr.InfoOf(node)
			if info.RoutineReference != nil {
				d, _ := v.Get(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(info.RoutineReference.RoutineOID())}).(*catalog.Procedure)
				if d == nil || !d.Seeded() {
					failureDetail = "User-defined or built-in mutable functions are not allowed."
					return
				}
			}
			if node.Properties().Volatility != expr.Immutable {
				failureDetail = "User-defined or built-in mutable functions are not allowed."
			}
		})
	}
	inspect(p.Expression())
	if failureDetail != "" {
		return nil, invalid(failureDetail)
	}
	refs, err := stored.ExpressionDependRefs(ctx, v, p.Expression())
	if err != nil {
		return nil, err
	}
	return refs, nil
}
