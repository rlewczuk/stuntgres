package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindJSONBSet(b functions.Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	count := 4
	if name == "jsonb_set_lax" {
		count = 5
	}
	labels := [5]string{"jsonb_in", "path", "replacement", "create_if_missing", "null_value_treatment"}
	if name == "jsonb_insert" {
		labels[3] = "insert_after"
	}
	ids := [5]sqltype.ID{sqltype.JSONB, 1009, sqltype.JSONB, sqltype.Bool, sqltype.Text}
	var small [5]functions.Argument
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
	if !visible || raw.ResolutionOnly || (name != "jsonb_set" && name != "jsonb_set_lax" && name != "jsonb_insert") || len(operands) > count {
		return signature()
	}
	e := functions.JSONBSet{Count: count, WrittenCount: len(operands), Variadic: raw.Variadic}
	var supplied [5]bool
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
	for slot := range 3 {
		if !supplied[slot] {
			return signature()
		}
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
	if !supplied[3] {
		typ := b.Type(sqltype.Bool)
		e.Args[3] = b.Literal(sqltype.Boolean(typ, name != "jsonb_insert"), raw.Pos)
	}
	if count == 5 && !supplied[4] {
		typ := b.Type(sqltype.Text)
		e.Args[4] = b.Literal(sqltype.String(typ, "use_json_null"), raw.Pos)
	}
	e.Info = b.Info(b.Type(sqltype.JSONB).Usage(), raw.Pos, properties)
	var usages [5]sqltype.Usage
	for i := range count {
		usages[i] = e.Args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBSet(name, e.Usage, usages[:count]...)
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

func jsonbSetSQL(e functions.JSONBSet, name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	if e.Count < 4 || e.Count > 5 || e.WrittenCount < 3 || e.WrittenCount > e.Count {
		return "", fmt.Errorf("invalid stored JSONB replacement")
	}
	parts := make([]string, e.WrittenCount)
	var supplied [5]bool
	for i := range e.WrittenCount {
		slot := int(e.WrittenSlots[i])
		if slot >= e.Count || supplied[slot] || e.Args[slot] == nil {
			return "", fmt.Errorf("invalid stored JSONB replacement argument")
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
