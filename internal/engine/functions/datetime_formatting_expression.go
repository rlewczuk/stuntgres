package functions

import (
	"stuntgres/internal/sqltype"
)

type DatetimeFormattingContext[E any] interface {
	LiteralContext[E]
	FunctionInput() sqltype.InputEnvironment
	FunctionOutput() sqltype.OutputEnvironment
}

type DatetimeFormatting[E Expression[C], C DatetimeFormattingContext[E], P any] struct {
	Info[P]
	Plan sqltype.DatetimeFormatPlan
	Args [2]E
}

func (e DatetimeFormatting[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values[0], values[1], c.FunctionInput(), c.FunctionOutput())
}

func DatetimeFormattingAnalysis(name string, index int) AnalysisIntent {
	if name != "to_char" || index > 0 {
		return StringTextArgument
	}
	return StringOtherArgument
}

type DatetimeFormattingSelection struct {
	Source      sqltype.ID
	Numeric     bool
	Resolution  Resolution
	Unsupported string
}

func ResolveDatetimeFormatting[E any](name string, visible, variadic bool, operands []Argument[E]) DatetimeFormattingSelection {
	numeric := func(id sqltype.ID) bool {
		return id == sqltype.Int2 || id == sqltype.Int4 || id == sqltype.Int8 || id == sqltype.Numeric || id == sqltype.Float4 || id == sqltype.Float8
	}
	if visible && !variadic && name == "to_timestamp" && len(operands) == 1 {
		d := operands[0].Usage.Type
		if d == nil || numeric(d.BaseType().ID()) {
			return DatetimeFormattingSelection{Unsupported: "epoch to_timestamp is unsupported"}
		}
	}
	if !visible || len(operands) != 2 || variadic {
		return DatetimeFormattingSelection{Resolution: Missing}
	}
	for i, o := range operands {
		if name == "to_char" && i == 0 {
			continue
		}
		if d := o.Usage.Type; d != nil && !sqltype.StringTextInput(d.BaseType().ID()) {
			return DatetimeFormattingSelection{Resolution: Missing}
		}
	}
	source := sqltype.Text
	if name == "to_char" {
		d := operands[0].Usage.Type
		if d == nil {
			return DatetimeFormattingSelection{Resolution: Ambiguous}
		}
		id := d.BaseType().ID()
		if numeric(id) {
			if id == sqltype.Int2 {
				id = sqltype.Float8
			}
			return DatetimeFormattingSelection{Source: id, Numeric: true}
		}
		switch id {
		case sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval:
			source = id
		case sqltype.Date:
			source = sqltype.Timestamptz
		case sqltype.Time:
			return DatetimeFormattingSelection{Unsupported: "time to interval conversion is unsupported"}
		default:
			return DatetimeFormattingSelection{Resolution: Missing}
		}
	}
	return DatetimeFormattingSelection{Source: source}
}

func DatetimeFormattingIntrinsicRule() IntrinsicPropertyRule { return ExactStable }

func DatetimeFormattingResult(name string) sqltype.ID {
	switch name {
	case "to_date":
		return sqltype.Date
	case "to_timestamp":
		return sqltype.Timestamptz
	}
	return sqltype.Text
}

func (e DatetimeFormatting[E, C, P]) Reconstruct(info Info[P], args [2]E) (DatetimeFormatting[E, C, P], error) {
	plan, err := e.Plan.Retag(info.Usage, args[0].ResultUsage())
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}
