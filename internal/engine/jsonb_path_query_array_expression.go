package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindJSONBPathQueryArray(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	const count = 4
	labels := [4]string{"target", "path", "vars", "silent"}
	ids := [4]sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool}
	var small [4]functions.Argument[BoundExpr]
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
	if !visible || raw.ResolutionOnly || name != "jsonb_path_query_array" || len(operands) > count {
		return signature()
	}
	e := functions.JSONBPathQueryArray[BoundExpr, *EvalContext, ExprProperties]{WrittenCount: len(operands), Variadic: raw.Variadic}
	var supplied [4]bool
	for i, o := range operands {
		slot := i
		if len(raw.ArgNames) > 0 && raw.ArgNames[i].Text != "" {
			slot = -1
			for j, label := range labels[:count] {
				if label == raw.ArgNames[i].Text {
					slot = j
					break
				}
			}
			e.Labels[i] = raw.ArgNames[i].Text
		}
		if slot < 0 || slot >= count || supplied[slot] {
			return signature()
		}
		supplied[slot] = true
		e.WrittenSlots[i] = uint8(slot)
		if o.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(o.Usage, b.typ(ids[slot]).Usage(), sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	for slot := range 2 {
		if !supplied[slot] {
			return signature()
		}
	}
	// PostgreSQL accepts redundant VARIADIC on this nonvariadic routine, but
	// named notation must leave its final written argument at the same ordinal.
	if raw.Variadic && len(raw.ArgNames) != 0 && len(operands) != 0 &&
		int(e.WrittenSlots[len(operands)-1]) != len(operands)-1 {
		return signature()
	}
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		slot := e.WrittenSlots[i]
		arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: b.typ(ids[slot]).Usage()})
		if err != nil {
			return nil, err
		}
		e.Args[slot] = arg
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	// Resolve and coerce ordinary arguments before rejecting scalar decorations,
	// as the existing JSON processing provider does. Never silently discard AST
	// aggregate/window modifiers transported by the shared function grammar.
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
	if !supplied[2] {
		typ := b.typ(sqltype.JSONB)
		value, err := typ.Input(b.ctx, "{}", -1)
		if err != nil {
			return nil, err
		}
		e.Args[2] = literalExpression{b.info(typ.Usage(), raw.Pos, ExprProperties{Volatility: Immutable}), value}
	}
	if !supplied[3] {
		typ := b.typ(sqltype.Bool)
		e.Args[3] = literalExpression{b.info(typ.Usage(), raw.Pos, ExprProperties{Volatility: Immutable}), sqltype.Boolean(typ, false)}
	}
	e.Info = functionInfo(b.info(b.typ(sqltype.JSONB).Usage(), raw.Pos, properties))
	var usages [4]sqltype.Usage
	for i := range count {
		usages[i] = e.Args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathQueryArray(e.Usage, usages[:]...)
	if err != nil {
		return nil, err
	}
	info, err := b.builtinRoutineInfo(b.info(e.Usage, raw.Pos, properties), jsonRoutineProvider, name, ids[:count]...)
	if err != nil {
		return nil, err
	}
	e.Info = functionInfo(info)
	e.Plan = plan
	return e, nil
}

func jsonbPathQueryArraySQL(e functions.JSONBPathQueryArray[BoundExpr, *EvalContext, ExprProperties], name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	if e.WrittenCount < 2 || e.WrittenCount > 4 {
		return "", fmt.Errorf("invalid stored JSONPath array query")
	}
	parts := make([]string, e.WrittenCount)
	var supplied [4]bool
	for i := range e.WrittenCount {
		slot := int(e.WrittenSlots[i])
		if slot >= 4 || supplied[slot] || e.Args[slot] == nil {
			return "", fmt.Errorf("invalid stored JSONPath array query argument")
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
