package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// inquiryCallSyntax is the shared envelope of pg_get_acl and the access
// inquiries after argument and FILTER analysis: the argument limit, labels
// and DEFAULT admission.
func inquiryCallSyntax(b Binder, raw parser.FunctionExpr) error {
	if len(raw.Args) > 100 {
		return expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return fmt.Errorf("invalid function argument labels")
	case issue.Positional:
		return expr.ErrorAt("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	if b.DefaultConsumer() {
		return expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	return nil
}

// inquiryDecorationError rejects aggregate decorations of a resolved scalar.
func inquiryDecorationError(raw parser.FunctionExpr) error {
	switch {
	case raw.Distinct:
		return expr.ErrorAt("42809", "DISTINCT specified, but "+qualifiedName(raw.Name)+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return expr.ErrorAt("42809", "ORDER BY specified, but "+qualifiedName(raw.Name)+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return expr.ErrorAt("42809", "FILTER specified, but "+qualifiedName(raw.Name)+" is not an aggregate function", raw.Pos)
	}
	return nil
}

// coerceInquiryFormal finalizes an argument to its fixed formal and applies
// the implicit conversion at the raw argument; a failed plan is missing.
func coerceInquiryFormal(b Binder, argument Argument, raw parser.Expr, target sqltype.Usage, missing func() error) (expr.Expr, error) {
	node, err := b.CoerceArgument(argument, CoercionRequest{Intent: FinalizeArgument, Target: target})
	if err != nil || node.ResultUsage() == target {
		return node, err
	}
	plan, failure := sqltype.PlanCoercion(node.ResultUsage(), target, sqltype.Implicit)
	if failure != nil {
		return nil, missing()
	}
	return b.CoerceArgument(Argument{Expression: node, Raw: raw, Usage: node.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
}
