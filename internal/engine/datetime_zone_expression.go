package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type datetimeZoneSignature struct {
	name   string
	args   [3]sqltype.ID
	count  int
	result sqltype.ID
}

var datetimeZoneSignatures = [...]datetimeZoneSignature{
	{"timezone", [3]sqltype.ID{sqltype.Text, sqltype.Timestamp}, 2, sqltype.Timestamptz},
	{"timezone", [3]sqltype.ID{sqltype.Interval, sqltype.Timestamp}, 2, sqltype.Timestamptz},
	{"timezone", [3]sqltype.ID{sqltype.Text, sqltype.Timestamptz}, 2, sqltype.Timestamp},
	{"timezone", [3]sqltype.ID{sqltype.Interval, sqltype.Timestamptz}, 2, sqltype.Timestamp},
	{"timezone", [3]sqltype.ID{sqltype.Text, sqltype.Timetz}, 2, sqltype.Timetz},
	{"timezone", [3]sqltype.ID{sqltype.Interval, sqltype.Timetz}, 2, sqltype.Timetz},
	{"timezone", [3]sqltype.ID{sqltype.Timestamp}, 1, sqltype.Timestamptz},
	{"timezone", [3]sqltype.ID{sqltype.Timestamptz}, 1, sqltype.Timestamp},
	{"timezone", [3]sqltype.ID{sqltype.Timetz}, 1, sqltype.Timetz},
	{"date_add", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval}, 2, sqltype.Timestamptz},
	{"date_add", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval, sqltype.Text}, 3, sqltype.Timestamptz},
	{"date_subtract", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval}, 2, sqltype.Timestamptz},
	{"date_subtract", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval, sqltype.Text}, 3, sqltype.Timestamptz},
}

func datetimeZoneCategory(id sqltype.ID) byte {
	if sqltype.IsCharacter(id) {
		return 'S'
	}
	if id == sqltype.Timetz {
		return 'D'
	}
	return ageFiniteCategory(id)
}
func datetimeZonePreferred(id sqltype.ID) bool {
	return id == sqltype.Text || id == sqltype.Timestamptz || id == sqltype.Interval
}

func (b *expressionBinder) bindDatetimeZone(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	previous := b.datetimeZoneOperands
	b.datetimeZoneOperands = true
	defer func() { b.datetimeZoneOperands = previous }()
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [3]comparisonOperand
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
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
	resolutionError := func(ambiguous bool) (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = datetimeFormattingTypeName(o)
		}
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, ambiguous)
	}
	if !visible || raw.Variadic {
		return resolutionError(false)
	}
	var viable [len(datetimeZoneSignatures)]bool
	bestExact, bestPreferred := -1, -1
	for j, signature := range datetimeZoneSignatures {
		if signature.name != name || signature.count != len(operands) {
			continue
		}
		ok, exact, preferred := true, 0, 0
		for i, o := range operands {
			if o.descriptor() == nil {
				continue
			}
			source, target := o.descriptor().BaseType().ID(), signature.args[i]
			if source == target {
				exact++
				continue
			}
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), b.typ(target).Usage(), sqltype.Implicit); err != nil {
				ok = false
				break
			}
			if datetimeZoneCategory(source) == datetimeZoneCategory(target) && datetimeZonePreferred(target) {
				preferred++
			}
		}
		if !ok {
			continue
		}
		if exact > bestExact || exact == bestExact && preferred > bestPreferred {
			viable = [len(datetimeZoneSignatures)]bool{}
			bestExact, bestPreferred = exact, preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			viable[j] = true
		}
	}
	for i, o := range operands {
		if o.descriptor() != nil {
			continue
		}
		category, conflict, preferred := byte(0), false, false
		for j, signature := range datetimeZoneSignatures {
			if !viable[j] {
				continue
			}
			cat := datetimeZoneCategory(signature.args[i])
			if cat == 'S' {
				category = 'S'
				break
			}
			if category != 0 && category != cat {
				conflict = true
			}
			category = cat
		}
		if category != 'S' && conflict {
			continue
		}
		for j, signature := range datetimeZoneSignatures {
			if viable[j] && datetimeZoneCategory(signature.args[i]) == category {
				preferred = preferred || datetimeZonePreferred(signature.args[i])
			}
		}
		for j, signature := range datetimeZoneSignatures {
			if viable[j] && (datetimeZoneCategory(signature.args[i]) != category || preferred && !datetimeZonePreferred(signature.args[i])) {
				viable[j] = false
			}
		}
	}
	selected, matches := 0, 0
	for j, ok := range viable {
		if ok {
			selected, matches = j, matches+1
		}
	}
	if matches != 1 {
		return resolutionError(matches > 1)
	}
	signature := datetimeZoneSignatures[selected]
	var args [3]BoundExpr
	properties := ExprProperties{Volatility: Immutable}
	if name != "timezone" && signature.count == 2 || name == "timezone" && (signature.count == 1 || signature.args[1] == sqltype.Timetz && signature.args[0] == sqltype.Text) {
		properties.Volatility = Stable
	}
	for i, o := range operands {
		target := b.typ(signature.args[i])
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
		args[i] = arg
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	info := b.info(b.typ(signature.result).Usage(), raw.Pos, properties)
	if name == "timezone" {
		e := timeZoneExpression{expressionInfo: info, function: true, local: signature.count == 1}
		if e.local {
			e.value = args[0]
		} else {
			e.zone, e.value = args[0], args[1]
		}
		return e, nil
	}
	op := sqltype.ArithmeticAdd
	if name == "date_subtract" {
		op = sqltype.ArithmeticSubtract
	}
	provider, err := sqltype.BindArithmetic(op, b.typ(sqltype.Timestamptz), b.typ(sqltype.Interval), b.typ(sqltype.Timestamptz))
	if err != nil {
		return nil, err
	}
	return datetimeZoneCalendarExpression{expressionInfo: info, name: name, args: args, count: signature.count, provider: provider}, nil
}

type datetimeZoneCalendarExpression struct {
	expressionInfo
	name     string
	args     [3]BoundExpr
	count    int
	provider sqltype.Arithmetic
}

func (e datetimeZoneCalendarExpression) children() []BoundExpr { return e.args[:e.count] }
func (e datetimeZoneCalendarExpression) intrinsicVolatility() Volatility {
	if e.count == 2 {
		return Stable
	}
	return Immutable
}
func (e datetimeZoneCalendarExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.children() {
		if randomLiteralNull(arg) {
			return sqltype.Null(e.usage.Type), nil
		}
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range e.children() {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i], null = v, null || v.IsNull()
	}
	if null {
		return sqltype.Null(e.usage.Type), nil
	}
	if e.count == 2 {
		return e.provider.Binary(c.Context, values[0], values[1], c.Input)
	}
	name, err := values[2].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.CalendarArithmetic(c.Context, e.provider, values[0], values[1], name, c.Input)
}
