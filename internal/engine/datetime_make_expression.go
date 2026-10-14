package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type datetimeMakeExpression struct {
	expressionInfo
	plan         sqltype.DatetimeMakePlan
	args         [7]BoundExpr // Declared parameter order, including interval defaults.
	count        int
	writtenCount int
	writtenSlots [7]uint8
	labels       [7]string // Written order; empty denotes positional syntax.
}

func (e *datetimeMakeExpression) children() []BoundExpr { return e.args[:e.count] }
func (e datetimeMakeExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	// Preflight already visited/folded constant children. Runtime strictness can
	// now short circuit a constant NULL without invoking the constructor/zone.
	for _, arg := range e.children() {
		if randomLiteralNull(arg) {
			return sqltype.Null(e.usage.Type), nil
		}
	}
	var values [7]sqltype.Value
	for i, arg := range e.children() {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.plan.Evaluate(c.Context, values[:e.count], c.Input)
}

func datetimeMakeSignature(name string) (labels [7]string, types [7]sqltype.ID, count int, result sqltype.ID) {
	switch name {
	case "make_date":
		labels = [7]string{"year", "month", "day"}
		count, result = 3, sqltype.Date
	case "make_time":
		labels = [7]string{"hour", "min", "sec"}
		count, result = 3, sqltype.Time
	case "make_timestamp":
		labels = [7]string{"year", "month", "mday", "hour", "min", "sec"}
		count, result = 6, sqltype.Timestamp
	case "make_timestamptz":
		labels = [7]string{"year", "month", "mday", "hour", "min", "sec", "timezone"}
		count, result = 7, sqltype.Timestamptz
	case "make_interval":
		labels = [7]string{"years", "months", "weeks", "days", "hours", "mins", "secs"}
		count, result = 7, sqltype.Interval
	}
	for i := range count {
		types[i] = sqltype.Int4
	}
	if name == "make_time" {
		types[2] = sqltype.Float8
	}
	if name == "make_timestamp" || name == "make_timestamptz" {
		types[5] = sqltype.Float8
	}
	if name == "make_timestamptz" {
		types[6] = sqltype.Text
	}
	if name == "make_interval" {
		types[6] = sqltype.Float8
	}
	return
}

func functionNamedType(raw parser.FunctionExpr, index int, typ string) string {
	if len(raw.ArgNames) > index && raw.ArgNames[index].Text != "" {
		return raw.ArgNames[index].Text + " => " + typ
	}
	return typ
}

func (b *expressionBinder) bindDatetimeMake(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	labels, types, count, result := datetimeMakeSignature(name)
	var small [7]comparisonOperand
	operands := small[:min(len(raw.Args), len(small))]
	// Bad signatures require every analyzed source type, but large calls never
	// retain executable storage. The child-analysis-before-100 rule is PG-owned.
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, arg := range raw.Args {
		operand, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = operand
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = functionNamedType(raw, i, datetimeFormattingTypeName(o))
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	named := false
	for i, label := range raw.ArgNames {
		if label.Text == "" {
			if named {
				return nil, expressionError("42601", "positional argument cannot follow named argument", raw.Args[i].SourcePosition())
			}
			continue
		}
		named = true
		for _, previous := range raw.ArgNames[:i] {
			if previous.Text == label.Text {
				return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", label.Text), label.Pos)
			}
		}
	}
	if !visible || raw.Variadic || result == 0 || len(operands) > count {
		return signature()
	}
	e := datetimeMakeExpression{count: count, writtenCount: len(raw.Args)}
	var supplied [7]bool
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
		}
		if slot < 0 || slot >= count || supplied[slot] {
			return signature()
		}
		supplied[slot] = true
		e.writtenSlots[i] = uint8(slot)
		if len(raw.ArgNames) > 0 {
			e.labels[i] = raw.ArgNames[i].Text
		}
		if d := o.descriptor(); d != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(types[slot]).Usage(), sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	if name == "make_timestamptz" && !supplied[6] {
		e.count = 6
	}
	for i := range e.count {
		if !supplied[i] && name != "make_interval" {
			return signature()
		}
	}
	properties := ExprProperties{Volatility: Immutable}
	if name == "make_timestamptz" {
		properties.Volatility = Stable
	}
	// Finalize in written order to retain unknown/parameter/coercion locations.
	for i, o := range operands {
		slot := int(e.writtenSlots[i])
		target := b.typ(types[slot])
		arg, err := b.finalizeOperand(o, target)
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target.Usage() {
			plan, err := sqltype.PlanCoercion(arg.ResultUsage(), target.Usage(), sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			arg = coercionExpression{b.info(target.Usage(), arg.SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit}
		}
		e.args[slot] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	for i := range e.count {
		if e.args[i] == nil {
			typ := b.typ(types[i])
			var value sqltype.Value
			var err error
			if types[i] == sqltype.Float8 {
				value, err = sqltype.Float(typ, 0)
			} else {
				value, err = sqltype.Integer(typ, 0)
			}
			if err != nil {
				return nil, err
			}
			e.args[i] = literalExpression{b.info(typ.Usage(), raw.Pos, ExprProperties{Volatility: Immutable}), value}
		}
	}
	e.expressionInfo = b.info(b.typ(result).Usage(), raw.Pos, properties)
	plan, err := sqltype.BindDatetimeMake(name, e.usage.Type, e.count)
	if err != nil {
		return nil, err
	}
	e.plan = plan
	return e, nil
}

// Definition/presentation paths retain written labels/order and omitted defaults;
// execution and dependency walkers use declared slots, never this display order.
func (e datetimeMakeExpression) sql(name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	if e.count < 1 || e.count > 7 || e.writtenCount < 0 || e.writtenCount > e.count {
		return "", fmt.Errorf("invalid stored datetime constructor")
	}
	parts := make([]string, e.writtenCount)
	for i := range e.writtenCount {
		slot := int(e.writtenSlots[i])
		if slot >= e.count || e.args[slot] == nil {
			return "", fmt.Errorf("invalid stored constructor argument")
		}
		text, err := render(e.args[slot])
		if err != nil {
			return "", err
		}
		if e.labels[i] != "" {
			text = identifier(e.labels[i]) + " => " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}
