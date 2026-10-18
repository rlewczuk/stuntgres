package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// ValidateCallSyntax is the scalar call envelope shared by several families:
// FILTER analysis, the 100-argument limit, then argument-label validation.
// Callers reach it only after their ordinary children are analyzed.
func ValidateCallSyntax(b Binder, raw parser.FunctionExpr) error {
	if err := b.AnalyzeFilter(raw.Filter); err != nil {
		return err
	}
	if len(raw.Args) > 100 {
		return expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	return CallLabelError(raw)
}

// CallLabelError reports malformed, positional-after-named and duplicate
// argument labels, in that order.
func CallLabelError(raw parser.FunctionExpr) error {
	issue := FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return fmt.Errorf("invalid function argument labels")
	case issue.Positional:
		return expr.ErrorAt("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	return nil
}

// CallDecorationError rejects aggregate or window decorations on a resolved
// scalar call, naming the first decoration PostgreSQL reports.
func CallDecorationError(raw parser.FunctionExpr) error {
	decoration, kind := "", "not an aggregate function"
	switch {
	case raw.Distinct:
		decoration = "DISTINCT"
	case len(raw.Order) != 0:
		decoration = "ORDER BY"
	case raw.Filter != nil:
		decoration = "FILTER"
	case raw.Window != nil:
		decoration, kind = "OVER", "not a window function nor an aggregate function"
	}
	if decoration != "" {
		return expr.ErrorAt("42809", decoration+" specified, but "+qualifiedName(raw.Name)+" is "+kind, raw.Pos)
	}
	return nil
}

// NamedArgumentType prefixes a diagnostic argument type with its label.
func NamedArgumentType(raw parser.FunctionExpr, index int, typ string) string {
	if len(raw.ArgNames) > index && raw.ArgNames[index].Text != "" {
		return raw.ArgNames[index].Text + " => " + typ
	}
	return typ
}

func qualifiedName(n parser.ObjectName) string {
	parts := make([]string, len(n.Parts))
	for i, p := range n.Parts {
		parts[i] = p.Text
	}
	return strings.Join(parts, ".")
}
