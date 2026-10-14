package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type DatetimeMakeContext[E any] interface {
	LiteralContext[E]
	FunctionInput() sqltype.InputEnvironment
}

type DatetimeMake[E Expression[C], C DatetimeMakeContext[E], P any] struct {
	Info[P]
	Plan         sqltype.DatetimeMakePlan
	Args         [7]E // Declared parameter order, including interval defaults.
	Count        int
	WrittenCount int
	WrittenSlots [7]uint8
	Labels       [7]string // Written order; empty denotes positional Syntax.
}

func (e *DatetimeMake[E, C, P]) Arguments() []E { return e.Args[:e.Count] }

func (e DatetimeMake[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	// Preflight already visited/folded constant children. Runtime strictness can
	// now short circuit a constant NULL without invoking the constructor/zone.
	for _, arg := range e.Arguments() {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [7]sqltype.Value
	for i, arg := range e.Arguments() {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values[:e.Count], c.FunctionInput())
}

func DatetimeMakeSignature(name string) (labels [7]string, types [7]sqltype.ID, count int, result sqltype.ID) {
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

type DatetimeMakeSelection struct {
	Count, WrittenCount int
	WrittenSlots        [7]uint8
	Labels              [7]string
	Resolution          Resolution
}
type DatetimeMakePreparation struct {
	types     *[7]sqltype.ID
	selection DatetimeMakeSelection
	valid     bool
	invalidAt int
	Demand    TemporalDemand
}

func PrepareDatetimeMake[E any](name string, visible, variadic bool, args []Argument[E], argNames []parser.Identifier, labels *[7]string, types *[7]sqltype.ID, count int, result sqltype.ID) DatetimeMakePreparation {
	if !visible || variadic || result == 0 || len(args) > count {
		return DatetimeMakePreparation{}
	}
	p := DatetimeMakePreparation{types: types, valid: true, invalidAt: len(args), selection: DatetimeMakeSelection{Count: count, WrittenCount: len(args)}}
	var supplied [7]bool
	for i, arg := range args {
		slot := i
		if len(argNames) > 0 && argNames[i].Text != "" {
			slot = -1
			for j, label := range labels[:count] {
				if label == argNames[i].Text {
					slot = j
					break
				}
			}
		}
		if slot < 0 || slot >= count || supplied[slot] {
			p.invalidAt = i
			break
		}
		supplied[slot] = true
		p.selection.WrittenSlots[i] = uint8(slot)
		if len(argNames) > 0 {
			p.selection.Labels[i] = argNames[i].Text
		}
		if arg.Usage.Type != nil {
			p.Demand |= temporalDemand(types[slot])
		}
	}
	if name == "make_timestamptz" && !supplied[6] {
		p.selection.Count = 6
	}
	if p.invalidAt == len(args) {
		for i := range p.selection.Count {
			if !supplied[i] && name != "make_interval" {
				p.invalidAt = len(args)
				p.selection.Resolution = Missing
				break
			}
		}
	}
	return p
}
func ResolveDatetimeMake[E any](prepared DatetimeMakePreparation, args []Argument[E], types TemporalTypes) DatetimeMakeSelection {
	if !prepared.valid {
		return DatetimeMakeSelection{Resolution: Missing}
	}
	for i, arg := range args {
		if i == prepared.invalidAt {
			return DatetimeMakeSelection{Resolution: Missing}
		}
		if arg.Usage.Type != nil {
			target := prepared.types[prepared.selection.WrittenSlots[i]]
			if _, err := sqltype.PlanCoercion(arg.Usage, types.Usage(target), sqltype.Implicit); err != nil {
				return DatetimeMakeSelection{Resolution: Missing}
			}
		}
	}
	return prepared.selection
}

func DatetimeMakeIntrinsicStable(name string) bool { return name == "make_timestamptz" }

func (e DatetimeMake[E, C, P]) Reconstruct(info Info[P], args [7]E) (DatetimeMake[E, C, P], error) {
	plan, err := sqltype.BindDatetimeMake(e.Plan.Name(), info.Usage.Type, e.Count)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}
