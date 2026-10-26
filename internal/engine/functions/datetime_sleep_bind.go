package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindDatetimeSleep binds pg_sleep, pg_sleep_for and pg_sleep_until. They sat
// in the final name switch, so the engine applies the shared gates first.
func bindDatetimeSleep(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	var small [1]Argument
	operands := small[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := AnalyzeDatetimeSleepArgument(b, child)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = operand
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	// The sleep family's 42883 never notes the missing signature.
	signature := func() error {
		names := make([]string, len(operands))
		for i, operand := range operands {
			names[i] = NamedArgumentType(raw, i, DiagnosticNameOrUnknown(operand.Usage.Type))
		}
		return ResolutionError(raw.Name, raw.Pos, names, false)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind, id := DatetimeSleepDefinition(name)
	if !DatetimeSleepShape(raw, visible, kind) {
		return nil, signature()
	}
	usage := b.Type(id).Usage()
	if !DatetimeSleepCompatible(operands[0].Usage, usage) {
		return nil, signature()
	}
	if decoration := DatetimeSleepDecoration(raw); decoration != "" {
		if decoration == "(*)" {
			return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
		}
		return nil, expr.ErrorAt("42809", decoration+" specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	arg, err := CoerceDatetimeSleepArgument(b, operands[0], usage)
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.IntrinsicProperties(DatetimeSleepIntrinsicRule()), arg.Properties())
	clockUsage := b.Type(sqltype.Timestamptz).Usage()
	info := b.Info(b.Type(sqltype.Void).Usage(), raw.Pos, properties)
	info, err = b.BuiltinRoutineInfo(info, DatetimeRoutineProvider, name, id)
	if err != nil {
		return nil, err
	}
	return NewDatetimeSleep(info, kind, arg, clockUsage)
}
