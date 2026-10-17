package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindJSONBPathQuery(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
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
	if !visible || raw.ResolutionOnly || !jsonbPathQueryFunctionName(name) || len(operands) > count {
		return signature()
	}
	e := struct {
		Args         [4]BoundExpr
		WrittenCount int
		WrittenSlots [4]uint8
		Labels       [4]string
		Variadic     bool
	}{WrittenCount: len(operands), Variadic: raw.Variadic}
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
	if name == "jsonb_path_query" {
		if err := b.stringSetAllowed(raw.Pos); err != nil {
			return nil, err
		}
		switch b.aggregateClause {
		case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
			b.queryHasTargetSet = true
		}
		properties.StageDependent = true
	}
	resultID := sqltype.JSONB
	if name == "jsonb_path_exists" || name == "jsonb_path_match" {
		resultID = sqltype.Bool
	}
	info, err := b.builtinRoutineInfo(b.info(b.typ(resultID).Usage(), raw.Pos, properties), jsonRoutineProvider, name, ids[:]...)
	if err != nil {
		return nil, err
	}
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = e.Args[i].ResultUsage()
	}
	if name == "jsonb_path_exists" {
		plan, err := sqltype.BindJSONBPathExists(b.typ(resultID).Usage(), usages[:]...)
		if err != nil {
			return nil, err
		}
		return functions.JSONBPathExists[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(info), Plan: plan, Args: e.Args, WrittenCount: e.WrittenCount, WrittenSlots: e.WrittenSlots, Labels: e.Labels, Variadic: e.Variadic}, nil
	}
	if name == "jsonb_path_match" {
		plan, err := sqltype.BindJSONBPathMatch(b.typ(resultID).Usage(), usages[:]...)
		if err != nil {
			return nil, err
		}
		return functions.JSONBPathMatch[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(info), Plan: plan, Args: e.Args, WrittenCount: e.WrittenCount, WrittenSlots: e.WrittenSlots, Labels: e.Labels, Variadic: e.Variadic}, nil
	}
	if name == "jsonb_path_query_first" {
		plan, err := sqltype.BindJSONBPathQueryFirst(b.typ(resultID).Usage(), usages[:]...)
		if err != nil {
			return nil, err
		}
		return functions.JSONBPathQueryFirst[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(info), Plan: plan, Args: e.Args, WrittenCount: e.WrittenCount, WrittenSlots: e.WrittenSlots, Labels: e.Labels, Variadic: e.Variadic}, nil
	}
	if name == "jsonb_path_query" {
		plan, err := sqltype.BindJSONBPathQuery(b.typ(resultID).Usage(), usages[:]...)
		if err != nil {
			return nil, err
		}
		return functions.JSONBPathQuery[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(info), Plan: plan, Args: e.Args, WrittenCount: e.WrittenCount, WrittenSlots: e.WrittenSlots, Labels: e.Labels, Variadic: e.Variadic}, nil
	}
	return nil, fmt.Errorf("invalid JSONPath query provider")
}

func jsonbPathQueryFunctionName(name string) bool {
	switch name {
	case "jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_first", "jsonb_path_query":
		return true
	}
	return false
}

func jsonbPathExistsSQL(e functions.JSONBPathExists[BoundExpr, *EvalContext, ExprProperties], name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	return jsonbPathQueryCallSQL(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, name, render, identifier)
}

func jsonbPathMatchSQL(e functions.JSONBPathMatch[BoundExpr, *EvalContext, ExprProperties], name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	return jsonbPathQueryCallSQL(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, name, render, identifier)
}

func jsonbPathQueryFirstSQL(e functions.JSONBPathQueryFirst[BoundExpr, *EvalContext, ExprProperties], name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	return jsonbPathQueryCallSQL(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, name, render, identifier)
}

func jsonbPathQuerySQL(e functions.JSONBPathQuery[BoundExpr, *EvalContext, ExprProperties], name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	return jsonbPathQueryCallSQL(e.Args, e.WrittenCount, e.WrittenSlots, e.Labels, e.Variadic, name, render, identifier)
}

func jsonbPathQueryCallSQL(args [4]BoundExpr, count int, slots [4]uint8, labels [4]string, variadic bool, name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	if count < 2 || count > 4 {
		return "", fmt.Errorf("invalid stored JSONPath query")
	}
	parts := make([]string, count)
	var supplied [4]bool
	for i := range count {
		slot := int(slots[i])
		if slot >= 4 || supplied[slot] || args[slot] == nil {
			return "", fmt.Errorf("invalid stored JSONPath query argument")
		}
		supplied[slot] = true
		text, err := render(args[slot])
		if err != nil {
			return "", err
		}
		if labels[i] != "" {
			text = identifier(labels[i]) + " => " + text
		}
		if variadic && i == count-1 {
			text = "VARIADIC " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}
