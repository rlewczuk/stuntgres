package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Parser spelling is not semantic routine kind. Resolve only names that have a
// real SQL declaration; all negative paths retain existing aggregate/cast owners.
// This runs before aggregate-only clause/recursive/partition gates, and never
// changes shared syntax or registers a scalar call in b.aggregates.
func (b *Binder) BindSQLRoutineSyntax(raw parser.Expr, policy ExpressionPolicy) (expr.Expr, bool, error) {
	if b.Clause.selectedCallReentry {
		b.Clause.selectedCallReentry = false
		return nil, false, nil
	}
	var call parser.FunctionExpr
	var cast *parser.CastExpr
	switch r := raw.(type) {
	case parser.AggregateExpr:
		if r.JSON != nil || r.Within || r.Variadic || !b.hasDynamicProcedureName(r.Name) {
			return nil, false, nil
		}
		parts := []parser.Identifier{{Text: r.Name, Pos: r.Pos}}
		if r.Schema != nil {
			parts = []parser.Identifier{*r.Schema, parts[0]}
		}
		call = parser.FunctionExpr{Name: parser.ObjectName{Parts: parts}, Args: r.Args, Pos: r.Pos, Star: r.Star, Distinct: r.Distinct, Variadic: r.Variadic, Order: r.Order, Filter: r.Filter, Window: r.Window, Within: r.Within, WithinPos: r.WithinPos}
	case parser.CastExpr:
		if r.Syntax != parser.FunctionCast || !b.hasDynamicProcedureName(r.Callee) {
			return nil, false, nil
		}
		parts := []parser.Identifier{{Text: r.Callee, Pos: r.Pos}}
		if r.TypeName.Schema != nil {
			parts = []parser.Identifier{*r.TypeName.Schema, parts[0]}
		}
		call = parser.FunctionExpr{Name: parser.ObjectName{Parts: parts}, Args: []parser.Expr{r.Child}, Pos: r.Pos}
		cast = &r
	default:
		return nil, false, nil
	}
	selected, handled, err := b.selectSQLCallable(call, cast)
	if err != nil || !handled {
		return nil, handled, err
	}
	node, err := b.finishSQLCallable(call, selected, raw, policy)
	return node, true, err
}

func (b *Binder) sqlFunctionCastTarget(raw parser.TypeName, operand ComparisonOperand) (sqltype.Usage, sqltype.CoercionPlan, bool) {
	// An unresolved external Param does not take the unknown-literal shortcut.
	// Exact typed functions won already; literal/IO/relabel casts precede the
	// ordinary implicit/unknown best-match phase (PostgreSQL10.3 step3).
	target, err := b.Host.ResolveAnnotationType(raw, false)
	if err != nil {
		return sqltype.Usage{}, sqltype.CoercionPlan{}, false
	}
	if literal, ok := operand.raw.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
		return target, sqltype.CoercionPlan{}, true
	}
	if operand.descriptor() == nil {
		return sqltype.Usage{}, sqltype.CoercionPlan{}, false
	}
	plan, err := sqltype.PlanCoercion(operand.Node.ResultUsage(), target, sqltype.Explicit)
	return target, plan, err == nil && plan.FunctionCastShortcut()
}

func legacyFunctionCastPredicate(raw parser.Expr) bool {
	switch child := raw.(type) {
	case parser.PredicateExpr, parser.LogicalExpr, parser.InListExpr, parser.ArrayComparisonExpr:
		return true
	case parser.OperatorExpr:
		if len(child.Name.Parts) == 1 {
			switch child.Name.Parts[0].Text {
			case "=", "<", ">", "<=", ">=", "<>":
				return true
			}
		}
	}
	return false
}
