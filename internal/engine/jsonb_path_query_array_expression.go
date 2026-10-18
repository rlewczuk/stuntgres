package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindJSONBPathQueryArray(b functions.Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	const count = 4
	labels := [4]string{"target", "path", "vars", "silent"}
	ids := [4]sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool}
	var small [4]functions.Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
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
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (expr.Expr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = functions.NamedArgumentType(raw, i, datetimeFormattingTypeName(o.Usage.Type))
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	failure := functions.AnalyzeLabels(raw.ArgNames, raw.Args)
	switch failure.Kind {
	case functions.PositionalAfterNamed:
		return nil, expr.ErrorAt("42601", "positional argument cannot follow named argument", failure.Pos)
	case functions.DuplicateLabel:
		return nil, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	if !visible || raw.ResolutionOnly || name != "jsonb_path_query_array" || len(operands) > count {
		return signature()
	}
	e := functions.JSONBPathQueryArray{WrittenCount: len(operands), Variadic: raw.Variadic}
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
			if _, err := sqltype.PlanCoercion(o.Usage, b.Type(ids[slot]).Usage(), sqltype.Implicit); err != nil {
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
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, o := range operands {
		slot := e.WrittenSlots[i]
		arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: b.Type(ids[slot]).Usage()})
		if err != nil {
			return nil, err
		}
		e.Args[slot] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	// Resolve and coerce ordinary arguments before rejecting scalar decorations,
	// as the existing JSON processing provider does. Never silently discard AST
	// aggregate/window modifiers transported by the shared function grammar.
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	if !supplied[2] {
		typ := b.Type(sqltype.JSONB)
		value, err := typ.Input(b.Context(), "{}", -1)
		if err != nil {
			return nil, err
		}
		e.Args[2] = b.Literal(value, raw.Pos)
	}
	if !supplied[3] {
		typ := b.Type(sqltype.Bool)
		e.Args[3] = b.Literal(sqltype.Boolean(typ, false), raw.Pos)
	}
	e.Info = b.Info(b.Type(sqltype.JSONB).Usage(), raw.Pos, properties)
	var usages [4]sqltype.Usage
	for i := range count {
		usages[i] = e.Args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathQueryArray(e.Usage, usages[:]...)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(e.Usage, raw.Pos, properties), functions.JSONRoutineProvider, name, ids[:count]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	e.Plan = plan
	return e, nil
}

func jsonbPathQueryArraySQL(e functions.JSONBPathQueryArray, name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
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
