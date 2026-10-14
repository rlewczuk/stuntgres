package functions

import (
	"stuntgres/internal/sqltype"
)

type DatetimeZoneCalendarContext[E any] interface {
	LiteralContext[E]
	FunctionInput() sqltype.InputEnvironment
}

type DatetimeZoneCalendar[E Expression[C], C DatetimeZoneCalendarContext[E], P any] struct {
	Info[P]
	Name     string
	Args     [3]E
	Count    int
	Provider sqltype.Arithmetic
}

func (e DatetimeZoneCalendar[E, C, P]) Arguments() []E { return e.Args[:e.Count] }

func (e DatetimeZoneCalendar[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Arguments() {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range e.Arguments() {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i], null = v, null || v.IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	if e.Count == 2 {
		return e.Provider.Binary(ctx, values[0], values[1], c.FunctionInput())
	}
	name, err := values[2].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.CalendarArithmetic(ctx, e.Provider, values[0], values[1], name, c.FunctionInput())
}

func (e DatetimeZoneCalendar[E, C, P]) IntrinsicStable() bool { return e.Count == 2 }

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

type DatetimeZoneSelection struct {
	Name       string
	Args       [3]sqltype.ID
	Count      int
	Result     sqltype.ID
	Resolution Resolution
}
type DatetimeZonePreparation struct {
	candidates []datetimeZoneSignature
	Demand     TemporalDemand
}

func PrepareDatetimeZone[E any](name string, visible, variadic bool, args []Argument[E]) DatetimeZonePreparation {
	if !visible || variadic {
		return DatetimeZonePreparation{}
	}
	p := DatetimeZonePreparation{}
	for _, signature := range datetimeZoneSignatures {
		if signature.name != name || signature.count != len(args) {
			continue
		}
		p.candidates = datetimeZoneSignatures[:]
		for i, arg := range args {
			if d := arg.Usage.Type; d != nil && d.BaseType().ID() != signature.args[i] {
				p.Demand |= temporalDemand(signature.args[i])
			}
		}
	}
	return p
}

func ResolveDatetimeZone[E any](prepared DatetimeZonePreparation, name string, operands []Argument[E], types TemporalTypes) DatetimeZoneSelection {
	if len(prepared.candidates) == 0 {
		return DatetimeZoneSelection{Resolution: Missing}
	}

	var viable [13]bool
	bestExact, bestPreferred := -1, -1
	for j, signature := range prepared.candidates {
		if signature.name != name || signature.count != len(operands) {
			continue
		}
		ok, exact, preferred := true, 0, 0
		for i, o := range operands {
			if o.Usage.Type == nil {
				continue
			}
			source, target := o.Usage.Type.BaseType().ID(), signature.args[i]
			if source == target {
				exact++
				continue
			}
			if _, err := sqltype.PlanCoercion(o.Usage, types.Usage(target), sqltype.Implicit); err != nil {
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
			viable = [13]bool{}
			bestExact, bestPreferred = exact, preferred
		}
		if exact == bestExact && preferred == bestPreferred {
			viable[j] = true
		}
	}
	for i, o := range operands {
		if o.Usage.Type != nil {
			continue
		}
		category, conflict, preferred := byte(0), false, false
		for j, signature := range prepared.candidates {
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
		for j, signature := range prepared.candidates {
			if viable[j] && datetimeZoneCategory(signature.args[i]) == category {
				preferred = preferred || datetimeZonePreferred(signature.args[i])
			}
		}
		for j, signature := range prepared.candidates {
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
		if matches > 1 {
			return DatetimeZoneSelection{Resolution: Ambiguous}
		}
		return DatetimeZoneSelection{Resolution: Missing}
	}
	signature := prepared.candidates[selected]
	return DatetimeZoneSelection{Name: signature.name, Args: signature.args, Count: signature.count, Result: signature.result}

}

func (s DatetimeZoneSelection) IntrinsicStable() bool {
	return s.Name != "timezone" && s.Count == 2 || s.Name == "timezone" && (s.Count == 1 || s.Args[1] == sqltype.Timetz && s.Args[0] == sqltype.Text)
}

func (e DatetimeZoneCalendar[E, C, P]) Reconstruct(info Info[P], args [3]E) (DatetimeZoneCalendar[E, C, P], bool, error) {
	op := sqltype.ArithmeticAdd
	if e.Name == "date_subtract" {
		op = sqltype.ArithmeticSubtract
	}
	provider, err := sqltype.BindArithmetic(op, args[0].ResultUsage().Type, args[1].ResultUsage().Type, info.Usage.Type)
	if err != nil {
		return e, false, err
	}
	changed := provider != e.Provider
	e.Info, e.Args, e.Provider = info, args, provider
	return e, changed, nil
}
