package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindJSONStripNulls(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	labels := [2]string{"target", "strip_in_arrays"}
	ids := [2]sqltype.ID{sqltype.JSON, sqltype.Bool}
	var small [2]functions.Argument[BoundExpr]
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = functionNamedType(raw, i, datetimeFormattingTypeName(o.Usage.Type))
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	failure := functions.AnalyzeLabels(raw.ArgNames, raw.Args)
	switch failure.Kind {
	case functions.PositionalAfterNamed:
		return nil, expressionError("42601", "positional argument cannot follow named argument", failure.Pos)
	case functions.DuplicateLabel:
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	if !visible || raw.ResolutionOnly || name != "json_strip_nulls" || len(operands) > 2 {
		return signature()
	}
	e := functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]{WrittenCount: len(operands), Variadic: raw.Variadic}
	var supplied [2]bool
	for i, o := range operands {
		slot := i
		if len(raw.ArgNames) > 0 && raw.ArgNames[i].Text != "" {
			slot = -1
			for j, label := range labels {
				if label == raw.ArgNames[i].Text {
					slot = j
					break
				}
			}
			e.Labels[i] = raw.ArgNames[i].Text
		}
		if slot < 0 || slot >= 2 || supplied[slot] {
			return signature()
		}
		supplied[slot], e.WrittenSlots[i] = true, uint8(slot)
		if o.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(o.Usage, b.typ(ids[slot]).Usage(), sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	if !supplied[0] {
		return signature()
	}
	if raw.Variadic && len(raw.ArgNames) > 0 && int(e.WrittenSlots[e.WrittenCount-1]) != e.WrittenCount-1 {
		return signature()
	}
	properties := ExprProperties{Volatility: Stable}
	for i, o := range operands {
		slot := e.WrittenSlots[i]
		arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: b.typ(ids[slot]).Usage()})
		if err != nil {
			return nil, err
		}
		e.Args[slot] = arg
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	decoration, message := "", "not an aggregate function"
	switch {
	case raw.Distinct:
		decoration = "DISTINCT"
	case len(raw.Order) != 0:
		decoration = "ORDER BY"
	case raw.Filter != nil:
		decoration = "FILTER"
	case raw.Window != nil:
		decoration, message = "OVER", "not a window function nor an aggregate function"
	}
	if decoration != "" {
		return nil, expressionError("42809", decoration+" specified, but "+objectNameText(raw.Name)+" is "+message, raw.Pos)
	}
	if !supplied[1] {
		typ := b.typ(sqltype.Bool)
		e.Args[1] = literalExpression{b.info(typ.Usage(), raw.Pos, ExprProperties{Volatility: Immutable}), sqltype.Boolean(typ, false)}
	}
	e.Info = functionInfo(b.info(b.typ(sqltype.JSON).Usage(), raw.Pos, properties))
	plan, err := sqltype.BindJSONStripNulls(e.Usage, e.Args[0].ResultUsage(), e.Args[1].ResultUsage())
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	return e, nil
}

func jsonStripNullsSQL(e functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties], name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	if e.WrittenCount < 1 || e.WrittenCount > 2 {
		return "", fmt.Errorf("invalid stored JSON stripping")
	}
	var local [2]string
	parts := local[:e.WrittenCount]
	var supplied [2]bool
	for i := range e.WrittenCount {
		slot := int(e.WrittenSlots[i])
		if slot >= 2 || supplied[slot] || e.Args[slot] == nil {
			return "", fmt.Errorf("invalid stored JSON stripping argument")
		}
		supplied[slot] = true
		text, err := render(e.Args[slot])
		if err != nil {
			return "", err
		}
		if e.Labels[i] != "" {
			text = identifier(e.Labels[i]) + " => " + text
		}
		if e.Variadic && i == e.WrittenCount-1 {
			text = "VARIADIC " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}
